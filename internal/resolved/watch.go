package resolved

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// WatchOwner reports when systemd-resolved appears on the bus or goes
// away. The returned channel gets true when resolve1 got an owner (resolved
// started) and false when it lost it (resolved stopped). A restart usually
// shows up as false, then true.
//
// The channel is closed when ctx is cancelled or the connection closes.
//
// Treat a value as "go and check": signals can arrive late, so call
// Available (or just try your next call) instead of relying on the value
// alone.
func (c *Client) WatchOwner(ctx context.Context) (<-chan bool, error) {
	// The bus itself sends NameOwnerChanged(name, oldOwner, newOwner)
	// whenever a name changes hands. A match rule asks it to send us only
	// the ones for resolve1 (argument 0 is the name).
	match := []dbus.MatchOption{
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, BusName),
	}
	if err := c.conn.AddMatchSignalContext(ctx, match...); err != nil {
		return nil, fmt.Errorf("watch %s on D-Bus: %w", BusName, err)
	}

	// godbus delivers every signal for this connection on the channels
	// given to conn.Signal. It closes them when the connection closes.
	signals := make(chan *dbus.Signal, 8)
	c.conn.Signal(signals)

	out := make(chan bool, 1)
	// This goroutine only translates signals into true/false. The
	// "go func() { ... }()" starts it; it runs until the signals channel
	// is closed or ctx is cancelled.
	go func() {
		// defer runs when the function returns, however it returns.
		defer close(out)
		defer c.conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return // connection closed
				}
				owned, isOurs := parseOwnerChanged(sig)
				if !isOurs {
					continue
				}
				select {
				case out <- owned:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// parseOwnerChanged checks that sig is NameOwnerChanged for resolve1 and
// returns whether the name has an owner now.
func parseOwnerChanged(sig *dbus.Signal) (owned, isOurs bool) {
	if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) != 3 {
		return false, false
	}
	// sig.Body is a []any. The ".(string)" type assertions check that each
	// element really is a string; ok is false otherwise.
	name, ok1 := sig.Body[0].(string)
	newOwner, ok2 := sig.Body[2].(string)
	if !ok1 || !ok2 || name != BusName {
		return false, false
	}
	return newOwner != "", true
}
