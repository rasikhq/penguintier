// Package dnssetup switches a computer to systemd-resolved for "Enable DNS
// support…", safely: it refuses setups it doesn't know, backs up the files
// the switch can change, checks that names still resolve afterwards, and
// undoes everything by itself when they don't.
//
// Why it is this careful: installing systemd-resolved changes how the
// whole computer looks up names. On Debian 13 the package's install script
// starts resolved before D-Bus knows its user, so resolved runs without its
// bus name, NetworkManager can't give it DNS servers, and /etc/resolv.conf
// already points at it: nothing resolves any more. Removing the package
// then rebuilds /etc/resolv.conf from resolved's (empty) server list, so
// that is no undo either. See "Enable DNS support" in docs/architecture.md.
//
// The files:
//
//   - check.go: Facts and Check, which decides whether the setup is one we
//     support (pure, table-tested). The tray runs it before asking for a
//     password; the helper runs it again as root.
//   - facts.go: Gather, which reads those facts from the system.
//   - backup.go: the backup of the affected files and the manifest that
//     records the switch's state.
//   - switch.go: Enable and Undo, the steps with their checks, written
//     against the System interface so tests can fake every command.
//
// cmd/penguintier-dns-helper runs Enable and Undo as root.
package dnssetup
