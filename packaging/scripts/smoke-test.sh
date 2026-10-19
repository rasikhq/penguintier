#!/usr/bin/env bash
# Install smoke test for the .deb/.rpm packages. Not shipped in the package.
#
# Run it as root inside a throwaway container, from the repo root, after
# `make package`:
#
#   podman run --rm -v "$PWD":/src:ro -w /src debian:12 packaging/scripts/smoke-test.sh
#
# (docker works the same way.) CI runs it in debian:12, debian:13,
# ubuntu:24.04 and fedora:latest (.github/workflows/package.yml).
#
# It installs the package with the distro's package manager (which also
# pulls in its dependencies), checks every installed file and its mode, runs
# `penguintier --version` and `systemd-analyze verify`, checks the systemd
# preset and the system user, checks that the third-party license texts are
# there, then upgrades, removes, reinstalls and purges
# it, checking that the scriptlets did the right thing at each step.
#
# systemd is not running inside the container, so the scriptlets take their
# "offline" path: units are enabled or disabled, never started.
# lintian/rpmlint run at the end for information only; they never fail it.
set -euo pipefail

dist_dir=${1:-dist}

# --- small helpers ---------------------------------------------------------

step() { printf '\n==> %s\n' "$*"; }
fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

if [ "$(id -u)" -ne 0 ]; then
	fail "run this as root, inside a throwaway container"
fi

# Pick the package manager and the package file for this distro.
if command -v apt-get >/dev/null; then
	format=deb
	pkg_file=$(ls "$dist_dir"/penguintier_*_amd64.deb)
	export DEBIAN_FRONTEND=noninteractive
elif command -v dnf >/dev/null; then
	format=rpm
	pkg_file=$(ls "$dist_dir"/penguintier-*.x86_64.rpm)
else
	fail "no apt-get or dnf: unsupported distro"
fi
# The package managers want a path, not a bare file name.
pkg_file=$(realpath "$pkg_file")
echo "Testing $pkg_file ($format)"

install_tools() {
	if [ "$format" = deb ]; then
		# The ubuntu container image is "minimized": dpkg skips files in
		# /usr/share/doc (except copyright). Real desktops install them, so
		# undo that to check the package's docs too.
		rm -f /etc/dpkg/dpkg.cfg.d/excludes
		apt-get update -q
		apt-get install -y -q --no-install-recommends systemd desktop-file-utils appstream
	else
		dnf install -y -q systemd desktop-file-utils appstream
	fi
}

install_package() {
	if [ "$format" = deb ]; then
		# Like `apt install ./file.deb` on a desktop: Recommends included.
		apt-get install -y -q "$pkg_file"
	else
		dnf install -y -q "$pkg_file"
	fi
}

# Install the same version again: the scriptlets see an upgrade.
reinstall_package() {
	if [ "$format" = deb ]; then
		apt-get install -y -q --reinstall "$pkg_file"
	else
		dnf reinstall -y -q "$pkg_file"
	fi
}

remove_package() {
	if [ "$format" = deb ]; then
		apt-get remove -y -q penguintier
	else
		dnf remove -y -q penguintier
	fi
}

# The "enabled" state of a unit: enabled, disabled, ... (prints the error
# text instead when the unit file is missing).
unit_state() {
	systemctl is-enabled "$1" 2>&1 || true
}

# Fails unless no enable symlink for our units is left anywhere in /etc.
check_no_enable_links() {
	local links
	links=$(find /etc/systemd -name 'penguintier*' 2>/dev/null || true)
	if [ -n "$links" ]; then
		fail "systemd enable links left behind: $links"
	fi
}

# --- the expected files ------------------------------------------------------

# "<mode> <path>" for every file the package must install, owned by root.
# Keep in sync with "contents" in .goreleaser.yaml.
expected_files() {
	cat <<'EOF'
755 /usr/bin/penguintier
755 /usr/libexec/penguintier
755 /usr/libexec/penguintier/penguintier-ui
755 /usr/libexec/penguintier/penguintier-dnsd
755 /usr/libexec/penguintier/penguintier-token-helper
755 /usr/libexec/penguintier/penguintier-dns-helper
644 /usr/lib/systemd/system/penguintier-dnsd.service
644 /usr/lib/systemd/system/penguintier-dnsd.path
644 /usr/lib/systemd/system-preset/80-penguintier.preset
644 /usr/lib/sysusers.d/penguintier.conf
644 /usr/share/polkit-1/actions/io.github.rasikhq.Penguintier.policy
644 /usr/share/polkit-1/rules.d/50-penguintier-dnsd.rules
644 /usr/share/applications/io.github.rasikhq.Penguintier.desktop
644 /usr/share/metainfo/io.github.rasikhq.Penguintier.metainfo.xml
755 /usr/share/doc/penguintier
644 /usr/share/doc/penguintier/LICENSE
644 /usr/share/doc/penguintier/THIRD_PARTY_LICENSES
644 /usr/share/doc/penguintier/README.md
644 /usr/share/doc/penguintier/CHANGELOG.md
644 /usr/share/man/man1/penguintier.1.gz
EOF
	if [ "$format" = deb ]; then
		echo "644 /usr/share/doc/penguintier/copyright"
	fi
	# Every app icon in the repo (sizes may change), at the same place
	# under /usr/share/icons/hicolor.
	local icon
	for icon in assets/icons/hicolor/*/apps/io.github.rasikhq.Penguintier.*; do
		echo "644 /usr/share/icons/hicolor/${icon#assets/icons/hicolor/}"
	done
}

check_files_installed() {
	local mode path actual
	while read -r mode path; do
		if [ ! -e "$path" ]; then
			fail "missing: $path"
		fi
		# stat -c: %a = mode in octal, %U:%G = owner and group names.
		actual=$(stat -c '%a %U:%G' "$path")
		if [ "$actual" != "$mode root:root" ]; then
			fail "$path is '$actual', want '$mode root:root'"
		fi
		echo "ok  $mode  $path"
	done < <(expected_files)
}

# THIRD_PARTY_LICENSES must carry the license text of everything compiled
# into the programs (see cmd/licensegen). Spot-check one entry per kind of
# code, and each license family that the entries use. Keep in sync with the
# dependencies we really have: systray (Apache-2.0), dbus (BSD-2-Clause), the
# Go standard library (BSD-3-Clause), Wails and React (MIT).
check_third_party_licenses() {
	local file=/usr/share/doc/penguintier/THIRD_PARTY_LICENSES
	local text
	# Compare the file's text with grep -F (plain text, not a pattern).
	for text in \
		"fyne.io/systray" \
		"github.com/godbus/dbus/v5" \
		"github.com/wailsapp/wails/v2" \
		"Go standard library" \
		"react-dom" \
		"Apache License" \
		"Redistribution and use in source and binary forms" \
		"Permission is hereby granted, free of charge"; do
		grep -qF -- "$text" "$file" || fail "$file does not mention '$text'"
	done
	echo "ok  $file lists the bundled components and their license texts"
}

check_files_removed() {
	local mode path
	while read -r mode path; do
		if [ -e "$path" ]; then
			fail "still there after removal: $path"
		fi
	done < <(expected_files)
	echo "ok  all package files are gone"
}

# --- the test ----------------------------------------------------------------

step "Install test tools (systemd, desktop-file-utils, appstream)"
install_tools

step "Install the package"
install_package

step "Check files and modes"
check_files_installed

step "Check the third-party license texts"
check_third_party_licenses

step "Run penguintier --version"
version_output=$(penguintier --version)
echo "$version_output"
case "$version_output" in
	"penguintier "[0-9]*) ;;
	*) fail "unexpected --version output" ;;
esac

step "Check the scriptlets' work: system user and systemd preset"
getent passwd penguintier-dns || fail "system user penguintier-dns was not created"
state=$(unit_state penguintier-dnsd.path)
[ "$state" = enabled ] || fail "penguintier-dnsd.path is '$state', want 'enabled'"
state=$(unit_state penguintier-dnsd.service)
[ "$state" = disabled ] || fail "penguintier-dnsd.service is '$state', want 'disabled'"
echo "ok  penguintier-dnsd.path enabled, penguintier-dnsd.service disabled"
[ ! -e /run/penguintier-first-install ] || fail "preinstall marker was not cleaned up"

if [ "$format" = deb ]; then
	# Suggests only: installing it would switch the system's DNS.
	if dpkg -s systemd-resolved >/dev/null 2>&1; then
		fail "systemd-resolved got installed, but it is only suggested"
	fi
	echo "ok  systemd-resolved not pulled in"
fi

step "systemd-analyze verify"
systemd-analyze verify --man=no \
	/usr/lib/systemd/system/penguintier-dnsd.path \
	/usr/lib/systemd/system/penguintier-dnsd.service
echo "ok  units verified"

step "Validate the .desktop and metainfo files"
desktop-file-validate /usr/share/applications/io.github.rasikhq.Penguintier.desktop
appstreamcli validate --no-net /usr/share/metainfo/io.github.rasikhq.Penguintier.metainfo.xml

step "Upgrade keeps the admin's choice (disabled stays disabled)"
systemctl disable penguintier-dnsd.path
reinstall_package
state=$(unit_state penguintier-dnsd.path)
[ "$state" = disabled ] || fail "after upgrade penguintier-dnsd.path is '$state', want 'disabled'"
systemctl enable penguintier-dnsd.path
reinstall_package
state=$(unit_state penguintier-dnsd.path)
[ "$state" = enabled ] || fail "after upgrade penguintier-dnsd.path is '$state', want 'enabled'"
echo "ok  upgrades keep the enable/disable state"

step "Remove the package"
remove_package
check_files_removed
check_no_enable_links
[ ! -e /usr/libexec/penguintier ] || fail "/usr/libexec/penguintier is still there"
echo "ok  removal disabled the units"

step "Reinstall after removal: units are enabled again"
install_package
state=$(unit_state penguintier-dnsd.path)
[ "$state" = enabled ] || fail "after reinstall penguintier-dnsd.path is '$state', want 'enabled'"
echo "ok  penguintier-dnsd.path enabled again"

step "Final removal"
if [ "$format" = deb ]; then
	apt-get purge -y -q penguintier
	if dpkg -s penguintier >/dev/null 2>&1; then
		fail "dpkg still knows the package after purge"
	fi
else
	remove_package
fi
check_files_removed
check_no_enable_links

step "lintian / rpmlint (information only)"
if [ "$format" = deb ]; then
	apt-get install -y -q --no-install-recommends lintian >/dev/null
	lintian "$pkg_file" || true
else
	dnf install -y -q rpmlint >/dev/null
	rpmlint "$pkg_file" || true
fi

printf '\nPASS: %s on %s\n' "$(basename "$pkg_file")" "$(. /etc/os-release && echo "$PRETTY_NAME")"
