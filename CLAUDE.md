# Penguintier

A Linux tray app for ZeroTier (MIT). It matches the official
macOS/Windows tray menu and makes ZeroTier's "Allow DNS Configuration" work
on Linux through systemd-resolved. Unofficial app. Not affiliated with
ZeroTier, Inc.

- Plan (source of truth): `docs/plan.md`
- Contracts and file ownership: `docs/architecture.md`
- How to test: `docs/testing.md`

## Stack

- Go, one module `github.com/rasikhq/penguintier`, no cgo: the tray
  (`fyne.io/systray`), the DNS daemon (`github.com/godbus/dbus/v5`), helpers.
- `ui/`: a separate Go module. Wails v2 + React/TypeScript, built with bun
  and the `webkit2_41` build tag. Small Join/About windows, launched on demand.
- Packaging: GoReleaser/nfpm (.deb + .rpm).
- App ID: `io.github.rasikhq.Penguintier`.

## Commands

```
make tools      # install pinned golangci-lint and wails into ~/go/bin
make lint test build ui-build
make ui-dev     # UI_SCREEN=join|about
make run        # tray from source
```

Go is pinned with a `toolchain` line in `go.mod` and `ui/go.mod`, and Go
downloads it automatically. If `go env GOTOOLCHAIN` prints `local`, run
`go env -w GOTOOLCHAIN=auto` once.

## Rules

### Go style: keep it beginner-friendly

Contributors may be new to Go. Write Go that a newcomer can read and
review:

- Plain, explicit code. No clever generics, reflection tricks or deep
  abstraction layers. A little repetition beats a confusing helper.
- Simple goroutine patterns: one owner goroutine per piece of state, and
  channels to send it commands. Avoid shared mutable state guarded by
  complex locking.
- Comment the why, and explain Go idioms a newcomer to Go might not
  know (defer, error wrapping with `%w`, `errors.Is`, zero values,
  embedding, `context`).
- Return errors with context: `fmt.Errorf("read token %s: %w", path, err)`.
  Never ignore errors without a comment saying why.
- Prefer the standard library. Adding a dependency needs a reason in the PR.
- Table-driven tests next to the code (`*_test.go`), fixtures in `testdata/`.
- Each PR description explains the new Go concepts it introduces.

### File ownership

Work is split into phases P0–P7. Each phase edits only the paths it owns
(table in `docs/architecture.md`). Touching another phase's file must be
called out in the PR description. Contract changes go into
`docs/architecture.md` first.

### go.mod / go.sum conflicts

P0 already imports every third-party dependency (blank imports in
`deps.go` files), so `go.mod` rarely changes. On a `go.mod`/`go.sum` merge
conflict: keep the union of both sides, then run `go mod tidy`. Delete a
package's `deps.go` once the package imports that dependency for real.

### Other

- `CGO_ENABLED=0` must keep building every root-module binary.
- Never copy code from zerotier/DesktopUI (MPL-2.0). Build from observed
  behavior and the API docs. CI runs a license check.
- `third_party/` holds patched copies of dependencies (now fyne.io/systray,
  used through a `replace` in `go.mod`). Keep their license files, mark
  every changed file, and list each change in its `PENGUINTIER.md`.
- Commits: author `Rasikh Q. <rasikhqadeer98@gmail.com>` (set in the repo's
  git config). No `Co-Authored-By:` trailers. One PR per phase into `master`.
