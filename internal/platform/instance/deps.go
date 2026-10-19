package instance

// The blank import (the "_") pulls in a dependency without using it yet,
// so go.mod and go.sum already list it before phase P5 writes the real
// code. Delete this file once the package imports it for real.
import (
	_ "github.com/godbus/dbus/v5" // keeps the dependency in go.mod until phase P5 uses it
)
