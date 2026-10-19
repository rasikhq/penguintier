<p align="center">
  <img src="assets/icons/hicolor/scalable/apps/io.github.rasikhq.Penguintier.svg" width="96" height="96" alt="">
</p>

<h1 align="center">Penguintier</h1>

<p align="center">
  A system tray app for <a href="https://www.zerotier.com/">ZeroTier</a> on Linux.<br>
  The same menu as the official ZeroTier app on macOS and Windows, and
  ZeroTier DNS that actually works on Linux.
</p>

> [!NOTE]
> **Unofficial; not affiliated with ZeroTier, Inc.** Penguintier talks to
> the ZeroTier service you already have installed; it is not a replacement
> for it.

<p align="center">
  <img src="docs/screenshots/tray-menu.png" width="270" align="top" alt="Penguintier's tray menu on GNOME: your address, Join New Network, two networks, Start at Login, About, Quit">
  &nbsp;
  <img src="docs/screenshots/tray-network.png" width="331" align="top" alt="A network's submenu: Network ID, the four Allow check boxes, status, interface, DNS domain and DNS row, addresses and routes">
</p>

## What it does

- **Your address:** "My Address: …" at the top; click it to copy.
- **Join, leave, come back:** Join New Network… asks for a 16-digit network
  ID. Disconnect keeps the network under Saved Networks with its settings,
  so Reconnect brings it back as it was. Forget removes it.
- **Per-network settings:** Allow Managed Addresses, Allow Global IPs,
  Allow Default Route Override and Allow DNS Configuration, as check boxes.
- **Per-network details:** status, interface, MAC address, type, DNS domain,
  addresses and routes. Click an address to copy it (without the `/24`).
- **SSO networks:** a "Log In to …" entry and one notification when a
  network needs you to log in, plus the login's expiry time.
- **The icon tells you the state:** online, offline, or needs attention.
- **DNS for ZeroTier networks**, through systemd-resolved
  ([how it works](#dns-for-zerotier-networks)).
- **Start at Login**, About, Quit, and `penguintier doctor` for when
  something doesn't work.

Penguintier runs on Ubuntu 24.04+, Debian 12+, current Fedora and Linux
Mint 22+ (x86_64). It shows its icon on any desktop whose panel supports
tray icons (StatusNotifierItem, also called AppIndicator); see
[your desktop](#3-check-your-desktops-tray).

## Install

### 1. Install ZeroTier

Penguintier needs the ZeroTier service (`zerotier-one`). Install it from
<https://www.zerotier.com/download/>; on Linux that is usually ZeroTier's
install script, which adds their package repository:

```bash
curl -s https://install.zerotier.com | sudo bash
```

Check that it runs:

```bash
sudo zerotier-cli info
```

It should print `200 info <your address> <version> ONLINE`.

### 2. Install Penguintier

Download the package for your distribution from the
[latest release](https://github.com/rasikhq/penguintier/releases/latest),
then install it with your package manager (it pulls in what it needs).

**Debian, Ubuntu, Linux Mint, Pop!_OS**

```bash
sudo apt install ./penguintier_0.1.0_amd64.deb
```

**Fedora**

```bash
sudo dnf install ./penguintier-0.1.0-1.x86_64.rpm
```

To check the download first, save `SHA256SUMS` from the same release next
to it and run `sha256sum --check --ignore-missing SHA256SUMS`.

Then start **Penguintier** from your app menu. To update, install a newer
package the same way.

### 3. Check your desktop's tray

| Desktop | What to do |
|---|---|
| KDE Plasma | Nothing; it works as is. |
| Xfce | Nothing with the default panel. If your panel has no tray, add the "Status Tray Plugin". |
| Cinnamon (Linux Mint) | Nothing; it works as is. |
| COSMIC (Pop!_OS) | Nothing; it works as is. |
| sway, Hyprland, niri with waybar | Nothing, as long as `tray` is in your waybar modules (it is in the default config). |
| MATE, Budgie, LXQt | Works once the panel has a tray applet. |
| **GNOME** | **Needs the AppIndicator extension** (see below). Ubuntu ships it turned on. |

#### GNOME

GNOME only shows tray icons with the "AppIndicator and KStatusNotifierItem
Support" extension. **Ubuntu** installs and turns it on by default, so there
is nothing to do. Elsewhere, install it, then **log out and back in**
(GNOME on Wayland only finds new extensions at login), then turn it on:

| Distribution | Install | Turn on |
|---|---|---|
| Debian | `sudo apt install gnome-shell-extension-appindicator` | `gnome-extensions enable ubuntu-appindicators@ubuntu.com` |
| Fedora | `sudo dnf install gnome-shell-extension-appindicator` | `gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com` |
| Others | from [extensions.gnome.org](https://extensions.gnome.org/extension/615/) | `gnome-extensions enable appindicatorsupport@rgcjonas.gmail.com` |

The Debian/Ubuntu package and the extensions.gnome.org version have
different IDs, which is why the commands differ. You can also turn it on in
the Extensions app.

If Penguintier finds no tray when it starts, it shows a notification with
the steps for your desktop. On GNOME, a left click opens the menu after a
short pause (GNOME waits to see if it's a double click), and GNOME doesn't
show tray tooltips.

## First run: authorizing Penguintier

The first time you start Penguintier, your desktop asks for your password:

> Penguintier needs to read the ZeroTier service's API token so it can show
> and change your ZeroTier networks.

ZeroTier only lets programs that know its API token control it, and the
token file (`/var/lib/zerotier-one/authtoken.secret`) is readable only by
root. After you enter your password, Penguintier saves a **copy of the
token in `~/.zeroTierOneAuthToken`**, readable only by you, so it doesn't
have to ask again. `zerotier-cli` reads the same file, so it then works
without `sudo` too.

If you close the prompt, nothing happens; the menu shows
"Authorize Penguintier…" so you can do it later. Penguintier also turns on
**Start at Login** after the first successful start (uncheck it in the
menu to stop that).

### What the token copy means for security

- Whoever has the token can fully control ZeroTier on this computer:
  join and leave networks and change their settings, including **Allow
  Default Route Override**, which can send all your traffic through a
  network.
- With the copy in your home folder, **any program running as you** can do
  that too, without a password prompt. Other users on the computer can't
  read it (the file is mode 0600, and Penguintier refuses to use it if
  that changes).
- To remove the copy, run `penguintier deauthorize`. A Penguintier that is
  already running keeps working until you quit it; the next start asks
  again.
- Penguintier's DNS service never uses your copy: systemd hands it a
  private copy of the token when it starts.

## DNS for ZeroTier networks

A ZeroTier network can carry DNS settings: a domain (say `office.example`)
and the DNS servers that answer for it. The network's admin sets them in
ZeroTier Central or their own controller.

### Why "Allow DNS Configuration" does nothing on Linux

On macOS and Windows, checking **Allow DNS Configuration** makes ZeroTier
send queries for that domain to the network's DNS servers. On Linux the
ZeroTier service saves the setting but doesn't apply it; that part was
never implemented there
([ZeroTierOne#2492](https://github.com/zerotier/ZeroTierOne/issues/2492)).
So `fileserver.office.example` doesn't resolve, even with the box checked.

### How Penguintier does it

The package installs a small system service, **penguintier-dnsd**. It
runs as its own unprivileged user and checks your networks every 5
seconds. For each network that has Allow DNS Configuration on, is
connected and has a DNS domain and servers, it tells
**systemd-resolved**: "on this network's interface, send queries for this
domain to these servers."

- It sets the domain as **routing-only** (`~office.example`) and doesn't
  make the network a default DNS route. So only names under that domain
  go to the network's DNS servers; everything else keeps using your normal
  DNS, as before.
- It **leaves alone** any interface whose DNS it didn't set itself, so it
  steps aside for other tools (and for ZeroTier, if it ever does this
  itself on Linux).
- Turning the setting off, leaving the network or stopping the service
  removes what it set.
- If systemd-resolved isn't running, it waits and applies the settings as
  soon as it starts.

Each network's submenu shows a **DNS** row:

| Row | Meaning |
|---|---|
| DNS: applied | Names under the network's domain go to its DNS servers. |
| DNS: not requested (Allow DNS is off) | Check Allow DNS Configuration to use the network's DNS. |
| DNS: network has no DNS | The network's admin hasn't set a DNS domain and servers. |
| DNS: waiting for systemd-resolved | This computer doesn't run systemd-resolved; see below. |
| DNS: daemon not running | `penguintier-dnsd` is stopped; run `penguintier doctor`. |
| DNS: link managed by systemd-networkd | systemd-networkd manages this interface, so resolved won't take settings from other programs. |
| DNS: set by another program | Something else set DNS on this interface; Penguintier leaves it alone. |

### Enable DNS support… (Debian)

Ubuntu, Linux Mint, Pop!_OS and Fedora run systemd-resolved already.
**Debian doesn't**: there, NetworkManager writes `/etc/resolv.conf`
directly, and the DNS row says "waiting for systemd-resolved". The menu
then offers **Enable DNS support…**, which switches the computer to
systemd-resolved:

1. It checks that this is a setup it knows how to switch safely: Debian
   (or a relative) with NetworkManager managing `/etc/resolv.conf`, as a
   normal desktop install has. If not, it tells you why and changes
   nothing.
2. It asks you, and then asks for your password once.
3. It backs up `/etc/resolv.conf`, `/etc/nsswitch.conf` and (if present)
   `/etc/systemd/resolved.conf` to `/var/lib/penguintier/dns-backup/`.
4. It installs the `systemd-resolved` package (only that one).
5. It checks that names still resolve. If they don't, it **undoes
   everything by itself** and puts the backed-up files back.

**To undo it later**, run:

```bash
pkexec /usr/libexec/penguintier/penguintier-dns-helper undo
```

That removes systemd-resolved again and restores the backed-up files.
`penguintier doctor` shows whether the switch is on and prints the same
command. If you want your old DNS setup back, undo the switch **before**
uninstalling Penguintier, since the command comes with the package.

## Troubleshooting

Run the doctor. It checks every part Penguintier depends on and says how
to fix what's wrong:

```bash
penguintier doctor
```

```text
Penguintier doctor

  info  Penguintier       0.1.0
  ok    ZeroTier service  ZeroTier 1.16.2, running at http://127.0.0.1:9993
  ok    API token         /home/you/.zeroTierOneAuthToken (yours, mode 0600)
  ok    API access        the token works: node a1b2c3d4e5, online
  ok    Tray host         GNOME (wayland): gnome-shell shows tray icons
  ok    Notifications     gnome-shell 48.7
  ok    Clipboard         wl-copy, xclip
  ok    Authorizing       pkexec, agent gnome-shell, helper /usr/libexec/penguintier/penguintier-token-helper
  ok    systemd-resolved  running
  ok    DNS daemon        penguintier-dnsd.service is active
  ok    Network DNS       1 applied

No problems found.
```

Common problems:

- **No icon.** On GNOME, see [GNOME](#gnome). Elsewhere, make sure your
  panel has a tray. `penguintier doctor` says which desktop it found.
- **"Waiting for ZeroTier system service…"** The ZeroTier service isn't
  running: `sudo systemctl enable --now zerotier-one`.
- **No password prompt** (common on sway and other setups without a polkit
  agent). `penguintier doctor` prints a command that saves the token copy
  by hand.
- **Copying does nothing.** Install `wl-clipboard` (Wayland) or `xclip`
  (X11). Without them, Penguintier shows the text in a notification
  instead.
- **Logs:** `journalctl --user -t penguintier` for the tray,
  `journalctl -u penguintier-dnsd` for the DNS service.

When you [report a bug](https://github.com/rasikhq/penguintier/issues),
please attach `penguintier doctor --json`. It includes your ZeroTier
address and network IDs; replace them if you'd rather not share them.

## Command line

```text
penguintier                     start the tray
penguintier doctor [--json]     check the setup and print a report
penguintier deauthorize [--yes] delete the token copy
penguintier --version           print the version
```

Started from a terminal, `penguintier` moves to the background and gives
the terminal back; `--foreground` keeps it there with its log. See
`man penguintier` for everything, including environment variables and
files.

## Uninstall

```bash
penguintier deauthorize          # delete the token copy (optional)
sudo apt remove penguintier      # Debian, Ubuntu, Mint
sudo dnf remove penguintier      # Fedora
```

Removing the package stops the DNS service, which removes the DNS settings
it made. If you used "Enable DNS support…" and want your old DNS setup
back, [undo it](#enable-dns-support-debian) first.

Your settings stay in `~/.config/penguintier/`, and the Start at Login
entry in `~/.config/autostart/io.github.rasikhq.Penguintier.desktop`
(your desktop ignores it once Penguintier is gone). Delete them if you
want them gone.

## Build from source

You need Go (it downloads the exact version pinned in `go.mod` by itself),
[bun](https://bun.sh/) 1.3, make, and the GTK 3 and WebKitGTK 4.1
development packages:

```bash
sudo apt install golang make libgtk-3-dev libwebkit2gtk-4.1-dev   # Debian, Ubuntu
sudo dnf install golang make gtk3-devel webkit2gtk4.1-devel       # Fedora
```

If `go env GOTOOLCHAIN` prints `local`, run `go env -w GOTOOLCHAIN=auto`
once. Then:

```bash
make tools                       # golangci-lint, wails, goreleaser into ~/go/bin
make lint test build ui-build    # check and build everything
make run                         # run the tray from source
make package                     # .deb and .rpm into dist/
```

## Contributing

Bug reports, tests on more desktops and pull requests are welcome. Before
writing code, read:

- [CLAUDE.md](CLAUDE.md): the project rules (Go style, file ownership,
  commits). The Go code is kept plain and well commented, so people new to
  Go can read and review it.
- [docs/architecture.md](docs/architecture.md): how the parts fit
  together, and the contracts between them.
- [docs/testing.md](docs/testing.md): how each part is tested, including
  a fake ZeroTier API (`make tray-mock`) for working on the tray without
  touching your real networks.

Penguintier is written from scratch from ZeroTier's documented API and the
official app's observed behavior. Please don't copy code from ZeroTier's
own desktop app (it is under a different license).

## License

[MIT](LICENSE). The packages also install
`/usr/share/doc/penguintier/THIRD_PARTY_LICENSES` with the licenses of the
libraries compiled into Penguintier.

Penguintier is unofficial and not affiliated with ZeroTier, Inc. ZeroTier
is a trademark of ZeroTier, Inc.
