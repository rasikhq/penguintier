// Package autostart turns "Start at Login" on and off by writing or
// removing ~/.config/autostart/io.github.rasikhq.Penguintier.desktop
// (Exec=penguintier --autostart).
//
// Desktops start every program listed in that folder when the user logs
// in. The format is the XDG Autostart spec:
// https://specifications.freedesktop.org/autostart-spec/latest/
package autostart
