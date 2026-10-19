package main

import (
	"context"
	"log/slog"

	"github.com/rasikhq/penguintier/internal/platform/autostart"
)

// desktopHooks is the real tray.Hooks: authorizing, Start at Login and
// "Enable DNS support…".
//
// Who touches what: StartAtLogin, SetStartAtLogin and Connected run on
// the tray's controller goroutine, one at a time, and they are the only
// code that touches state after the tray starts. Authorize and EnableDNS
// run on goroutines of their own and never touch state, so no lock is
// needed.
type desktopHooks struct {
	state         appState
	statePath     string
	autostartPath string
	// autoStartAtLogin: turn Start at Login on after the first successful
	// run. Off for development builds, whose "penguintier" is usually not
	// on the PATH that the autostart file runs.
	autoStartAtLogin bool
}

// Authorize gets the API token through pkexec (see authorize.go).
func (h *desktopHooks) Authorize(ctx context.Context) error {
	return authorize(ctx)
}

// EnableDNS installs systemd-resolved after asking (see enabledns.go).
func (h *desktopHooks) EnableDNS(ctx context.Context) error {
	return enableDNS(ctx)
}

// StartAtLogin reports whether the autostart file is there and on.
func (h *desktopHooks) StartAtLogin() bool {
	return autostart.Enabled(h.autostartPath)
}

// SetStartAtLogin is the user's choice from the menu. It is remembered, so
// Penguintier never turns Start at Login back on by itself.
func (h *desktopHooks) SetStartAtLogin(on bool) error {
	var err error
	if on {
		err = autostart.Enable(h.autostartPath)
	} else {
		err = autostart.Disable(h.autostartPath)
	}
	if err != nil {
		return err
	}
	h.state.StartAtLoginDecided = true
	h.save()
	return nil
}

// Connected runs the first time in a run that the tray reaches ZeroTier
// with a working token. After the first such run ever, Start at Login is
// turned on, unless the user has already chosen.
func (h *desktopHooks) Connected() {
	if h.state.StartAtLoginDecided || !h.autoStartAtLogin {
		return
	}
	if !autostart.Enabled(h.autostartPath) {
		if err := autostart.Enable(h.autostartPath); err != nil {
			slog.Warn("cannot turn on Start at Login", "err", err)
			return // try again next run
		}
		slog.Info("Start at Login is on", "file", h.autostartPath)
	}
	h.state.StartAtLoginDecided = true
	h.save()
}

// save writes the state file, and only logs a failure: the worst case is
// that a one-time step happens again next run.
func (h *desktopHooks) save() {
	if err := saveState(h.statePath, h.state); err != nil {
		slog.Warn("cannot save the state file", "err", err)
	}
}
