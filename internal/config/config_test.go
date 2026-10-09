package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
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
	require.NoError(t, err)

	assert.Equal(t, ":9097", cfg.Listen, "listen default")
	assert.Equal(t, 30*time.Second, time.Duration(cfg.Debounce.Window), "debounce default")
	assert.NotNil(t, cfg.Filters.SkipRetryingTasks)
	assert.True(t, *cfg.Filters.SkipRetryingTasks, "skip_retrying_tasks default")
	assert.NotNil(t, cfg.Filters.OnTaskFailed)
	assert.True(t, *cfg.Filters.OnTaskFailed, "on_task_failed default")
	assert.Equal(t, "MULTICA_NOTIFY_SIGNING_SECRET", cfg.SigningSecretEnv)
	assert.True(t, cfg.IsEnabled(), "master switch default")
}

func TestLoadRejectsMissingChannels(t *testing.T) {
	path := write(t, "listen: ':9000'\n")
	_, err := Load(path)
	assert.Error(t, err, "config without channels accepted")
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
	_, err := Load(path)
	assert.Error(t, err, "duplicate channel names accepted")
}

func TestLoadRejectsHalfTLS(t *testing.T) {
	path := write(t, `
tls: {cert: /x/crt}
channels:
  - name: a
    type: ntfy
    options: {topic: t}
`)
	_, err := Load(path)
	assert.Error(t, err, "cert without key accepted")
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
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, time.Duration(cfg.Debounce.Window))
}

func TestLoadRejectsBadDuration(t *testing.T) {
	path := write(t, `
debounce: {window: soon}
channels:
  - name: a
    type: ntfy
    options: {topic: t}
`)
	_, err := Load(path)
	assert.Error(t, err, "invalid duration accepted")
}

func TestChannelEnabledDefaultsTrueAndHonorsFalse(t *testing.T) {
	path := write(t, `
channels:
  - name: on
    type: ntfy
    options: {topic: t}
  - name: off
    type: ntfy
    enabled: false
    options: {topic: t}
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.True(t, cfg.Channels[0].IsEnabled(), "channel without enabled should default to true")
	assert.False(t, cfg.Channels[1].IsEnabled(), "enabled: false not honored")
}

func TestMasterSwitchHonorsFalse(t *testing.T) {
	path := write(t, `
enabled: false
channels:
  - name: a
    type: ntfy
    options: {topic: t}
`)
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.False(t, cfg.IsEnabled(), "master switch not honored")
}
