//go:build (linux || freebsd || openbsd || netbsd) && !android

// This file was added by the Penguintier project; it is not part of
// upstream fyne.io/systray. Like the rest of this directory, it is under
// the Apache License, Version 2.0. See PENGUINTIER.md.

package systray

import (
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

// TestClickWhileRemoving checks the fix for a crash: a click delivered
// while its menu item is being removed (ResetMenu) used to send on the
// item's already-closed ClickedCh, which panics ("send on closed
// channel").
func TestClickWhileRemoving(t *testing.T) {
	// A click that loses the race logs "no menu item with ID"; keep the
	// test output readable.
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })

	for i := 0; i < 300; i++ {
		item := newMenuItem("item", "", nil)

		// Several goroutines click the item over and over, like a tray
		// host delivering clicks, while it is removed. Sooner or later one
		// of them is between looking the item up and sending on its
		// channel when Remove closes that channel.
		stop := make(chan struct{})
		var clickers sync.WaitGroup
		for c := 0; c < 4; c++ {
			clickers.Add(1)
			go func() {
				defer clickers.Done()
				for {
					select {
					case <-stop:
						return
					default:
						systrayMenuItemSelected(item.id)
					}
				}
			}()
		}
		time.Sleep(100 * time.Microsecond) // let the clickers get going
		item.Remove()
		close(stop)
		clickers.Wait()
	}
}
