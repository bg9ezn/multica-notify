//go:build integration

// Integration tests run multica-notify's real pipeline (verify → journal →
// filter → debounce → channels) against real dependencies: an ntfy server
// started by `make test-integration` (deploy/compose/test.yml) plus a
// deliberately dead apprise endpoint proving channel isolation. Run via
// `make test-integration`, not `go test` directly.
package integration

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bg9ezn/multica-notify/internal/channel"
	"github.com/bg9ezn/multica-notify/internal/channel/apprise"
	"github.com/bg9ezn/multica-notify/internal/channel/ntfy"
	"github.com/bg9ezn/multica-notify/internal/event"
	"github.com/bg9ezn/multica-notify/internal/hookserver"
	"github.com/bg9ezn/multica-notify/internal/message"
)

const testSecret = "whsec_6d756c746963612d6e6f746966792d676f6c64656e2d7365637265742d30303031"

func mustHex(secret string) []byte {
	raw, err := hex.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		panic(err)
	}
	return raw
}

func signBody(stamp string, body []byte) string {
	mac := hmac.New(sha256.New, mustHex(testSecret))
	mac.Write([]byte(stamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func ntfyBaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_NTFY_URL")
	if url == "" {
		url = "http://127.0.0.1:8085"
	}
	// Skip (not fail) when the compose dependency is not up, so a plain
	// `go test ./...` without docker stays useful; make test-integration
	// always brings the server up first.
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second)
	if err != nil {
		t.Skipf("ntfy not reachable at %s (start via make test-integration): %v", url, err)
	}
	conn.Close()
	return url
}

type bridge struct {
	server *httptest.Server
	ntfy   string
	topic  string
}

func startBridge(t *testing.T) *bridge {
	t.Helper()
	ntfyBase := ntfyBaseURL(t)
	topic := "multica-it-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	ntfyCh, err := ntfy.New("phone", map[string]string{"server": ntfyBase, "topic": topic})
	if err != nil {
		t.Fatal(err)
	}
	// apprise pointed at a port with no listener: sends to it fail, and the
	// pipeline must isolate that without blocking the ntfy channel.
	dead, err := apprise.New("dead-apprise", map[string]string{"url": "http://127.0.0.1:1/notify/none"})
	if err != nil {
		t.Fatal(err)
	}

	verifier, err := hookserver.NewVerifier(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := message.NewRenderer(nil)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := event.Open(t.TempDir() + "/journal.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	deps := &hookserver.Deps{
		Filter: &event.Filter{
			IssueStatuses:     map[string]bool{"in_review": true, "done": true},
			OnTaskFailed:      true,
			SkipRetryingTasks: true,
		},
		Debouncer: event.NewDebouncer(50 * time.Millisecond),
		Renderer:  renderer,
		Journal:   journal,
		Channels:  []channel.Channel{ntfyCh, dead},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h := hookserver.NewHandler(verifier, deps)
	server := httptest.NewServer(h)
	t.Cleanup(func() {
		server.Close()
		journal.Close()
		h.Close(2 * time.Second)
	})
	return &bridge{server: server, ntfy: ntfyBase, topic: topic}
}

// post signs and posts one delivery body at the given timestamp, returning
// status and response body.
func (b *bridge) post(t *testing.T, body map[string]any, stamp time.Time) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, b.server.URL+"/hooks/issue_status", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	s := strconv.FormatInt(stamp.Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-multica-timestamp", s)
	req.Header.Set("x-multica-signature", signBody(s, raw))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, string(respBody)
}

func issueBody(delivery string, number int, status string) map[string]any {
	issue := map[string]any{
		"id": "11111111-1111-1111-1111-" + strconv.Itoa(100000000000+number), "number": number,
		"title": "Integration check", "status": status,
	}
	return map[string]any{
		"hook_key": "issue_status", "trigger": "event",
		"event_type":  "issue.status_changed",
		"delivery_id": delivery, "invocation_id": delivery + "-i", "attempt": 1,
		"issue_id": issue["id"], "input": map[string]any{"issue": issue, "status_changed": true},
	}
}

type ntfyMessage struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

func ntfyMessages(t *testing.T, b *bridge) []ntfyMessage {
	t.Helper()
	// Read back via the per-topic JSON endpoint: ntfy 2.29's root-level
	// /json?poll=1 serves the web SPA instead of messages (verified on the
	// deployment target), while /<topic>/json is reliable.
	resp, err := http.Get(b.ntfy + "/" + b.topic + "/json?poll=1")
	require.NoError(t, err, "ntfy read")
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out []ntfyMessage
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m ntfyMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if m.Title != "" || m.Message != "" {
			out = append(out, m)
		}
	}
	return out
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	// Mature async assertion under the hood; the named wrapper keeps the
	// scenario call sites readable.
	assert.Eventually(t, cond, timeout, 20*time.Millisecond, "condition not met within timeout")
}

func contains(messages []ntfyMessage, substr string) bool {
	for _, m := range messages {
		if strings.Contains(m.Title, substr) || strings.Contains(m.Message, substr) {
			return true
		}
	}
	return false
}

func TestEndToEndStatusChangeReachesNtfy(t *testing.T) {
	b := startBridge(t)
	before := len(ntfyMessages(t, b))

	status, respBody := b.post(t, issueBody("it-1", 12, "in_review"), time.Now())
	require.Equal(t, http.StatusOK, status, "body=%s", respBody)

	waitFor(t, 5*time.Second, func() bool { return len(ntfyMessages(t, b)) > before })
	assert.True(t, contains(ntfyMessages(t, b), "#12"), "ntfy message with #12 not found")
}

func TestReplayRejectedAndNoSecondNotification(t *testing.T) {
	b := startBridge(t)
	before := len(ntfyMessages(t, b))

	raw, err := json.Marshal(issueBody("it-2", 13, "in_review"))
	if err != nil {
		t.Fatal(err)
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	do := func() (int, string) {
		req, err := http.NewRequest(http.MethodPost, b.server.URL+"/hooks/issue_status", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-multica-timestamp", stamp)
		req.Header.Set("x-multica-signature", signBody(stamp, raw))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return resp.StatusCode, string(body)
	}

	if code, body := do(); code != http.StatusOK {
		t.Fatalf("first send: %d %s", code, body)
	}
	if code, body := do(); code != http.StatusUnauthorized {
		t.Fatalf("replay: %d %s, want 401", code, body)
	}

	time.Sleep(300 * time.Millisecond)
	if got := len(ntfyMessages(t, b)); got != before+1 {
		t.Fatalf("ntfy messages = %d, want exactly %d", got, before+1)
	}
}

func TestBadSignatureRejected(t *testing.T) {
	b := startBridge(t)
	raw, err := json.Marshal(issueBody("it-3", 14, "in_review"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, b.server.URL+"/hooks/issue_status", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-multica-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("x-multica-signature", "v1="+strings.Repeat("ab", 32))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d, want 401", resp.StatusCode)
	}
}

func TestTaskFailureReachesNtfyWhileAppriseDown(t *testing.T) {
	b := startBridge(t)
	before := len(ntfyMessages(t, b))

	body := map[string]any{
		"hook_key": "task_lifecycle", "trigger": "event",
		"event_type":  "task.failed",
		"delivery_id": "it-4", "invocation_id": "it-4-i", "attempt": 1,
		"issue_id": "i-4",
		"input": map[string]any{
			"task_id": "t-4", "issue_id": "i-4", "status": "failed",
			"failure_reason": "runtime offline", "retry_pending": false,
		},
	}
	status, _ := b.post(t, body, time.Now())
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}

	waitFor(t, 5*time.Second, func() bool { return len(ntfyMessages(t, b)) > before })
	if !contains(ntfyMessages(t, b), "runtime offline") {
		t.Fatal("failure reason not carried into the notification")
	}
}
