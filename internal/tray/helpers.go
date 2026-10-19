package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Where helper programs (penguintier-ui, penguintier-token-helper) are
// installed. See "Install paths" in docs/architecture.md.
const (
	// LibexecDir is where the packages install them.
	LibexecDir = "/usr/libexec/penguintier"
	// EnvLibexecDir names a directory to look in first.
	EnvLibexecDir = "PENGUINTIER_LIBEXEC_DIR"
)

// libexecDir is LibexecDir, as a variable only so tests can point it at
// an empty directory (a machine with Penguintier installed would otherwise
// find the real helpers). Nothing else changes it.
var libexecDir = LibexecDir

// FindHelper returns the path of the helper program called name, looking
// in this order:
//
//  1. $PENGUINTIER_LIBEXEC_DIR
//  2. the directory of the running penguintier program
//  3. /usr/libexec/penguintier
//  4. extraDirs, in order (the tray passes ui/build/bin in development)
//
// "extraDirs ...string" makes FindHelper "variadic": callers pass zero or
// more extra strings, and the function sees them as a []string.
func FindHelper(name string, extraDirs ...string) (string, error) {
	var dirs []string
	if dir := os.Getenv(EnvLibexecDir); dir != "" {
		dirs = append(dirs, dir)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	dirs = append(dirs, libexecDir)
	dirs = append(dirs, extraDirs...)

	for _, dir := range dirs {
		path, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if isExecutable(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found in %s", name, strings.Join(dirs, ", "))
}

// isExecutable reports whether path is a regular file that may be run.
func isExecutable(path string) bool {
	// gosec flags paths built from environment variables. Here that is the
	// point: $PENGUINTIER_LIBEXEC_DIR is a documented override, set by the
	// user who runs the tray.
	info, err := os.Stat(path) //nolint:gosec // documented helper locations
	if err != nil {
		return false
	}
	// 0o111 are the three "x" bits (owner, group, others) of the mode.
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
