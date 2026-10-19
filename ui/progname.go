package main

// This file uses cgo: the comment right above `import "C"` is C code that
// Go compiles along with this package, and C.something calls into it.
// The `#cgo pkg-config` line asks pkg-config for GLib's compiler flags, the
// same GLib that GTK and Wails use.

// #cgo pkg-config: glib-2.0
// #include <stdlib.h>
// #include <glib.h>
import "C"

import "unsafe"

// setProgramName sets the program name GTK uses for the Wayland app ID and
// for the X11 WM_CLASS ("io.github.rasikhq.Penguintier",
// "Io.github.rasikhq.Penguintier": GTK capitalizes the class part itself).
// Desktops match a window to its .desktop file by these (StartupWMClass,
// tried against the first part first), to show the app's icon in the
// taskbar and the task switcher.
//
// It must run before GTK starts. On X11, GTK writes WM_CLASS when it
// creates the window, and Wails v2 sets its Linux.ProgramName option only
// after that. Without this, X11 desktops such as Cinnamon saw
// "penguintier-ui" and showed a blank taskbar icon.
func setProgramName(name string) {
	// C.CString copies the Go string into memory that Go's garbage
	// collector doesn't manage, so it is freed by hand. defer runs the free
	// when this function returns. GLib keeps its own copy.
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.g_set_prgname(cname)
}

// programName returns the name GLib now uses for this program (for the
// test; Go test files can't use cgo themselves).
func programName() string {
	return C.GoString(C.g_get_prgname())
}
