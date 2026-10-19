# Assets

Penguintier's icons. Original artwork, MIT license like the rest of the
repo. Penguintier is an unofficial app and is not affiliated with ZeroTier,
Inc., so the icon is deliberately not the ZeroTier logo.

| Path | What it is |
|---|---|
| `icons/hicolor/scalable/apps/io.github.rasikhq.Penguintier.svg` | The app icon (source for 32 px and up). |
| `brand/app-icon-small.svg` | The app icon simplified for 16–24 px, drawn on a 16 px grid. |
| `icons/hicolor/<size>/apps/io.github.rasikhq.Penguintier.png` | Generated PNGs, 16 to 256 px. Installed into the hicolor icon theme. |
| `tray/penguintier-{online,offline,attention}.svg` | Tray icons in color (16 px grid). |
| `tray/penguintier-{online,offline,attention}.png` | Generated 64 px PNGs of the tray icons. |
| `tray/penguintier-{online,offline,attention}-symbolic.svg` | One-color tray icons that GNOME and GTK recolor to match the panel. |
| `brand/preview.html` | Preview page: every size, on light and dark, in mock panels. |
| `render-icons.py` | Rebuilds every PNG from the SVGs. |

## Changing an icon

1. Edit the SVG.
2. `python3 assets/render-icons.py` from the repo root. It also refreshes
   `ui/build/appicon.png`, the UI's window icon.
3. Check `assets/brand/preview.html` in a browser.

Rules for the `-symbolic` icons: fills only (no strokes) and no masks,
because GTK recolors them by overriding `fill`. Draw them in `#2e3436`;
give a part `class="warning"` to have it painted in the theme's warning
color instead.

## Colors

| Use | Color |
|---|---|
| Body (app icon) | `#1E2B4A`, gradient `#2C4170` → `#1B2747` |
| Body (16–24 px and tray, lighter for dark panels) | `#2B3C66` |
| Orbit and peer | `#1EC8E0` |
| Beak and feet | `#FF9E2C` |
| Attention badge | `#F5A524` |
| Offline greys | `#6B7385`, `#8F98AA`, `#E3E6EB` |
