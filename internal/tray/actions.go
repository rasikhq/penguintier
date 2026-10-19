package tray

import (
	"context"
	"errors"
	"log/slog"

	"github.com/rasikhq/penguintier/internal/platform/notify"
	"github.com/rasikhq/penguintier/internal/ztapi"
)

// The click handlers below all run on the controller goroutine. API calls
// are quick (the service is on this machine, with a 2-second timeout), so
// they run right here. Anything that waits for the user (the Join dialog,
// the password prompt of Authorize) runs in its own goroutine and sends
// its result back on a channel.

// handleClick does what the clicked entry's action says. It returns true
// when the tray should quit.
func (c *controller) handleClick(ctx context.Context, key string) (quit bool) {
	action, ok := c.actions[key]
	if !ok {
		return false // the entry is gone or disabled by now
	}
	slog.Debug("menu click", "item", key, "action", action)

	switch action.Kind {
	case ActCopy:
		c.copy(ctx, action.Text, action.Notify)
	case ActJoin:
		c.startJoin(ctx)
	case ActSetFlag:
		c.setFlag(ctx, action.Network, action.Flag, action.Value)
	case ActOpenURL:
		c.open(ctx, action.Text)
	case ActDisconnect:
		c.disconnect(ctx, action.Network)
	case ActReconnect:
		c.reconnect(ctx, action.Network)
	case ActForget:
		c.forget(ctx, action.Network)
	case ActAuthorize:
		c.startAuthorize(ctx)
	case ActStartAtLogin:
		c.setStartAtLogin(ctx, action.Value)
	case ActEnableDNS:
		c.startEnableDNS(ctx)
	case ActAbout:
		c.about(ctx)
	case ActQuit:
		return true
	}
	return false
}

// copy puts text on the clipboard. When that fails, a notification shows
// the text so the user can copy it by hand.
func (c *controller) copy(ctx context.Context, text string, announce bool) {
	if err := c.copyText(ctx, text); err != nil {
		slog.Warn("cannot copy to the clipboard", "err", err)
		c.notify(ctx, notify.Notification{
			Summary: "Copy this text",
			Body:    text + "\n\nPenguintier could not reach the clipboard. Install wl-clipboard (Wayland) or xclip (X11).",
		})
		return
	}
	if announce {
		c.notify(ctx, notify.Notification{Summary: "Copied to clipboard", Body: text, Transient: true})
	}
}

// startJoin opens the Join dialog, unless one is open already.
func (c *controller) startJoin(ctx context.Context) {
	if c.joinDialogOpen {
		return
	}
	c.joinDialogOpen = true

	// Copy what the goroutine needs into local variables, so it never
	// reads the controller's fields.
	ask := c.askNetworkID
	results := c.joinResults
	go func() {
		id, ok, err := ask(ctx)
		select {
		case results <- joinResult{id: id, ok: ok, err: err}:
		case <-ctx.Done():
			// The controller has stopped and will never receive; give up
			// so this goroutine can end.
		}
	}()
}

// handleJoinResult joins the network the user typed in the Join dialog.
func (c *controller) handleJoinResult(ctx context.Context, res joinResult) {
	c.joinDialogOpen = false
	if res.err != nil {
		c.showError(ctx, "Cannot join a network", res.err)
		return
	}
	if !res.ok || c.client == nil {
		return // cancelled
	}
	if _, err := c.client.Join(ctx, res.id); err != nil {
		c.showError(ctx, "Cannot join network "+res.id.String(), err)
		return
	}
	// Show "Joining…" until the network appears in the list.
	c.joining[res.id] = c.now()
}

// setFlag turns one "Allow ..." setting of a joined network on or off.
func (c *controller) setFlag(ctx context.Context, id ztapi.NetworkID, flag ztapi.Flag, value bool) {
	// SetFlags joins a network that is not joined. If the network was left
	// since the menu was built (e.g. with zerotier-cli), do nothing rather
	// than join it again.
	if _, joined := c.vm.network(id); !joined || c.client == nil {
		return
	}
	if _, err := c.client.SetFlags(ctx, id, map[ztapi.Flag]bool{flag: value}); err != nil {
		c.showError(ctx, "Cannot change "+flagLabel(flag), err)
	}
}

// open opens an SSO login page in the browser.
func (c *controller) open(ctx context.Context, url string) {
	if err := c.openURL(url); err != nil {
		c.showError(ctx, "Cannot open the login page", err)
	}
}

// disconnect saves a network's settings, then leaves it.
func (c *controller) disconnect(ctx context.Context, id ztapi.NetworkID) {
	n, joined := c.vm.network(id)
	if !joined || c.client == nil {
		return
	}
	// Save first. If that fails, stay connected: leaving would lose the
	// settings for good.
	if err := c.store.Put(id, ztapi.SavedFromNetwork(n.Network)); err != nil {
		c.showError(ctx, "Cannot save the network's settings, so it stays connected", err)
		return
	}
	c.loadSaved()
	if err := c.client.Leave(ctx, id); err != nil {
		c.showError(ctx, "Cannot leave network "+id.String(), err)
	}
}

// reconnect joins a saved network again with its saved settings.
func (c *controller) reconnect(ctx context.Context, id ztapi.NetworkID) {
	saved, ok := c.saved[id]
	if !ok || c.client == nil {
		return
	}
	// SetFlags is a POST /network/{id}: for a network that is not joined,
	// it joins and applies the settings in one call.
	if _, err := c.client.SetFlags(ctx, id, saved.Flags()); err != nil {
		c.showError(ctx, "Cannot reconnect to network "+id.String(), err)
		return
	}
	c.joining[id] = c.now()
}

// forget deletes a saved network.
func (c *controller) forget(ctx context.Context, id ztapi.NetworkID) {
	if err := c.store.Remove(id); err != nil {
		c.showError(ctx, "Cannot forget network "+id.String(), err)
		return
	}
	c.loadSaved()
}

// startAuthorize runs Hooks.Authorize in its own goroutine, because it
// waits for the user to answer a password prompt.
func (c *controller) startAuthorize(ctx context.Context) {
	if c.authorizing {
		return
	}
	c.authorizing = true

	hooks := c.hooks
	results := c.authResults
	go func() {
		err := hooks.Authorize(ctx)
		select {
		case results <- err:
		case <-ctx.Done():
		}
	}()
}

// handleAuthResult reports a failed Authorize, or makes the next poll use
// the new token.
func (c *controller) handleAuthResult(ctx context.Context, err error) {
	c.authorizing = false
	if errors.Is(err, ErrCancelled) {
		slog.Info("authorization cancelled")
		return // the user dismissed the prompt: nothing to report
	}
	if err != nil {
		c.showError(ctx, "Cannot authorize Penguintier", err)
		return
	}
	c.newClientNeeded = true
}

// startEnableDNS runs Hooks.EnableDNS in its own goroutine: it asks the
// user first, then waits for the installation.
func (c *controller) startEnableDNS(ctx context.Context) {
	if c.enablingDNS {
		return
	}
	c.enablingDNS = true

	hooks := c.hooks
	results := c.dnsResults
	go func() {
		err := hooks.EnableDNS(ctx)
		select {
		case results <- err:
		case <-ctx.Done():
		}
	}()
}

// handleEnableDNSResult tells the user how "Enable DNS support…" went.
func (c *controller) handleEnableDNSResult(ctx context.Context, err error) {
	c.enablingDNS = false
	switch {
	case errors.Is(err, ErrCancelled):
		slog.Info("enabling DNS support cancelled")
	case err != nil:
		c.showError(ctx, "Cannot enable DNS support", err)
	default:
		c.notify(ctx, notify.Notification{
			Summary: "DNS support is on",
			Body: "This computer now uses systemd-resolved, and names resolve. ZeroTier networks with " +
				"“Allow DNS Configuration” now get their DNS settings, and each network's DNS row shows the status. " +
				"Your previous files are backed up, and “penguintier doctor” shows how to switch back.",
		})
	}
}

// setStartAtLogin turns starting at login on or off.
func (c *controller) setStartAtLogin(ctx context.Context, on bool) {
	if err := c.hooks.SetStartAtLogin(on); err != nil {
		c.showError(ctx, "Cannot change Start at Login", err)
	}
	c.startAtLogin = c.hooks.StartAtLogin()
}

// about opens the About window, or shows the same facts in a notification
// when the window can't be opened.
func (c *controller) about(ctx context.Context) {
	if err := c.startAbout(ctx); err != nil {
		slog.Warn("cannot open the About window", "err", err)
		c.notify(ctx, notify.Notification{
			Summary: "Penguintier " + c.version,
			Body: "A tray app for ZeroTier on Linux. MIT license.\n" +
				"Unofficial app. Not affiliated with ZeroTier, Inc.\nhttps://github.com/rasikhq/penguintier",
		})
	}
}

// handleNotificationAction opens the login page when the user clicks a
// login notification.
func (c *controller) handleNotificationAction(ctx context.Context, ev notify.ActionEvent) {
	id, ok := c.ssoNotifications[ev.ID]
	if !ok {
		return // not one of our login notifications
	}
	// Use the network's current login address: each login attempt gets a
	// new one. If it no longer needs a login, there is nothing to open.
	n, joined := c.vm.network(id)
	if !joined || !needsLogin(n) {
		return
	}
	c.open(ctx, n.AuthenticationURL)
}

// notify shows a notification, and only logs if that fails.
func (c *controller) notify(ctx context.Context, msg notify.Notification) {
	if _, err := c.notifier.Notify(ctx, msg); err != nil {
		slog.Warn("cannot show a notification", "summary", msg.Summary, "err", err)
	}
}

// showError logs a failed action and tells the user about it.
func (c *controller) showError(ctx context.Context, summary string, err error) {
	slog.Warn(summary, "err", err)
	c.notify(ctx, notify.Notification{Summary: summary, Body: err.Error()})
}
