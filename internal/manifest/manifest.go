// Package manifest generates the Multica plugin manifest for this bridge.
// The template (template.json) is the single source of truth: one file, one
// placeholder (BRIDGE_HOST_PLACEHOLDER) that appears in every transport URL
// and in the net: egress scope.
package manifest

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const placeholder = "BRIDGE_HOST_PLACEHOLDER"

//go:embed template.json
var template string

// Generate renders the plugin manifest for a bridge host: the placeholder is
// replaced in every transport URL and in the net: scope (exact host, as
// Multica requires).
func Generate(bridgeHost string) (string, error) {
	host := strings.TrimSpace(bridgeHost)
	if host == "" {
		return "", fmt.Errorf("bridge host is required")
	}
	if !strings.Contains(template, placeholder) {
		return "", fmt.Errorf("manifest template is missing the %s placeholder", placeholder)
	}
	return strings.ReplaceAll(template, placeholder, host), nil
}

// Write generates the manifest and stores it at path, creating parent
// directories as needed. An existing file is never overwritten unless force
// is set.
func Write(path, bridgeHost string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	content, err := Generate(bridgeHost)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
