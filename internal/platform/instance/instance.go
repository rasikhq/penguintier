package instance

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// BusName is the name the running copy owns: the app ID.
const BusName = "io.github.rasikhq.Penguintier"

// The running copy also exports a small object with the freedesktop
// "Application" interface, so a second copy can tell it "the user started
// Penguintier again" (Activate) before quitting.
const (
	objectPath = dbus.ObjectPath("/io/github/rasikhq/Penguintier")
	appIface   = "org.freedesktop.Application"
)

// ErrAlreadyRunning means another copy of Penguintier owns BusName.
var ErrAlreadyRunning = errors.New("Penguintier is already running") //nolint:revive,staticcheck // "Penguintier" is a name, so it starts with a capital

// Lock is the claimed name. It stays claimed until Release, or until the
// program exits.
type Lock struct {
	conn        *dbus.Conn
	activations chan struct{}
}

// application is the exported object. godbus turns its exported methods
// into D-Bus methods; the last result must be a *dbus.Error (nil means
// success). godbus calls them from its own goroutines.
type application struct {
	activations chan struct{}
}

// Activate is org.freedesktop.Application's Activate method. Its one
// argument, "platform data", holds desktop details (such as a startup
// token) that Penguintier doesn't need, so it has the blank name "_".
func (a *application) Activate(_ map[string]dbus.Variant) *dbus.Error {
	// A select with a default case never waits: if an activation is
	// already waiting to be handled, this one adds nothing.
	select {
	case a.activations <- struct{}{}:
	default:
	}
	return nil
}

// Claim takes BusName on the session bus. It returns ErrAlreadyRunning
// when another copy has it.
//
// It opens a private connection (not dbus.SessionBus, which the tray icon
// library shares), so Release can close it without disturbing the tray.
func Claim() (*Lock, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the session bus: %w", err)
	}
	lock, err := claimOn(conn)
	if err != nil {
		_ = conn.Close() // the claim error is the one worth reporting
		return nil, err
	}
	return lock, nil
}

// claimOn does the work of Claim on an open connection. Tests call it
// with a connection to a private bus.
func claimOn(conn *dbus.Conn) (*Lock, error) {
	// Export the object first, so it is there as soon as others can see
	// the name. The channel holds one activation while none is handled.
	activations := make(chan struct{}, 1)
	if err := conn.Export(&application{activations: activations}, objectPath, appIface); err != nil {
		return nil, fmt.Errorf("export %s: %w", objectPath, err)
	}

	// DoNotQueue: if the name is taken, say so at once instead of putting
	// us in line to get it when the owner leaves.
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("claim D-Bus name %s: %w", BusName, err)
	}
	switch reply {
	case dbus.RequestNameReplyPrimaryOwner, dbus.RequestNameReplyAlreadyOwner:
		return &Lock{conn: conn, activations: activations}, nil
	case dbus.RequestNameReplyExists:
		return nil, ErrAlreadyRunning
	default:
		// RequestNameReplyInQueue can't happen with DoNotQueue.
		return nil, fmt.Errorf("claim D-Bus name %s: unexpected reply %d", BusName, reply)
	}
}

// Activations receives a value each time another copy calls Activate,
// that is, each time the user starts Penguintier while it runs.
func (l *Lock) Activations() <-chan struct{} {
	return l.activations
}

// Activate tells the running copy that the user started Penguintier
// again. The second copy calls it after Claim returned ErrAlreadyRunning,
// so the running copy can say so: a notification from the running copy
// stays, while one from a copy that quits at once is removed by GNOME
// together with its sender.
func Activate(ctx context.Context) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to the session bus: %w", err)
	}
	defer conn.Close()
	return activateOn(ctx, conn)
}

// activateOn does the work of Activate on an open connection.
func activateOn(ctx context.Context, conn *dbus.Conn) error {
	// An empty map: no platform data. FlagNoAutoStart: never start a copy
	// through D-Bus activation; one is already running.
	call := conn.Object(BusName, objectPath).CallWithContext(ctx, appIface+".Activate",
		dbus.FlagNoAutoStart, map[string]dbus.Variant{})
	if call.Err != nil {
		return fmt.Errorf("activate the running Penguintier: %w", call.Err)
	}
	return nil
}

// Lost returns a channel that is closed when the connection to the session
// bus is lost. That happens when the user logs out: the session bus stops,
// and a tray left running would have no icon and no menu, so it should
// quit. (A channel that is closed, never sent on, is Go's usual "this
// happened" signal; ctx.Done() works the same way.)
func (l *Lock) Lost() <-chan struct{} {
	return l.conn.Context().Done()
}

// Release gives the name back, so another copy can start.
func (l *Lock) Release() error {
	// Closing the connection alone would free the name too, but the bus
	// handles that a moment later. ReleaseName waits for the bus's answer,
	// so the name is free when Release returns.
	_, releaseErr := l.conn.ReleaseName(BusName)
	closeErr := l.conn.Close()
	// errors.Join returns nil when both are nil, otherwise an error
	// holding the ones that are not.
	return errors.Join(releaseErr, closeErr)
}
