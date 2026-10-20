package tray

import (
	"context"
	"errors"
)

// Hooks are the menu features that cmd/penguintier provides: they touch
// the rest of the desktop, so they live outside this package. StubHooks
// stands in for them in tests and tools.
//
// An interface lists methods a type must have. Any type with these
// methods can be used as Hooks; it doesn't have to say so (there is no
// "implements" keyword in Go).
//
// Authorize and EnableDNS may wait a long time for the user (a password
// prompt, a question), so the controller calls them from their own
// goroutines. The other methods are called on the controller goroutine,
// one at a time.
type Hooks interface {
	// Authorize gets the ZeroTier API token (pkexec + the token helper)
	// and saves the user's copy. The tray picks up the new token on its
	// next poll. It returns ErrCancelled when the user dismissed the
	// password prompt.
	Authorize(ctx context.Context) error
	// StartAtLogin reports whether Penguintier starts when the user logs
	// in.
	StartAtLogin() bool
	// SetStartAtLogin turns starting at login on or off. It is only
	// called when the user clicks "Start at Login".
	SetStartAtLogin(on bool) error
	// Connected is called the first time in a run that the service
	// answers with a working token.
	Connected()
	// EnableDNS asks the user to confirm, then installs systemd-resolved
	// ("Enable DNS support…"). It returns ErrCancelled when the user said
	// no.
	EnableDNS(ctx context.Context) error
}

// ErrCancelled is what Hooks return when the user cancelled: dismissed a
// password prompt, or answered "Cancel". The tray shows nothing then.
var ErrCancelled = errors.New("cancelled by the user")

// StubHooks is a stand-in Hooks: Authorize explains the manual way, Start
// at Login stays off, and EnableDNS explains the manual install.
type StubHooks struct{}

// Authorize returns an error that tells the user where to find the
// manual way.
func (StubHooks) Authorize(context.Context) error {
	return errors.New("not available here. Run “penguintier doctor” in a terminal for the manual way")
}

// StartAtLogin always reports false.
func (StubHooks) StartAtLogin() bool {
	return false
}

// SetStartAtLogin always fails.
func (StubHooks) SetStartAtLogin(bool) error {
	return errors.New("not available here")
}

// Connected does nothing.
func (StubHooks) Connected() {}

// EnableDNS returns an error that tells the user what to install.
func (StubHooks) EnableDNS(context.Context) error {
	return errors.New("not available here. Install the systemd-resolved package yourself")
}
