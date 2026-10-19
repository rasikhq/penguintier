package instance

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// BusName is the name the running copy owns: the app ID.
const BusName = "io.github.rasikhq.Penguintier"

// ErrAlreadyRunning means another copy of Penguintier owns BusName.
var ErrAlreadyRunning = errors.New("Penguintier is already running") //nolint:revive,staticcheck // "Penguintier" is a name, so it starts with a capital

// Lock is the claimed name. It stays claimed until Release, or until the
// program exits.
type Lock struct {
	conn *dbus.Conn
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
	// DoNotQueue: if the name is taken, say so at once instead of putting
	// us in line to get it when the owner leaves.
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, fmt.Errorf("claim D-Bus name %s: %w", BusName, err)
	}
	switch reply {
	case dbus.RequestNameReplyPrimaryOwner, dbus.RequestNameReplyAlreadyOwner:
		return &Lock{conn: conn}, nil
	case dbus.RequestNameReplyExists:
		return nil, ErrAlreadyRunning
	default:
		// RequestNameReplyInQueue can't happen with DoNotQueue.
		return nil, fmt.Errorf("claim D-Bus name %s: unexpected reply %d", BusName, reply)
	}
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
