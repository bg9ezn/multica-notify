package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateReplacesEveryPlaceholder(t *testing.T) {
	got, err := Generate("192.168.0.231")
	require.NoError(t, err)

	assert.NotContains(t, got, "BRIDGE_HOST_PLACEHOLDER")
	assert.Contains(t, got, `"net:192.168.0.231"`)
	assert.Contains(t, got, "https://192.168.0.231:9097/hooks/issue-status")
	// Count occurrences: three transport URLs must all be filled.
	assert.Equal(t, 3, strings.Count(got, "192.168.0.231:9097"))
}

func TestGenerateRequiresHost(t *testing.T) {
	_, err := Generate("   ")
	assert.Error(t, err, "blank host accepted")
}

func TestWriteRefusesOverwriteUnlessForced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "multica.plugin.json")

	require.NoError(t, Write(path, "host1", false))
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(first), "host1")

	err = Write(path, "host2", false)
	require.Error(t, err, "existing file must not be silently overwritten")

	require.NoError(t, Write(path, "host2", true))
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(second), "host2")
	assert.NotContains(t, string(second), "host1")
}
