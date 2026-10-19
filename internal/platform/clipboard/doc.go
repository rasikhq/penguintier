// Package clipboard copies text to the desktop clipboard: wl-copy on
// Wayland, then xclip or xsel on X11 (GNOME and KDE also run X11 apps
// through XWayland, and sync that clipboard with Wayland's).
//
// When no tool works, Copy returns an error and the caller shows the text
// in a notification instead, so the user can still copy it by hand.
package clipboard
