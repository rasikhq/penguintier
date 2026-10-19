package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/rasikhq/penguintier/internal/dnssetup"
	"github.com/rasikhq/penguintier/internal/resolved"
)

// realSystem is dnssetup.System for a real Debian-family computer: it runs
// the system's own tools as root.
type realSystem struct {
	bus *resolved.Client // connected on first use
}

func (s *realSystem) close() {
	if s.bus != nil {
		_ = s.bus.Close() // nothing to do if closing fails at exit
	}
}

// InstallResolved installs systemd-resolved and nothing else:
// --no-install-recommends skips the helpers that would edit
// /etc/nsswitch.conf, --no-remove makes apt-get stop instead of removing
// any other package, and the dpkg options keep existing settings files.
func (s *realSystem) InstallResolved(ctx context.Context) error {
	// DPkg::Lock::Timeout makes apt-get wait (up to 2 minutes) instead of
	// failing when another package manager runs, such as an update.
	return runTool(ctx, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", "install", "-y", "-q",
		"--no-install-recommends", "--no-remove", "-o", "DPkg::Lock::Timeout=120",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold",
		dnssetup.PackageResolved)
}

// Purge removes packages and their settings with dpkg itself, which
// refuses (instead of removing more) if another package needs them.
//
// It first waits until no other package manager runs. After a crash, the
// interrupted apt-get may still be installing; purging in the middle would
// fail, or the install would finish after the undo and switch DNS again.
func (s *realSystem) Purge(ctx context.Context, packages []string) error {
	if err := waitForDpkg(ctx); err != nil {
		return err
	}
	return runTool(ctx, nil, "dpkg", append([]string{"--purge"}, packages...)...)
}

// dpkgLock is the lock apt-get and dpkg hold while they change packages.
const dpkgLock = "/var/lib/dpkg/lock-frontend"

// waitForDpkg waits until nobody holds dpkg's lock, or ctx ends.
func waitForDpkg(ctx context.Context) error {
	for {
		held, err := lockHeld(dpkgLock)
		if err != nil || !held {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("another package manager is still running: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// lockHeld reports whether another process holds a write lock on path.
// apt and dpkg use fcntl locks; F_GETLK asks the kernel who would block us,
// without taking the lock. A missing file means nobody holds it.
func lockHeld(path string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // dpkg's fixed lock file
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check %s: %w", path, err)
	}
	defer f.Close()
	// Ask about a write lock on the whole file (Start 0, Len 0 = to the end).
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: 0, Len: 0}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lock); err != nil {
		return false, fmt.Errorf("check %s: %w", path, err)
	}
	// The kernel sets Type to F_UNLCK when nothing would block us.
	return lock.Type != syscall.F_UNLCK, nil
}

// ResolvedOnBus asks the system bus whether org.freedesktop.resolve1 has
// an owner.
func (s *realSystem) ResolvedOnBus(ctx context.Context) bool {
	if s.bus == nil {
		c, err := resolved.Connect()
		if err != nil {
			return false
		}
		s.bus = c
	}
	up, err := s.bus.Available(ctx)
	return err == nil && up
}

func (s *realSystem) RestartResolved(ctx context.Context) error {
	return runTool(ctx, nil, "systemctl", "restart", "systemd-resolved.service")
}

func (s *realSystem) ReloadNetworkManager(ctx context.Context, what string) error {
	return runTool(ctx, nil, "nmcli", "general", "reload", what)
}

func (s *realSystem) Lookup(ctx context.Context, name string) error {
	return dnssetup.Lookup(ctx, name)
}

// runTool runs one of the system's tools and returns an error with the
// end of its output when it fails. extraEnv is added to the environment.
func runTool(ctx context.Context, extraEnv []string, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s not found: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // fixed system tools with fixed arguments
	cmd.Env = append(os.Environ(), extraEnv...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, lastLines(out.String(), 3))
	}
	return nil
}

// lastLines returns the last n non-empty lines of s, joined by " / ".
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}
