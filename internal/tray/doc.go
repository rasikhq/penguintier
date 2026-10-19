// Package tray runs the system tray icon and its menu.
//
// How the pieces fit together:
//
//   - viewmodel.go: NewViewModel turns what the ZeroTier service reported
//     (plus the saved networks and the tray's own state) into a ViewModel:
//     plain data, no I/O.
//   - menu.go: Build turns a ViewModel into a tree of Nodes, the menu as
//     plain data. It is a pure function (same input, same output, no side
//     effects), so the golden-file tests can check every menu.
//   - icon.go: the icon state (online, offline, attention) and tooltip.
//   - labels.go: escaping "_" in menu text for the tray host.
//   - controller.go: the controller goroutine. It polls the service every
//     2.5 seconds, compares the new Nodes with the shown ones, and changes
//     the real menu as little as possible.
//   - actions.go: what each menu click does.
//   - backend.go: the small interface between the controller and the tray
//     icon library (fyne.io/systray), so tests can use a fake.
//   - helpers.go and dialogs.go: finding and starting penguintier-ui
//     (Join and About windows), with zenity or kdialog as a fallback.
//   - hooks.go: Authorize and Start at Login, which phase P5 provides.
//   - run.go: Run, which ties it all together.
package tray
