package hookserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bg9ezn/multica-notify/internal/channel"
	"github.com/bg9ezn/multica-notify/internal/event"
	"github.com/bg9ezn/multica-notify/internal/message"
)

// signBody computes the delivery signature the way Multica does:
// HMAC-SHA256(secret, timestamp + "." + rawBody), hex, "v1=" prefixed.
// testSecret / hexOf32Bytes / mustBytes live in verify_test.go (same package).
func signBody(ts string, body []byte) string {
	mac := hmac.New(sha256.New, mustBytes(hexOf32Bytes))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// capturingChannel records delivered messages for assertions.
type capturingChannel struct {
	mu       sync.Mutex
	name     string
	messages []message.Message
}

func (c *capturingChannel) Name() string { return c.name }

func (c *capturingChannel) Send(_ context.Context, msg message.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, msg)
	return nil
}

func (c *capturingChannel) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

func (c *capturingChannel) last() message.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return message.Message{}
	}
	return c.messages[len(c.messages)-1]
}

func (c *capturingChannel) titles() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.messages))
	for _, m := range c.messages {
		out = append(out, m.Title)
	}
	return out
}

func newTestHandler(t *testing.T, chs ...channel.Channel) *Handler {
	t.Helper()
	verifier, err := NewVerifier(testSecret)
	require.NoError(t, err)
	renderer, err := message.NewRenderer(nil)
	require.NoError(t, err)
	deps := &Deps{
		Filter: &event.Filter{
			IssueStatuses:     map[string]bool{"in_review": true},
			OnTaskFailed:      true,
			SkipRetryingTasks: true,
		},
		Debouncer: event.NewDebouncer(10 * time.Millisecond),
		Renderer:  renderer,
		Channels:  chs,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h := NewHandler(verifier, deps)
	t.Cleanup(func() {
		deps.Debouncer.Flush()
		h.Close(2 * time.Second)
	})
	return h
}

// postSigned performs one authenticated delivery against the handler.
func postSigned(t *testing.T, h *Handler, body []byte, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hooks/issue_status", bytes.NewReader(body))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("x-multica-timestamp", ts)
	req.Header.Set("x-multica-signature", signBody(ts, body))
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func issueStatusBody(delivery, status string) []byte {
	b, err := json.Marshal(map[string]any{
		"hook_key":      "issue_status",
		"trigger":       "event",
		"event_type":    "issue.status_changed",
		"delivery_id":   delivery,
		"invocation_id": delivery + "-i",
		"attempt":       1,
		"actor":         map[string]string{"type": "plugin"},
		"issue_id":      "11111111-1111-1111-1111-111111111111",
		"input": map[string]any{
			"issue": map[string]any{
				"id":     "11111111-1111-1111-1111-111111111111",
				"number": 7,
				"title":  "Ship it",
				"status": status,
			},
			"status_changed": true,
		},
	})
	if err != nil {
		panic(err) // marshaling a literal map of primitives cannot fail
	}
	return b
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m), "response not json: %s", rec.Body.String())
	return m
}

func TestHandlerAcceptsAndNotifies(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	rec := postSigned(t, h, issueStatusBody("d1", "in_review"), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	assert.Eventually(t, func() bool { return cap.count() > 0 },
		2*time.Second, 5*time.Millisecond, "notification should reach the channel")
	msg := cap.last()
	assert.NotEmpty(t, msg.Title)
	assert.NotEmpty(t, msg.Body)
	assert.Contains(t, msg.Title, "#7", "title should carry the issue ref")
}

func TestHandlerFiltersNonMatchingStatus(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	rec := postSigned(t, h, issueStatusBody("d2", "done"), nil) // filter: only in_review
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, decodeBody(t, rec)["skipped"] != nil, "expected a skip reason")

	// The debounced path never fires for filtered events.
	assert.Never(t, func() bool { return cap.count() > 0 },
		150*time.Millisecond, 20*time.Millisecond, "filtered event must not notify")
}

func TestHandlerDeduplicatesAcrossRedelivery(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	// Journal-backed dedupe: a host retry carries the same delivery_id but a
	// fresh timestamp (so a fresh signature — the verifier's replay guard
	// must NOT be what catches this; the journal is).
	deps := h.loadDeps()
	deps.Journal = mustJournal(t)
	h.UpdateDeps(deps)

	body := issueStatusBody("d3", "in_review")
	deliverAt := func(at time.Time) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/hooks/issue_status", bytes.NewReader(body))
		stamp := strconv.FormatInt(at.Unix(), 10)
		req.Header.Set("x-multica-timestamp", stamp)
		req.Header.Set("x-multica-signature", signBody(stamp, body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, deliverAt(time.Now()).Code, "first delivery")
	rec := deliverAt(time.Now().Add(time.Second))
	require.Equal(t, http.StatusOK, rec.Code, "duplicate must not be rejected")
	assert.Equal(t, true, decodeBody(t, rec)["duplicate"], "duplicate must be flagged")

	assert.Eventually(t, func() bool { return cap.count() == 1 },
		2*time.Second, 5*time.Millisecond, "exactly one notification")
	assert.Never(t, func() bool { return cap.count() > 1 },
		100*time.Millisecond, 20*time.Millisecond, "redelivery must not notify twice")
}

func TestHandlerRejectsBadSignatureAndReplay(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	// Bad signature.
	rec := postSigned(t, h, issueStatusBody("d4", "in_review"), func(req *http.Request) {
		req.Header.Set("x-multica-signature", "v1="+hex.EncodeToString(make([]byte, 32)))
	})
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	// Replay: the exact same bytes (same timestamp, same signature) must be
	// caught by the verifier's seen-set. The fixed timestamp keeps the test
	// deterministic across second boundaries.
	body := issueStatusBody("d5", "in_review")
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := signBody(ts, body)
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/hooks/issue_status", bytes.NewReader(body))
		req.Header.Set("x-multica-timestamp", ts)
		req.Header.Set("x-multica-signature", sig)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusOK, send().Code, "first send")
	require.Equal(t, http.StatusUnauthorized, send().Code, "replay must be rejected")

	assert.Eventually(t, func() bool { return cap.count() == 1 },
		2*time.Second, 5*time.Millisecond, "exactly one notification for the original")
	assert.Never(t, func() bool { return cap.count() > 1 },
		100*time.Millisecond, 20*time.Millisecond, "replay must not notify again")
}

func TestHandlerIsolatesFailingChannel(t *testing.T) {
	good := &capturingChannel{name: "good"}
	bad := &failingChannel{name: "bad"}
	h := newTestHandler(t, good, bad)

	rec := postSigned(t, h, issueStatusBody("d6", "in_review"), nil)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Eventually(t, func() bool { return good.count() > 0 },
		2*time.Second, 5*time.Millisecond, "healthy channel must still deliver")
}

func TestHandlerDebounceSendsLatestOnly(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	// Three flips inside the window; the filter only passes in_review, so the
	// two in_review flips collapse into one notification.
	_ = postSigned(t, h, issueStatusBody("d7", "in_review"), nil)
	_ = postSigned(t, h, issueStatusBody("d8", "done"), nil)
	_ = postSigned(t, h, issueStatusBody("d9", "in_review"), nil)

	assert.Eventually(t, func() bool { return cap.count() > 0 },
		2*time.Second, 5*time.Millisecond, "debounced notification should fire")
	assert.Never(t, func() bool { return cap.count() > 1 },
		100*time.Millisecond, 20*time.Millisecond, "flips must collapse to one notification")
	assert.Contains(t, cap.last().Title, "in_review", "latest state must be carried")
}

func TestHandlerEventDeliveryWithoutDeliveryID(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)
	deps := h.loadDeps()
	deps.Journal = mustJournal(t)
	h.UpdateDeps(deps)

	// Real event deliveries carry no delivery_id (schedule-only field
	// host-side). A host retry regenerates invocation_id but repeats
	// occurred_at, so the composite key must dedupe them.
	body := func(invocation string) []byte {
		b, err := json.Marshal(map[string]any{
			"hook_key":        "issue_status",
			"trigger":         "event",
			"event_type":      "issue.status_changed",
			"invocation_id":   invocation,
			"attempt":         1,
			"workspace_id":    "ws-1",
			"installation_id": "inst-1",
			"issue_id":        "i-77",
			"occurred_at":     "2026-10-10T02:00:00Z",
			"input": map[string]any{
				"issue":          map[string]any{"id": "i-77", "number": 8, "title": "No-id event", "status": "in_review"},
				"status_changed": true,
			},
		})
		if err != nil {
			panic(err) // marshaling a literal map of primitives cannot fail
		}
		return b
	}

	send := func(invocation string) *httptest.ResponseRecorder {
		b := body(invocation)
		req := httptest.NewRequest(http.MethodPost, "/hooks/issue_status", bytes.NewReader(b))
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("x-multica-timestamp", stamp)
		req.Header.Set("x-multica-signature", signBody(stamp, b))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, send("inv-a").Code, "first event delivery")
	rec := send("inv-b") // fresh invocation_id, same logical event
	require.Equal(t, http.StatusOK, rec.Code, "retry must not be rejected")
	assert.Equal(t, true, decodeBody(t, rec)["duplicate"], "event retry must be deduped by composite key")

	assert.Eventually(t, func() bool { return cap.count() == 1 },
		2*time.Second, 5*time.Millisecond)
	assert.Never(t, func() bool { return cap.count() > 1 },
		100*time.Millisecond, 20*time.Millisecond)
}

func TestHandlerMutedSuppressesFanOut(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)
	base := h.loadDeps()

	// Swap in rebuilt copies (like the SIGHUP reload path does) rather than
	// mutating the live struct: a worker may be reading it concurrently.
	mutedDeps := *base
	mutedDeps.Muted = true
	h.UpdateDeps(&mutedDeps)

	rec := postSigned(t, h, issueStatusBody("d-mute", "in_review"), nil)
	require.Equal(t, http.StatusOK, rec.Code, "muted bridge must still answer 200 (host must not retry)")
	assert.Never(t, func() bool { return cap.count() > 0 },
		150*time.Millisecond, 20*time.Millisecond, "muted bridge must not deliver")

	// Unmute (SIGHUP reload path) -> events flow again.
	unmutedDeps := *base
	unmutedDeps.Muted = false
	h.UpdateDeps(&unmutedDeps)
	rec = postSigned(t, h, issueStatusBody("d-unmute", "in_review"), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Eventually(t, func() bool { return cap.count() > 0 },
		2*time.Second, 5*time.Millisecond, "unmuted bridge delivers again")
}

func TestHandlerRejectsNonPostAndUnknownPaths(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/hooks/issue_status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/elsewhere", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func mustJournal(t *testing.T) *event.Journal {
	t.Helper()
	j, err := event.Open(t.TempDir() + "/journal.jsonl")
	require.NoError(t, err)
	t.Cleanup(func() { j.Close() })
	return j
}

// failingChannel always errors; the pipeline must isolate it.
type failingChannel struct{ name string }

func (f *failingChannel) Name() string { return f.name }

func (f *failingChannel) Send(_ context.Context, _ message.Message) error {
	return errFailing
}

var errFailing = errFailingType{}

type errFailingType struct{}

func (errFailingType) Error() string { return "always fails" }
