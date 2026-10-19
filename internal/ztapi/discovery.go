package ztapi

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Fixed locations, from docs/architecture.md.
const (
	// DefaultPort is the API port when zerotier-one.port can't be read.
	DefaultPort = 9993

	// SystemTokenPath is the service's own token. It is normally readable
	// by root only; penguintier-token-helper reads it through pkexec.
	SystemTokenPath = "/var/lib/zerotier-one/authtoken.secret" //nolint:gosec // a file path, not a credential

	// SystemPortPath holds the port the service listens on, e.g. "9993".
	SystemPortPath = "/var/lib/zerotier-one/zerotier-one.port"

	// EnvAPIURL overrides where the API is, e.g. "http://127.0.0.1:19993"
	// for zt-mock.
	EnvAPIURL = "PENGUINTIER_API_URL"

	// EnvTokenFile names a token file to try before the usual ones.
	EnvTokenFile = "PENGUINTIER_TOKEN_FILE"

	// localAuthFileName is the second token file looked for in $HOME.
	localAuthFileName = ".zerotier-local-auth"

	// maxTokenFileSize is far more than a 24-character token needs. It stops
	// us from reading a huge file by mistake.
	maxTokenFileSize = 4096
)

// These are variables, not constants, only so tests can point them at
// temporary files. Nothing else changes them.
var (
	systemTokenPath = SystemTokenPath
	systemPortPath  = SystemPortPath

	// zeroTierPrograms are the places a zerotier-one package installs the
	// service program. If none exists, ZeroTier is not installed.
	zeroTierPrograms = []string{
		"/usr/sbin/zerotier-one", // Debian, Ubuntu, Fedora
		"/usr/bin/zerotier-one",  // Arch
		"/usr/local/sbin/zerotier-one",
		"/usr/local/bin/zerotier-one",
	}
)

// FindToken looks for the API token, in this order:
//
//  1. the file named by $PENGUINTIER_TOKEN_FILE
//  2. ~/.zeroTierOneAuthToken
//  3. ~/.zerotier-local-auth
//  4. /var/lib/zerotier-one/authtoken.secret, if this user can read it
//
// It returns the token and the file it came from. The first file that
// exists wins: if that file is unsafe or malformed, FindToken returns that
// error instead of silently trying the next file, so the problem gets
// fixed. If no file is found it returns ErrTokenMissing.
func FindToken() (token, path string, err error) {
	// 1. An explicitly configured file must exist.
	if envPath := os.Getenv(EnvTokenFile); envPath != "" {
		token, err := ReadTokenFile(envPath)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", EnvTokenFile, err)
		}
		return token, envPath, nil
	}

	// 2 and 3. Files in the home directory. If $HOME is not set we just
	// skip them.
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range []string{UserTokenFileName, localAuthFileName} {
			p := filepath.Join(home, name)
			token, err := ReadTokenFile(p)
			if errors.Is(err, fs.ErrNotExist) {
				continue // not there: try the next file
			}
			if err != nil {
				return "", "", err
			}
			return token, p, nil
		}
	}

	// 4. The service's own file. This is not a private file of ours (it
	// belongs to root or the zerotier-one user), so the owner check does
	// not apply. As a normal user we usually get "permission denied",
	// which just means "not found".
	token, err = readToken(systemTokenPath, false)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return "", "", ErrTokenMissing
	}
	if err != nil {
		return "", "", err
	}
	return token, systemTokenPath, nil
}

// ReadTokenFile reads a token from a private file of the current user.
//
// It returns an *InsecureTokenFileError if the file belongs to someone else
// or other users can read or change it, and an *InvalidTokenError if the
// content is not a token. A missing file gives an error that matches
// fs.ErrNotExist (check with errors.Is).
func ReadTokenFile(path string) (string, error) {
	return readToken(path, true)
}

// readToken reads and checks a token file. checkPrivate turns on the
// owner and permission check.
func readToken(path string, checkPrivate bool) (string, error) {
	f, err := os.Open(path) //nolint:gosec // reading a token file is the whole point
	if errors.Is(err, fs.ErrPermission) && checkPrivate {
		// We may not read it, but we can still see who owns it. A token
		// copied with sudo belongs to root: say that, not just
		// "permission denied".
		if info, statErr := os.Stat(path); statErr == nil { //nolint:gosec // the same token path we just tried to open
			if ownerErr := checkPrivateFile(path, info); ownerErr != nil {
				return "", ownerErr
			}
		}
	}
	if err != nil {
		return "", fmt.Errorf("open token file: %w", err)
	}
	defer f.Close() // read-only file: a close error can't lose data

	// Stat the file we opened (not the path again), so the file can't be
	// swapped between the check and the read.
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat token file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s is not a regular file", path)
	}
	if checkPrivate {
		if err := checkPrivateFile(path, info); err != nil {
			return "", err
		}
	}

	data, err := io.ReadAll(io.LimitReader(f, maxTokenFileSize))
	if err != nil {
		return "", fmt.Errorf("read token file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if !ValidToken(token) {
		return "", &InvalidTokenError{Path: path}
	}
	return token, nil
}

// checkPrivateFile returns an *InsecureTokenFileError unless the file
// belongs to the current user and only that user can read or write it.
func checkPrivateFile(path string, info fs.FileInfo) error {
	// info.Sys() holds the raw, OS-specific stat data as an "any" value.
	// The ".(*syscall.Stat_t)" is a type assertion: "I expect this to be a
	// *syscall.Stat_t". With the two-value form, ok is false instead of a
	// crash if it isn't.
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("token file %s: cannot read its owner", path)
	}
	owner := int(stat.Uid)
	me := os.Geteuid()
	// 0o077 is the group and "other" bits (rwx for each). Any of them set
	// means someone besides the owner can read or change the token.
	if owner != me || info.Mode().Perm()&0o077 != 0 {
		return &InsecureTokenFileError{Path: path, Mode: info.Mode().Perm(), OwnerUID: owner, WantUID: me}
	}
	return nil
}

// APIURLs returns the base URLs to try, in order. Normally that is
// http://127.0.0.1:PORT, then http://[::1]:PORT, with PORT from Port.
// $PENGUINTIER_API_URL replaces both.
func APIURLs() ([]string, error) {
	if override := os.Getenv(EnvAPIURL); override != "" {
		u, err := url.Parse(override)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("%s=%q: want a URL such as http://127.0.0.1:9993", EnvAPIURL, override)
		}
		return []string{strings.TrimRight(override, "/")}, nil
	}

	port := strconv.Itoa(Port())
	// net.JoinHostPort adds the [brackets] an IPv6 address needs in a URL.
	return []string{
		"http://" + net.JoinHostPort("127.0.0.1", port),
		"http://" + net.JoinHostPort("::1", port),
	}, nil
}

// Port returns the service's API port: the number in
// /var/lib/zerotier-one/zerotier-one.port, or DefaultPort (9993) if that
// file is missing or doesn't hold a valid port.
func Port() int {
	data, err := os.ReadFile(systemPortPath)
	if err != nil {
		return DefaultPort // missing or unreadable: use the default
	}
	// strconv.Atoi turns "9993" into the number 9993.
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port < 1 || port > 65535 {
		return DefaultPort
	}
	return port
}

// zeroTierInstalled reports whether the zerotier-one program exists.
func zeroTierInstalled() bool {
	for _, p := range zeroTierPrograms {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
