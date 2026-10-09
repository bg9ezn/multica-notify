package apprise

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

func TestNewRequiresURL(t *testing.T) {
	_, err := New("a", map[string]string{})
	assert.Error(t, err, "missing url accepted")
}

func TestSendPostsAppriseAPIPayload(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, err := New("wecom", map[string]string{"url": srv.URL + "/notify/wecom"})
	require.NoError(t, err)

	require.NoError(t, a.Send(context.Background(), message.Message{Title: "t", Body: "b"}))
	assert.Equal(t, map[string]any{
		"title": "t", "body": "b", "type": "info", "format": "text",
	}, got)
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

	a, err := New("a", map[string]string{"url": srv.URL})
	require.NoError(t, err)

	require.NoError(t, a.Send(context.Background(), message.Message{Title: "t", Body: "b"}),
		"retry did not recover")
	assert.Equal(t, 2, attempts)
}

func TestSendDoesNotRetryClientError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	a, err := New("a", map[string]string{"url": srv.URL})
	require.NoError(t, err)

	assert.Error(t, a.Send(context.Background(), message.Message{Title: "t", Body: "b"}), "401 accepted")
	assert.Equal(t, 1, attempts, "4xx must not retry")
}

func TestSendMessageTypeOverridesDefault(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, err := New("a", map[string]string{"url": srv.URL})
	require.NoError(t, err)

	require.NoError(t, a.Send(context.Background(), message.Message{Title: "t", Body: "b", Type: "success"}))
	assert.Equal(t, "success", got["type"], "message type should override the default")
}
