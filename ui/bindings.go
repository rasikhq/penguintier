//go:build bindings

package main

// `wails build` and `wails dev` first build this program with the
// "bindings" build tag and run it with no arguments, to generate the
// TypeScript bindings in frontend/wailsjs. This file is only compiled in
// that build. It picks a screen so run reaches wails.Run instead of
// stopping with a usage error.
//
// It must be "about": in this mode wails.Run returns right away, and the
// About screen's exit code is 0. (Join's would be 1, i.e. "cancelled",
// which Wails reports as a failed build.)
func init() {
	defaultScreen = screenAbout
}
