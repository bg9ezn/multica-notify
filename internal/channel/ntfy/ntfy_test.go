package ntfy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bg9ezn/multica-notify/internal/message"
)

func TestNewRequiresTopic(t *testing.T) {
	_, err := New("n", map[string]string{})
	assert.Error(t, err, "missing topic accepted")
}

func TestNewRejectsBadPriority(t *testing.T) {
	_, err := New("n", map[string]string{"topic": "t", "priority": "9"})
	assert.Error(t, err, "priority 9 accepted")
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
	require.NoError(t, err)

	require.NoError(t, n.Send(context.Background(), message.Message{Title: "标题", Body: "正文"}))
	assert.Equal(t, "multica", got["topic"])
	assert.Equal(t, "标题", got["title"])
	assert.Equal(t, "正文", got["message"])
	assert.Equal(t, float64(5), got["priority"])
	assert.Equal(t, "Bearer tk_test", auth)
}

func TestServerDefault(t *testing.T) {
	n, err := New("n", map[string]string{"topic": "t"})
	require.NoError(t, err)
	assert.Equal(t, "https://ntfy.sh", n.server)
}
