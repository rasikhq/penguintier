package main

import "testing"

// TestSetProgramName checks that GLib reports the App ID as the program
// name afterwards. The X11 WM_CLASS and the Wayland app ID come from it.
func TestSetProgramName(t *testing.T) {
	setProgramName(appID)
	if got := programName(); got != appID {
		t.Errorf("program name = %q, want %q", got, appID)
	}
}
