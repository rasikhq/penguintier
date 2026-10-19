// Command penguintier-ui shows Penguintier's small windows. The tray starts
// it on demand, and it exits when its window closes:
//
//	penguintier-ui join    Join Network dialog. Prints the 16-hex-digit
//	                       network ID on stdout and exits 0. Cancel, or
//	                       closing the window, exits 1.
//	penguintier-ui about   About window. Exits 0 when closed.
//
// Anything else prints usage and exits 2. This contract is in
// docs/architecture.md ("UI process").
//
// The window follows the desktop's light or dark style (see theme.go).
package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// assets holds the built React app (frontend/dist), embedded into the
// binary. `wails build` (or `make ui-build`) creates frontend/dist, so run it
// once before a plain `go build` or `go vet` in this directory.
//
//go:embed all:frontend/dist
var assets embed.FS

// appIcon is the window icon. X11 desktops show it in the title bar and the
// task switcher; Wayland desktops take the icon from the .desktop file
// instead, matched through the program name below.
//
//go:embed build/appicon.png
var appIcon []byte

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// appID is Penguintier's App ID. It is also used as the window's WM class,
// so desktops match the window to io.github.rasikhq.Penguintier.desktop.
const appID = "io.github.rasikhq.Penguintier"

// The screens this binary can show (the first command-line argument).
const (
	screenJoin  = "join"
	screenAbout = "about"
)

// defaultScreen is used when no argument is given. It stays empty (no
// default) except in the special "bindings" build; see bindings.go.
var defaultScreen = ""

func main() {
	// os.Exit skips deferred calls, so it is only called here, at the top.
	os.Exit(run(os.Args[1:]))
}

// run shows the requested window and returns the process exit code.
func run(args []string) int {
	if len(args) == 0 && defaultScreen != "" {
		args = []string{defaultScreen}
	}
	if len(args) != 1 {
		printUsage()
		return 2
	}

	screen := args[0]
	var title string
	var width, height int
	// Sizes fit the content exactly (not counting the title bar). If you
	// change the layout in frontend/src, check that nothing gets cut off.
	switch screen {
	case screenJoin:
		title, width, height = "Join Network", 420, 184
	case screenAbout:
		title, width, height = "About Penguintier", 360, 324
	default:
		printUsage()
		return 2
	}

	// Name the program before GTK starts, so desktops match the window to
	// Penguintier's .desktop file (see progname.go).
	setProgramName(appID)

	// Pick light or dark before the window exists, and paint the window
	// in the matching color so it doesn't flash white while the page loads.
	style, dark := applyDesktopStyle()
	background := &options.RGBA{R: 250, G: 250, B: 251, A: 255}
	if dark {
		background = &options.RGBA{R: 34, G: 34, B: 38, A: 255}
	}

	app := NewApp(screen, style.accentColor)

	// wails.Run opens the window and blocks until it closes.
	err := wails.Run(&options.App{
		Title:            title,
		Width:            width,
		Height:           height,
		DisableResize:    true,
		BackgroundColour: background,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		// Bind makes App's exported methods callable from TypeScript
		// (generated into frontend/wailsjs/go/main/App.d.ts).
		Bind: []interface{}{
			app,
		},
		Linux: &linux.Options{
			// Wails sets the program name too, but only after the window
			// exists; setProgramName above already did it in time.
			ProgramName: appID,
			Icon:        appIcon,
			// Two small static windows don't need the GPU. Software
			// rendering avoids blank windows on some GPU drivers.
			WebviewGpuPolicy: linux.WebviewGpuPolicyNever,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "penguintier-ui:", err)
		return 1
	}
	return app.exitCode()
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: penguintier-ui join|about")
}
