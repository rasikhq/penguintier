package instance

import (
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/resolved/resolvedtest"
)

// TestClaim: the first claim wins, a second one is refused, and after
// Release the name can be claimed again.
func TestClaim(t *testing.T) {
	bus := resolvedtest.StartTestBus(t)
	connect := func() *dbus.Conn {
		t.Helper()
		conn, err := dbus.Connect(bus.Address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}

	first, err := claimOn(connect())
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := claimOn(connect()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second claim: err = %v, want ErrAlreadyRunning", err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	third, err := claimOn(connect())
	if err != nil {
		t.Fatalf("claim after Release: %v", err)
	}
	_ = third.Release()
}
