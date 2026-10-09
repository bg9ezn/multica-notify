package hookserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "whsec_" + hexOf32Bytes

// hexOf32Bytes is hex("multica-notify-golden-secret-0001").
const hexOf32Bytes = "6d756c746963612d6e6f746966792d676f6c64656e2d7365637265742d30303031"

func mustBytes(hexStr string) []byte {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		panic(err)
	}
	return raw
}

// sign computes a delivery signature with the documented algorithm,
// independently of the implementation under test.
func sign(secretHex, ts string, body []byte) string {
	mac := hmac.New(sha256.New, mustBytes(secretHex))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifierAcceptsWellFormedDelivery(t *testing.T) {
	v, err := NewVerifier(testSecret)
	require.NoError(t, err)

	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"hook_key":"issue_status"}`)
	require.NoError(t, v.Verify(body, ts, sign(hexOf32Bytes, ts, body), now))
}

func TestVerifierRejectionMatrix(t *testing.T) {
	v, err := NewVerifier(testSecret)
	require.NoError(t, err)

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
			assert.ErrorIs(t, v.Verify(tc.body, tc.ts, tc.sig, now), tc.wantError)
		})
	}
}

func TestVerifierRejectsReplayWithinWindow(t *testing.T) {
	v, err := NewVerifier(testSecret)
	require.NoError(t, err)

	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"delivery":1}`)
	sig := sign(hexOf32Bytes, ts, body)

	require.NoError(t, v.Verify(body, ts, sig, now), "first delivery rejected")
	// The same bytes, seconds later: the timestamp is still inside the
	// window, so only the remembered signature catches this.
	assert.ErrorIs(t, v.Verify(body, ts, sig, now.Add(30*time.Second)), ErrReplayedDelivery)
}

func TestVerifierSignatureValidityIsBodyBound(t *testing.T) {
	// A signature computed over one body must not validate another body even
	// if a prefix matches — this is the "re-serialize and sign that" trap the
	// official handler warns about.
	v, err := NewVerifier(testSecret)
	require.NoError(t, err)

	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := sign(hexOf32Bytes, ts, []byte(`{"a":1}`))
	assert.ErrorIs(t, v.Verify([]byte(`{"a":12}`), ts, sig, now), ErrBadSignature)
}

func TestNewVerifierRejectsNonHexSecret(t *testing.T) {
	_, err := NewVerifier("not-hex-at-all")
	assert.Error(t, err)
	_, err = NewVerifier("whsec_zz")
	assert.Error(t, err)
}
