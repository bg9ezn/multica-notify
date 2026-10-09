// Command mocksender signs and POSTs a synthetic Multica hook delivery —
// the fastest way to exercise a multica-notify deployment without touching
// real Multica state.
//
// Examples:
//
//	mocksender -url https://bridge:9097/hooks/issue-status -secret whsec_... \
//	  -event issue.status_changed -issue-id 6f0a... -number 12 \
//	  -title "Fix login" -status in_review
//
//	mocksender -url ... -secret ... -replay            # second send must 401
//	mocksender -url ... -secret ... -bad-signature     # must 401
//	mocksender -url ... -secret ... -skew 10m          # stale timestamp
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func main() {
	var (
		url           string
		secret        string
		hook          string
		eventType     string
		trigger       string
		issueID       string
		number        int
		title         string
		status        string
		taskID        string
		failureReason string
		retryPending  bool
		replay        bool
		badSignature  bool
		skew          time.Duration
	)
	flag.StringVar(&url, "url", "", "hook transport URL (required)")
	flag.StringVar(&secret, "secret", "", "signing secret, whsec_-prefixed or bare hex (required)")
	flag.StringVar(&hook, "hook", "issue_status", "hook_key")
	flag.StringVar(&eventType, "event", "issue.status_changed", "event_type")
	flag.StringVar(&trigger, "trigger", "event", "trigger (event|schedule)")
	flag.StringVar(&issueID, "issue-id", randomID(), "issue id")
	flag.IntVar(&number, "number", 12, "issue number")
	flag.StringVar(&title, "title", "Mock issue from mocksender", "issue title")
	flag.StringVar(&status, "status", "in_review", "issue status")
	flag.StringVar(&taskID, "task-id", "", "task id (task.* events)")
	flag.StringVar(&failureReason, "failure-reason", "", "failure_reason (task.failed)")
	flag.BoolVar(&retryPending, "retry-pending", false, "retry_pending flag (task.failed)")
	flag.BoolVar(&replay, "replay", false, "send the identical signed payload twice")
	flag.BoolVar(&badSignature, "bad-signature", false, "corrupt the signature on purpose")
	flag.DurationVar(&skew, "skew", 0, "timestamp offset, e.g. 10m to test the staleness window")
	flag.Parse()

	if url == "" || secret == "" {
		fmt.Fprintln(os.Stderr, "usage: mocksender -url <hook-url> -secret <whsec_...> [options]")
		os.Exit(2)
	}

	input := buildInput(eventType, issueID, number, title, status, taskID, failureReason, retryPending)
	delivery := randomID()
	body := map[string]any{
		"hook_key":       hook,
		"trigger":        trigger,
		"event_type":     eventType,
		"delivery_id":    delivery,
		"invocation_id":  randomID(),
		"attempt":        1,
		"actor":          map[string]any{"type": "member", "id": "mocksender"},
		"issue_id":       issueID,
		"input":          input,
		"callback_url":   "",
		"callback_token": "",
	}
	raw, err := json.Marshal(body)
	if err != nil {
		fail("marshal: %v", err)
	}

	send := func() {
		ts := strconv.FormatInt(time.Now().Add(skew).Unix(), 10)
		mac := hmac.New(sha256.New, mustSecret(secret))
		mac.Write([]byte(ts))
		mac.Write([]byte("."))
		mac.Write(raw)
		sig := "v1=" + hex.EncodeToString(mac.Sum(nil))
		if badSignature {
			sig = "v1=" + strings.Repeat("0", 64)
		}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			fail("request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-multica-timestamp", ts)
		req.Header.Set("x-multica-signature", sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fail("POST: %v", err)
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		fmt.Printf("status=%d delivery=%s body=%s\n", resp.StatusCode, delivery, strings.TrimSpace(string(respBody)))
	}

	send()
	if replay {
		fmt.Println("-- replaying identical bytes --")
		send()
	}
}

func buildInput(eventType, issueID string, number int, title, status, taskID, failureReason string, retryPending bool) any {
	switch {
	case strings.HasPrefix(eventType, "issue."):
		return map[string]any{
			"issue": map[string]any{
				"id":     issueID,
				"number": number,
				"title":  title,
				"status": status,
			},
			"status_changed": true,
		}
	default: // task.*
		return map[string]any{
			"task_id":        taskID,
			"issue_id":       issueID,
			"status":         status,
			"failure_reason": failureReason,
			"retry_pending":  retryPending,
		}
	}
}

func mustSecret(secret string) []byte {
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(secret), "whsec_"))
	if err != nil || len(raw) == 0 {
		fail("invalid secret: must be hex (optionally whsec_-prefixed)")
	}
	return raw
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mocksender: "+format+"\n", args...)
	os.Exit(1)
}
