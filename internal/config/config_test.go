package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := write(t, `
channels:
  - name: phone
    type: ntfy
    options:
      topic: multica
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":9097" {
		t.Errorf("listen default = %q", cfg.Listen)
	}
	if time.Duration(cfg.Debounce.Window) != 30*time.Second {
		t.Errorf("debounce default = %v", cfg.Debounce.Window)
	}
	if cfg.Filters.SkipRetryingTasks == nil || !*cfg.Filters.SkipRetryingTasks {
		t.Errorf("skip_retrying_tasks default = %v, want true", cfg.Filters.SkipRetryingTasks)
	}
	if cfg.Filters.OnTaskFailed == nil || !*cfg.Filters.OnTaskFailed {
		t.Errorf("on_task_failed default = %v, want true", cfg.Filters.OnTaskFailed)
	}
	if cfg.SigningSecretEnv != "MULTICA_NOTIFY_SIGNING_SECRET" {
		t.Errorf("signing env default = %q", cfg.SigningSecretEnv)
	}
}

func TestLoadRejectsMissingChannels(t *testing.T) {
	path := write(t, "listen: ':9000'\n")
	if _, err := Load(path); err == nil {
		t.Fatal("config without channels accepted")
	}
}

func TestLoadRejectsDuplicateChannelNames(t *testing.T) {
	path := write(t, `
channels:
  - name: a
    type: ntfy
    options: {topic: t}
  - name: a
    type: webhook
    options: {url: "http://x"}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("duplicate channel names accepted")
	}
}

func TestLoadRejectsHalfTLS(t *testing.T) {
	path := write(t, `
tls: {cert: /x/crt}
channels:
  - name: a
    type: ntfy
    options: {topic: t}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("cert without key accepted")
	}
}

func TestLoadParsesDurations(t *testing.T) {
	path := write(t, `
debounce: {window: 5s}
channels:
  - name: a
    type: ntfy
    options: {topic: t}
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if time.Duration(cfg.Debounce.Window) != 5*time.Second {
		t.Errorf("window = %v", cfg.Debounce.Window)
	}
}

func TestLoadRejectsBadDuration(t *testing.T) {
	path := write(t, `
debounce: {window: soon}
channels:
  - name: a
    type: ntfy
    options: {topic: t}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("invalid duration accepted")
	}
}
