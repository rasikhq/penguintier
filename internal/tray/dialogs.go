package tray

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/rasikhq/penguintier/internal/ztapi"
)

// uiName is the Join/About window program (the ui/ module).
const uiName = "penguintier-ui"

// joinTitle and joinPrompt are the zenity/kdialog fallback's texts.
const (
	joinTitle  = "Join ZeroTier Network"
	joinPrompt = "Network ID (16 characters):"
)

// fallbackDialog is a program that can ask for one line of text when
// penguintier-ui is missing or fails.
type fallbackDialog struct {
	name string
	args []string
}

// fallbackDialogs are tried in order. Both print the text and exit 0 on
// OK, and exit 1 on Cancel.
var fallbackDialogs = []fallbackDialog{
	{name: "zenity", args: []string{"--entry", "--title=" + joinTitle, "--text=" + joinPrompt, "--width=360"}},
	{name: "kdialog", args: []string{"--title", joinTitle, "--inputbox", joinPrompt}},
}

// uiLauncher starts the Join and About windows.
type uiLauncher struct {
	// devDir is one more place to look for penguintier-ui (see
	// Config.DevUIDir).
	devDir string
}

// findUI returns penguintier-ui's path.
func (u uiLauncher) findUI() (string, error) {
	if u.devDir != "" {
		return FindHelper(uiName, u.devDir)
	}
	return FindHelper(uiName)
}

// uiEnv is the environment for penguintier-ui: ours, plus
// WEBKIT_DISABLE_DMABUF_RENDERER=1, which avoids blank windows with some
// NVIDIA drivers. GTK_THEME is left alone: the UI picks light or dark
// itself.
func uiEnv() []string {
	return append(os.Environ(), "WEBKIT_DISABLE_DMABUF_RENDERER=1")
}

// askNetworkID asks the user for a network ID and waits for the answer.
// It uses the Join window, or zenity/kdialog when that can't be shown.
// The bool is false when the user cancelled. Cancelling ctx closes the
// window.
func (u uiLauncher) askNetworkID(ctx context.Context) (ztapi.NetworkID, bool, error) {
	if path, err := u.findUI(); err != nil {
		slog.Info("Join window not found, falling back to zenity and kdialog", "err", err)
	} else {
		out, code, err := runDialog(ctx, path, []string{"join"}, uiEnv())
		switch {
		case err == nil && code == 0:
			return parseNetworkID(out)
		case err == nil && code == 1:
			return "", false, nil // the user cancelled
		default:
			slog.Warn("Join window failed, falling back to zenity and kdialog", "path", path, "exit", code, "err", err)
		}
	}

	for _, d := range fallbackDialogs {
		path, err := exec.LookPath(d.name)
		if err != nil {
			continue // not installed
		}
		out, code, err := runDialog(ctx, path, d.args, nil)
		switch {
		case err == nil && code == 0:
			return parseNetworkID(out)
		case err == nil && code == 1:
			return "", false, nil
		default:
			slog.Warn("dialog failed", "program", d.name, "exit", code, "err", err)
		}
	}
	return "", false, errors.New("no Join window: penguintier-ui, zenity and kdialog are all missing or failed")
}

// parseNetworkID checks what a dialog printed.
func parseNetworkID(out string) (ztapi.NetworkID, bool, error) {
	id, err := ztapi.ParseNetworkID(strings.TrimSpace(out))
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// runDialog runs a dialog program and waits for it to close. It returns
// what the program printed and its exit code. err is set only when the
// program could not run, or was killed.
func runDialog(ctx context.Context, path string, args []string, env []string) (out string, code int, err error) {
	// CommandContext kills the program if ctx is cancelled (the tray
	// quits), so no window outlives the tray.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env // nil means "the same environment as ours"
	// Pass the dialog's error messages through to our own log.
	cmd.Stderr = os.Stderr
	stdout, err := cmd.Output()

	// An *exec.ExitError means the program ran and exited with a non-zero
	// code. ExitCode is -1 when a signal killed it instead.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
		return string(stdout), exitErr.ExitCode(), nil
	}
	if err != nil {
		return "", -1, fmt.Errorf("run %s: %w", path, err)
	}
	return string(stdout), 0, nil
}

// startAbout opens the About window and returns at once.
func (u uiLauncher) startAbout(ctx context.Context) error {
	path, err := u.findUI()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, "about")
	cmd.Env = uiEnv()
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", path, err)
	}
	// Collect the exit status when the window closes, so the finished
	// process doesn't stay behind as a zombie (see open.URL).
	go func() {
		if err := cmd.Wait(); err != nil && ctx.Err() == nil {
			slog.Warn("About window failed", "err", err)
		}
	}()
	return nil
}
