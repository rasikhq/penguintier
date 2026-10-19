module github.com/rasikhq/penguintier

go 1.24.0

toolchain go1.27.2

require (
	fyne.io/systray v1.12.2
	github.com/godbus/dbus/v5 v5.2.4
)

require golang.org/x/sys v0.27.0 // indirect

// fyne.io/systray comes from a patched copy of v1.12.2 in this repo, which
// fixes a crash when a menu click arrives while the menu is rebuilt. See
// third_party/fyne-systray/PENGUINTIER.md.
replace fyne.io/systray => ./third_party/fyne-systray
