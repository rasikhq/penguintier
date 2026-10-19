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
// This is the P0 placeholder. Phase P2 owns ui/ and builds the real windows.
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
	// Sizes fit the placeholder content (not counting the title bar).
	switch screen {
	case screenJoin:
		title, width, height = "Join Network", 440, 216
	case screenAbout:
		title, width, height = "About Penguintier", 420, 224
	default:
		printUsage()
		return 2
	}

	app := NewApp(screen)

	// wails.Run opens the window and blocks until it closes.
	err := wails.Run(&options.App{
		Title:         title,
		Width:         width,
		Height:        height,
		DisableResize: true,
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
			ProgramName: appID,
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
