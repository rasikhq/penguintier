// Command penguintier-dnsd is the system service that applies ZeroTier
// network DNS settings (servers and search domains) to systemd-resolved,
// for networks where "Allow DNS Configuration" is on.
//
// It runs as the penguintier-dns system user, reconciles every 5 seconds
// and reverts everything it set when it stops.
//
// This is a P0 stub that only prints "not implemented yet". Phase P4 owns
// this file. See docs/architecture.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "penguintier-dnsd: not implemented yet")
}
