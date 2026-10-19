# Penguintier architecture and contracts

This file holds the agreements every phase builds against. The phases
(P1–P7) run in parallel sessions, so **change a contract here first, in its
own PR, before writing code that depends on the change.** The full plan, with
background and reasoning, is in [plan.md](plan.md).

## Overview

Penguintier is an unofficial ZeroTier tray app for Linux, with two goals:

- match the official macOS/Windows tray menu, and
- make ZeroTier's "Allow DNS Configuration" work on Linux, through
  systemd-resolved.

It has two Go modules:

| Module | Path | cgo | What it holds |
|---|---|---|---|
| `github.com/rasikhq/penguintier` | `/` | no (`CGO_ENABLED=0`) | the tray, the DNS daemon, helpers and all shared code |
| `github.com/rasikhq/penguintier/ui` | `ui/` | yes (GTK + WebKitGTK) | the Wails v2 + React/TypeScript Join and About windows |

`ui/` is its own module so the main binaries stay static. Running
`go build ./...` from the repo root does not include `ui/`, so use
`make ui-build` for it.

## Binaries

| Binary | Source | Installed at | Runs as |
|---|---|---|---|
| `penguintier` | `cmd/penguintier` | `/usr/bin/penguintier` | the desktop user |
| `penguintier-ui` | `ui/` | `/usr/libexec/penguintier/penguintier-ui` | the desktop user, started on demand by the tray |
| `penguintier-token-helper` | `cmd/penguintier-token-helper` | `/usr/libexec/penguintier/penguintier-token-helper` | root, through `pkexec` |
| `penguintier-dns-helper` | `cmd/penguintier-dns-helper` | `/usr/libexec/penguintier/penguintier-dns-helper` | root, through `pkexec` ("Enable DNS support…") |
| `penguintier-dnsd` | `cmd/penguintier-dnsd` | `/usr/libexec/penguintier/penguintier-dnsd` | system user `penguintier-dns`, under systemd |
| `zt-mock` | `cmd/zt-mock` | not shipped (dev only) | developer |
| `ztctl` | `cmd/ztctl` | not shipped (dev only) | developer |
| `licensegen` | `cmd/licensegen` | not shipped (build tool of `make third-party-licenses`) | developer, GoReleaser |

### Shared packages

| Package | Purpose |
|---|---|
| `internal/ztapi` | Local API client: models (`NetworkID`, `Status` with an "unknown" fallback), token and port discovery, `net/http` client (`Proxy: nil`, 2 s timeout), typed errors (`ErrNotInstalled`, `ErrServiceDown`, `ErrTokenMissing`, `ErrUnauthorized`, timeouts, protocol and HTTP errors), and the saved-networks store, including a one-time import of DesktopUI's `~/.zerotier_ui/saved_networks.json`. |
| `internal/ztapi/ztmock` | A fake ZeroTier API (`http.Handler`) with the seven scenarios of `zt-mock`. Importable, so other packages' tests (the tray's golden menus, the DNS daemon) run against it through `httptest`. `cmd/zt-mock` is a thin command around it. |
| `internal/resolved` | systemd-resolved over D-Bus: `SetLinkDNS`, `SetLinkDomains` (routing-only `~domain`), `SetLinkDefaultRoute(false)`, `RevertLink`, link property reads, `Available()`. |
| `internal/dnsrule` | Which networks get DNS applied, and with what (`Check`): allowDNS on, status OK, an interface, a valid domain and server. It is pure, and the DNS daemon, the tray and `doctor` all use it. |
| `internal/dnsstatus` | Each network's DNS state (`Evaluate`, pure) from facts read unprivileged over the system bus (`Probe`): resolve1 link properties, `penguintier-dnsd.service`'s `ActiveState` (systemd1), and whether systemd-networkd manages the link (network1). |
| `internal/tray` | View model, a pure menu builder `Build(ViewModel) []Node`, and the controller. |
| `internal/doctor` | The report behind `penguintier doctor [--json]`: `Gather` (I/O) → `Facts` → `Evaluate` (pure) → `Report`. |
| `internal/platform/clipboard` | `wl-copy` on Wayland, then `xclip`/`xsel`, then a notification showing the text. |
| `internal/platform/notify` | Desktop notifications over D-Bus. |
| `internal/platform/open` | Open URLs with `xdg-open`. |
| `internal/platform/autostart` | The "Start at Login" autostart file. |
| `internal/platform/instance` | Single instance through a D-Bus name. |
| `internal/platform/watcher` | Is a tray host (`org.kde.StatusNotifierWatcher`) running? The GNOME AppIndicator extension's state, and fix steps per desktop and distribution. |
| `internal/dnssetup` | "Enable DNS support…": which setups are supported (`Check`, pure), the backup and restore of the affected files, and the switch with its automatic undo (`Enable`, `Undo`). The tray uses the check, and `penguintier-dns-helper` runs the rest as root. |

## Contracts

### Identity

- The app ID is `io.github.rasikhq.Penguintier`. It is used for:
  - the .desktop file name and the app icon name,
  - the D-Bus single-instance name,
  - the program name of the Join and About windows, which gives their
    Wayland app ID and X11 WM_CLASS (`setProgramName` in
    `ui/progname.go`, called before GTK starts), matched by
    `StartupWMClass` in the .desktop file,
  - the polkit action prefix (`io.github.rasikhq.Penguintier.read-token`).
- The README and the About window say that Penguintier is an unofficial
  app and is not affiliated with ZeroTier, Inc.

### Install paths

- `/usr/bin/penguintier`
- `/usr/libexec/penguintier/{penguintier-ui,penguintier-token-helper,penguintier-dns-helper,penguintier-dnsd}`
- `/usr/share/man/man1/penguintier.1.gz`: the man page, from
  `packaging/man/penguintier.1` (gzipped by `make man`).
- Helper lookup order (how the tray finds `penguintier-ui`, the token
  helper and the DNS helper):
  1. `$PENGUINTIER_LIBEXEC_DIR`
  2. the directory of the running `penguintier` binary
  3. `/usr/libexec/penguintier`
- `pkexec` needs the real installed path to match the policy's
  `exec.path` annotation, so the token helper only works through polkit
  from `/usr/libexec/penguintier` (see `make dev-install-helper`).

### Environment variables

| Variable | Meaning |
|---|---|
| `PENGUINTIER_API_URL` | Base URL of the ZeroTier API, e.g. `http://127.0.0.1:9993`. Overrides port discovery. Used with `zt-mock`. |
| `PENGUINTIER_TOKEN_FILE` | Read the API token from this file first. |
| `PENGUINTIER_LIBEXEC_DIR` | Where to find `penguintier-ui`, `penguintier-token-helper` and `penguintier-dns-helper`. |
| `PENGUINTIER_LOG` | Log level: `debug`, `info` (default), `warn`, `error`. Only `penguintier-dnsd` reads it so far, and the tray logs at `info`. |

These and the files below are documented for users in
`packaging/man/penguintier.1`. Keep the man page in step when they change.

### ZeroTier API token

Lookup order:

1. the file in `$PENGUINTIER_TOKEN_FILE`
2. `~/.zeroTierOneAuthToken` (`zerotier-cli` reads this file too)
3. `~/.zerotier-local-auth`
4. `/var/lib/zerotier-one/authtoken.secret`, if readable

Rules:

- The first file that exists wins. If that file is unsafe or malformed,
  that is the error. The next file is not tried, so the problem gets
  fixed instead of hidden. (The system token is only "found" when it is
  readable.)
- Refuse a token file the user doesn't own, or one that is group- or
  world-readable, and offer to fix it (`doctor` prints `chmod 600 <file>`,
  and "Authorize Penguintier…" writes a fresh private copy).
- "Authorize Penguintier…" runs `pkexec penguintier-token-helper` (polkit
  action `io.github.rasikhq.Penguintier.read-token`, `auth_admin` for
  active sessions and `no` otherwise, with an `exec.path` annotation naming
  `/usr/libexec/penguintier/penguintier-token-helper`). The helper prints
  the token on stdout. The tray writes it to `~/.zeroTierOneAuthToken` with
  mode 0600, atomically (temp file, then rename), then connects again.
- Token helper exit codes: 0 printed the token, 1 cannot read the token
  file, 2 arguments given (it takes none), 3 the file holds no valid token.
- pkexec exit codes: 126 means the user dismissed the prompt, and the tray
  stays quiet. 127 means a wrong password, not allowed, or no polkit agent.
  The tray then shows a notification pointing to `penguintier doctor`,
  which prints the manual command:
  `sudo cat /var/lib/zerotier-one/authtoken.secret | install -m600 /dev/stdin ~/.zeroTierOneAuthToken`.
- Only the first interactive run asks by itself (see state.json below).
  Otherwise the menu shows "Authorize Penguintier…". A run with
  `--autostart` never asks.
- `penguintier deauthorize [--yes]` deletes that copy, after asking unless
  `--yes` is given. A tray that is running keeps its access until it
  quits. Exit codes: 0 deleted (or there was nothing to delete), 1 not
  deleted, 2 bad command line.

### ZeroTier API address

- Port: the contents of `/var/lib/zerotier-one/zerotier-one.port`, else 9993.
- Address: `127.0.0.1` first, then `[::1]`.
- HTTP client: stdlib `net/http`, `Proxy: nil`, 2 s timeout.

### Config files

- `$XDG_CONFIG_HOME/penguintier/saved_networks.json` (default
  `~/.config/penguintier/`)
- `$XDG_CONFIG_HOME/penguintier/state.json` (mode 0600), owned by
  `cmd/penguintier`. Every flag starts false:
  - `firstRunDone`: the first interactive run has happened. Only that run
    asks for authorization by itself.
  - `desktopUIImported`: the one-time import of DesktopUI's saved networks
    has run (at start-up, before the menu loads them).
  - `startAtLoginDecided`: Start at Login was turned on automatically, or
    the user set it in the menu. From then on Penguintier never changes it
    by itself.
- Autostart: `$XDG_CONFIG_HOME/autostart/io.github.rasikhq.Penguintier.desktop`
  with `Exec=penguintier --autostart` and `TryExec=penguintier` (the file
  stays in the home folder after an uninstall, and TryExec makes the desktop
  skip it then). With `--autostart` the tray never
  shows first-run prompts. "Start at Login" writes or removes it. It counts
  as off when the file has `Hidden=true` or
  `X-GNOME-Autostart-enabled=false`. It is turned on after the first run
  that reaches ZeroTier with a working token, unless `startAtLoginDecided`
  is set. Development builds (`version == "dev"`) never turn it on by
  themselves.

### Desktop integration

- Started from a terminal (stdin and stderr are a
  terminal), `penguintier` starts `penguintier --foreground` in the
  background (new session, output to the journal through `systemd-cat
  --identifier=penguintier`) and returns, so the terminal is free and
  Ctrl+C doesn't quit the tray. `--foreground` keeps it in the terminal
  with the log on stderr. `--autostart` and development builds
  (`version == "dev"`) always stay in the foreground. Log:
  `journalctl --user -t penguintier`.
- To stay a single instance, the tray owns the session bus name
  `io.github.rasikhq.Penguintier` (`DoNotQueue`) and exports
  `org.freedesktop.Application` at `/io/github/rasikhq/Penguintier`. A
  second launch calls its `Activate` (none with `--autostart`) and exits 0,
  and the running tray then shows "Penguintier is already running". The tray
  quits when its session bus connection is lost (the user logged out), so
  no tray without an icon is left running into the next login.
- Notifications go out on connections that stay open while the tray
  runs, because GNOME Shell removes an app's notifications as soon as the
  connection that sent them closes. Each one has a `default` action (the
  click), even when it does nothing. Without one, a click makes GNOME
  launch the .desktop entry, which would start a second Penguintier.
- When no tray host has owned
  `org.kde.StatusNotifierWatcher` with `IsStatusNotifierHostRegistered`
  true for 10 seconds, the tray shows one notification with fix steps for
  the desktop (`$XDG_CURRENT_DESKTOP`) and, on GNOME, the distribution
  (`/etc/os-release`) and the AppIndicator extension's state, as GNOME
  Shell reports it (`org.gnome.Shell.Extensions.GetExtensionInfo` on the
  bus name `org.gnome.Shell`). It shows at most once per login.
  `$XDG_RUNTIME_DIR/penguintier/no-tray-host-notice` holds the key of the
  login it was shown in, logind's graphical session ID
  (`login1:<id>`, from `User.Display` of
  `/org/freedesktop/login1/user/self`), or else the session bus's ID
  (`bus:<id>`). The folder alone is not enough, because it outlives a
  logout while any of the user's processes run. The extension's UUID is
  `ubuntu-appindicators@ubuntu.com` on Debian and Ubuntu,
  `appindicatorsupport@rgcjonas.gmail.com` elsewhere.
- Each network's submenu has a read-only "DNS: …" row
  (`internal/dnsstatus`). Its states, also used by `doctor --json`, are
  `applied`, `waiting-for-resolved`, `daemon-not-running`, `link-managed`,
  `set-by-other`, `pending`, `unknown`, `not-requested`, `not-connected`,
  `no-dns`. The probe only touches D-Bus when some network qualifies.
- "Enable DNS support…" shows while a network is
  `waiting-for-resolved`. See the next section.

### Enable DNS support (switching the computer to systemd-resolved)

Installing systemd-resolved changes how the whole computer resolves names,
so a mistake cuts off the internet. It is done by
`penguintier-dns-helper`, as root behind one polkit prompt
(`pkexec`, action `io.github.rasikhq.Penguintier.enable-dns`,
`auth_admin`), which backs up first, checks after, and undoes everything
itself when a check fails.

This is needed because on Debian 13 the package's install script starts resolved before
D-Bus knows its user (`Unknown username "systemd-resolve"`), so resolved
runs without its bus name, NetworkManager can't hand it DNS servers, and
`/etc/resolv.conf` already points at it, so no name resolves. Removing the
package then rebuilds `/etc/resolv.conf` from resolved's empty server list,
so that is no undo either. (Seen on a real machine on 2026-10-19 and
reproduced in a container, as docs/testing.md describes.)

#### Supported setup

Everything below must hold. Otherwise nothing is changed, and the tray
explains which point failed and how to switch by hand.

1. A Debian-family system (`ID` or `ID_LIKE` debian/ubuntu in
   `/etc/os-release`) with `apt-get` and `dpkg`, and systemd as PID 1.
2. `systemd-resolved` is not installed (`not-installed` or `config-files`
   in dpkg) and not running, and neither `resolvconf` nor `openresolv` is
   installed.
3. NetworkManager runs and writes `/etc/resolv.conf` itself. Its
   DnsManager `Mode` is `default` and `RcManager` is `symlink`, and
   `/etc/resolv.conf` is a regular file starting with
   `# Generated by NetworkManager`. systemd-networkd is not running.
4. A name lookup works now (`getent ahosts deb.debian.org`, else
   `debian.org`), so the same lookup can prove DNS still works afterwards.
5. No earlier switch is unfinished (see the backup below).

#### Backup

Before anything changes, the files the switch can touch are copied to
`/var/lib/penguintier/dns-backup/` (folder 0755, and each copy keeps its
mode and owner), with `manifest.json` describing them and the state. The
files are `/etc/resolv.conf`, `/etc/nsswitch.conf` and, if it exists,
`/etc/systemd/resolved.conf`. These are not secrets, so the folder is
readable and `doctor` can report it. Only one backup exists at a time.

#### Switch

The switch runs `apt-get install --no-install-recommends systemd-resolved`
(only that package, so `/etc/nsswitch.conf` is not edited), then:

1. Wait up to 10 s for `org.freedesktop.resolve1` on the bus. If resolved
   runs without it (the race above), restart it once and wait 10 s more.
2. Wait up to 20 s until the lookup from the check works. If it doesn't,
   ask NetworkManager to send its DNS again (`nmcli general reload
   dns-full`) and wait 10 s more.
3. On success the manifest says `enabled`, and the backup stays for a
   later undo.

#### Automatic undo

Any failure after the backup, or SIGTERM/SIGINT, starts the undo. It waits
until no package manager holds dpkg's lock, purges the packages this
switch installed, puts every backed-up file back
byte for byte (and removes `/etc/systemd/resolved.conf` if it didn't exist
before), runs `nmcli general reload dns-rc`, and checks the lookup works
again. The manifest then says `rolled-back`. If even that fails, the helper
says so and prints the manual steps, and the backup stays.

#### Undo later

`pkexec /usr/libexec/penguintier/penguintier-dns-helper undo`
runs the same undo for an `enabled` or an unfinished (`in-progress`)
switch. `doctor` shows the backup's state and this command.

#### Helper contract

`penguintier-dns-helper enable|undo|check` takes nothing else, and no paths
from the caller. Progress lines go to stderr, and the last line on stdout
is JSON: `{"result": "enabled" | "rolled-back" | "refused" |
"undone" | "failed", "message": "…", "backup": "…"}`. Exit codes: 0
enabled / undone / check passed, 1 failed but restored (rolled back),
2 wrong usage, 3 refused (unsupported setup, nothing changed), 4 failed and
not fully restored (manual steps printed).

### `penguintier doctor`

- Text by default, `--json` for scripts. Exit code 1 when a check failed,
  0 otherwise (warnings don't count), 2 for a bad command line.
- JSON: `{"schemaVersion": 1, "ok": bool, "checks": [...], "networks":
  [...]}`. Each check has `id`, `title`, `status` (`ok`, `info`, `warn`,
  `fail`, `skip`), `summary`, and optionally `fix` and `details`. The IDs,
  in order: `penguintier.version`, `zerotier.service`, `zerotier.token`,
  `zerotier.auth`, `desktop.tray`, `desktop.notifications`,
  `desktop.clipboard`, `polkit`, `dns.resolved`, `dns.daemon`,
  `dns.networks`, `dns.setup` (the state of "Enable DNS support…"'s
  switch, with the undo command). Fields and IDs are only ever added, and a
  removal or a change of meaning bumps `schemaVersion`.

### Icons

| Name | Use | Placeholder source (P2 replaces) |
|---|---|---|
| `io.github.rasikhq.Penguintier` | app icon (hicolor theme) | `assets/icons/hicolor/{scalable,16x16…256x256}/apps/` |
| `penguintier-online` | tray state: online | `assets/tray/penguintier-online.{svg,png}` |
| `penguintier-offline` | tray state: offline | `assets/tray/penguintier-offline.{svg,png}` |
| `penguintier-attention` | tray state: needs attention | `assets/tray/penguintier-attention.{svg,png}` |
| `penguintier-{online,offline,attention}-symbolic` | one-color tray states that GTK recolors to match the panel | `assets/tray/penguintier-*-symbolic.svg` (not used yet, because the tray sends PNG pixels, but kept for an icon-name tray or a GNOME extension) |

### Tray rules

- Click handlers only send a command on a channel. No blocking I/O in
  handlers.
- One controller goroutine polls the API every 2.5 s.
- When only text or check state changes, update menu items in place. Call
  `ResetMenu` and rebuild only when the structure changes (networks added
  or removed).
- Sort networks by ID.
- Menu depth ≤ 2, so addresses and routes are flattened into the network's
  submenu as sections.
- Labels go over dbusmenu, where `_` marks a keyboard shortcut. Spec hosts
  (KDE, GTK) need every `_` doubled, but GNOME Shell's AppIndicator
  extension only strips the first one. The tray escapes for the host that owns
  `org.kde.StatusNotifierWatcher` (see "Underscores in menu labels" in
  testing.md).

### UI process (`penguintier-ui`)

The tray starts it on demand. It exits when its window closes.

| Command | Window | Result |
|---|---|---|
| `penguintier-ui join` | Join network | Prints the 16-hex-character network ID on stdout and exits 0. Cancel or closing the window exits 1. |
| `penguintier-ui about` | About | Exits 0 when closed. |
| anything else | none | Usage on stderr, exit 2. |

- The UI never touches the network and never sees the token.
- The tray starts it with `WEBKIT_DISABLE_DMABUF_RENDERER=1` (avoids blank
  windows on some NVIDIA setups).
- Fallback when it is missing or fails: zenity or kdialog.

### DNS daemon (`penguintier-dnsd`)

- systemd service, `User=penguintier-dns` (created by
  `packaging/sysusers/penguintier.conf`).
- Gets the token with `LoadCredential=` and never reads `identity.secret`.
- A polkit rule allows that user only the resolve1 actions
  `set-dns-servers`, `set-domains`, `set-default-route` and `revert`.
- Reconciles every 5 s with a pure `Plan()` function, and reverts
  everything on SIGTERM.
- Leaves alone any link whose DNS it did not set.
- Idles while systemd-resolved is absent, and wakes when
  `org.freedesktop.resolve1` appears on the bus.
- Started by `penguintier-dnsd.path` once
  `/var/lib/zerotier-one/authtoken.secret` exists.
- Exits on HTTP 401 so `Restart=on-failure` reloads the token credential.
- The credential is named `zt-authtoken`
  (`LoadCredential=zt-authtoken:/var/lib/zerotier-one/authtoken.secret`).
- Remembers the links it changed in `/run/penguintier-dnsd/applied.json`
  (its `RuntimeDirectory=`, mode 0700, kept across restarts until reboot).
  Only the daemon can read it. Other programs (the tray's DNS row,
  `doctor`) read the link state from resolve1 and the unit state from
  systemd instead.
- To stop it, stop both units (`penguintier-dnsd.path` and
  `penguintier-dnsd.service`), because the path unit restarts a service
  stopped on its own.

### Version

Each binary that reports a version has `var version = "dev"` in its
`main` package. Builds set it with
`-ldflags "-X main.version=<version>"` (`make build` uses `git describe`,
and GoReleaser uses the tag).

The git tag is the only place the version number comes from. Tag
`v0.1.0` gives packages `penguintier_0.1.0_amd64.deb` and
`penguintier-0.1.0-1.x86_64.rpm`, and `penguintier --version` prints
`penguintier 0.1.0`. Nothing in the code needs bumping for a release. The
release checklist in testing.md ("P7") lists the files that describe a
release (CHANGELOG.md, the metainfo `<releases>`).

### Go toolchain

- `go.mod` and `ui/go.mod` each have a `toolchain` line (Go 1.27.x). CI and
  release builds use exactly that version, so release binaries get the
  latest standard-library security fixes.
- The `go` line is the minimum version for building: 1.24 for the root
  module (Debian 13's Go), 1.25 for `ui/` (required by Wails).
- With `GOTOOLCHAIN=auto` (the default, also on Debian 13) Go downloads the
  pinned toolchain on first use and checks it against the Go checksum
  database. If `go env GOTOOLCHAIN` prints `local` (some distros, such as
  Fedora, set that), run `go env -w GOTOOLCHAIN=auto` once.
- When `govulncheck` in CI reports a standard-library issue, bump the
  `toolchain` line in both `go.mod` files.

### Static build check (P0)

Checked on 2026-10-19 (Debian 13, GNOME 48 Wayland, Go 1.27.2,
`fyne.io/systray` v1.12.2):

- `CGO_ENABLED=0 go build ./...` succeeds. CI repeats this on every PR.
- A test program calling `systray.Run` (icon, title, tooltip, one menu
  item), built with `CGO_ENABLED=0`:
  - `file` reports "statically linked", and `ldd` reports "not a dynamic
    executable". There is no glibc version floor.
  - When run, it exported `org.kde.StatusNotifierItem-<pid>-1` on the session
    bus and exited cleanly on `systray.Quit()`.
  - On stock GNOME it logs `systray error: failed to register: The name
    org.kde.StatusNotifierWatcher was not provided by any .service files`.
    That is the missing-tray-host case P5 detects (GNOME needs the
    AppIndicator extension).

### Third-party code

`fyne.io/systray` (Apache-2.0) is used from a patched copy of v1.12.2 in
`third_party/fyne-systray/`, through
`replace fyne.io/systray => ./third_party/fyne-systray` in `go.mod`. The
patch fixes a crash when a menu click arrives while the menu is rebuilt.
`third_party/fyne-systray/PENGUINTIER.md` lists every difference from
upstream, and `third_party/README.md` has the rules for that folder. The
copy is its own Go module, so `go test ./...` from the root skips it.
`make test` and CI test it separately.

## Stubs

P0 started every command as a stub that printed
`<name>: not implemented yet`. All of them are implemented now, and
`penguintier --version` prints `penguintier <version>`.

## File ownership

Each phase edits only the paths it owns. That is what lets phases run in
parallel without merge conflicts. If a phase needs to change someone else's
file, it calls this out in its PR description.

| Paths | Owner |
|---|---|
| `internal/ztapi/`, `cmd/zt-mock/`, `cmd/ztctl/`, `cmd/penguintier-token-helper/`, `packaging/polkit/*.policy` | P1: ztapi, zt-mock, token helper |
| `ui/**`, `assets/**` | P2: UI dialogs, icons and brand |
| `internal/tray/`, `internal/platform/{clipboard,notify,open}/`, `third_party/fyne-systray/` | P3: tray core |
| `internal/resolved/`, `internal/dnsrule/` (moved out of the daemon by P5), `cmd/penguintier-dnsd/`, `packaging/systemd/penguintier-dnsd.*`, `packaging/polkit/*.rules`, `packaging/sysusers/`, `scripts/zt-selftest-network.sh` | P4: DNS daemon |
| `cmd/penguintier/`, `cmd/penguintier-dns-helper/`, `internal/doctor/`, `internal/dnsstatus/`, `internal/dnssetup/`, `internal/platform/{autostart,instance,watcher}/`, `scripts/dns-lab/` (may also touch P3's files) | P5: desktop integration |
| `.goreleaser.yaml`, `cmd/licensegen/`, `packaging/{scripts,metainfo,desktop}/`, `packaging/systemd/*.preset`, `.github/workflows/{package,release}.yml` | P6: packaging |
| `README.md`, `CHANGELOG.md`, `packaging/man/`, `docs/screenshots/`, `scripts/changelog-section.awk`, `.github/FUNDING.yml`, `funding.json` | P7: release |
| `CLAUDE.md`, `docs/architecture.md`, `.golangci.yml`, `.gitignore`, `.github/workflows/ci.yml`, `.vscode/` | P0. Later changes are small and called out in the PR. |
| `docs/plan.md` | read-only copy of the approved plan |

Shared files, with rules:

| Path | Rule |
|---|---|
| `go.mod`, `go.sum` | P0 already imports every dependency, so these rarely change. On a merge conflict, keep the union of both sides, then run `go mod tidy`. |
| `Makefile` | Each phase edits only the recipes of its own targets (the `TODO(Px)` ones). |
| `docs/testing.md` | Each phase edits only its own section. |
