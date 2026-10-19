package resolved

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// The errors callers can check for with errors.Is. Every error from this
// package wraps one of these when the cause is known, and keeps resolved's
// own message after it, for the logs. For example:
//
//	SetLinkDNS link 12: the interface is managed by systemd-networkd: Link zt3jn5bvfq is managed.
var (
	// ErrNotRunning means systemd-resolved is not on the bus: it is not
	// installed, stopped, or restarting.
	ErrNotRunning = errors.New("systemd-resolved is not running")

	// ErrNoSuchLink means resolved does not know the interface (yet). It
	// learns about new interfaces from the kernel a moment after they
	// appear.
	ErrNoSuchLink = errors.New("systemd-resolved does not know this interface")

	// ErrLinkManaged means systemd-networkd manages the interface, so
	// resolved takes its DNS settings from networkd only and refuses
	// changes over D-Bus. The fix is a networkd .network file for the
	// interface, or telling networkd to leave zt* interfaces alone.
	ErrLinkManaged = errors.New("the interface is managed by systemd-networkd")

	// ErrAccessDenied means polkit said no: the caller is not root and no
	// polkit rule allows it (is 50-penguintier-dnsd.rules installed?).
	ErrAccessDenied = errors.New("not allowed to change systemd-resolved settings")

	// ErrNoInterface means there is no network interface with that name.
	ErrNoInterface = errors.New("network interface not found")
)

// D-Bus error names, from the D-Bus spec and from systemd's
// src/libsystemd/sd-bus/bus-common-errors.h.
const (
	errNameServiceUnknown  = "org.freedesktop.DBus.Error.ServiceUnknown"
	errNameNameHasNoOwner  = "org.freedesktop.DBus.Error.NameHasNoOwner"
	errNameAccessDenied    = "org.freedesktop.DBus.Error.AccessDenied"
	errNameInteractiveAuth = "org.freedesktop.DBus.Error.InteractiveAuthorizationRequired"
	errNameNoSuchLink      = "org.freedesktop.resolve1.NoSuchLink"
	// resolved answers LinkBusy for links that systemd-networkd manages
	// ("Link %s is managed.") and for loopback devices.
	errNameLinkBusy = "org.freedesktop.resolve1.LinkBusy"
)

// mapError turns an error from a D-Bus call into one that wraps one of the
// errors above when it can. op says what we were doing, e.g. "SetLinkDNS
// link 12", and starts the message.
func mapError(op string, err error) error {
	if err == nil {
		return nil
	}

	// errors.As looks through wrapped errors for one of the given type and,
	// if found, copies it into the variable. godbus returns D-Bus error
	// replies as dbus.Error values; the pointer form shows up too (our
	// tests' fake returns *dbus.Error), so check both.
	var name, message string
	var valueErr dbus.Error
	var pointerErr *dbus.Error
	switch {
	case errors.As(err, &valueErr):
		name, message = valueErr.Name, valueErr.Error()
	case errors.As(err, &pointerErr):
		name, message = pointerErr.Name, pointerErr.Error()
	default:
		// Not a D-Bus error reply: a timeout, a cancelled context, a lost
		// connection. Keep it as it is (wrapped, so errors.Is still works
		// for context.DeadlineExceeded and friends).
		return fmt.Errorf("%s: %w", op, err)
	}

	var sentinel error
	switch name {
	case errNameServiceUnknown, errNameNameHasNoOwner:
		sentinel = ErrNotRunning
	case errNameNoSuchLink:
		sentinel = ErrNoSuchLink
	case errNameLinkBusy:
		sentinel = ErrLinkManaged
	case errNameAccessDenied, errNameInteractiveAuth:
		sentinel = ErrAccessDenied
	default:
		// An error we have no name for: keep the D-Bus name, it is the
		// best clue in a bug report.
		return fmt.Errorf("%s: %s: %w", op, name, err)
	}
	return fmt.Errorf("%s: %w: %s", op, sentinel, message)
}
