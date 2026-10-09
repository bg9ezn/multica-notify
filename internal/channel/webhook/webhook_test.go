package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bg9ezn/multica-notify/internal/message"
)

func TestEmptyURLDisablesChannel(t *testing.T) {
	w, err := New("debug", map[string]string{"url": "   "})
	if err != nil {
		t.Fatalf("disabled channel returned error: %v", err)
	}
	if w != nil {
		t.Fatal("expected nil channel for empty url")
	}
}

func TestSendPostsTitleBodyMeta(t *testing.T) {
	var got struct {
		Title string            `json:"title"`
		Body  string            `json:"body"`
		Meta  map[string]string `json:"meta"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w, err := New("debug", map[string]string{"url": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.Message{
		Title: "t", Body: "b",
		Meta: map[string]string{"event_type": "issue.status_changed", "delivery_id": "d1"},
	}
	if err := w.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.Title != "t" || got.Body != "b" || got.Meta["delivery_id"] != "d1" {
		t.Fatalf("payload = %+v", got)
	}
}
