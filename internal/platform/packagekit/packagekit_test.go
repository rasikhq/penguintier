package packagekit

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/resolved/resolvedtest"
)

const testID = "systemd-resolved;257.13-1;amd64;debian-trixie-main"

// fakePackageKit answers like PackageKit for one package, testID.
//
// godbus runs each incoming call in its own goroutine, so a mutex guards
// the fields the tests read and change.
type fakePackageKit struct {
	conn *dbus.Conn

	mu        sync.Mutex
	known     bool // the package exists in the sources
	installed bool
	deny      bool // InstallPackages fails as if the password prompt was dismissed
	calls     []string
	next      int
}

func (pk *fakePackageKit) record(call string) {
	pk.mu.Lock()
	defer pk.mu.Unlock()
	pk.calls = append(pk.calls, call)
}

// CreateTransaction exports a new transaction object.
func (pk *fakePackageKit) CreateTransaction() (dbus.ObjectPath, *dbus.Error) {
	pk.mu.Lock()
	pk.next++
	path := dbus.ObjectPath(fmt.Sprintf("/%d_fake", pk.next))
	pk.mu.Unlock()
	if err := pk.conn.Export(&fakeTransaction{pk: pk, path: path}, path, transactionIface); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return path, nil
}

// fakeTransaction is one transaction object.
type fakeTransaction struct {
	pk   *fakePackageKit
	path dbus.ObjectPath
}

func (tx *fakeTransaction) SetHints(hints []string) *dbus.Error {
	tx.pk.record(fmt.Sprintf("SetHints %v", hints))
	return nil
}

func (tx *fakeTransaction) Resolve(filter uint64, names []string) *dbus.Error {
	tx.pk.record(fmt.Sprintf("Resolve %d %v", filter, names))
	tx.pk.mu.Lock()
	known, installed := tx.pk.known, tx.pk.installed
	tx.pk.mu.Unlock()
	// Like PackageKit, answer with signals after the method returns.
	go func() {
		switch {
		case installed:
			tx.emit("Package", uint32(infoInstalled), testID, "resolver")
		case known:
			tx.emit("Package", uint32(2), testID, "resolver") // 2 = available
		}
		tx.emit("Finished", uint32(exitSuccess), uint32(5))
		tx.emit("Destroy")
	}()
	return nil
}

func (tx *fakeTransaction) InstallPackages(flags uint64, ids []string) *dbus.Error {
	tx.pk.record(fmt.Sprintf("InstallPackages %d %v", flags, ids))
	tx.pk.mu.Lock()
	deny := tx.pk.deny
	if !deny {
		tx.pk.installed = true
	}
	tx.pk.mu.Unlock()
	go func() {
		if deny {
			tx.emit("ErrorCode", uint32(errorNotAuthorized), "Failed to obtain authentication.")
			tx.emit("Finished", uint32(2), uint32(5)) // 2 = failed
		} else {
			tx.emit("Package", uint32(12), testID, "resolver") // 12 = installing
			tx.emit("Finished", uint32(exitSuccess), uint32(900))
		}
		tx.emit("Destroy")
	}()
	return nil
}

func (tx *fakeTransaction) emit(signal string, body ...any) {
	_ = tx.pk.conn.Emit(tx.path, transactionIface+"."+signal, body...) // a lost signal makes the test fail anyway
}

// startFake runs a fake PackageKit on a private bus and returns a client
// connected to the same bus.
func startFake(t *testing.T) (*fakePackageKit, *Client) {
	t.Helper()
	bus := resolvedtest.StartTestBus(t)
	conn, err := dbus.Connect(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	pk := &fakePackageKit{conn: conn, known: true}
	if err := conn.Export(pk, objectPath, managerIface); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("take %s: %v %v", busName, reply, err)
	}

	client, err := ConnectAddress(bus.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return pk, client
}

func TestInstall(t *testing.T) {
	pk, client := startFake(t)

	already, err := client.Install(t.Context(), "systemd-resolved")
	if err != nil || already {
		t.Fatalf("Install = %t, %v; want a fresh install", already, err)
	}
	want := []string{
		"SetHints [interactive=true]",
		fmt.Sprintf("Resolve %d [systemd-resolved]", uint64(1<<16|1<<18)),
		"SetHints [interactive=true]",
		fmt.Sprintf("InstallPackages 2 [%s]", testID),
	}
	pk.mu.Lock()
	calls := slices.Clone(pk.calls)
	pk.calls = nil
	pk.mu.Unlock()
	if !slices.Equal(calls, want) {
		t.Errorf("calls:\n%q\nwant\n%q", calls, want)
	}

	// Installed now: nothing more to do.
	already, err = client.Install(t.Context(), "systemd-resolved")
	if err != nil || !already {
		t.Errorf("second Install = %t, %v; want already installed", already, err)
	}
	pk.mu.Lock()
	defer pk.mu.Unlock()
	if slices.ContainsFunc(pk.calls, func(c string) bool { return strings.HasPrefix(c, "Install") }) {
		t.Errorf("an installed package was installed again: %q", pk.calls)
	}
}

func TestInstallErrors(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		pk, client := startFake(t)
		pk.mu.Lock()
		pk.known = false
		pk.mu.Unlock()
		if _, err := client.Install(t.Context(), "systemd-resolved"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("prompt dismissed", func(t *testing.T) {
		pk, client := startFake(t)
		pk.mu.Lock()
		pk.deny = true
		pk.mu.Unlock()
		if _, err := client.Install(t.Context(), "systemd-resolved"); !errors.Is(err, ErrNotAuthorized) {
			t.Errorf("err = %v, want ErrNotAuthorized", err)
		}
	})
	t.Run("no PackageKit", func(t *testing.T) {
		bus := resolvedtest.StartTestBus(t)
		client, err := ConnectAddress(bus.Address)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		if _, err := client.Install(t.Context(), "systemd-resolved"); !errors.Is(err, ErrNotAvailable) {
			t.Errorf("err = %v, want ErrNotAvailable", err)
		}
	})
}

func TestTransactionError(t *testing.T) {
	if err := transactionError(exitCancelled, 0, ""); !errors.Is(err, ErrCancelled) {
		t.Errorf("cancelled: %v", err)
	}
	if err := transactionError(2, 10, "download failed"); err == nil || err.Error() != "PackageKit: download failed" {
		t.Errorf("other: %v", err)
	}
}
