package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// level is a log level. iota counts up from 0 inside a const block, so
// levelDebug < levelInfo < levelWarn < levelError.
type level int

const (
	levelDebug level = iota
	levelInfo
	levelWarn
	levelError
)

// logger writes one line per message to stderr. Under systemd, stderr goes
// to the journal, and a "<N>" prefix tells journald the priority (7 debug,
// 6 info, 4 warning, 3 error; see sd-daemon(3)), so `journalctl -p warning`
// works. On a terminal it prints a time and a level word instead.
//
// It is deliberately tiny: the daemon logs a few lines an hour.
type logger struct {
	w       io.Writer
	min     level
	journal bool
}

// newLogger makes a logger. levelName is $PENGUINTIER_LOG ("debug", "info",
// "warn" or "error"; "" means info). journal says whether stderr goes to
// the journal (systemd sets $JOURNAL_STREAM then).
func newLogger(w io.Writer, levelName string, journal bool) *logger {
	l := &logger{w: w, min: levelInfo, journal: journal}
	switch strings.ToLower(strings.TrimSpace(levelName)) {
	case "debug":
		l.min = levelDebug
	case "", "info":
		l.min = levelInfo
	case "warn", "warning":
		l.min = levelWarn
	case "error":
		l.min = levelError
	default:
		l.Warnf("unknown PENGUINTIER_LOG level %q; using info", levelName)
	}
	return l
}

// The "format string, args ...any" pair works like fmt.Printf.

func (l *logger) Debugf(format string, args ...any) { l.log(levelDebug, format, args...) }
func (l *logger) Infof(format string, args ...any)  { l.log(levelInfo, format, args...) }
func (l *logger) Warnf(format string, args ...any)  { l.log(levelWarn, format, args...) }
func (l *logger) Errorf(format string, args ...any) { l.log(levelError, format, args...) }

func (l *logger) log(lvl level, format string, args ...any) {
	if lvl < l.min {
		return
	}
	msg := fmt.Sprintf(format, args...)
	// One message is one journal entry: keep it on one line.
	msg = strings.ReplaceAll(msg, "\n", " ")

	var line string
	if l.journal {
		line = fmt.Sprintf("<%d>%s\n", journalPriority(lvl), msg)
	} else {
		line = fmt.Sprintf("%s %-5s %s\n", time.Now().Format(time.TimeOnly), levelWord(lvl), msg)
	}
	// Nothing sensible to do if stderr is gone.
	_, _ = io.WriteString(l.w, line)
}

func journalPriority(lvl level) int {
	switch lvl {
	case levelDebug:
		return 7
	case levelWarn:
		return 4
	case levelError:
		return 3
	default:
		return 6
	}
}

func levelWord(lvl level) string {
	switch lvl {
	case levelDebug:
		return "DEBUG"
	case levelWarn:
		return "WARN"
	case levelError:
		return "ERROR"
	default:
		return "INFO"
	}
}
