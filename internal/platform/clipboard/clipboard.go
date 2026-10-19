package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNoTool means no clipboard tool could be used: none is installed, or
// there is no Wayland or X11 display to copy to.
var ErrNoTool = errors.New("no clipboard tool found (install wl-clipboard or xclip)")

// timeout limits how long one tool may take. They normally finish in a few
// milliseconds.
const timeout = 3 * time.Second

// tool is one clipboard program and how to call it. Each of them reads the
// text from standard input.
type tool struct {
	name string
	args []string
	// display is the environment variable that must be set for the tool
	// to work: WAYLAND_DISPLAY for Wayland, DISPLAY for X11.
	display string
}

// tools are tried in this order.
var tools = []tool{
	{name: "wl-copy", display: "WAYLAND_DISPLAY"},
	{name: "xclip", args: []string{"-selection", "clipboard"}, display: "DISPLAY"},
	{name: "xsel", args: []string{"--clipboard", "--input"}, display: "DISPLAY"},
}

// Copy puts text on the clipboard. It tries each tool in turn and returns
// nil as soon as one works. If none can be used it returns ErrNoTool; if
// tools ran but all failed it returns the last failure (which also matches
// ErrNoTool with errors.Is).
func Copy(ctx context.Context, text string) error {
	var lastErr error
	for _, t := range tools {
		if os.Getenv(t.display) == "" {
			continue // no display of that kind in this session
		}
		path, err := exec.LookPath(t.name)
		if err != nil {
			continue // not installed
		}
		if err := run(ctx, path, t.args, text); err != nil {
			lastErr = fmt.Errorf("%s: %w", t.name, err)
			continue
		}
		return nil
	}
	if lastErr != nil {
		// Two %w verbs: errors.Is matches both ErrNoTool and the cause.
		return fmt.Errorf("%w: %w", ErrNoTool, lastErr)
	}
	return ErrNoTool
}

// run starts one tool with text on its standard input and waits for it.
func run(ctx context.Context, path string, args []string, text string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	// defer runs cancel when run returns, whatever path it takes. That
	// frees the timer behind the timeout.
	defer cancel()

	// gosec warns about running a program whose path is a variable; here
	// it always comes from the fixed tools list above.
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // path is one of the fixed tools
	cmd.Stdin = strings.NewReader(text)
	// Stdout and Stderr stay nil, which means /dev/null. That matters: all
	// three tools fork a background copy of themselves that keeps serving
	// the clipboard. If we gave them a pipe, that copy would inherit it and
	// Wait would block until the clipboard changes.
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
