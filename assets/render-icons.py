#!/usr/bin/env python3
"""Render Penguintier's PNG icons from their SVG sources.

Run it from the repo root after changing an SVG:

    python3 assets/render-icons.py

It draws with librsvg, the SVG renderer GNOME itself uses, through
PyGObject. GNOME desktops usually have both already; otherwise, on
Debian/Ubuntu: sudo apt install python3-gi python3-gi-cairo gir1.2-rsvg-2.0
"""

import os
import sys

import gi

gi.require_version("Rsvg", "2.0")
from gi.repository import Rsvg  # noqa: E402  (must come after require_version)

import cairo  # noqa: E402

APP_ID = "io.github.rasikhq.Penguintier"
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

APP_SVG = f"assets/icons/hicolor/scalable/apps/{APP_ID}.svg"
# Small sizes use a simpler drawing made for a 16 px grid.
APP_SMALL_SVG = "assets/brand/app-icon-small.svg"

# (source SVG, output PNG, size in pixels)
JOBS = []
for size in (16, 22, 24):
    JOBS.append((APP_SMALL_SVG, f"assets/icons/hicolor/{size}x{size}/apps/{APP_ID}.png", size))
for size in (32, 48, 64, 128, 256):
    JOBS.append((APP_SVG, f"assets/icons/hicolor/{size}x{size}/apps/{APP_ID}.png", size))
for state in ("online", "offline", "attention"):
    JOBS.append((f"assets/tray/penguintier-{state}.svg", f"assets/tray/penguintier-{state}.png", 64))
# The UI's window icon (embedded by ui/main.go). 128 px, not larger: on
# X11, GTK 3 drops a 512 px window icon without a word, and taskbars that
# read _NET_WM_ICON (Cinnamon, Xfce) then show a blank icon.
JOBS.append((APP_SVG, "ui/build/appicon.png", 128))


def render(svg_path, png_path, size):
    handle = Rsvg.Handle.new_from_file(os.path.join(ROOT, svg_path))
    surface = cairo.ImageSurface(cairo.FORMAT_ARGB32, size, size)
    viewport = Rsvg.Rectangle()
    viewport.x, viewport.y, viewport.width, viewport.height = 0, 0, size, size
    handle.render_document(cairo.Context(surface), viewport)
    out = os.path.join(ROOT, png_path)
    os.makedirs(os.path.dirname(out), exist_ok=True)
    surface.write_to_png(out)
    print(f"{png_path} ({size} px) <- {svg_path}")


def main():
    for svg_path, png_path, size in JOBS:
        render(svg_path, png_path, size)
    return 0


if __name__ == "__main__":
    sys.exit(main())
