// Package resolvedtest has test helpers for code that talks to
// systemd-resolved: a private dbus-daemon (Bus) and a fake resolved (Fake)
// to export on it. Tests in internal/resolved and cmd/penguintier-dnsd use
// them, so nothing touches the real system bus.
package resolvedtest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoDBusDaemon means the dbus-daemon program is not installed. Tests
// skip (or, in CI, fail) on it; see StartTestBus.
var ErrNoDBusDaemon = errors.New("dbus-daemon is not installed. Install it with “apt install dbus-daemon” on Debian and Ubuntu, or “dnf install dbus-daemon” on Fedora")

// busConfig is a minimal bus configuration: listen on a socket in dir, and
// let anyone (that is, the test's own user) own any name and call anything.
// %s is replaced with the directory.
const busConfig = `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:dir=%s</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`

// Bus is a private dbus-daemon running for one test.
type Bus struct {
	// Address is the D-Bus address to connect to, such as
	// "unix:path=/tmp/ptbus-123/dbus-abc,guid=...".
	Address string

	cmd *exec.Cmd
	dir string
}

// StartBus starts a private dbus-daemon. Call Close when done. It returns
// ErrNoDBusDaemon if dbus-daemon is missing. Tests use StartTestBus.
func StartBus() (*Bus, error) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		return nil, ErrNoDBusDaemon
	}

	// A short directory under $TMPDIR (usually /tmp): Unix socket paths
	// may be at most 107 bytes long.
	dir, err := os.MkdirTemp("", "ptbus-")
	if err != nil {
		return nil, fmt.Errorf("make bus directory: %w", err)
	}
	configPath := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(busConfig, dir)), 0o600); err != nil {
		_ = os.RemoveAll(dir) // best effort: we are already failing
		return nil, fmt.Errorf("write bus config: %w", err)
	}

	// --nofork keeps it as our child, so Close can stop it.
	// --print-address=1 prints the address it listens on to stdout (fd 1).
	// context.Background() because Close stops it, not a context.
	cmd := exec.CommandContext(context.Background(), daemon, "--config-file="+configPath, "--nofork", "--nopidfile", "--print-address=1") //nolint:gosec // test helper running the system's dbus-daemon
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("dbus-daemon stdout: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("start dbus-daemon: %w", err)
	}
	bus := &Bus{cmd: cmd, dir: dir}

	// Read the address line, but don't wait forever. The goroutine sends
	// its result on a channel; select waits for it or for the timer.
	type result struct {
		line string
		err  error
	}
	lines := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		lines <- result{line, err}
	}()
	select {
	case r := <-lines:
		if r.err != nil {
			bus.Close()
			return nil, fmt.Errorf("read dbus-daemon address: %w", r.err)
		}
		bus.Address = strings.TrimSpace(r.line)
	case <-time.After(10 * time.Second):
		bus.Close()
		return nil, errors.New("dbus-daemon did not print its address within 10 s")
	}
	return bus, nil
}

// Close stops the dbus-daemon and removes its directory.
func (b *Bus) Close() {
	if b.cmd.Process != nil {
		_ = b.cmd.Process.Kill() // it may have exited already; nothing to do then
		_ = b.cmd.Wait()         // reap it; the error is just "killed"
	}
	_ = os.RemoveAll(b.dir) // best effort: it is under $TMPDIR anyway
}

// TB is the part of *testing.T that StartTestBus needs. Taking an
// interface keeps the "testing" package out of this non-test package; a
// *testing.T has all these methods, so it fits.
type TB interface {
	Helper()
	Cleanup(f func())
	Skip(args ...any)
	Fatal(args ...any)
}

// StartTestBus starts a private bus for one test and stops it when the
// test ends. Without dbus-daemon the test is skipped on a developer
// machine but fails in CI ($CI is set), so CI never silently stops running
// these tests.
func StartTestBus(t TB) *Bus {
	t.Helper()
	bus, err := StartBus()
	if errors.Is(err, ErrNoDBusDaemon) && os.Getenv("CI") == "" {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	return bus
}
