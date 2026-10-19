package tray

import (
	"context"
	"log/slog"

	"fyne.io/systray"

	"github.com/rasikhq/penguintier/internal/platform/notify"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// Config is what Run needs from the program that starts the tray.
type Config struct {
	// Version is the app version, shown when the About window can't open.
	Version string
	// Hooks provides Authorize and Start at Login. nil means StubHooks.
	Hooks Hooks
	// DevUIDir is one more directory to look for penguintier-ui in, after
	// the usual ones. Development builds set it to ui/build/bin, where
	// `make ui-build` puts it. Leave it empty in release builds.
	DevUIDir string
}

// Run shows the tray icon and its menu. It blocks until the user clicks
// Quit or ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	store, err := ztapi.DefaultSavedStore()
	if err != nil {
		return err
	}

	// Notifications are nice to have: without a notification service the
	// tray still works, and logs the messages instead.
	var n notifier = logNotifier{}
	var actions <-chan notify.ActionEvent
	if desktop, err := notify.New(); err != nil {
		slog.Warn("notifications are off", "err", err)
	} else {
		defer desktop.Close()
		n = desktop
		actions = desktop.Actions()
	}

	c, err := newController(cfg, fyneBackend{}, store, n, actions)
	if err != nil {
		return err
	}

	// WithCancel returns a child context and a function that cancels it.
	// Cancelling tells the controller (and the dialogs it started) to stop.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	controllerDone := make(chan struct{})

	// Set these before systray.Run: the title also becomes the icon's ID
	// on D-Bus, which some panels show in their settings.
	systray.SetTitle("Penguintier")
	systray.SetTooltip("Penguintier")
	systray.SetIcon(c.icons[IconOffline])

	onReady := func() {
		// Build the first menu before returning. The library holds back
		// the tray host's first menu request until onReady returns, and
		// GNOME's AppIndicator extension reads the menu only once when the
		// icon appears (later only when the menu opens, and it never opens
		// an empty menu). A host that got an empty menu here would show
		// none at all.
		//
		// The controller loop then runs in its own goroutine, so onReady
		// returns. Starting a goroutine with "go" happens after everything
		// before it, so the loop sees what this first refresh did; from
		// then on only the loop touches the controller.
		c.refresh(ctx)
		go func() {
			defer close(controllerDone)
			c.run(ctx)
			systray.Quit() // ends systray.Run below; safe to call twice
		}()
	}
	onExit := func() {
		// Runs when systray.Run is about to return, also after Quit from
		// the menu. Stop the controller and wait until it has finished.
		cancel()
		<-controllerDone
	}

	// systray.Run blocks until systray.Quit is called.
	systray.Run(onReady, onExit)
	return nil
}

// logNotifier is used when the desktop has no notification service: it
// writes each notification to the log instead.
type logNotifier struct{}

func (logNotifier) Notify(_ context.Context, msg notify.Notification) (uint32, error) {
	slog.Info("notification", "summary", msg.Summary, "body", msg.Body)
	return 0, nil
}
