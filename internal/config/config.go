// Package config loads and validates the multica-notify YAML configuration.
//
// The config file is the operator's single surface: listen address, TLS
// material, the signing-secret env var name, event filters, debounce
// behavior, the idempotency journal path, and the notification channels.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration so YAML values like "30s" parse directly.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a quoted string like \"30s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// TLSSection points at the serving certificate. When nil the hook server
// speaks plain HTTP — valid for tests and loopback development; production
// deployments must serve HTTPS because Multica's manifest validator only
// accepts https:// transport URLs.
type TLSSection struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// FilterConfig decides which decoded events become notifications.
type FilterConfig struct {
	// IssueStatuses restricts issue.status_changed notifications to these
	// statuses. An empty list means every status change is notified.
	IssueStatuses []string `yaml:"issue_statuses"`
	// OnTaskCompleted toggles task.completed notifications. Off by default:
	// completions are usually visible where the user already looks.
	OnTaskCompleted bool `yaml:"on_task_completed"`
	// OnTaskFailed toggles task.failed notifications. On by default — a
	// terminal failure is exactly what a human needs to know about.
	OnTaskFailed *bool `yaml:"on_task_failed"`
	// SkipRetryingTasks drops task.failed deliveries whose retry_pending flag
	// is set. Multica marks intermediate attempts that way (the agent run may
	// still recover), so notifying on them is noise. Default true.
	SkipRetryingTasks *bool `yaml:"skip_retrying_tasks"`
}

type DebounceConfig struct {
	// Window is how long status flips on the same issue are collapsed. The
	// notification fires once when the window closes, carrying the latest
	// state. Zero disables debouncing (send immediately).
	Window Duration `yaml:"window"`
}

// ChannelConfig is one outgoing notification target. Options are consumed by
// the channel implementation named by Type; the core never inspects them.
type ChannelConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
	// Enabled defaults to true. Set false to keep a channel declared but
	// dormant — e.g. apprise-api not deployed yet — without deleting its
	// options.
	Enabled *bool             `yaml:"enabled,omitempty"`
	Options map[string]string `yaml:"options"`
}

// IsEnabled reports whether the channel should be built.
func (c ChannelConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// TemplateConfig overrides one built-in template set ("issue_status", "task",
// "heartbeat"). Title and Body are text/template sources evaluated against
// the decoded event.
type TemplateConfig struct {
	Title string `yaml:"title"`
	Body  string `yaml:"body"`
}

type Config struct {
	// Enabled is the master switch. false = the bridge still accepts and
	// journals hook deliveries (answering the host promptly) but suppresses
	// all fan-out — a global mute. Channels are still built and validated so
	// config typos surface at startup or reload, not when re-enabled.
	// Defaults to true.
	Enabled            *bool                     `yaml:"enabled,omitempty"`
	Listen             string                    `yaml:"listen"`
	TLS                *TLSSection               `yaml:"tls,omitempty"`
	SigningSecretEnv   string                    `yaml:"signing_secret_env"`
	Filters            FilterConfig              `yaml:"filters"`
	Debounce           DebounceConfig            `yaml:"debounce"`
	IdempotencyJournal string                    `yaml:"idempotency_journal"`
	Channels           []ChannelConfig           `yaml:"channels"`
	Templates          map[string]TemplateConfig `yaml:"templates,omitempty"`
}

// IsEnabled reports whether notifications should fan out.
func (c *Config) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Load reads, defaults, and validates the configuration file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) ApplyDefaults() {
	if c.Enabled == nil {
		t := true
		c.Enabled = &t
	}
	if c.Listen == "" {
		c.Listen = ":9097"
	}
	if c.SigningSecretEnv == "" {
		c.SigningSecretEnv = "MULTICA_NOTIFY_SIGNING_SECRET"
	}
	if c.Debounce.Window == 0 {
		c.Debounce.Window = Duration(30 * time.Second)
	}
	if c.IdempotencyJournal == "" {
		c.IdempotencyJournal = "data/journal.jsonl"
	}
	if c.Filters.SkipRetryingTasks == nil {
		t := true
		c.Filters.SkipRetryingTasks = &t
	}
	if c.Filters.OnTaskFailed == nil {
		t := true
		c.Filters.OnTaskFailed = &t
	}
}

func (c *Config) Validate() error {
	if len(c.Channels) == 0 {
		return fmt.Errorf("config: at least one channel is required")
	}
	seen := map[string]bool{}
	for i, ch := range c.Channels {
		if ch.Name == "" {
			return fmt.Errorf("config: channels[%d].name is required", i)
		}
		if seen[ch.Name] {
			return fmt.Errorf("config: duplicate channel name %q", ch.Name)
		}
		seen[ch.Name] = true
		if ch.Type == "" {
			return fmt.Errorf("config: channels[%d].type is required", i)
		}
	}
	if c.TLS != nil && (c.TLS.Cert == "" || c.TLS.Key == "") {
		return fmt.Errorf("config: tls.cert and tls.key must both be set")
	}
	return nil
}
