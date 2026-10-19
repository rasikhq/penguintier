# Package scripts

The first four are the package's "scriptlets": the package manager runs
them as root when Penguintier is installed, upgraded or removed.
`.goreleaser.yaml` puts the same script into both the .deb and the .rpm,
so each one handles both formats' arguments (explained at the top of each
file).

| File | deb name | rpm name | What it does |
|---|---|---|---|
| `preinstall.sh` | preinst | %pre | Marks a first install for postinstall. |
| `postinstall.sh` | postinst | %post | Creates the `penguintier-dns` user; first install: enables and starts `penguintier-dnsd.path`; upgrade: restarts the daemon if it runs. |
| `preremove.sh` | prerm | %preun | Real removal only: stops and disables the DNS units. |
| `postremove.sh` | postrm | %postun | `systemctl daemon-reload`. |

`smoke-test.sh` is not shipped. It installs the built package in a
container and checks it; see "P6: Packaging" in
[docs/testing.md](../../docs/testing.md).
