package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/rasikhq/penguintier/internal/platform/autostart"
	"github.com/rasikhq/penguintier/internal/platform/instance"
	"github.com/rasikhq/penguintier/internal/platform/notify"
	"github.com/rasikhq/penguintier/internal/platform/watcher"
	"github.com/rasikhq/penguintier/internal/tray"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// startTray runs the tray until the user quits. It is a variable holding a
// function so main_test.go can swap in a stand-in: the real tray blocks
// until Quit.
var startTray = func(autostartRun bool, stderr io.Writer) int {
	// The context is cancelled on Ctrl+C or SIGTERM, which quits the tray
	// cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runTray(ctx, autostartRun); err != nil {
		fmt.Fprintln(stderr, "penguintier:", err)
		return 1
	}
	return 0
}

// runTray does the start-up steps, then shows the tray.
func runTray(ctx context.Context, autostartRun bool) error {
	// 1. Only one copy. A second one says so and quits (exit code 0).
	lock, err := instance.Claim()
	if errors.Is(err, instance.ErrAlreadyRunning) {
		slog.Info("Penguintier is already running")
		if !autostartRun {
			notifyAlreadyRunning(ctx)
		}
		return nil
	}
	if err != nil {
		return err
	}
	// defer runs this when runTray returns, however it returns.
	defer func() {
		// The bus frees the name when we exit anyway, so an error here
		// changes nothing.
		_ = lock.Release()
	}()

	// 2. What happened in earlier runs.
	statePath, err := defaultStatePath()
	if err != nil {
		return err
	}
	state, err := loadState(statePath)
	if err != nil {
		slog.Warn("starting with a fresh state", "err", err)
	}

	// 3. Once: bring over the official ZeroTier app's saved networks.
	if !state.DesktopUIImported {
		importDesktopUI(&state)
	}

	// 4. Only the first interactive run asks for authorization by itself.
	// A run at login (--autostart) never does, and doesn't count.
	prompt := !autostartRun && !state.FirstRunDone
	if !autostartRun {
		state.FirstRunDone = true
	}
	if err := saveState(statePath, state); err != nil {
		slog.Warn("cannot save the state file", "err", err)
	}

	autostartPath, err := autostart.DefaultPath()
	if err != nil {
		return err
	}
	hooks := &desktopHooks{
		state:            state,
		statePath:        statePath,
		autostartPath:    autostartPath,
		autoStartAtLogin: version != "dev",
	}

	// 5. Tell the user once per login if no tray host shows the icon. The
	// goroutine ends with the tray (trayCtx is cancelled when runTray
	// returns).
	trayCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go noticeMissingTrayHost(trayCtx)

	cfg := tray.Config{Version: version, Hooks: hooks, PromptAuthorize: prompt}
	if version == "dev" {
		// `make run` (go run): also find the UI that `make ui-build` made.
		cfg.DevUIDir = filepath.Join("ui", "build", "bin")
	}
	return tray.Run(trayCtx, cfg)
}

// importDesktopUI copies the official ZeroTier app's saved networks into
// ours, once (ztapi's ImportDesktopUI explains how "once" works). A failure
// is logged and tried again next run.
func importDesktopUI(state *appState) {
	store, err := ztapi.DefaultSavedStore()
	if err != nil {
		slog.Warn("cannot import saved networks", "err", err)
		return
	}
	n, err := store.ImportDesktopUI()
	if err != nil {
		slog.Warn("cannot import saved networks", "err", err)
		return
	}
	if n > 0 {
		slog.Info("imported saved networks from ZeroTier's desktop app", "count", n, "from", store.DesktopUIPath)
	}
	state.DesktopUIImported = true
}

// notifyAlreadyRunning tells the user that the tray is already there.
func notifyAlreadyRunning(ctx context.Context) {
	n, err := notify.New()
	if err != nil {
		slog.Warn("cannot show a notification", "err", err)
		return
	}
	defer n.Close()
	_, err = n.Notify(ctx, notify.Notification{
		Summary: "Penguintier is already running",
		Body:    "Its icon is in the system tray. If you can't see it, run “penguintier doctor” in a terminal.",
	})
	if err != nil {
		slog.Warn("cannot show a notification", "err", err)
	}
}

// noticeMissingTrayHost waits until no tray host has shown up for the
// grace period, then shows one notification with fix steps for this
// desktop. It shows it at most once per login (watcher.NoticeShown).
func noticeMissingTrayHost(ctx context.Context) {
	if watcher.NoticeShown() {
		return
	}
	// dbus.SessionBus is the connection the tray icon library also uses;
	// it must not be closed.
	conn, err := dbus.SessionBus()
	if err != nil {
		return
	}
	ready := func() bool {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		status, err := watcher.Check(checkCtx, conn)
		// When the check itself fails we can't tell; don't nag then.
		return err != nil || status.Ready()
	}
	if !watcher.WaitMissing(ctx, ready, watcher.Grace, watcher.CheckInterval) {
		return // the tray quit first
	}

	desktop := watcher.DetectDesktop()
	advice := watcher.AdviceFor(desktop, watcher.CheckExtension(ctx, conn))
	slog.Warn("no tray host: the icon is invisible", "desktop", desktop.String(), "advice", advice.Text())

	n, err := notify.New()
	if err != nil {
		slog.Warn("cannot show a notification", "err", err)
		return
	}
	defer n.Close()
	_, err = n.Notify(ctx, notify.Notification{
		Summary: "Penguintier is running, but its icon can't be shown",
		Body:    advice.Text() + "\nThe icon then appears by itself.",
	})
	if err != nil {
		slog.Warn("cannot show a notification", "err", err)
		return
	}
	if err := watcher.RememberNoticeShown(); err != nil {
		slog.Warn("cannot remember the tray notice", "err", err)
	}
}
