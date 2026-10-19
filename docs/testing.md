# Testing Penguintier

How to test each part. **Each phase fills in only its own section** (see
"File ownership" in [architecture.md](architecture.md)), so parallel PRs
don't conflict here.

## Quick reference

| Command | What it does |
|---|---|
| `make test` | Unit tests with the race detector (`go test -race ./...`). |
| `make lint` | gofmt check, `go vet`, golangci-lint. |
| `make build` | Static binaries into `bin/`. |
| `make ui-build` | The Wails UI into `ui/build/bin/penguintier-ui`. |
| `make ui-dev` | The UI with hot reload (`UI_SCREEN=join` or `about`). |
| `make mock` | zt-mock, the fake ZeroTier API. |
| `make live-test` | Tests against the real local ZeroTier service (build tag `live`). |
| `make vuln` | govulncheck: known vulnerabilities in dependencies and the standard library. |
| `make licenses` | Every dependency has an allowed license (MIT, BSD, Apache-2.0, ISC, MPL-2.0). |

CI (`.github/workflows/ci.yml`) runs the lint, test, build, vuln and
licenses checks, plus a UI build on Ubuntu 24.04.

## Conventions

- Tests sit next to the code, in `*_test.go` files.
- Use table-driven tests (see `cmd/penguintier/main_test.go`).
- Test data goes in a `testdata/` directory next to the test; the Go tool
  ignores those directories when building.
- Tests that need a real ZeroTier service use the `live` build tag
  (`//go:build live`) and run only with `make live-test`.
- Every PR lists its own manual test steps.

## P0: Foundation

- `make lint test build ui-build` passes.
- `go run ./cmd/penguintier --version` prints `penguintier dev (not implemented yet)`.
- `make ui-dev` opens the placeholder Join window; `make ui-dev UI_SCREEN=about`
  opens About.

## P1: ztapi, zt-mock, token helper

### Unit tests (`make test`)

- `internal/ztapi`: every endpoint and every error mapping against
  `httptest` servers: connection refused → `ErrServiceDown` (or
  `ErrNotInstalled` when no `zerotier-one` program exists), connection
  closed → `ErrServiceDown`, 401 → `ErrUnauthorized`, timeout →
  `ErrTimeout`, bad JSON → `*ProtocolError`, other codes → `*HTTPError`,
  the `[::1]` fallback (skipped if the machine has no IPv6 loopback).
  Token discovery runs against a temporary `$HOME`; the saved store checks
  its exact file layout, atomic writes and the DesktopUI import.
- `internal/ztapi/ztmock`: every zt-mock scenario through the real client,
  including join → configured timing with a fake clock.
- `cmd/ztctl`, `cmd/zt-mock`, `cmd/penguintier-token-helper`: exit codes
  and output.

Fixtures are in `internal/ztapi/testdata/`:

| Directory | What |
|---|---|
| `1.16.2/` | Real replies from ZeroTier One 1.16.2 (`/status`, `/network`, `/peer`), with node IDs, network IDs, IPs and MACs replaced by fake ones. |
| `handmade/` | Cases a single node rarely shows: SSO, access denied, IPv6, several networks with unknown fields and `"via": null`, and a DesktopUI `saved_networks.json`. |

To refresh the real fixtures after a ZeroTier upgrade, capture them (only
`cat` runs as root; the files are private to you):

```
(umask 077; T=$(sudo cat /var/lib/zerotier-one/authtoken.secret); mkdir -p /tmp/zt-capture; for p in status network peer; do curl -s -H "X-ZT1-Auth: $T" "http://127.0.0.1:9993/$p" > "/tmp/zt-capture/$p.json"; done)
```

Then replace every node ID, network ID, IP address and MAC with a fake
value before committing (documentation IP ranges: 192.0.2.0/24,
198.51.100.0/24, 203.0.113.0/24, 2001:db8::/32).

### zt-mock

```
make mock                                    # scenario "online" on 127.0.0.1:19993
make mock MOCK_ARGS="--scenario joining"
make mock MOCK_ARGS="--list"                 # all scenario names
```

It prints two `export` lines (`PENGUINTIER_API_URL` and a private token file
in `PENGUINTIER_TOKEN_FILE`); paste them into another terminal, then run
`ztctl` or the tray there. Type a scenario name and Enter in the zt-mock
terminal to switch scenarios. The token file is deleted when zt-mock stops.

| Scenario | What the client sees |
|---|---|
| `online` | two healthy networks (IPv4 + IPv6, routes with and without a gateway, a DNS domain) |
| `service-down` | every connection closed without a reply → `ErrServiceDown` |
| `unauthorized` | 401 for everything except `/health` |
| `sso-required` | one network waiting for SSO login, one logged in (expires in 8 h) |
| `access-denied` | one network the controller has not authorized |
| `many-networks` | 12 networks: every status (plus an unknown one), underscores, a long non-ASCII name, many routes |
| `joining` | one network still requesting its configuration; a new join stays hidden for 5 s, then requests configuration for 10 s |

In every scenario a join first shows `REQUESTING_CONFIGURATION`, then gets
a configuration (2 s, or as described above).

### ztctl against the real service

`ztctl` needs a token copy first (see the token helper below):

```
go run ./cmd/ztctl status
go run ./cmd/ztctl list
go run ./cmd/ztctl join <network-id>
go run ./cmd/ztctl set <network-id> allowDNS=true
go run ./cmd/ztctl leave <network-id>
```

Exit codes: 0 success, 1 the call failed (with a hint for the common
cases), 2 wrong usage.

### Token helper and polkit policy

`pkexec` only shows the Penguintier prompt when the helper is installed at
the path named in the policy, so install both system-wide:

```
make dev-install-helper
pkexec /usr/libexec/penguintier/penguintier-token-helper
```

Expected: a polkit prompt saying "Penguintier needs to read the ZeroTier
service's API token…", then the token printed. To save a private copy
(what "Authorize Penguintier…" will do), which `ztctl` and `zerotier-cli`
then use:

```
(umask 077 && pkexec /usr/libexec/penguintier/penguintier-token-helper > ~/.zeroTierOneAuthToken)
```

Helper exit codes: 0 printed the token, 1 cannot read the token file,
2 arguments given (it takes none), 3 the file holds no valid token. pkexec
itself exits 126 when the prompt is dismissed and 127 when authorization
fails or no polkit agent runs.

To remove the dev install:

```
sudo rm /usr/libexec/penguintier/penguintier-token-helper /usr/share/polkit-1/actions/io.github.rasikhq.Penguintier.policy
rm ~/.zeroTierOneAuthToken
```

### Live tests (`make live-test`)

They use the real service and need the token copy above; without one they
skip.

- `TestLiveReadOnly` calls `/health`, `/status`, `/network`,
  `/network/{id}` and `/peer`, and changes nothing.
- `TestLiveJoinToggleLeave` runs only with `PENGUINTIER_LIVE_JOIN=1`. It
  joins a throwaway network, toggles all four flags, and leaves. The default
  ID is your node address + `dead01`: a network that doesn't exist on your
  node's own built-in controller, so nothing leaves the machine. Use
  `PENGUINTIER_LIVE_NETWORK=<id>` for another one. It refuses to touch a
  network that is already joined.

```
make live-test
PENGUINTIER_LIVE_JOIN=1 make live-test
```

## P2: UI dialogs, icons and brand

Automated (all run by `make ui-build`, so also by the CI UI job):

- `bun test` in `ui/frontend` checks the network ID text rules
  (`src/networkId.test.ts`): hex only, lowercase, at most 16, and how pasted
  text with spaces, dashes or words around the ID is cleaned.
- `cd ui && go test -tags webkit2_41 .` (after `make ui-build`, which creates
  `frontend/dist`) checks the Go side: network ID validation and the
  light/dark decision. CI does not run it yet (the CI `go test` covers the
  root module only).
- The CI UI job checks that an unknown screen name exits 2.

Join window (`make ui-dev UI_SCREEN=join`, or the built binary):

- Focus starts in the Network ID field; you can type right away.
- Typing keeps only 0-9 and a-f, and uppercase becomes lowercase. Typing in
  the middle of the ID keeps the caret where it was.
- Pasting `8056 C2E2-1c00 0001` or `Network ID: 8056c2e21c000001` fills
  in `8056c2e21c000001`.
- The counter shows n / 16. Join stays disabled until there are 16
  characters; Enter does nothing before that.
- Enter (or Join) closes the window. `ui/build/bin/penguintier-ui join;
  echo $?` prints the ID on stdout, then `0`.
- Esc, Cancel or closing the window prints nothing; the exit code is `1`.

About window (`make ui-dev UI_SCREEN=about`, or `penguintier-ui about`):

- Shows the icon, the name, the version (`make ui-build` stamps
  `git describe`; an unstamped build says "Development build"), the MIT
  license line, the repo link and the disclaimer.
- The link opens github.com/rasikhq/penguintier in your browser, never in
  the window itself. Go opens a fixed address (`repoURL` in `ui/app.go`).
- Esc or closing the window exits 0.

Light and dark:

- Switch Settings → Appearance between Default and Dark and reopen a
  window: the page and the title bar follow. The accent color (Join button,
  link) follows the desktop's accent on GNOME 47+ and KDE.
- How it works: WebKitGTK only reports `prefers-color-scheme: dark` when the
  GTK theme is dark, and on GNOME the Dark Style switch no longer changes
  the GTK 3 theme. `ui/theme.go` reads the desktop portal's `color-scheme`
  and sets `GTK_THEME=<theme>:dark` before GTK starts. A `GTK_THEME` you set
  yourself always wins: `GTK_THEME=Adwaita penguintier-ui about` is light.

Launching from the tray (notes for P3 and P5):

- Start `penguintier-ui` with `WEBKIT_DISABLE_DMABUF_RENDERER=1` in its
  environment (blank windows on some NVIDIA setups otherwise). The UI also
  turns off GPU rendering for its webview (`WebviewGpuPolicyNever`).
- Focus on GNOME Wayland: started from a background process with no
  window and no activation token (`setsid penguintier-ui join` from a
  script), the window still got keyboard focus with the caret in the field
  (checked on Debian 13, GNOME 48, WebKitGTK 2.52). Recheck from the real
  tray menu in P5.
- The window's Wayland app ID and X11 WM class are
  `io.github.rasikhq.Penguintier`, so once the .desktop file is installed
  the dash and Alt+Tab show the Penguintier icon. (Not checked yet: needs
  the packaged .desktop file from P6.)

Icons:

- `xdg-open assets/brand/preview.html` shows the app icon at every size
  and the tray states (color and symbolic) on light and dark, in a mock
  GNOME top bar and desktop panels. It loads the real SVG and PNG files.
- After editing an SVG, run `python3 assets/render-icons.py` to rebuild
  the PNGs (and `ui/build/appicon.png`).

## P3: Tray core

### Unit tests (`make test`)

`internal/tray`:

- **Golden menus.** `TestBuildGolden` runs every zt-mock scenario (plus
  "token-missing", a client without a token) through `httptest`, builds the
  menu, and compares the icon, tooltip and menu with
  `internal/tray/testdata/golden/<scenario>.txt`. In those files `[x]` /
  `[ ]` are check boxes, `▸` opens a submenu, `(disabled)` is greyed out
  and `→` is what a click does. After an intended menu change, rewrite them
  and review the diff:

  ```
  go test ./internal/tray -update
  git diff internal/tray/testdata
  ```

  A new zt-mock scenario makes this test fail until its golden file exists.
- **Tray rules.** `TestBuildRules`: entry keys are unique, the menu is at
  most two levels deep, networks are sorted by ID.
- **No-op update.** `TestRefreshNoChangeNoCalls` polls every scenario
  again and fails if the controller calls the tray library at all when
  nothing changed. `TestRefreshUpdatesInPlace` (a changed check box is one
  `Check` call) and `TestRefreshRebuildsOnStructureChange` (a network left
  → `ResetMenu`) cover the other two cases.
- **Click goroutines.** `TestRebuildStopsClickWatchers` rebuilds the menu
  100 times and checks the goroutine count stays flat; `TestRun` drives
  the real loop: a click arrives on the library's channel, Quit ends the
  loop with every click goroutine stopped.
- **Actions** against zt-mock: copy (addresses without `/prefix`),
  check boxes, Disconnect → Reconnect (with the saved settings) → Forget,
  Join with the "Joining…" row, the one-time SSO notification and its
  action, Authorize, the About fallback, Quit.
- **Underscores in labels** (see below): `TestEscapeLabel*` check the
  escaping against both host behaviors, for every label of every golden
  menu. `TestTrayHostStyle` runs the host detection on a private
  `dbus-daemon`.
- **Join dialog:** fake `penguintier-ui`, `zenity` and `kdialog` scripts
  check exit codes (0 = ID, 1 = cancel, other = try the next one), the
  `join` argument and `WEBKIT_DISABLE_DMABUF_RENDERER=1`. `TestFindHelper`
  checks the helper lookup order.

`internal/platform`:

- `clipboard`: the tool order (wl-copy on Wayland, then xclip, then xsel)
  with fake tools in `$PATH`.
- `notify`: a fake notification service on a private `dbus-daemon`
  (skipped if `dbus-daemon` is not installed): body markup escaping, hints,
  actions, and that an `ActionInvoked` signal from another program is
  ignored.
- `open`: only `http`/`https` addresses reach a fake `xdg-open`.

### Underscores in menu labels

The tray menu goes over D-Bus as "dbusmenu", where `_` marks a keyboard
shortcut and is hidden, so a network called `lab_01` shows as `lab01`
unless escaped. Hosts disagree on the escaping:

- KDE Plasma and GTK-based hosts follow the spec: `__` shows one `_`.
- GNOME Shell's AppIndicator extension (version 59) removes only the
  first `_` that is followed by another character, and shows `__` as two.

So the tray asks D-Bus who owns `org.kde.StatusNotifierWatcher`; if that
process is `gnome-shell` it escapes only the first underscore, otherwise
all of them. Check it with `make tray-mock MOCK_ARGS="--scenario
many-networks"`: `lab_01`, `private_net` and `some_new_state` must show
exactly like that.

To see the menu as the tray host gets it (labels after escaping):

```
busctl --user call org.kde.StatusNotifierItem-$(pgrep -f 'bin/penguintier$')-1 \
  /StatusNotifierItem/menu com.canonical.dbusmenu GetLayout iias -- 0 -1 0
```

(The library answers the first call one level deep only, a workaround for
GTK hosts; call it twice.)

### Before testing on GNOME

GNOME has no tray without the AppIndicator extension, and copying needs
wl-clipboard (or xclip):

```
sudo apt install gnome-shell-extension-appindicator wl-clipboard
gnome-extensions enable ubuntu-appindicators@ubuntu.com
```

Log out and back in after installing (GNOME on Wayland only finds new
extensions at login), then enable the extension. Without a tray host the
tray logs `failed to register: The name org.kde.StatusNotifierWatcher was
not provided`.

How GNOME's extension (version 59) behaves, seen while testing P3:

- It ignores the `ItemIsMenu` hint. A left click opens the menu after the
  double-click delay (it waits to see whether a second click follows). A
  double click calls the icon's `Activate` method instead. Penguintier has
  none, so the extension logs `Penguintier, does not support activation`
  once, and from then on a single left click opens the menu at once.
  Right click always opens it.
- It reads the menu once, when the icon appears, and afterwards only when
  the menu is opened. An empty popup never opens, so an icon that showed
  an empty menu at first would never get one. That is why the tray builds
  its first menu before it registers (see `onReady` in
  `internal/tray/run.go`).
- `journalctl --user -b | grep -i appindicator` shows its messages. The
  `PropertyNotFound` errors on every icon change are harmless: it asks for
  optional properties (attention and overlay icons) the library does not
  export.
- It does not show tooltips.

### Manual: `make tray-mock`

```
make ui-build                                   # once: the Join and About windows
make tray-mock                                  # scenario "online"
make tray-mock MOCK_ARGS="--scenario many-networks"
```

The tray starts against zt-mock. Type a scenario name and Enter in the
terminal to switch; the menu follows within 2.5 s. Ctrl+C stops both.
To switch from somewhere else instead (another terminal, a script), feed
zt-mock from a file:

```
touch /tmp/scenario
tail -n0 -f /tmp/scenario | make tray-mock
echo sso-required >> /tmp/scenario      # in another terminal
```

The tray keeps its saved networks in `bin/tray-mock-config/`, not in your
real `~/.config/penguintier/`.

- **online**: icon online. "My Address: a1b2c3d4e5" copies and shows a
  "Copied" notification. Two network submenus with check marks. Toggle a
  check box: it flips within a moment and the menu doesn't flicker.
  Disconnect `office` → it moves under Saved Networks; Reconnect → back,
  with the same check boxes; Disconnect + Forget → gone.
- **many-networks**: icon attention; tooltip (KDE) "12 networks, 5 need
  attention"; underscores show as typed; the long name ends in "…"; a
  "Log In to sso_corp…" entry at the top opens the fake SSO page in the
  browser (it doesn't exist; the browser opening is the test). One
  notification "ZeroTier login required" appears once, with "Open login".
- **sso-required**: the same login entry and notification for
  `corp-sso`; `partner-sso` shows "SSO login expires <time>".
- **access-denied**: icon attention, the network shows "Status: Access
  denied" and no check mark.
- **service-down**: icon offline, only "Waiting for ZeroTier system
  service…" plus the bottom section. Switch back to **online**: the menu
  returns.
- **unauthorized**: icon attention, "Authorize Penguintier…" shows a
  notification with the manual command (P5 makes it real).
- **joining**: Join New Network… → enter any 16-hex ID → a disabled
  "Joining <id>…" row for 5 s, then the network with "Requesting
  configuration", and after 10 s more its name and address. On GNOME 48
  Wayland the Join window, started from the tray menu, opens with keyboard
  focus in the ID field.
- Left-click on the icon opens the menu (on GNOME after a short delay; see
  above).
- About Penguintier opens the About window; Quit removes the icon.

### Manual: `make run` against the real service

Needs `~/.zeroTierOneAuthToken` (see the token helper above). Join a
network you control, or a throwaway one: your node address + `dead01`
never gets a configuration but still joins and leaves.

- Join New Network… → enter the ID → "Joining…" until it is listed.
- Toggle "Allow DNS Configuration" (and the others): `zerotier-cli
  listnetworks -j` shows the new values.
- Click an address and paste it somewhere: no `/24` at the end.
- Disconnect → the network moves to Saved Networks; Reconnect → it is
  joined again with the same check boxes; Disconnect, then Forget → gone,
  also from `~/.config/penguintier/saved_networks.json`.
- `sudo systemctl stop zerotier-one` → "Waiting for ZeroTier system
  service…" and the offline icon; start it again → the menu comes back.

## P4: DNS daemon

### Unit tests (`make test`)

- `cmd/penguintier-dnsd/plan_test.go`: table tests for `Desired` (which
  networks qualify: allowDNS on, status OK, an interface, a valid domain and
  at least one valid server; domain and server clean-up) and `Plan` (new
  link, nothing to do, someone else's link, drift, changed servers or
  domain, updates that stopped halfway, revert, shutdown, several links).
  Every `Plan` case is also carried out on a simulated resolved and planned
  again: the second plan must not write anything.
- `internal/resolved`: the real D-Bus client against `resolvedtest.Fake`,
  a fake `org.freedesktop.resolve1` on a private `dbus-daemon` started by
  the test: set/read round trip, DefaultRoute, error mapping (no such link,
  link managed by systemd-networkd, polkit refusal, resolved not running),
  and the `NameOwnerChanged` watcher. Also `IfIndex` against a fake
  `/sys/class/net`.
- `cmd/penguintier-dnsd/daemon_test.go`: the daemon's real check loop
  against zt-mock and the fake resolved: applies once and then writes
  nothing, re-applies after drift and after the interface is recreated,
  leaves others' settings alone, recognizes its links after a restart,
  logs a managed link once, idles without resolved (no API calls), exits on
  HTTP 401, keeps settings while ZeroTier is down, wakes up when resolved
  appears and reverts on shutdown, and stops when the bus goes away.

The D-Bus tests need `dbus-daemon` (Debian/Ubuntu package `dbus-daemon`,
Fedora `dbus-daemon`). Without it they are skipped locally, but fail in CI
(`$CI` is set), so CI always runs them.

### Units and polkit rule

```
systemd-analyze verify --man=no packaging/systemd/penguintier-dnsd.service packaging/systemd/penguintier-dnsd.path
systemd-analyze security --offline=yes packaging/systemd/penguintier-dnsd.service
```

`verify` only complains that `/usr/libexec/penguintier/penguintier-dnsd`
is not installed. `security` should say about **1.1 OK** (target: 2 or
lower). What is left: host network access (it needs the interfaces and
127.0.0.1:9993), AF_UNIX/AF_INET sockets, and no `PrivateUsers=` (systemd
can't set up a user namespace inside LXC or rootless containers). The P6
smoke test runs `verify` on the installed units in four distros.

### Running it by hand

Without root, without systemd and without polkit, the daemon can still
show its idle path (on a machine without systemd-resolved, such as stock
Debian) and its start-up checks:

```
make build
PENGUINTIER_TOKEN_FILE=~/.zeroTierOneAuthToken RUNTIME_DIRECTORY=$(mktemp -d) bin/penguintier-dnsd
```

It logs `systemd-resolved is not running; waiting for it` once and nothing
more; Ctrl+C stops it. Set `PENGUINTIER_LOG=debug` for more. With
systemd-resolved running it would try to apply settings, which needs the
polkit rule for your user, so test that part with the package as below.

### End-to-end test (VM or systemd container)

Do this in a VM (Ubuntu 24.04 is the main target) or a systemd container,
not on your desktop: it installs packages, joins a network and changes DNS
settings.

1. Install ZeroTier One (<https://www.zerotier.com/download/>),
   systemd-resolved, the test tools and the package (`make package`
   first):

   ```
   sudo apt install systemd-resolved dnsmasq-base jq curl
   sudo apt install ./dist/penguintier_*_amd64.deb
   systemctl status penguintier-dnsd.path penguintier-dnsd.service   # both active
   journalctl -u penguintier-dnsd                                     # "ZeroTier API at ..."
   ```

2. Make the test network. On a node with a network controller this needs
   no account; otherwise the script prints the steps for my.zerotier.com:

   ```
   scripts/zt-selftest-network.sh          # prints the network ID and interface
   ```

3. Start a DNS server for `pt.test` on the node's ZeroTier IP, then switch
   Allow DNS on:

   ```
   sudo dnsmasq --no-resolv --no-hosts --bind-interfaces --listen-address=10.199.47.1 --address=/host.pt.test/10.199.47.1
   go run ./cmd/ztctl set <network-id> allowDNS=true
   ```

4. Within 5 seconds:

   ```
   resolvectl status <zt-interface>   # DNS Servers: 10.199.47.1, DNS Domain: ~pt.test, Default Route: no
   resolvectl query host.pt.test      # 10.199.47.1 -- link: <zt-interface>
   resolvectl query example.com       # still answered through your normal DNS
   journalctl -u penguintier-dnsd     # "DNS queries for pt.test now go to its servers"
   ```

5. Drift and restarts:
   - `sudo resolvectl revert <zt-interface>`: set again within 5 s.
   - `sudo systemctl restart systemd-resolved`: resolved keeps the
     settings itself (it saves them under `/run`), so the daemon writes
     nothing. With `sudo systemctl stop systemd-resolved; sudo rm -r
     /run/systemd/resolve/netif; sudo systemctl start systemd-resolved` it
     re-applies about 0.3 s after resolved is back.
   - `sudo systemctl kill -s KILL penguintier-dnsd`: systemd restarts it;
     the log says "1 link(s) applied before" and nothing is rewritten.
   - `go run ./cmd/ztctl set <network-id> allowDNS=false`: "DNS settings
     reverted"; `true` applies again.

6. Stop: `sudo systemctl stop penguintier-dnsd.path penguintier-dnsd.service`
   logs "stopping: reverting the DNS settings of 1 link(s)", and
   `resolvectl status <zt-interface>` shows no DNS servers or domains. Stop
   both units: stopping only the service makes the .path unit start it again
   at once (systemd warns about that).

7. Without resolved: on Debian without the `systemd-resolved` package the
   daemon logs `systemd-resolved is not running; waiting for it` once and
   then stays quiet (it doesn't even call the ZeroTier API).
   `sudo apt install systemd-resolved` makes it apply within a second, with
   no restart.

8. Clean up: `scripts/zt-selftest-network.sh down`.

#### In a podman container

A systemd container works for all of this, with a few tweaks. Tested
with rootless podman on Debian 13, using the host's ZeroTier 1.16.2 binary
(built for Debian 13, so use a `debian:13` image):

```
podman run -d --name pt-e2e --systemd=always --privileged -v "$PWD":/src:ro <image with systemd, dbus, polkitd, zerotier-one>
podman exec pt-e2e mount --make-rshared /
```

- `--privileged` (still rootless) lets systemd-resolved keep
  `CAP_SYS_ADMIN` and ZeroTier create its tap device.
- `mount --make-rshared /`: systemd mounts each service's credentials in a
  helper process and moves them into `/run/credentials/`. With podman's
  private mounts the move is invisible to the service, so
  `LoadCredential=` silently delivers nothing ("API token credential ... is
  missing"). This only affects containers.
- Debian images have a `policy-rc.d` that stops package scripts from
  starting services, so after installing systemd-resolved run
  `systemctl start systemd-resolved`.
- Without real ZeroTier (for example on `ubuntu:24.04`, where the Debian 13
  binary needs a newer OpenSSL), run `zt-mock --listen 127.0.0.1:9993` as
  a service, put `ztmockztmockztmockztmock` in
  `/var/lib/zerotier-one/authtoken.secret`, and create the "online"
  scenario's interface with `ip tuntap add dev zt34000001 mode tap`. The
  daemon then sets 10.147.17.1 and `~home.arpa` on it.

## P5: Desktop integration

TODO(P5): onboarding, autostart, single instance, missing tray host,
"Enable DNS support…", `doctor`.

## P6: Packaging

### Build the packages

```
make tools      # once: also installs goreleaser into ~/go/bin
make package
```

`make package` runs `goreleaser release --snapshot --clean` with
[.goreleaser.yaml](../.goreleaser.yaml). GoReleaser first runs
`make ui-build`, so the UI toolchain must work too (wails, bun, and the
GTK/WebKitGTK headers: `libgtk-3-dev libwebkit2gtk-4.1-dev`). The result is
in `dist/`:

- `penguintier_<version>_amd64.deb`
- `penguintier-<version>-1.x86_64.rpm`
- `SHA256SUMS`

Snapshot versions look like `0.0.1~snapshot.c9a573b`. Nothing is uploaded.
`go install` builds goreleaser from source, which takes a few minutes; a
prebuilt binary from <https://goreleaser.com/install/> works too (set
`GORELEASER=/path/to/goreleaser` if it is not in `~/go/bin`).

Local packages carry a UI built against your own glibc. Release packages
are built in a `debian:12` container, so they run on glibc 2.36 and newer.

### Install smoke test in containers

[packaging/scripts/smoke-test.sh](../packaging/scripts/smoke-test.sh)
installs the package in a throwaway container and checks:

- every file is installed, with the right mode, owned by root;
- `penguintier --version` runs;
- the `penguintier-dns` system user exists;
- the preset enabled `penguintier-dnsd.path` (and not the service);
- `systemd-resolved` was not pulled in (deb; it is only "Suggests");
- `systemd-analyze verify` accepts both units;
- the .desktop and metainfo files validate;
- an upgrade keeps a disabled path unit disabled, and an enabled one enabled;
- removal deletes every file and the enable symlink;
- reinstalling after removal enables the path unit again;
- purge (deb) leaves nothing behind.

lintian (deb) and rpmlint (rpm) run at the end for information only.

It needs podman or docker. Run it from the repo root after `make package`:

```
podman run --rm -v "$PWD":/src:ro -w /src debian:12 packaging/scripts/smoke-test.sh
podman run --rm -v "$PWD":/src:ro -w /src debian:13 packaging/scripts/smoke-test.sh
podman run --rm -v "$PWD":/src:ro -w /src ubuntu:24.04 packaging/scripts/smoke-test.sh
podman run --rm -v "$PWD":/src:ro -w /src fedora:latest packaging/scripts/smoke-test.sh
```

systemd does not run inside these containers, so the scriptlets only enable
and disable units there; nothing is started. CI
([package.yml](../.github/workflows/package.yml)) builds the packages in
`debian:12` and runs the same script on all four images, on every PR and
push to master. [release.yml](../.github/workflows/release.yml) runs that
workflow first when a `v*` tag is pushed, then publishes the .deb, .rpm and
`SHA256SUMS` as a GitHub Release.

### On a real system

Use a VM, or your own machine if you are fine with installing it there:

```
sudo apt install ./dist/penguintier_*_amd64.deb      # or: sudo dnf install ./dist/penguintier-*.rpm
systemctl status penguintier-dnsd.path
```

- "Penguintier" shows up in the app launcher with its icon.
- `penguintier-dnsd.path` is enabled and "active (waiting)". Until P4
  replaces the stub daemon, it turns "failed (Result:
  unit-start-limit-hit)" a few seconds after
  `/var/lib/zerotier-one/authtoken.secret` exists (so right away when
  ZeroTier is installed): the stub exits at once, so the path unit starts
  it again and again until systemd gives up.
- `sudo apt purge penguintier` (or `sudo dnf remove penguintier`) removes
  every file and the enable symlink. The `penguintier-dns` system user stays
  on purpose (see `packaging/scripts/postremove.sh`).

`apt remove` (without purge) also works, and a later reinstall enables the
path unit again (see `packaging/scripts/preinstall.sh` for why that needs a
marker file).

## P7: Manual matrix

TODO(P7): VMs (GNOME Boxes or quickemu, with snapshots):

- Ubuntu 24.04 GNOME: primary, DNS end-to-end.
- Fedora GNOME: no tray by default, SELinux.
- Fedora KDE: tooltip.
- Debian 13 GNOME: the "Enable DNS support…" path.
- Debian 12 XFCE: X11.
- Mint 22 Cinnamon.
- Fedora Sway: no polkit agent.
- Pop!_OS COSMIC.
