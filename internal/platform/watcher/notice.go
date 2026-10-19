package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/godbus/dbus/v5"
)

// The tray host check waits this long before deciding there is none:
// at login the tray often starts before the panel.
const (
	Grace         = 10 * time.Second
	CheckInterval = 2 * time.Second
)

// WaitMissing calls ready every interval. It returns true as soon as
// ready has been false for the whole grace period, counted from the first
// false, and false when ctx is cancelled first. A tray host that comes
// back starts the count again.
func WaitMissing(ctx context.Context, ready func() bool, grace, interval time.Duration) bool {
	var missingSince time.Time // the zero value means "not missing"
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		switch {
		case ready():
			missingSince = time.Time{}
		case missingSince.IsZero():
			missingSince = time.Now()
		case time.Since(missingSince) >= grace:
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// The "no tray host" notice is shown at most once per login. A marker file
// in $XDG_RUNTIME_DIR holds the key of the login it was shown in (see
// LoginKey). The folder itself is not enough: it is only emptied when the
// user's last process ends, and with other programs still running that may
// be long after a logout.

// LoginKey names the current login. It is logind's ID for the user's
// graphical session, such as "login1:20", which is new at every login.
// Without logind it is the session bus's ID ("bus:…"), which changes when
// the bus restarts (on most systems at every login). It is "" when neither
// can be read; then the notice can only be limited to once per run.
//
// session is a session bus connection; logind is asked on the system bus.
func LoginKey(ctx context.Context, session *dbus.Conn) string {
	return loginKey(ctx, session, "")
}

// loginKey does the work of LoginKey. systemAddress is the system bus's
// address; "" means the real one. Tests pass a private bus.
func loginKey(ctx context.Context, session *dbus.Conn, systemAddress string) string {
	if id := displaySession(ctx, systemAddress); id != "" {
		return "login1:" + id
	}
	var busID string
	err := session.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetId", 0).Store(&busID)
	if err == nil && busID != "" {
		return "bus:" + busID
	}
	return ""
}

// displaySession asks logind for the user's graphical session: the
// Display property of /org/freedesktop/login1/user/self ("self" is the
// user calling). It returns "" if that can't be read or there is none.
func displaySession(ctx context.Context, systemAddress string) string {
	var conn *dbus.Conn
	var err error
	if systemAddress == "" {
		conn, err = dbus.ConnectSystemBus()
	} else {
		conn, err = dbus.Connect(systemAddress)
	}
	if err != nil {
		return ""
	}
	defer conn.Close()

	var v dbus.Variant
	err = conn.Object(login1Name, login1UserSelf).CallWithContext(ctx, propertiesGet, dbus.FlagNoAutoStart,
		login1UserIface, "Display").Store(&v)
	if err != nil {
		return ""
	}
	// The value is a D-Bus struct "(so)": the session ID and its object
	// path. godbus fills a Go struct with the same fields in order.
	var display struct {
		ID   string
		Path dbus.ObjectPath
	}
	if err := v.Store(&display); err != nil {
		return ""
	}
	return display.ID
}

// logind's names on the system bus, from org.freedesktop.login1(5).
const (
	login1Name      = "org.freedesktop.login1"
	login1UserSelf  = dbus.ObjectPath("/org/freedesktop/login1/user/self")
	login1UserIface = "org.freedesktop.login1.User"
)

// noticeMarker returns the marker file's path, or "" when
// $XDG_RUNTIME_DIR is not set.
func noticeMarker() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "penguintier", "no-tray-host-notice")
}

// NoticeShown reports whether the notice was already shown in the login
// named by key (from LoginKey). An empty key is never "shown".
func NoticeShown(key string) bool {
	path := noticeMarker()
	if path == "" || key == "" {
		return false
	}
	data, err := os.ReadFile(path) //nolint:gosec // our own marker file
	return err == nil && string(data) == key
}

// RememberNoticeShown records that the notice was shown in the login named
// by key.
func RememberNoticeShown(key string) error {
	path := noticeMarker()
	if path == "" {
		return errors.New("XDG_RUNTIME_DIR is not set")
	}
	if key == "" {
		return errors.New("no login key to remember the notice by")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("remember the tray notice: %w", err)
	}
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		return fmt.Errorf("remember the tray notice: %w", err)
	}
	return nil
}
