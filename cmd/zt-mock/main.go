// Command zt-mock is a fake ZeroTier One local API for development.
//
// It will serve canned scenarios (online, service-down, unauthorized,
// sso-required, access-denied, many-networks, joining) so the tray can be
// tested without a real ZeroTier service. Run it with `make mock`.
//
// This is a P0 stub. Phase P1 owns this file. See docs/architecture.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "zt-mock: not implemented yet")
}
