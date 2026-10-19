package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
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
// in $XDG_RUNTIME_DIR remembers it: that folder belongs to the login and
// is emptied when the user logs out.

// noticeMarker returns the marker file's path, or "" when
// $XDG_RUNTIME_DIR is not set.
func noticeMarker() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "penguintier", "no-tray-host-notice")
}

// NoticeShown reports whether the notice was already shown in this login.
func NoticeShown() bool {
	path := noticeMarker()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// RememberNoticeShown records that the notice was shown in this login.
func RememberNoticeShown() error {
	path := noticeMarker()
	if path == "" {
		return errors.New("XDG_RUNTIME_DIR is not set")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("remember the tray notice: %w", err)
	}
	// O_CREATE makes the file if needed; an existing one is fine too.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("remember the tray notice: %w", err)
	}
	return f.Close()
}
