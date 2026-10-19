// Command penguintier-token-helper prints ZeroTier's local API token.
//
// The tray runs it through pkexec (polkit action
// io.github.rasikhq.Penguintier.read-token), so it runs as root and can read
// /var/lib/zerotier-one/authtoken.secret. The tray then saves the token to
// ~/.zeroTierOneAuthToken (mode 0600).
//
// This is a P0 stub. Phase P1 owns this file. See docs/architecture.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "penguintier-token-helper: not implemented yet")
	// Exit 1 (failure): callers read the token from stdout, and an empty
	// stdout with exit 0 would look like an empty token.
	os.Exit(1)
}
