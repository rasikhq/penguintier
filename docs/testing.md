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

TODO(P2): Join dialog checks (hex filter, 16 characters, Enter/Esc, exit
codes), About window, icon preview page.

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
