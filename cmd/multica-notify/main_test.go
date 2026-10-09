package main

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlogLevel(t *testing.T) {
	cases := []struct {
		name           string
		quiet, verbose int
		want           slog.Level
	}{
		{"default is info", 0, 0, slog.LevelInfo},
		{"one -q is error-only", 1, 0, slog.LevelError},
		{"quiet wins over verbose", 2, 3, slog.LevelError},
		{"one -v is debug", 0, 1, slog.LevelDebug},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, slogLevel(tc.quiet, tc.verbose))
		})
	}
}
