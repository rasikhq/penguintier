package main

import (
	"context"
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name  string
		level slog.Level
		ok    bool
	}{
		{"", slog.LevelInfo, true},
		{"info", slog.LevelInfo, true},
		{"debug", slog.LevelDebug, true},
		{" DEBUG ", slog.LevelDebug, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"verbose", slog.LevelInfo, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level, ok := parseLevel(tt.name)
			if level != tt.level || ok != tt.ok {
				t.Errorf("parseLevel(%q) = %v, %t; want %v, %t", tt.name, level, ok, tt.level, tt.ok)
			}
		})
	}
}

// TestSetLogLevel: after setLogLevel("debug") the default logger lets debug
// messages through, and an unknown name falls back to info.
func TestSetLogLevel(t *testing.T) {
	// SetLogLoggerLevel returns the level that was set before; put it back
	// when the test ends, so other tests see the usual default.
	old := slog.SetLogLoggerLevel(slog.LevelInfo)
	t.Cleanup(func() { slog.SetLogLoggerLevel(old) })

	ctx := context.Background()
	setLogLevel("debug")
	if !slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Error(`after setLogLevel("debug"), debug messages are still off`)
	}
	setLogLevel("nonsense")
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		t.Error(`after an unknown level, debug messages are still on; want the info default`)
	}
	if !slog.Default().Enabled(ctx, slog.LevelInfo) {
		t.Error(`after an unknown level, info messages are off`)
	}
}
