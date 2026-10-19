# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-19

The first release.

### Added

- Tray menu with the same entries as the official ZeroTier app on macOS
  and Windows:
  - "My Address" (click to copy), Join New Network…, and a "Joining…" row
    until the network shows up.
  - One submenu per network: Network ID, the four Allow check boxes
    (Managed Addresses, Global IPs, Default Route Override, DNS
    Configuration), status, interface, MAC, type, DNS domain, addresses
    and routes (click to copy, addresses without the `/prefix`), and
    Disconnect.
  - Saved networks with Reconnect (with the saved settings) and Forget.
    The official tray app's saved networks
    (`~/.zerotier_ui/saved_networks.json`) are imported once.
  - SSO: a "Log In to …" entry, a one-time notification, and the login's
    expiry time.
  - "Waiting for ZeroTier system service…" while the service is down.
  - Start at Login, About, Quit.
- Tray icon for online, offline and needs attention, and a tooltip summary
  (on desktops that show tooltips).
- DNS for ZeroTier networks on Linux: the `penguintier-dnsd` system service
  hands each network's DNS domain and servers to systemd-resolved as a
  routing-only domain, so only that domain's names go to the network's
  servers. It leaves alone interfaces whose DNS it didn't set. A "DNS: …"
  row in each network's submenu shows the state.
- "Enable DNS support…" for Debian desktops without systemd-resolved:
  backs up the DNS files, installs systemd-resolved behind one password
  prompt, checks that names still resolve and undoes everything by itself
  if they don't. `penguintier-dns-helper undo` switches back later.
- First-run authorization through a polkit prompt: the ZeroTier API token
  is copied to `~/.zeroTierOneAuthToken` (mode 0600), which `zerotier-cli`
  uses too. `penguintier deauthorize` deletes the copy.
- `penguintier doctor [--json]`: checks ZeroTier, the token, the tray
  host, notifications, the clipboard, polkit, systemd-resolved and the DNS
  service, with a fix for each problem.
- A notification with per-desktop fix steps when no tray host is running
  (for example GNOME without the AppIndicator extension).
- Single instance, notifications, copying with wl-clipboard, xclip or xsel
  (or a notification when none is installed).
- Join and About windows (Wails + React), following the desktop's light or
  dark style.
- .deb and .rpm packages for x86_64, with the `penguintier(1)` man page and
  the licenses of all bundled third-party code.

[Unreleased]: https://github.com/rasikhq/penguintier/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/rasikhq/penguintier/releases/tag/v0.1.0
