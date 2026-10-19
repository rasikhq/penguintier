package tray

import (
	"context"
	"errors"
)

// Hooks are the menu features that phase P5 (desktop integration)
// provides. StubHooks stands in for them until then.
//
// An interface lists methods a type must have. Any type with these three
// methods can be used as Hooks; it doesn't have to say so (there is no
// "implements" keyword in Go).
type Hooks interface {
	// Authorize gets the ZeroTier API token (pkexec + the token helper)
	// and saves the user's copy. It may wait a long time for a password
	// prompt, so the controller calls it from its own goroutine. The tray
	// picks up the new token on its next poll.
	Authorize(ctx context.Context) error
	// StartAtLogin reports whether Penguintier starts when the user logs
	// in.
	StartAtLogin() bool
	// SetStartAtLogin turns starting at login on or off.
	SetStartAtLogin(on bool) error
}

// manualAuthorizeCommand gives the user a token copy by hand, until
// "Authorize Penguintier…" works.
const manualAuthorizeCommand = "(umask 077; sudo cat /var/lib/zerotier-one/authtoken.secret > ~/.zeroTierOneAuthToken)"

// StubHooks is a stand-in Hooks: Authorize explains the manual way, and
// Start at Login stays off.
type StubHooks struct{}

// Authorize returns an error that tells the user what to run instead.
func (StubHooks) Authorize(context.Context) error {
	return errors.New("not available yet. For now, run this in a terminal: " + manualAuthorizeCommand)
}

// StartAtLogin always reports false.
func (StubHooks) StartAtLogin() bool {
	return false
}

// SetStartAtLogin always fails.
func (StubHooks) SetStartAtLogin(bool) error {
	return errors.New("not available yet")
}
