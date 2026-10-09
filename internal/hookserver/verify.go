package hookserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// signatureTolerance matches the host side: the official example handler
// accepts five minutes of clock drift, and a delivery outside the window is
// rejected even with a valid signature.
const signatureTolerance = 5 * time.Minute

var (
	ErrMissingHeaders   = errors.New("missing signature headers")
	ErrBadTimestamp     = errors.New("timestamp outside the accepted window")
	ErrBadSignature     = errors.New("signature does not match")
	ErrReplayedDelivery = errors.New("this request was already delivered")
)

// Verifier implements the plugin author's side of the Multica hook signature
// contract, following the four disciplines taught by the official example
// handler (examples/plugins/triage-notify/server/handler.mjs):
//
//  1. verify against the RAW bytes, before any parsing;
//  2. bound the delivery with the signed timestamp (±5 minutes);
//  3. compare signatures in constant time;
//  4. remember every accepted signature inside the window — the host cannot
//     do this part, and without it a captured request can be replayed until
//     the window expires.
type Verifier struct {
	secret []byte

	seenMu sync.Mutex
	seen   map[string]time.Time
}

// NewVerifier builds a Verifier from the signing secret shown once by Multica
// next to the install token. The secret is hex, optionally prefixed with
// "whsec_"; anything else is an operator error worth failing on at startup.
func NewVerifier(secret string) (*Verifier, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(secret), "whsec_")
	raw, err := hex.DecodeString(trimmed)
	if err != nil || len(raw) == 0 {
		return nil, fmt.Errorf("signing secret must be hex (optionally whsec_-prefixed)")
	}
	return &Verifier{secret: raw, seen: make(map[string]time.Time)}, nil
}

// Verify checks one delivery. The headers are the ones Multica sets:
// x-multica-timestamp (unix seconds as a string, part of the signed material)
// and x-multica-signature ("v1=<hex hmac>").
func (v *Verifier) Verify(rawBody []byte, timestampHeader, signatureHeader string, now time.Time) error {
	if timestampHeader == "" || signatureHeader == "" {
		return ErrMissingHeaders
	}
	ts, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return ErrBadTimestamp
	}
	drift := now.Unix() - ts
	if drift < 0 {
		drift = -drift
	}
	if drift > int64(signatureTolerance/time.Second) {
		return ErrBadTimestamp
	}

	presented := strings.TrimPrefix(signatureHeader, "v1=")
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(timestampHeader))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(presented)) {
		return ErrBadSignature
	}

	if !v.remember(presented, now) {
		return ErrReplayedDelivery
	}
	return nil
}

// remember records an accepted signature. It returns false when the exact
// signature was already accepted inside the window — i.e. the same bytes were
// delivered twice.
func (v *Verifier) remember(signature string, now time.Time) bool {
	v.seenMu.Lock()
	defer v.seenMu.Unlock()
	for sig, at := range v.seen {
		if now.Sub(at) > signatureTolerance {
			delete(v.seen, sig)
		}
	}
	if _, dup := v.seen[signature]; dup {
		return false
	}
	v.seen[signature] = now
	return true
}
