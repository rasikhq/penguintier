# Build directory

- `appicon.png`: the window icon (512 px), embedded by `main.go`.
  Generated from the app icon by `python3 assets/render-icons.py`; don't
  edit it by hand.
- `bin/`: build output (`penguintier-ui`), not committed.

Penguintier is Linux-only, so the template's `darwin/` and `windows/`
directories were removed.
