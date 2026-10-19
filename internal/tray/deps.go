package tray

// The blank import (the "_") pulls in a dependency without using it yet,
// so go.mod and go.sum already list it before phase P3 writes the real
// code. Delete this file once the package imports it for real.
import (
	_ "fyne.io/systray" // keeps the dependency in go.mod until phase P3 uses it
)
