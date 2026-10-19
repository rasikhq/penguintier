package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// repoURL is the only web page the UI ever opens (from the About window).
const repoURL = "https://github.com/rasikhq/penguintier"

// networkIDPattern matches a ZeroTier network ID: exactly 16 hex digits.
var networkIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// errBadNetworkID is returned for anything that isn't 16 hex digits.
// errors.New makes a fixed error value that callers can compare against.
var errBadNetworkID = errors.New("a network ID is 16 characters: 0-9 and a-f")

// App is shared between Go and the React frontend. Its exported methods
// (capitalized names) are "bound": Wails generates a TypeScript function
// for each one in frontend/wailsjs/go/main/App.d.ts, so React can call Go
// directly and await the result as a Promise. A returned error rejects
// that Promise.
type App struct {
	ctx    context.Context // Wails runtime context, set in startup
	screen string          // "join" or "about"
	accent string          // the desktop's accent color (CSS), or ""

	// Wails calls bound methods on its own goroutines, while main reads the
	// exit code after the window closes. The mutex makes sure the two never
	// touch code at the same time.
	mu   sync.Mutex
	code int // the process exit code
}

// NewApp creates the App for one screen. accent is the desktop's accent
// color as a CSS color, or "" to keep the default blue.
func NewApp(screen, accent string) *App {
	// Closing the Join window without joining counts as cancel (exit 1).
	// Closing the About window is the normal way out (exit 0).
	code := 0
	if screen == screenJoin {
		code = 1
	}
	return &App{screen: screen, accent: accent, code: code}
}

// startup is called by Wails when the window is ready. The context is
// needed for runtime calls such as runtime.Quit.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// exitCode returns the process exit code chosen so far. It starts with a
// lowercase letter, so it is not exported and Wails does not bind it.
func (a *App) exitCode() int {
	a.mu.Lock()
	defer a.mu.Unlock() // defer runs this when the function returns
	return a.code
}

func (a *App) setExitCode(code int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.code = code
}

// Screen tells the frontend which window to render: "join" or "about".
func (a *App) Screen() string {
	return a.screen
}

// AccentColor returns the desktop's accent color as a CSS color, or "".
func (a *App) AccentColor() string {
	return a.accent
}

// Version returns the app version for the About window.
func (a *App) Version() string {
	return version
}

// Join prints the network ID on stdout (the tray reads it from there) and
// closes the window with exit code 0. It returns an error, and keeps the
// window open, if id is not 16 hex digits.
func (a *App) Join(id string) error {
	id, err := normalizeNetworkID(id)
	if err != nil {
		return err
	}
	fmt.Println(id)
	a.setExitCode(0)
	runtime.Quit(a.ctx)
	return nil
}

// Cancel closes the Join window with exit code 1.
func (a *App) Cancel() {
	a.setExitCode(1)
	runtime.Quit(a.ctx)
}

// Close closes the About window with exit code 0.
func (a *App) Close() {
	a.setExitCode(0)
	runtime.Quit(a.ctx)
}

// OpenRepo opens the project page in the default web browser. The address
// is fixed here in Go, so the web page can't use this to open anything else.
func (a *App) OpenRepo() {
	runtime.BrowserOpenURL(a.ctx, repoURL)
}

// normalizeNetworkID lowercases id, drops surrounding spaces, and checks that
// the result is a network ID. The frontend already filters what the user
// types; this check is the last line of defence before the ID reaches the
// tray.
func normalizeNetworkID(id string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !networkIDPattern.MatchString(id) {
		return "", errBadNetworkID
	}
	return id, nil
}
