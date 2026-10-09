// Package ntfy delivers notifications to an ntfy server via its JSON publish
// API. Publishing JSON to the server root (rather than the topic URL with
// headers) sidesteps HTTP header encoding pitfalls for non-ASCII titles.
package ntfy

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

type Ntfy struct {
	name     string
	server   string
	topic    string
	token    string
	priority int
	client   *http.Client
}

// New builds the channel. Options:
//
//	topic    — required; the ntfy topic to publish to
//	server   — optional; default https://ntfy.sh (self-host for privacy)
//	token    — optional; bearer token when the server requires auth
//	priority — optional; ntfy priority 1..5, default 3
func New(name string, opts map[string]string) (*Ntfy, error) {
	topic := strings.TrimSpace(opts["topic"])
	if topic == "" {
		return nil, fmt.Errorf("ntfy channel %q: topic is required", name)
	}
	server := strings.TrimRight(strings.TrimSpace(opts["server"]), "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	priority := 3
	if p := strings.TrimSpace(opts["priority"]); p != "" {
		if _, err := fmt.Sscanf(p, "%d", &priority); err != nil || priority < 1 || priority > 5 {
			return nil, fmt.Errorf("ntfy channel %q: priority must be 1..5, got %q", name, p)
		}
	}
	return &Ntfy{
		name:     name,
		server:   server,
		topic:    topic,
		token:    opts["token"],
		priority: priority,
		client:   &http.Client{Timeout: sendTimeout},
	}, nil
}

func (n *Ntfy) Name() string { return n.name }

func (n *Ntfy) Send(ctx context.Context, msg message.Message) error {
	payload := map[string]any{
		"topic":    n.topic,
		"title":    msg.Title,
		"message":  msg.Body,
		"tags":     []string{"bell"},
		"priority": n.priority,
	}
	var headers map[string]string
	if n.token != "" {
		headers = map[string]string{"Authorization": "Bearer " + n.token}
	}
	return httpx.PostJSON(ctx, n.client, n.server+"/", payload, headers, 2)
}
