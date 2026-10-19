package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogger(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		journal bool
		want    string
	}{
		{"journal, info level", "", true, "<6>info 1\n<4>warn 2\n<3>error 3\n"},
		{"journal, debug level", "debug", true, "<7>debug 0\n<6>info 1\n<4>warn 2\n<3>error 3\n"},
		{"journal, warn level", "WARN", true, "<4>warn 2\n<3>error 3\n"},
		{"unknown level falls back to info", "loud", true, "<4>unknown PENGUINTIER_LOG level \"loud\" (using info)\n<6>info 1\n<4>warn 2\n<3>error 3\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := newLogger(&buf, tt.level, tt.journal)
			l.Debugf("debug %d", 0)
			l.Infof("info %d", 1)
			l.Warnf("warn %d", 2)
			l.Errorf("error %d", 3)
			if buf.String() != tt.want {
				t.Errorf("logged\n%q\nwant\n%q", buf.String(), tt.want)
			}
		})
	}

	// On a terminal: a time and a level word, and one line per message.
	var buf bytes.Buffer
	newLogger(&buf, "", false).Warnf("two\nlines")
	if got := buf.String(); !strings.HasSuffix(got, " WARN  two lines\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("terminal line = %q", got)
	}
}
