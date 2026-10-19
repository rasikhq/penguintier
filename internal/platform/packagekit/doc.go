// Package packagekit installs a package through PackageKit's D-Bus API.
// The tray's "Enable DNS support…" uses it to install systemd-resolved.
//
// PackageKit (org.freedesktop.PackageKit on the system bus) is the
// distribution-neutral software service behind GNOME Software and KDE
// Discover. It runs apt, dnf, zypper, ... for us, and asks polkit whether
// the user may install software, which shows the desktop's usual password
// prompt. So Penguintier itself never runs anything as root here.
//
// The API works in "transactions": ask the service for a new transaction
// object, call one method on it (Resolve, InstallPackages, ...), and
// listen to the object's signals (Package, ErrorCode, Finished) to get the
// results. Spec: https://www.freedesktop.org/software/PackageKit/gtk-doc/
package packagekit
