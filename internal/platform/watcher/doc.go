// Package watcher checks whether a tray host is running, i.e. whether
// anything owns org.kde.StatusNotifierWatcher on the session bus and a
// panel has registered with it to draw the icons. When none is, it tells
// the user how to get one on their desktop.
//
// Tray icons on Linux use the StatusNotifierItem protocol: each app
// registers its icon with the "watcher", and the "host" (a panel, or
// GNOME's AppIndicator extension) asks the watcher for the icons and draws
// them. Stock GNOME has neither, so a tray app is invisible there until
// the AppIndicator extension is turned on.
//
//   - watcher.go: Check, the D-Bus check, and the GNOME extension's state.
//   - advice.go: which desktop and distribution this is, and the fix steps
//     for it.
//   - notice.go: waiting out the grace period, and showing the notice only
//     once per login.
package watcher
