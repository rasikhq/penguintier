package ztapi

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
)

// How errors work here (Go has no exceptions):
//
// Functions return an error value as their last result; nil means success.
// The variables below are "sentinel" errors: fixed values callers compare
// against. Errors are usually wrapped with more context on the way up,
// using fmt.Errorf("...: %w", err). The %w verb keeps the original error
// inside the new one, and errors.Is(err, ErrServiceDown) looks through all
// the wrapping layers to find it. So always use errors.Is, never ==.
//
// The struct types further down (HTTPError, ProtocolError, ...) carry extra
// data. Check for those with errors.As, which also looks through wrapping:
//
//	var httpErr *ztapi.HTTPError
//	if errors.As(err, &httpErr) {
//		fmt.Println("status code", httpErr.StatusCode)
//	}

var (
	// ErrNotInstalled means the service did not answer and no zerotier-one
	// program was found on this machine.
	ErrNotInstalled = errors.New("the ZeroTier One service is not installed")

	// ErrServiceDown means the service did not answer (nothing listening,
	// or the connection was closed before a reply), but ZeroTier is
	// installed.
	ErrServiceDown = errors.New("the ZeroTier One service is not running")

	// ErrTokenMissing means no API token was found, so only Health can be
	// called. The tray offers "Authorize Penguintier…" for this.
	ErrTokenMissing = errors.New("no ZeroTier API token found")

	// ErrUnauthorized means the service answered 401: the token is wrong,
	// usually because the service created a new one.
	ErrUnauthorized = errors.New("the ZeroTier service rejected the API token")

	// ErrTimeout means the service did not reply within the time limit
	// (2 seconds by default).
	ErrTimeout = errors.New("the ZeroTier service did not reply in time")
)

// HTTPError is an HTTP reply with an unexpected status code (anything that
// is not 2xx and not 401, which is ErrUnauthorized).
type HTTPError struct {
	Method     string // "GET", "POST", ...
	Path       string // e.g. "/network/8056c2e21c000001"
	StatusCode int    // e.g. 404
	Body       string // the start of the reply body, for debugging
}

// Error makes *HTTPError satisfy Go's built-in error interface, which is
// just "has an Error() string method". Go interfaces are satisfied
// implicitly: there is no "implements" keyword.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode))
}

// IsNotFound reports whether err is an HTTP 404 from the service, for
// example from Network or Leave with an ID that is not joined.
func IsNotFound(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

// ProtocolError means the service replied, but not with what we expected,
// for example a body that is not valid JSON.
type ProtocolError struct {
	Method string
	Path   string
	Err    error // what went wrong, e.g. a JSON syntax error
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("%s %s: unexpected reply from the ZeroTier service: %v", e.Method, e.Path, e.Err)
}

// Unwrap lets errors.Is and errors.As look inside, at e.Err.
func (e *ProtocolError) Unwrap() error {
	return e.Err
}

// InsecureTokenFileError means a token file belongs to another user, or
// other users can read it. Penguintier refuses to use such a file: anyone
// who can read the token can control this machine's ZeroTier networks.
type InsecureTokenFileError struct {
	Path     string
	Mode     fs.FileMode // the file's permission bits, e.g. 0644
	OwnerUID int         // who owns the file
	WantUID  int         // the user running Penguintier
}

func (e *InsecureTokenFileError) Error() string {
	if e.OwnerUID != e.WantUID {
		return fmt.Sprintf("refusing to use token file %s because it belongs to user ID %d, not to you (user ID %d)",
			e.Path, e.OwnerUID, e.WantUID)
	}
	return fmt.Sprintf("refusing to use token file %s because other users can read or change it (mode %04o). Fix it with: chmod 600 %s",
		e.Path, e.Mode.Perm(), e.Path)
}

// Fixable reports whether FixTokenFileMode can fix the problem: the file is
// ours and only its permissions are too open.
func (e *InsecureTokenFileError) Fixable() bool {
	return e.OwnerUID == e.WantUID
}

// InvalidTokenError means a token does not look like a ZeroTier API token
// (24 characters, a-z and 0-9).
type InvalidTokenError struct {
	Path string // the file it came from; "" if it did not come from a file
}

func (e *InvalidTokenError) Error() string {
	// The token itself is never put in an error message: errors end up in
	// logs and bug reports.
	if e.Path == "" {
		return "invalid ZeroTier API token: want 24 characters, a-z and 0-9"
	}
	return fmt.Sprintf("invalid ZeroTier API token in %s: want 24 characters, a-z and 0-9", e.Path)
}
