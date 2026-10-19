// Package resolved sets per-link DNS in systemd-resolved over D-Bus
// (org.freedesktop.resolve1): SetLinkDNS, SetLinkDomains (routing-only
// "~domain" entries), SetLinkDefaultRoute(false) and RevertLink, plus link
// property reads and an Available check.
//
// This is a P0 stub with no code yet. Phase P4 owns this package.
// See docs/architecture.md.
package resolved
