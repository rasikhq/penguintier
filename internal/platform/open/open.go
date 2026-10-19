package open

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
)

// URL opens an http:// or https:// address in the user's default browser.
// It returns as soon as xdg-open has started; it does not wait for the
// browser.
//
// Other schemes (file:, ssh:, a custom app handler, ...) are refused. The
// SSO login address comes from a network's controller, which is someone
// else's server, so it must not be able to make us open a local file or
// start an arbitrary program.
func URL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("open %q: %w", rawURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("open %q: only http and https addresses are opened", rawURL)
	}

	// The address starts with "http", so xdg-open can't mistake it for a
	// command-line option such as "--help". Nothing should stop xdg-open
	// half way (the page should open even if the tray quits meanwhile),
	// so its context is never cancelled.
	cmd := exec.CommandContext(context.Background(), "xdg-open", rawURL)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start xdg-open: %w", err)
	}

	// When a child process ends, Linux keeps a small "zombie" entry for it
	// until the parent collects its exit status. cmd.Wait does that, so it
	// must be called even though we don't care about the result. It blocks
	// until xdg-open exits, so it runs in its own goroutine ("go" starts a
	// function concurrently and returns at once).
	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Warn("xdg-open failed", "url", rawURL, "err", err)
		}
	}()
	return nil
}
