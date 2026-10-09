// Package webhook posts the rendered notification as generic JSON — the
// debugging channel and the door for anything this project does not
// natively speak. An empty URL disables the channel (New returns a nil
// channel and no error), so an example config can ship it commented in.
package webhook

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bg9ezn/multica-notify/internal/httpx"
	"github.com/bg9ezn/multica-notify/internal/message"
)

const sendTimeout = 5 * time.Second

type Webhook struct {
	name   string
	url    string
	client *http.Client
}

// New builds the channel. Options:
//
//	url — the POST target; empty (or whitespace) disables the channel.
func New(name string, opts map[string]string) (*Webhook, error) {
	url := strings.TrimSpace(opts["url"])
	if url == "" {
		return nil, nil
	}
	return &Webhook{
		name:   name,
		url:    url,
		client: &http.Client{Timeout: sendTimeout},
	}, nil
}

func (w *Webhook) Name() string { return w.name }

func (w *Webhook) Send(ctx context.Context, msg message.Message) error {
	if w == nil {
		return fmt.Errorf("webhook channel: disabled channel invoked")
	}
	payload := struct {
		Title string            `json:"title"`
		Body  string            `json:"body"`
		Meta  map[string]string `json:"meta,omitempty"`
	}{msg.Title, msg.Body, msg.Meta}
	return httpx.PostJSON(ctx, w.client, w.url, payload, nil, 2)
}
