package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed example.yaml
var exampleYAML string

// ExampleYAML returns the shipped annotated example configuration. It is
// embedded from internal/config/example.yaml — the single source of truth —
// and TestEmbeddedExampleIsValid keeps it in lockstep with the config fields.
func ExampleYAML() string {
	return exampleYAML
}

// InitConfig writes the example configuration to path, creating parent
// directories as needed. An existing file is never overwritten unless force
// is set.
func InitConfig(path string, force bool) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use -force to overwrite)", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}
	if err := os.WriteFile(path, []byte(exampleYAML), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
