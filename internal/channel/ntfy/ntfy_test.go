package ntfy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bg9ezn/multica-notify/internal/message"
)

func TestNewRequiresTopic(t *testing.T) {
	if _, err := New("n", map[string]string{}); err == nil {
		t.Fatal("missing topic accepted")
	}
}

func TestNewRejectsBadPriority(t *testing.T) {
	if _, err := New("n", map[string]string{"topic": "t", "priority": "9"}); err == nil {
		t.Fatal("priority 9 accepted")
	}
}

func TestSendPublishesJSONToRoot(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := New("phone", map[string]string{
		"server": srv.URL, "topic": "multica", "token": "tk_test", "priority": "5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), message.Message{Title: "标题", Body: "正文"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got["topic"] != "multica" || got["title"] != "标题" || got["message"] != "正文" {
		t.Fatalf("payload = %v", got)
	}
	if got["priority"] != float64(5) {
		t.Fatalf("priority = %v", got["priority"])
	}
	if auth != "Bearer tk_test" {
		t.Fatalf("auth = %q", auth)
	}
}

func TestServerDefault(t *testing.T) {
	n, err := New("n", map[string]string{"topic": "t"})
	if err != nil {
		t.Fatal(err)
	}
	if n.server != "https://ntfy.sh" {
		t.Errorf("server = %q", n.server)
	}
}
