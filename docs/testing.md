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
