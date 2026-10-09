package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bg9ezn/multica-notify/internal/message"
)

func TestEmptyURLDisablesChannel(t *testing.T) {
	w, err := New("debug", map[string]string{"url": "   "})
	require.NoError(t, err, "disabled channel returned error")
	assert.Nil(t, w, "expected nil channel for empty url")
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
	require.NoError(t, err)

	msg := message.Message{
		Title: "t", Body: "b",
		Meta: map[string]string{"event_type": "issue.status_changed", "delivery_id": "d1"},
	}
	require.NoError(t, w.Send(context.Background(), msg))

	assert.Equal(t, "t", got.Title)
	assert.Equal(t, "b", got.Body)
	assert.Equal(t, "d1", got.Meta["delivery_id"])
}
