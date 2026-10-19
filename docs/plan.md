# Penguintier v1: plan

## Context

ZeroTier has a good tray app on macOS and Windows. Linux has none.
- The official app (zerotier/DesktopUI: Rust, MPL-2.0) says Linux "may build but there are still outstanding issues". It never ships for Linux.
- Linux users get only `zerotier-cli`, or tralph3/ZeroTier-GUI (Tkinter, no tray, idle since 2023).
- On Linux, "Allow DNS Configuration" does nothing. The service only logs a warning (ZeroTierOne issue #2492; the upstream fix, PR #2575, is still WIP).

Penguintier is an MIT-licensed Linux tray power tool. Goals:
1. Match the official tray menu feature for feature.
2. Make ZeroTier DNS actually work on Linux.
3. Stay easy to read, review and grow, including for contributors who are new to Go.

**Repo today:** a Tauri 2 + React + Vite + bun template on branch `feat/setup`, remote `github.com/rasikhq/penguintier`. It holds only a demo:
- `pkexec cat authtoken.secret` on every launch
- fetch `/status`

It also has a typo'd identifier (`come.rasikh.penguintier`), and `LICENSE` was deleted in the init commit. **The template is replaced, not kept.** Choosing Tauri/Rust was partly shaped by this scaffold; that was revisited, and the stack below was chosen fresh.

## Locked decisions (from user)

- **v1 scope:** official-tray parity plus the Linux DNS fix. Nothing else.
- **Desktops:** any desktop that works without custom per-desktop code. That covers KDE, GNOME (with the AppIndicator extension), XFCE, Cinnamon, MATE, Budgie, LXQt, COSMIC and waybar/sway. A dedicated GNOME extension waits until later.
- **Distro floor:** Ubuntu 24.04+, Debian 12+, current Fedora, Mint 22+. The polkit JS rules require this.
- **Packaging:** .deb + .rpm on GitHub Releases. AUR, AppImage, Flatpak and OBS repos come later.
- **License:** restore MIT with `git checkout 7109743 -- LICENSE`.
- **Stack: Go + React.** Tray, DNS daemon and helpers are written in Go. The Join/About windows are React + TypeScript via Wails v2, launched on demand as a separate process that exits on close. The user may grow these into a full GUI later.
- **Icons/brand:** designed by Claude. The user picks one from an HTML preview page. The icon is original, not the ZeroTier logo. The README and About say "Unofficial; not affiliated with ZeroTier, Inc."
- **DNS:** done through systemd-resolved. Where resolved is missing (Debian), the tray offers "Enable DNS support…", which installs systemd-resolved through PackageKit behind a single polkit prompt.
- **Execution:** each phase runs in its own session and worktree and ends in one PR to `master`. Phases run in parallel where they own disjoint files.

## Tech choice (why)

| Option | Verdict |
|---|---|
| **Go + `fyne.io/systray`** for the tray | **Chosen.** Pure Go on Linux (godbus, no GTK). StatusNotifierItem with dbusmenu, check items, submenus, `ResetMenu` and tooltip. Re-registers when the tray host restarts. `ItemIsMenu` is set when no tap handlers are set, so left-click opens the menu. Tailscale's Linux tray uses it. Static binary (`CGO_ENABLED=0`, to confirm in P0), so no glibc floor. Go is quick to learn. |
| Rust + ksni | Best tray library and smallest footprint, but harder for most contributors to read and review than Go. Rejected for maintainability. |
| Tauri / Electron tray (libappindicator) | On Linux: no click events, no tooltip, heavy runtime. Rejected. |
| Python + Qt | Readable, but shipping Python/Qt across distros is the most painful option. Rejected. |
| **Wails v2 + React/TS** for windows | **Chosen.** Stable Go + webview framework, and it reuses React skills. Build tag `webkit2_41`. Wails v3 is still beta. |
| **GoReleaser (nfpm inside)** for packaging | **Chosen.** One config builds .deb + .rpm with systemd units, polkit, sysusers and scripts, plus checksums and the GitHub release. AUR support later comes for free. |

## Architecture

There is one Go module, `github.com/rasikhq/penguintier`, built with no cgo. `ui/` is its own Go module because it uses cgo/webkit.

**Shared library**
- `internal/ztapi` (local API client):
  - **Models:** structs, a `NetworkID` type, and `Status` with an "unknown" fallback.
  - **Discovery:** token and port lookup.
  - **HTTP client:** stdlib `net/http`, `Proxy: nil`, 2 s timeout.
  - **Typed errors:** `ErrNotInstalled`, `ErrServiceDown`, `ErrTokenMissing`, `ErrUnauthorized`, timeouts, protocol and HTTP errors.
  - **Saved-networks store:** includes a one-time import of DesktopUI's `~/.zerotier_ui/saved_networks.json`.
- `internal/resolved`: godbus resolve1 calls `SetLinkDNS`, `SetLinkDomains` (routing-only `~domain`), `SetLinkDefaultRoute(false)` and `RevertLink`, plus link property reads and `Available()`.
- `internal/tray`: view model, a pure menu builder, and the controller.
- `internal/platform/*`: `clipboard`, `notify`, `open`, `autostart`, `instance`, `watcher`, `packagekit`.
- `internal/doctor`.

**`cmd/penguintier`** (the tray)
- Subcommands `doctor [--json]` and `deauthorize`, plus the `--autostart` flag.

**`cmd/penguintier-dnsd`** (system service)
- Runs as `User=penguintier-dns` (created via sysusers). It gets the token through `LoadCredential=`, so it never reads `identity.secret`.
- A polkit rule grants it only the resolve1 `set-dns-servers`, `set-domains`, `set-default-route` and `revert` actions.
- Every 5 s it reconciles, using a pure `Plan()` function, and reverts everything on SIGTERM.
- It leaves alone any link whose DNS it did not set, so it steps aside if ZeroTier ever ships native DNS.
- It idles while resolved is absent and wakes when resolve1 appears on the bus.
- It is started by `penguintier-dnsd.path`, which waits for `authtoken.secret` to exist.

**`cmd/penguintier-token-helper`** (tiny static binary)
- Runs via pkexec under the polkit action `io.github.rasikhq.Penguintier.read-token` and prints the token.
- The unprivileged tray writes the token to `~/.zeroTierOneAuthToken` (mode 0600, atomic write). `zerotier-cli` already reads this file.

**`cmd/zt-mock`** (dev only)
- Uses `httptest`-style handlers to serve fake-API scenarios: online, service-down, unauthorized, sso-required, access-denied, many-networks, joining.

**`ui/`** (Wails v2 app, frontend in React + TS + Vite, built with bun via `wails.json`)
- `penguintier-ui join` prints a 16-hex-character network ID and exits 0; cancel exits 1.
- `penguintier-ui about` shows the About window.
- The UI never touches the network and never sees the token.

**Contracts** (written to `docs/architecture.md` in P0 so parallel sessions agree)
- **App ID:** `io.github.rasikhq.Penguintier`. Used for the .desktop file, icons, the D-Bus single-instance name, the Wails/WM class and the polkit prefix.
- **Install paths:** `/usr/bin/penguintier` and `/usr/libexec/penguintier/{penguintier-ui,penguintier-token-helper,penguintier-dnsd}`.
- **Helper lookup order:** `$PENGUINTIER_LIBEXEC_DIR`, then the tray's own directory, then `/usr/libexec/penguintier`.
- **Environment variables:** `PENGUINTIER_API_URL`, `PENGUINTIER_TOKEN_FILE`, `PENGUINTIER_LIBEXEC_DIR`, `PENGUINTIER_LOG`.
- **Token order:** env → `~/.zeroTierOneAuthToken` → `~/.zerotier-local-auth` → the system token if readable.
  - Refuse a token file the user doesn't own or that is group/world-readable, and offer to fix it.
- **Port order:** `zerotier-one.port`, then 9993. Address order: 127.0.0.1, then [::1].
- **Config files:** `$XDG_CONFIG_HOME/penguintier/{saved_networks.json,state.json}`.
- **Autostart file:** `~/.config/autostart/io.github.rasikhq.Penguintier.desktop` with `Exec=penguintier --autostart`.
- **Icon names:** `io.github.rasikhq.Penguintier` for the app, plus `penguintier-{online,offline,attention}` tray PNG/SVG states.
- **Tray rules:**
  - Click handlers only send commands on a channel; no blocking I/O in handlers.
  - A controller goroutine polls every 2.5 s.
  - When only text or check state changes, update items in place. Call `ResetMenu` and rebuild only when the structure changes (networks added or removed).
  - Sort networks by ID.
  - Keep menu depth ≤ 2: addresses and routes are flattened into the network submenu as sections.
  - Test whether `_` in labels needs escaping.
- **Code style:** Go code stays plain and well commented, for contributors new to Go: no clever generics, simple goroutine patterns. Each PR explains new Go concepts it introduces.

## v1 tray menu (parity + Linux extras)

**Parity with the official tray:**
- "My Address: <id>": click copies it and shows a notification.
- "Join New Network…" opens the Wails dialog (zenity/kdialog as fallback). A disabled "Joining…" row shows until the network appears.
- One submenu per network, titled "name (nwid)" with a check mark:
  - "Network ID" (click copies).
  - Checkboxes: Allow Managed Addresses, Allow Global IPs, Allow Default Route Override, Allow DNS Configuration.
  - Read-only rows: Status, Ethernet, Device, Type, DNS Domain.
  - SSO: expiry time, and "Open SSO Login URL…".
  - Address and route sections. Click copies; addresses are copied without the `/prefix`.
  - "Disconnect": saves the network's settings, then sends DELETE.
- "(no networks joined)" when there are none.
- Saved (disconnected) networks with Reconnect and Forget.
- Networks that need SSO get a top-level item and a one-time notification.
- "Waiting for ZeroTier system service…" when the service is unreachable.
- "Start at Login", About, Quit.

**Linux extras in v1:**
- Icon state (online / offline / attention) and a tooltip summary.
- Allow DNS that actually works, plus a per-network "DNS: applied / needs resolved / daemon off / link managed" row.
- "Enable DNS support…" via PackageKit.
- "Authorize Penguintier…" onboarding.
- A notice when no tray host is found, with fix steps for each desktop.
- Single instance.
- `penguintier doctor` for bug reports.

**Clipboard:**
- Try `wl-copy` on Wayland, then `xclip`/`xsel` on X11, and finally a notification showing the text.
- The package Recommends `wl-clipboard` and `xclip`.
- The official app only uses xclip, which breaks on Wayland.

## Repo layout (created in P0; the Tauri template is removed)

```
go.mod go.sum  Makefile  .golangci.yml  .goreleaser.yaml  LICENSE README.md CLAUDE.md CHANGELOG.md
docs/{architecture,testing}.md
cmd/{penguintier,penguintier-dnsd,penguintier-token-helper,zt-mock}/main.go
internal/{ztapi,resolved,tray,doctor,platform/{clipboard,notify,open,autostart,instance,watcher,packagekit}}/
ui/{go.mod,main.go,app.go,wails.json,build/, frontend/{package.json,bun.lock,vite.config.ts,src/{main,Join,About}.tsx}}
assets/icons/hicolor/{scalable,16..256}/apps/   assets/tray/   assets/brand/preview.html
packaging/{scripts/,desktop/,metainfo/,systemd/{penguintier-dnsd.service,.path,80-penguintier.preset},
           polkit/{io.github.rasikhq.Penguintier.policy,50-penguintier-dnsd.rules},sysusers/penguintier.conf}
scripts/zt-selftest-network.sh       .github/workflows/{ci,package,release}.yml
```

**Makefile targets:** `build test lint ui-dev ui-build run mock tray-mock live-test dev-install-helper package`.

## Phases (one session each, one PR each)

```
P0 ──┬─ P1 ztapi+helper ─────┬─ P3 tray core ─┐
     ├─ P2 UI + icons/brand ─┤                ├─ P5 desktop integration ─ P7 release
     └─ P6 packaging + CI ───┴─ P4 DNS daemon ┘
Wave 1 = {P1, P2, P6} in parallel. Wave 2 = {P3, P4} in parallel.
```

**Rules for every phase:**
- Edit only the paths the phase owns.
- P0 stubs already import every dependency, so `go.mod` rarely changes.
- On a `go.sum`/`go.mod` conflict: keep the union of both sides, then run `go mod tidy`.

**P0: Foundation** (sequential, on `feat/setup`, PR into master)
- **Cleanup:**
  - Restore LICENSE.
  - Delete the Tauri template: `src-tauri/`, root `package.json`, `bun.lock`, `src/`, `index.html`, the vite/tsconfig files, `public/` and `node_modules`.
- **Scaffolding:**
  - Run `wails init -t react-ts` into `ui/`, switch it to bun, and set `-tags webkit2_41`. Add placeholder Join/About screens selected by argv.
  - Create the Go module with compiling stubs for all `cmd/` and `internal/` packages; all dependencies are imported.
  - Confirm `CGO_ENABLED=0` builds the tray with fyne systray.
- **Docs and tooling:**
  - `docs/architecture.md`: the contracts above, plus a map of which phase owns which files.
  - `CLAUDE.md`: include the beginner-friendly Go style rule.
  - Makefile and `.golangci.yml`.
  - Valid placeholder files for the systemd units, polkit files, sysusers config, .desktop file and icons.
- **CI:** `ci.yml` runs gofmt, `go vet`, golangci-lint, `go test -race`, govulncheck and `go-licenses check`, plus a UI build job.
- **Done when:** `make lint test build ui-build` passes and CI is green.

**P1: ztapi, zt-mock, token helper** (wave 1)
- **Owns:** `internal/ztapi`, `cmd/{zt-mock,penguintier-token-helper}`, `packaging/polkit/*.policy`.
- **Tests:**
  - `httptest` coverage for every endpoint and error mapping.
  - Fixtures captured from 1.16.2 and sanitized, stored in `testdata/`.
  - Live tests behind the `live` build tag.
- **Dev tool:** `cmd/ztctl` with status, list, join, leave and set.

**P2: UI dialogs + icons/brand** (wave 1)
- **Owns:** `ui/**`, `assets/**`.
- **Join dialog:** hex-only input filter. The Join button stays disabled until 16 characters are entered. Enter submits and Esc cancels.
- **About:** version, MIT license, repo link, disclaimer.
- **Brand work:**
  - 2–3 SVG concepts (penguin with an orbit/network motif) in `assets/brand/preview.html` for the user to pick from.
  - Once picked: the final hicolor set and tray state icons.

**P6: Packaging + CI** (wave 1, built against the stubs)
- **Owns:** `.goreleaser.yaml`, `packaging/{scripts,metainfo,systemd/*.preset}`, `.github/workflows/{package,release}.yml`.
- **Package dependencies:**
  - deb Depends: `pkexec, polkitd, libwebkit2gtk-4.1-0, libgtk-3-0`.
  - rpm Requires: `polkit, webkit2gtk4.1, gtk3`.
  - Recommends: `zerotier-one, wl-clipboard, xclip`.
  - Suggests (not Recommends): `systemd-resolved`.
- **postinst:** runs sysusers, `daemon-reload`, then presets and starts `penguintier-dnsd.path`.
- **Builds:**
  - The Go binaries are static.
  - The UI binary is built inside a `debian:12` container (glibc 2.36 floor) on an `ubuntu-24.04` runner.
- **CI checks:**
  - Install smoke tests in debian:12, ubuntu:24.04 and fedora:latest containers.
  - Pushing a `v*` tag publishes the .deb, .rpm and SHA256SUMS.

**P3: Tray core** (wave 2, needs P1)
- **Owns:** `internal/tray`, `internal/platform/{clipboard,notify,open}`.
- **Deliverables:**
  - A pure `Build(ViewModel) []Node` function plus the controller.
  - The full parity menu.
  - Icon states and tooltip.
  - Notifications sent over godbus.
  - Links opened with `xdg-open`.
  - Golden-file menu snapshots for every zt-mock scenario.

**P4: DNS daemon** (wave 2, needs P1)
- **Owns:** `internal/resolved`, `cmd/penguintier-dnsd`, `packaging/systemd/penguintier-dnsd.*`, `packaging/polkit/*.rules`, `packaging/sysusers/`, `scripts/zt-selftest-network.sh`.
- **Tests:**
  - Table tests for the planner.
  - Tests against a fake resolve1 exported on a private `dbus-daemon`.
- **Unit hardening:**
  - `ProtectSystem=strict`, an empty `CapabilityBoundingSet`, `IPAddressAllow=localhost`, `SystemCallFilter=@system-service`.
  - Target `systemd-analyze security` ≤ ~2.
- **Runtime behavior:**
  - Exit on HTTP 401 so `Restart=on-failure` reloads the token credential.
  - Watch for resolve1 to appear on the bus.

**P5: Desktop integration** (needs P2, P3, P4)
- **Owns:** `cmd/penguintier`, `internal/{doctor,platform/{autostart,instance,watcher,packagekit}}`. It may also touch P3's files.
- **Onboarding:**
  - Prompt automatically only on the first interactive run, never at autostart. Otherwise show an "Authorize Penguintier…" item.
  - Handle pkexec exit codes: 126 = the user dismissed it; 127 = no polkit agent, so print a manual command via `doctor`.
- **Other deliverables:**
  - DesktopUI import.
  - Autostart toggle.
  - Single instance: claim the D-Bus name with `DoNotQueue`.
  - Missing-tray-host check: is anything owning `org.kde.StatusNotifierWatcher` after a 10 s grace period?
  - DNS status row.
  - "Enable DNS support…" via PackageKit D-Bus. Verify whether NetworkManager needs a reload afterwards.
  - `doctor [--json]` and `deauthorize`.

**P7: Release candidate** (sequential)
- **README:** install steps, GNOME extension steps, a DNS explainer, a security note about the token copy, `doctor` troubleshooting, and the disclaimer.
- **Release files:** CHANGELOG, metainfo, and a man page.
- **Funding:** `.github/FUNDING.yml` and `funding.json`.
- **Release:** run the full manual matrix, fix what it finds, then tag `v0.1.0`.

## Verification

- **Unit tests:**
  - ztapi: fixtures for every status, IPv6, `via:null` and unknown fields; error mapping; token discovery with a temporary HOME; atomic writes of the saved store.
  - Tray: golden menus, the no-op-update check.
  - DNS: planner tables.
- **Live tests:** `make live-test` runs against the real 1.16.2 service.
  - Read-only calls first.
  - Then join a throwaway network ID, toggle its flags, and leave.
  - `scripts/zt-selftest-network.sh` creates a network on the node's own controller with DNS domain `pt.test`. That gives an end-to-end DNS test with no account; run it in a VM.
- **Packages:** container install smoke tests, `systemd-analyze verify`, `penguintier doctor --json`.
- **Manual matrix** (GNOME Boxes or quickemu VMs, with snapshots):
  - Ubuntu 24.04 GNOME: primary, DNS end-to-end.
  - Fedora GNOME: no tray by default, SELinux.
  - Fedora KDE: tooltip.
  - Debian 13 GNOME: the "Enable DNS support" path.
  - Debian 12 XFCE: X11.
  - Mint 22 Cinnamon.
  - Fedora Sway: no polkit agent.
  - Pop!_OS COSMIC.
- **Every PR** lists its own manual test steps.

## Key risks

| Risk | Mitigation |
|---|---|
| Tray invisible on GNOME without the extension | Watcher check, a notification, `doctor`, and the README. The icon appears on its own once the extension is enabled. |
| Menu flicker, or a click landing on the wrong item after a rebuild | Update items in place; call `ResetMenu` only on structural change; keep stable ordering. |
| fyne systray gaps (tooltip ignored on GNOME, label escaping) | Tooltips are cosmetic. Test escaping in P3. Fallback: write our own small SNI with godbus. |
| Clipboard on GNOME Wayland | `wl-copy`, then xclip via XWayland, then a notification. |
| Wails/webkit: package size, NVIDIA blank window, Wayland focus | Spawn with `WEBKIT_DISABLE_DMABUF_RENDERER=1`. zenity fallback. Test in P2 and P5. |
| NetworkManager/networkd overriding link DNS, or "link is managed" errors | Re-apply on drift every 5 s; show the error in the tray row and in `doctor`. |
| The token copy widens local control | Mode 0600, owner/mode checks, `deauthorize`, documented. A system broker can come later. |
| Code from DesktopUI (MPL-2.0) creeping in | Clean-room build from observed behavior and the API spec. `go-licenses` runs in CI. |

## Later backlog (post-v1)

- **More packaging:** AUR (GoReleaser `aurs`), AppImage, OBS apt/rpm repos, a Nix flake. Flathub only once there is a main window and a token-import flow.
- **More features:**
  - Peers/paths view, alerts.
  - ZeroTier Central API: Legacy v1 personal tokens and New Central v2.
  - Hosts-file sync.
- **Desktop and architecture:** a GNOME Shell extension, the full management GUI in `ui/`, and a system D-Bus broker to replace the per-user token copy.
