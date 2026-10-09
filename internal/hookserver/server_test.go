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
	return c.messages[len(c.messages)-1]
}

// newTestHandler builds a handler wired to the given channels with a short
// debounce window and a filter accepting only in_review.
func newTestHandler(t *testing.T, chs ...channel.Channel) *Handler {
	t.Helper()
	verifier, err := NewVerifier(testSecret)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	renderer, err := message.NewRenderer(nil)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
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

// postSigned performs one authenticated delivery against the test server.
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
	b, _ := json.Marshal(map[string]any{
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
	return b
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestHandlerAcceptsAndNotifies(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	rec := postSigned(t, h, issueStatusBody("d1", "in_review"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	waitFor(t, 2*time.Second, func() bool { return cap.count() > 0 })
	msg := cap.last()
	if msg.Title == "" || msg.Body == "" {
		t.Fatalf("rendered message incomplete: %+v", msg)
	}
	if !bytes.Contains([]byte(msg.Title), []byte("#7")) {
		t.Fatalf("title missing issue ref: %q", msg.Title)
	}
}

func TestHandlerFiltersNonMatchingStatus(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	rec := postSigned(t, h, issueStatusBody("d2", "done"), nil) // filter: only in_review
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not json: %v", err)
	}
	if resp["skipped"] == "" {
		t.Fatalf("expected skip reason in response: %s", rec.Body.String())
	}
	// The debounced path never fires for filtered events.
	time.Sleep(50 * time.Millisecond)
	if cap.count() != 0 {
		t.Fatalf("filtered event notified anyway: %+v", cap.last())
	}
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

	if rec := deliverAt(time.Now()); rec.Code != http.StatusOK {
		t.Fatalf("first delivery: %d, body=%s", rec.Code, rec.Body.String())
	}
	rec := deliverAt(time.Now().Add(time.Second))
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate rejected with %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"duplicate":true`)) {
		t.Fatalf("duplicate not flagged: %s", rec.Body.String())
	}
	waitFor(t, 2*time.Second, func() bool { return cap.count() > 0 })
	time.Sleep(30 * time.Millisecond)
	if cap.count() != 1 {
		t.Fatalf("delivered %d times, want 1", cap.count())
	}
}

func TestHandlerRejectsBadSignatureAndReplay(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	// Bad signature.
	rec := postSigned(t, h, issueStatusBody("d4", "in_review"), func(req *http.Request) {
		req.Header.Set("x-multica-signature", "v1="+hex.EncodeToString(make([]byte, 32)))
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: status = %d, want 401", rec.Code)
	}

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
	if rec := send(); rec.Code != http.StatusOK {
		t.Fatalf("first send: status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if rec := send(); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay: status = %d, want 401", rec.Code)
	}
	waitFor(t, 2*time.Second, func() bool { return cap.count() > 0 })
	time.Sleep(30 * time.Millisecond)
	if cap.count() != 1 {
		t.Fatalf("replay delivered extra notifications: %d", cap.count())
	}
}

func TestHandlerIsolatesFailingChannel(t *testing.T) {
	good := &capturingChannel{name: "good"}
	bad := &failingChannel{name: "bad"}
	h := newTestHandler(t, good, bad)

	if rec := postSigned(t, h, issueStatusBody("d6", "in_review"), nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	waitFor(t, 2*time.Second, func() bool { return good.count() > 0 })
}

func TestHandlerDebounceSendsLatestOnly(t *testing.T) {
	cap := &capturingChannel{name: "cap"}
	h := newTestHandler(t, cap)

	// Three flips inside the window; the filter only passes in_review, so the
	// two in_review flips collapse into one notification.
	_ = postSigned(t, h, issueStatusBody("d7", "in_review"), nil)
	_ = postSigned(t, h, issueStatusBody("d8", "done"), nil)
	_ = postSigned(t, h, issueStatusBody("d9", "in_review"), nil)

	waitFor(t, 2*time.Second, func() bool { return cap.count() > 0 })
	time.Sleep(30 * time.Millisecond)
	if got := cap.count(); got != 1 {
		t.Fatalf("notifications = %d, want 1 (latest-only)", got)
	}
	if msg := cap.last(); !bytes.Contains([]byte(msg.Title), []byte("in_review")) {
		t.Fatalf("latest state not carried: %q", msg.Title)
	}
}

func TestHandlerRejectsNonPostAndUnknownPaths(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/hooks/issue_status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /hooks/...: status = %d, want 405", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/elsewhere", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /elsewhere: status = %d, want 404", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status = %d, want 200", rec.Code)
	}
}

func mustJournal(t *testing.T) *event.Journal {
	t.Helper()
	j, err := event.Open(t.TempDir() + "/journal.jsonl")
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
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
