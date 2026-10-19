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
| `make licenses` | Every dependency (of the root module and of `ui/`) has an allowed license (MIT, BSD, Apache-2.0, ISC, MPL-2.0). |
| `make third-party-licenses` | `bin/THIRD_PARTY_LICENSES`: the license texts the packages install (see P6). |

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

TODO(P3): golden menu snapshots for every zt-mock scenario, the no-op update
check, `make tray-mock`.

## P4: DNS daemon

TODO(P4): planner table tests, fake resolve1 on a private `dbus-daemon`,
`systemd-analyze security`, `scripts/zt-selftest-network.sh` in a VM.

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

### Third-party licenses

The programs contain other people's code, and their licenses (MIT, BSD,
Apache-2.0) require passing on the license text with the compiled
programs. `make third-party-licenses` writes it all into
`bin/THIRD_PARTY_LICENSES`; `make package` runs it before building (the
second `before` hook in `.goreleaser.yaml`), and the packages install the
file as `/usr/share/doc/penguintier/THIRD_PARTY_LICENSES`.

```
make third-party-licenses
less bin/THIRD_PARTY_LICENSES
```

What goes in, and where it comes from (`cmd/licensegen` puts it together):

| Part | Which components | How they are found |
|---|---|---|
| Go standard library | the Go that builds the programs | `LICENSE` in `$(go env GOROOT)` |
| Go modules | every package compiled into `penguintier`, `penguintier-dnsd`, `penguintier-token-helper` (root module) and `penguintier-ui` (`ui/` module) | `go-licenses report` lists them and their license files, the same tool as `make licenses`. For `ui/` it uses the build tags of the real build (`UI_GO_TAGS` in the Makefile), so it sees the same packages as `wails build`. |
| JavaScript | what Vite bundles into the Join/About windows: `dependencies` in `ui/frontend/package.json` and theirs, recursively (React, react-dom, scheduler) | read from `ui/frontend/node_modules` (installed with `bun install --frozen-lockfile`, so it matches `bun.lock`): `package.json` and the `LICENSE`/`COPYING`/`NOTICE` files |

The file is made from the dependencies as they are, so adding a dependency
needs no extra step. It also refuses to guess: a package whose license
`go-licenses` cannot identify, or an npm package without a `LICENSE` file
or `license` field, stops the build with the package's name. Dev tools
(vite, typescript) are not bundled and are not listed. `vite.config.ts`
turns off Vite's modulepreload polyfill for the same reason: it is Vite's
own code and would be inlined into the bundle.

Check a change to this by comparing with what is really in the binaries:

```
make ui-build build
go version -m ui/build/bin/penguintier-ui bin/penguintier    # the "dep" lines
```

Every module listed there has to be in `THIRD_PARTY_LICENSES`.
`go test ./cmd/licensegen` covers the file format and the error cases.

### Install smoke test in containers

[packaging/scripts/smoke-test.sh](../packaging/scripts/smoke-test.sh)
installs the package in a throwaway container and checks:

- every file is installed, with the right mode, owned by root;
- `penguintier --version` runs;
- the `penguintier-dns` system user exists;
- the preset enabled `penguintier-dnsd.path` (and not the service);
- `/usr/share/doc/penguintier/THIRD_PARTY_LICENSES` names the main bundled
  components (systray, dbus, Wails, Go, React) and holds the Apache, BSD
  and MIT license texts;
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
