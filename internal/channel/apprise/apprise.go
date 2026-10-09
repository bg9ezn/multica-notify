// Package apprise delivers notifications through an apprise-api instance
// (github.com/caronc/apprise-api). The endpoint URL embeds the apprise config
// key, e.g. http://127.0.0.1:8000/notify/wecom — one channel per configured
// apprise key, and Apprise itself fans out to the real services (WeCom,
// DingTalk, Feishu, Telegram, email, ...).
package apprise

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

type Apprise struct {
	name       string
	url        string
	notifyType string
	client     *http.Client
}

// New builds the channel. Options:
//
//	url  — required; apprise-api notify endpoint including the config key
//	type — optional apprise notify type, default "info"
func New(name string, opts map[string]string) (*Apprise, error) {
	url := strings.TrimSpace(opts["url"])
	if url == "" {
		return nil, fmt.Errorf("apprise channel %q: url is required", name)
	}
	notifyType := opts["type"]
	if notifyType == "" {
		notifyType = "info"
	}
	return &Apprise{
		name:       name,
		url:        url,
		notifyType: notifyType,
		client:     &http.Client{Timeout: sendTimeout},
	}, nil
}

func (a *Apprise) Name() string { return a.name }

func (a *Apprise) Send(ctx context.Context, msg message.Message) error {
	notifyType := a.notifyType
	if msg.Type != "" {
		notifyType = msg.Type
	}
	payload := map[string]string{
		"title":  msg.Title,
		"body":   msg.Body,
		"type":   notifyType,
		"format": "text",
	}
	return httpx.PostJSON(ctx, a.client, a.url, payload, nil, 2)
}
