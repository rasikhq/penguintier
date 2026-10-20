package main

import (
	"log/slog"
	"strings"
)

// envLog names the environment variable that picks the log level. The DNS
// daemon reads the same variable (cmd/penguintier-dnsd/log.go), and
// docs/architecture.md and the man page describe it.
const envLog = "PENGUINTIER_LOG"

// setLogLevel applies $PENGUINTIER_LOG to the tray's log. The tray logs
// through log/slog's default logger, which writes one line per message to
// stderr (the journal, when the tray runs in the background). A name it
// doesn't know keeps the default, info, and says so.
func setLogLevel(name string) {
	level, ok := parseLevel(name)
	slog.SetLogLoggerLevel(level)
	if !ok {
		slog.Warn("unknown "+envLog+" level, using info", "value", name)
	}
}

// parseLevel turns a level name into a slog.Level: "debug", "info", "warn"
// (or "warning") and "error", in any case, and "" for info. ok is false
// for any other name; the level is info then.
func parseLevel(name string) (level slog.Level, ok bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "info":
		return slog.LevelInfo, true
	case "debug":
		return slog.LevelDebug, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}
