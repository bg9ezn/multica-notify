package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbeddedExampleIsValid is the drift guard: whenever a config field
// changes, this fails until internal/config/example.yaml is updated, so the
// -init-config output can never go stale.
func TestEmbeddedExampleIsValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "example.yaml")
	require.NoError(t, os.WriteFile(path, []byte(ExampleYAML()), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err, "embedded example must parse and validate")

	assert.Len(t, cfg.Channels, 3, "example should demonstrate all three channel types")
	assert.True(t, cfg.Channels[0].IsEnabled(), "primary example channel should be enabled")
}

func TestInitConfigWritesAndRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	require.NoError(t, InitConfig(path, false))
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(written), "issue_statuses:", "written file should be the example")

	err = InitConfig(path, false)
	require.Error(t, err, "existing file must not be silently overwritten")
	assert.Contains(t, err.Error(), "already exists")
}

func TestInitConfigForceOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("listen: ':9999'\n"), 0o644))

	require.NoError(t, InitConfig(path, true))
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, ":9097", cfg.Listen, "force should have replaced the file with the example")
}

func TestInitConfigCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "config.yaml")
	require.NoError(t, InitConfig(path, false))
	assert.FileExists(t, path)
}
