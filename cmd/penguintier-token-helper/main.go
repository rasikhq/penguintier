// Command penguintier-token-helper prints ZeroTier's local API token.
//
// The tray runs it through pkexec (polkit action
// io.github.rasikhq.Penguintier.read-token), so it runs as root and can read
// /var/lib/zerotier-one/authtoken.secret. The tray then saves the token to
// ~/.zeroTierOneAuthToken (mode 0600).
//
// It takes no arguments and reads only that one fixed path, so the polkit
// prompt can't be used to read any other file. It prints the token only if
// it looks like one (24 characters, a-z and 0-9): even if the path were
// replaced by a link to another file, that file's content is not printed.
// It uses only the Go standard library, on purpose: this program runs as
// root, so it should be small enough to read in one sitting.
//
// Output: the token and a newline on stdout; error messages on stderr.
//
// Exit codes:
//
//	0  the token was printed
//	1  the token file could not be read (missing, permission denied,
//	   not a regular file)
//	2  wrong usage: arguments were given
//	3  the file does not contain a valid token
//
// When it is started through pkexec, pkexec itself exits with 126 if the
// user dismissed the password prompt, and with 127 if authorization failed
// or no polkit agent is running.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// tokenPath is the service's token file. It is a constant on purpose: the
// helper must never read a path chosen by its caller.
const tokenPath = "/var/lib/zerotier-one/authtoken.secret" //nolint:gosec // a file path, not a credential

// Exit codes (see the package comment).
const (
	exitOK           = 0
	exitReadError    = 1
	exitUsage        = 2
	exitInvalidToken = 3
)

// tokenLength is the length of a ZeroTier API token. This repeats
// internal/ztapi.TokenLength instead of importing it, to keep this root
// program self-contained; main_test.go checks that the two agree.
const tokenLength = 24

func main() {
	os.Exit(run(tokenPath, os.Args[1:], os.Stdout, os.Stderr))
}

// run does the work. The path is a parameter only so tests can use a
// temporary file; main always passes tokenPath.
func run(path string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: pkexec /usr/libexec/penguintier/penguintier-token-helper (takes no arguments)")
		return exitUsage
	}

	token, err := readToken(path)
	if err != nil {
		fmt.Fprintln(stderr, "penguintier-token-helper:", err)
		return exitReadError
	}
	if !validToken(token) {
		// Never print the content: it may not be a token at all.
		fmt.Fprintf(stderr, "penguintier-token-helper: %s does not contain a valid ZeroTier API token\n", path)
		return exitInvalidToken
	}

	fmt.Fprintln(stdout, token)
	return exitOK
}

// readToken reads the token file, without surrounding spaces or newlines.
func readToken(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is the fixed tokenPath outside tests
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	defer f.Close() // read-only file: a close error can't lose data

	// Refuse anything that is not a plain file (a device or a pipe could
	// make us read forever).
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read token: %s is not a regular file", path)
	}

	// A token is 24 bytes; read a little more so a longer file is noticed
	// (and rejected by validToken), but never a lot.
	data, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// validToken reports whether s is exactly 24 characters of a-z and 0-9.
func validToken(s string) bool {
	if len(s) != tokenLength {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
