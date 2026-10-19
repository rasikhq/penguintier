package instance

import (
	"errors"
	"testing"
	"time"

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

// TestLost: when the bus goes away (the user logged out), Lost is closed.
func TestLost(t *testing.T) {
	bus := resolvedtest.StartTestBus(t)
	conn, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := claimOn(conn)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-lock.Lost():
		t.Fatal("Lost is closed while the bus runs")
	default:
	}

	bus.Close() // the session bus stops
	select {
	case <-lock.Lost():
	case <-time.After(5 * time.Second):
		t.Fatal("Lost was not closed after the bus stopped")
	}
}

// TestActivate: a second copy's Activate reaches the running copy.
func TestActivate(t *testing.T) {
	bus := resolvedtest.StartTestBus(t)
	first, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	lock, err := claimOn(first)
	if err != nil {
		t.Fatal(err)
	}

	second, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	for i := 0; i < 3; i++ { // more than the channel holds: none may block
		if err := activateOn(t.Context(), second); err != nil {
			t.Fatalf("Activate %d: %v", i, err)
		}
	}
	select {
	case <-lock.Activations():
	case <-time.After(5 * time.Second):
		t.Fatal("the running copy got no activation")
	}

	// Nobody running: Activate fails instead of hanging.
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := activateOn(t.Context(), second); err == nil {
		t.Error("Activate with nobody running: no error")
	}
}
