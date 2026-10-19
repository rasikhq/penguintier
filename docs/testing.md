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

TODO(P1): httptest coverage, sanitized fixtures from ZeroTier 1.16.2 in
`testdata/`, live tests, how to run `make dev-install-helper`.

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

TODO(P6): container install smoke tests (debian:12, ubuntu:24.04,
fedora:latest), `systemd-analyze verify`.

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
