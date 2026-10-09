package hookserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

// testSecret mirrors the golden vector style: a fixed hex secret so tests can
// precompute signatures independently of the implementation.
const testSecret = "whsec_" + hexOf32Bytes

// hexOf32Bytes is hex("multica-notify-golden-secret-0001") padded to 32 raw
// bytes; tests only rely on self-consistency plus one cross-check against the
// documented algorithm.
const hexOf32Bytes = "6d756c746963612d6e6f746966792d676f6c64656e2d7365637265742d30303031"

func sign(secretHex, ts string, body []byte) string {
	mac := hmac.New(sha256.New, mustBytes(secretHex))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func mustBytes(hexStr string) []byte {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestVerifierAcceptsWellFormedDelivery(t *testing.T) {
	v, err := NewVerifier(testSecret)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"hook_key":"issue_status"}`)
	if err := v.Verify(body, ts, sign(hexOf32Bytes, ts, body), now); err != nil {
		t.Fatalf("valid delivery rejected: %v", err)
	}
}

func TestVerifierRejectionMatrix(t *testing.T) {
	v, err := NewVerifier(testSecret)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"delivery":1}`)

	cases := []struct {
		name      string
		ts        string
		sig       string
		body      []byte
		wantError error
	}{
		{"missing headers", "", "", body, ErrMissingHeaders},
		{"stale timestamp", strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10), "v1=00", body, ErrBadTimestamp},
		{"future timestamp", strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10), "v1=00", body, ErrBadTimestamp},
		{"garbage timestamp", "not-a-number", "v1=00", body, ErrBadTimestamp},
		{"wrong signature", ts, "v1=" + hex.EncodeToString(make([]byte, 32)), body, ErrBadSignature},
		{"signed different body", ts, sign(hexOf32Bytes, ts, []byte(`{"delivery":2}`)), body, ErrBadSignature},
		{"signature not hex", ts, "v1=zz", body, ErrBadSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := v.Verify(tc.body, tc.ts, tc.sig, now); err != tc.wantError {
				t.Fatalf("Verify error = %v, want %v", err, tc.wantError)
			}
		})
	}
}

func TestVerifierRejectsReplayWithinWindow(t *testing.T) {
	v, err := NewVerifier(testSecret)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"delivery":1}`)
	sig := sign(hexOf32Bytes, ts, body)

	if err := v.Verify(body, ts, sig, now); err != nil {
		t.Fatalf("first delivery rejected: %v", err)
	}
	// The same bytes, seconds later: the timestamp is still inside the
	// window, so only the remembered signature catches this.
	if err := v.Verify(body, ts, sig, now.Add(30*time.Second)); err != ErrReplayedDelivery {
		t.Fatalf("replay error = %v, want %v", err, ErrReplayedDelivery)
	}
}

func TestVerifierSignatureValidityIsBodyBound(t *testing.T) {
	// A signature computed over one body must not validate another body even
	// if a prefix matches — this is the "re-serialize and sign that" trap the
	// official handler warns about.
	v, _ := NewVerifier(testSecret)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := sign(hexOf32Bytes, ts, []byte(`{"a":1}`))
	if err := v.Verify([]byte(`{"a":12}`), ts, sig, now); err != ErrBadSignature {
		t.Fatalf("prefix-adjacent body accepted: %v", err)
	}
}

func TestNewVerifierRejectsNonHexSecret(t *testing.T) {
	if _, err := NewVerifier("not-hex-at-all"); err == nil {
		t.Fatal("non-hex secret accepted")
	}
	if _, err := NewVerifier("whsec_zz"); err == nil {
		t.Fatal("bad whsec_ secret accepted")
	}
}
