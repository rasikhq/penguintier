package packagekit

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// Names on the system bus.
const (
	busName          = "org.freedesktop.PackageKit"
	objectPath       = dbus.ObjectPath("/org/freedesktop/PackageKit")
	managerIface     = "org.freedesktop.PackageKit"
	transactionIface = "org.freedesktop.PackageKit.Transaction"
)

// Numbers from PackageKit's enums (pk-enum.h). Filters and transaction
// flags are bit fields: the enum value is the number of the bit to set,
// so the filter "newest" (16) is sent as 1<<16.
const (
	filterNewest         = 16 // only the newest version of each package
	filterArch           = 18 // only this machine's architecture
	flagOnlyTrusted      = 1  // only signed packages from configured sources
	infoInstalled        = 1  // Package signal: this version is installed
	exitSuccess          = 1  // Finished signal: it worked
	exitCancelled        = 3
	errorPackageNotFound = 8
	errorNotAuthorized   = 48 // polkit said no, or the prompt was dismissed
)

// The errors Install returns for the common cases. Check them with
// errors.Is.
var (
	// ErrNotAvailable: PackageKit is not installed.
	ErrNotAvailable = errors.New("PackageKit is not installed")
	// ErrNotAuthorized: the password prompt was dismissed, or the user may
	// not install software. PackageKit doesn't tell those apart.
	ErrNotAuthorized = errors.New("not authorized to install software")
	// ErrNotFound: no package of that name in the configured sources.
	ErrNotFound = errors.New("package not found")
	// ErrCancelled: the transaction was cancelled (for example in GNOME
	// Software).
	ErrCancelled = errors.New("installation cancelled")
)

// Client talks to PackageKit over one D-Bus connection. Use it from one
// goroutine at a time.
type Client struct {
	conn *dbus.Conn
}

// Connect connects to the system bus.
func Connect() (*Client, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the system D-Bus: %w", err)
	}
	return &Client{conn: conn}, nil
}

// ConnectAddress connects to the bus at a D-Bus address. Tests use it with
// a private bus.
func ConnectAddress(address string) (*Client, error) {
	conn, err := dbus.Connect(address)
	if err != nil {
		return nil, fmt.Errorf("connect to D-Bus at %s: %w", address, err)
	}
	return &Client{conn: conn}, nil
}

// Close closes the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Install installs the package called name (such as "systemd-resolved")
// from the distribution's sources. It returns true, and installs nothing,
// when the package is installed already.
//
// PackageKit asks for an administrator password through polkit itself.
// Install waits until the installation has finished, which may take a
// minute; cancelling ctx stops waiting (PackageKit carries on).
func (c *Client) Install(ctx context.Context, name string) (alreadyInstalled bool, err error) {
	// 1. Resolve the name to a package ID such as
	// "systemd-resolved;257.13-1;amd64;debian-trixie-main". The Package
	// signal also says whether that version is installed.
	pkgs, err := c.transaction(ctx, "Resolve", uint64(1<<filterNewest|1<<filterArch), []string{name})
	if err != nil {
		return false, fmt.Errorf("look up %s: %w", name, err)
	}
	if len(pkgs) == 0 {
		return false, fmt.Errorf("look up %s: %w", name, ErrNotFound)
	}
	for _, p := range pkgs {
		if p.info == infoInstalled {
			return true, nil
		}
	}

	// 2. Install it.
	if _, err := c.transaction(ctx, "InstallPackages", uint64(1<<flagOnlyTrusted), []string{pkgs[0].id}); err != nil {
		return false, fmt.Errorf("install %s: %w", name, err)
	}
	return false, nil
}

// pkg is one Package signal.
type pkg struct {
	info uint32 // infoInstalled, or another PkInfoEnum value
	id   string // "name;version;arch;data"
}

// transaction runs one PackageKit transaction: it creates the transaction
// object, calls method with args on it, and collects the object's signals
// until Finished.
func (c *Client) transaction(ctx context.Context, method string, args ...any) ([]pkg, error) {
	var path dbus.ObjectPath
	err := c.conn.Object(busName, objectPath).CallWithContext(ctx, managerIface+".CreateTransaction", 0).Store(&path)
	if err != nil {
		if isServiceUnknown(err) {
			return nil, ErrNotAvailable
		}
		return nil, fmt.Errorf("start a PackageKit transaction: %w", err)
	}

	// Listen before calling the method, so no signal is missed. The match
	// rule asks the bus for this transaction's signals only.
	match := []dbus.MatchOption{
		dbus.WithMatchSender(busName),
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(transactionIface),
	}
	if err := c.conn.AddMatchSignalContext(ctx, match...); err != nil {
		return nil, fmt.Errorf("listen to the PackageKit transaction: %w", err)
	}
	// Clean-up when we are done. ctx may be cancelled by then, so use a
	// fresh one for removing the match rule.
	defer c.conn.RemoveMatchSignalContext(context.Background(), match...) //nolint:errcheck // nothing to do if this fails
	signals := make(chan *dbus.Signal, 64)
	c.conn.Signal(signals)
	defer c.conn.RemoveSignal(signals)

	tx := c.conn.Object(busName, path)
	// "interactive=true" lets PackageKit ask for a password; without it,
	// polkit may refuse at once.
	if err := tx.CallWithContext(ctx, transactionIface+".SetHints", 0, []string{"interactive=true"}).Err; err != nil {
		return nil, fmt.Errorf("PackageKit SetHints: %w", err)
	}
	if err := tx.CallWithContext(ctx, transactionIface+"."+method, 0, args...).Err; err != nil {
		return nil, fmt.Errorf("PackageKit %s: %w", method, err)
	}

	var pkgs []pkg
	var errCode uint32
	var errDetails string
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case sig, ok := <-signals:
			if !ok {
				return nil, errors.New("lost the connection to the system D-Bus")
			}
			if sig.Path != path {
				continue
			}
			switch sig.Name {
			case transactionIface + ".Package":
				// Body: info (u), package_id (s), summary (s).
				if len(sig.Body) == 3 {
					info, _ := sig.Body[0].(uint32)
					id, _ := sig.Body[1].(string)
					pkgs = append(pkgs, pkg{info: info, id: id})
				}
			case transactionIface + ".ErrorCode":
				// Body: code (u), details (s). Finished follows.
				if len(sig.Body) == 2 {
					errCode, _ = sig.Body[0].(uint32)
					errDetails, _ = sig.Body[1].(string)
				}
			case transactionIface + ".Finished":
				// Body: exit (u), runtime in ms (u).
				var exit uint32
				if len(sig.Body) == 2 {
					exit, _ = sig.Body[0].(uint32)
				}
				if exit == exitSuccess {
					return pkgs, nil
				}
				return nil, transactionError(exit, errCode, errDetails)
			case transactionIface + ".Destroy":
				return nil, errors.New("the PackageKit transaction ended without a result")
			}
		}
	}
}

// transactionError turns a failed transaction's exit and error codes into
// an error, wrapping one of the Err* values when the cause is known.
func transactionError(exit, code uint32, details string) error {
	switch {
	case code == errorNotAuthorized:
		return fmt.Errorf("%w: %s", ErrNotAuthorized, details)
	case code == errorPackageNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, details)
	case exit == exitCancelled:
		return ErrCancelled
	case details != "":
		return fmt.Errorf("PackageKit: %s", details)
	default:
		return fmt.Errorf("PackageKit: the transaction failed (exit code %d)", exit)
	}
}

// isServiceUnknown reports whether err is the bus saying nobody owns the
// name and no service file can start it.
func isServiceUnknown(err error) bool {
	var dbusErr dbus.Error
	return errors.As(err, &dbusErr) && dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown"
}
