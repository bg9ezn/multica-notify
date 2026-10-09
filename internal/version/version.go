// Package version resolves the binary's version string.
//
// Precedence:
//  1. the ldflags-injected Value (Makefile and release builds —
//     "v0.1.0" or "v0.1.0-5-gc892d98" via git describe);
//  2. the module version recorded by `go install ...@version`;
//  3. the VCS revision embedded by Go 1.18+ builds inside a git tree
//     ("devel+<sha>", "-dirty" when the tree had uncommitted changes);
//  4. "dev".
//
// This closes the go-install gap: binaries built outside the Makefile used to
// report a bare "dev" with no way to trace what was running.
package version

import (
	"runtime/debug"
	"strings"
)

// Value is overridden at build time:
// -ldflags "-X github.com/bg9ezn/multica-notify/internal/version.Value=$(VERSION)"
var Value = ""

// Get resolves the effective version.
func Get() string {
	return resolve(Value, debug.ReadBuildInfo)
}

func resolve(ldflagsValue string, buildInfo func() (*debug.BuildInfo, bool)) string {
	if v := strings.TrimSpace(ldflagsValue); v != "" {
		return v
	}
	info, ok := buildInfo()
	if !ok {
		return "dev"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		return "devel+" + rev + "-dirty"
	}
	return "devel+" + rev
}
