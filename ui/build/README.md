# Build directory

- `appicon.png`: the window icon (128 px, because GTK 3 drops larger ones
  on X11), embedded by `main.go`.
  It is generated from the app icon by `python3 assets/render-icons.py`, so
  don't edit it by hand.
- `bin/`: build output (`penguintier-ui`), not committed.

Penguintier is Linux-only, so the template's `darwin/` and `windows/`
directories were removed.
