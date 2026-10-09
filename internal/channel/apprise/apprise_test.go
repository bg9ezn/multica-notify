package apprise

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bg9ezn/multica-notify/internal/message"
)

func TestNewRequiresURL(t *testing.T) {
	if _, err := New("a", map[string]string{}); err == nil {
		t.Fatal("missing url accepted")
	}
}

func TestSendPostsAppriseAPIPayload(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, err := New("wecom", map[string]string{"url": srv.URL + "/notify/wecom"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Send(context.Background(), message.Message{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got["title"] != "t" || got["body"] != "b" || got["type"] != "info" || got["format"] != "text" {
		t.Fatalf("payload = %v", got)
	}
}

func TestSendRetriesOnServerError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, _ := New("a", map[string]string{"url": srv.URL})
	if err := a.Send(context.Background(), message.Message{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestSendDoesNotRetryClientError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	a, _ := New("a", map[string]string{"url": srv.URL})
	if err := a.Send(context.Background(), message.Message{Title: "t", Body: "b"}); err == nil {
		t.Fatal("401 accepted")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}
