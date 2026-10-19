// Package resolved sets per-link DNS in systemd-resolved over D-Bus
// (org.freedesktop.resolve1 on the system bus). penguintier-dnsd uses it to
// give each ZeroTier interface the network's DNS servers and a routing-only
// "~domain".
//
// What it offers:
//
//   - Writes, through resolved's Manager object: SetLinkDNS, SetLinkDomains,
//     SetLinkDefaultRoute and RevertLink. Each needs the matching polkit
//     action (packaging/polkit/50-penguintier-dnsd.rules grants them to the
//     penguintier-dns user).
//   - Reads, which any user may do: Link returns a link's current DNS
//     servers, domains and DefaultRoute setting.
//   - Available tells whether resolved is on the bus at all, and
//     WatchOwner reports when it appears or goes away.
//   - IfIndex turns an interface name such as "ztabcdefgh" into the number
//     resolved wants.
//
// # D-Bus in a nutshell
//
// D-Bus is the message bus Linux desktops and services use to call each
// other. Think of it as local RPC:
//
//   - A program connects to a bus (the "system bus" for system services)
//     and may own a well-known name, here "org.freedesktop.resolve1".
//   - It exports objects at paths ("/org/freedesktop/resolve1"). Each
//     object has interfaces ("org.freedesktop.resolve1.Manager"), and an
//     interface has methods, properties and signals.
//   - Every value has a type signature. "a(iay)" reads as: an array (a) of
//     structs (the parentheses) holding an int32 (i) and a byte array (ay).
//     godbus maps Go types onto these: int32 is i, []byte is ay, a Go
//     struct is a D-Bus struct, a slice is an array.
//
// We use github.com/godbus/dbus/v5, a pure-Go D-Bus library (no cgo).
//
// The resolve1 API is documented in org.freedesktop.resolve1(5)
// ("man org.freedesktop.resolve1").
package resolved
