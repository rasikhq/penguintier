# fyne.io/systray, patched for Penguintier

This directory is a copy of [fyne.io/systray](https://github.com/fyne-io/systray)
**v1.12.2** (tag `v1.12.2`, commit `fd428cd3214656deb9ecfd5691a8324ae669156d`),
the tray icon library Penguintier uses. fyne.io/systray is itself a fork of
[getlantern/systray](https://github.com/getlantern/systray).

The root `go.mod` uses this copy instead of the published module:

```
replace fyne.io/systray => ./third_party/fyne-systray
```

## License

Everything in this directory is under the **Apache License, Version 2.0**
(see [LICENSE](LICENSE), "Copyright 2014 Brave New Software Project, Inc."),
not under Penguintier's MIT license. What the Apache License asks of a
modified copy, and how this copy does it:

- The license text is kept: `LICENSE` is unchanged.
- Copyright and attribution notices are kept: `LICENSE`, `README.md` and
  every unchanged file are byte-for-byte upstream.
- Changed files say so: each changed or added file has a notice at the
  top, and the changes are listed below.
- Upstream has no `NOTICE` file, so there is none to carry along.

## What differs from upstream

| File | Change |
|---|---|
| `example/` | Left out (a sample program and a screenshot, not needed to build). |
| `systray.go` | `systrayMenuItemSelected` keeps `menuItemsLock` read-locked until the click is delivered. Before, it unlocked first, so `MenuItem.Remove` (called by `ResetMenu`) could close the item's `ClickedCh` in between, and the click then panicked with "send on closed channel", crashing the app. |
| `penguintier_race_test.go` | New. Clicks an item from several goroutines while removing it. It panics on the upstream code and passes with the fix. |
| `PENGUINTIER.md` | New: this file. |

## Checking the copy

To see every difference from upstream:

```
go mod download fyne.io/systray@v1.12.2
diff -r "$(go env GOMODCACHE)/fyne.io/systray@v1.12.2" third_party/fyne-systray
```

The only differences should be the ones in the table above. To run the
copy's tests (CI and `make test` do this too):

```
cd third_party/fyne-systray && go test -race ./...
```

## Moving to a newer upstream version

1. Copy the new version here the same way, without `example/`.
2. If upstream has not fixed the race, apply the `systray.go` change again,
   with its notice.
3. Run the copy's tests. `penguintier_race_test.go` must pass.
4. Update the version in the root `go.mod` `require` line and in this file.
