# Third-party code

Copies of other projects' code that Penguintier carries with its own
changes. Each directory keeps its original license file, which applies to
everything in that directory instead of Penguintier's MIT license, and a
`PENGUINTIER.md` that names the upstream version and lists every change.

| Directory | Upstream | License | Why it is here |
|---|---|---|---|
| [fyne-systray](fyne-systray/) | fyne.io/systray v1.12.2 | Apache-2.0 | Fixes a crash when a menu click arrives while the tray menu is rebuilt. |

Rules for this folder:

- Keep the upstream files as they are, apart from the listed changes.
- Every changed or added file gets a notice at the top saying that
  Penguintier changed it, and the change goes into `PENGUINTIER.md`.
- Never put code here that comes from zerotier/DesktopUI (MPL-2.0).
