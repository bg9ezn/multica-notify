package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func info(version string, settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: version}, Settings: settings}, true
	}
}

func TestResolvePrefersLdflagsInjection(t *testing.T) {
	got := resolve("v0.1.0-5-gc892d98", func() (*debug.BuildInfo, bool) { return nil, false })
	assert.Equal(t, "v0.1.0-5-gc892d98", got,
		"ldflags value must win even when build info is unavailable")
}

func TestResolveTrimsLdflagsWhitespace(t *testing.T) {
	assert.Equal(t, "v0.1.0", resolve(" v0.1.0 ", func() (*debug.BuildInfo, bool) { return nil, false }))
}

func TestResolveFallsBackToModuleVersion(t *testing.T) {
	// go install ...@v1.2.3 records the module version; "(devel)" marks a
	// plain build and must NOT be reported as a version.
	got := resolve("", info("v0.1.0"))
	assert.Equal(t, "v0.1.0", got)

	got = resolve("", info("(devel)",
		debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"}),
	)
	assert.Equal(t, "devel+0123456789ab", got, "(devel) must fall through to VCS revision")
}

func TestResolveVCSSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{
			name:     "clean tree",
			settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}},
			want:     "devel+0123456789ab",
		},
		{
			name: "dirty tree",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef"},
				{Key: "vcs.modified", Value: "true"},
			},
			want: "devel+0123456789ab-dirty",
		},
		{
			name:     "no vcs info at all",
			settings: nil,
			want:     "dev",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolve("", info("(devel)", tc.settings...)))
		})
	}
}

func TestResolveWithoutBuildInfo(t *testing.T) {
	assert.Equal(t, "dev", resolve("", func() (*debug.BuildInfo, bool) { return nil, false }))
}
