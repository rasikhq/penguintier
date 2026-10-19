// Package assets gives Go programs the image files they need at run time,
// built into the binary so nothing has to be installed next to it.
//
// It lives here, next to the files, because a //go:embed line can only
// reach files in its own directory or below it, never "../".
package assets

import "embed"

// Tray holds the tray icon states, read with
// Tray.ReadFile("tray/penguintier-online.png") (and -offline, -attention).
//
// The "//go:embed" comment is a compiler instruction, not a normal comment:
// at build time Go copies every matching file into the program and makes
// this variable a read-only file system holding them. It must sit right
// above the variable, with no blank line in between.
//
//go:embed tray/*.png
var Tray embed.FS
