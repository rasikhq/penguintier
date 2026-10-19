# Penguintier

A system tray app for [ZeroTier](https://www.zerotier.com/) on Linux.

> **Unofficial; not affiliated with ZeroTier, Inc.**

> **Status: early development.** Nothing works yet. This repository holds
> the project skeleton; features land phase by phase (see
> [docs/plan.md](docs/plan.md)).

## Planned for v1

- The same tray menu as the official macOS/Windows app: your address, join
  and leave networks, per-network settings, SSO login, saved networks.
- "Allow DNS Configuration" that actually works on Linux, through
  systemd-resolved.
- .deb and .rpm packages for Ubuntu 24.04+, Debian 12+, Fedora and Mint 22+.

## Development

You need Go, bun, and the GTK 3 and WebKitGTK 4.1 development packages
(on Debian/Ubuntu: `libgtk-3-dev libwebkit2gtk-4.1-dev`). Go downloads the
exact version pinned in `go.mod` by itself; if `go env GOTOOLCHAIN` prints
`local`, run `go env -w GOTOOLCHAIN=auto` once.

```sh
make tools   # installs golangci-lint and the Wails CLI
make lint test build ui-build
```

See [docs/architecture.md](docs/architecture.md) and
[docs/testing.md](docs/testing.md).

## License

[MIT](LICENSE)
