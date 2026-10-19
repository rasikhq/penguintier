#!/bin/sh
# postinstall: runs after the package's files are in place, on a first
# install and on every upgrade.
#
# It creates the DNS daemon's system user and sets up its systemd units:
#   - first install: enable penguintier-dnsd.path (through the preset file)
#     and start it, so the DNS daemon starts once ZeroTier is running;
#   - upgrade: keep the admin's enable/disable choice, and only restart the
#     daemon if it is already running, so it picks up the new binary.
#
# nfpm uses this one script for both package formats, and they pass
# different arguments:
#   .deb (dpkg "postinst"): $1 = "configure" and $2 = the version that was
#        configured before (empty on a first install); or $1 = "abort-..."
#        when dpkg rolls back a failed upgrade or removal.
#   .rpm (rpm "%post"): $1 = how many copies of the package are installed
#        after this step: 1 = first install, 2 = upgrade.
#
# A failing postinstall leaves the package half-installed, so problems the
# user can fix later (for example systemd not running inside a container)
# only print a message: those commands end in "|| true" or "|| echo ...".
set -e

# Work out whether this is a first install or an upgrade.
action=upgrade
case "$1" in
	configure)
		if [ -z "$2" ]; then
			action=install
		fi
		;;
	abort-remove)
		# A removal failed after preremove.sh had already stopped and
		# disabled the units: set them up again, like a first install.
		action=install
		;;
	abort-upgrade | abort-deconfigure)
		# dpkg is restoring the old version after a failed upgrade.
		;;
	1)
		action=install
		;;
	[0-9]*)
		# rpm upgrade (2 or more).
		;;
	*)
		# An argument we don't know: do nothing rather than guess.
		exit 0
		;;
esac

# preinstall.sh leaves this file on every new install. It also covers a deb
# reinstall after "apt remove", where $2 is set (see preinstall.sh).
if [ -e /run/penguintier-first-install ]; then
	action=install
	rm -f /run/penguintier-first-install
fi

# Create the penguintier-dns system user that runs the DNS daemon. It is
# defined in /usr/lib/sysusers.d/penguintier.conf; running this again when
# the user already exists changes nothing.
if command -v systemd-sysusers >/dev/null 2>&1; then
	systemd-sysusers penguintier.conf ||
		echo "penguintier: systemd-sysusers failed; penguintier-dnsd needs the penguintier-dns user" >&2
fi

# No systemctl (some minimal containers): nothing else to set up.
if ! command -v systemctl >/dev/null 2>&1; then
	exit 0
fi

# Is systemd running right now? It is not inside most containers and
# chroots. systemctl can still enable units there (it only creates
# symlinks), but it cannot reload or start anything.
systemd_running=false
if [ -d /run/systemd/system ]; then
	systemd_running=true
fi

if [ "$systemd_running" = true ]; then
	# Make systemd read the new or changed unit files.
	systemctl daemon-reload || true
fi

if [ "$action" = install ]; then
	# Enable penguintier-dnsd.path if the preset files say so.
	# /usr/lib/systemd/system-preset/80-penguintier.preset enables it; an
	# admin can override that in /etc/systemd/system-preset/.
	systemctl preset penguintier-dnsd.path ||
		echo "penguintier: could not enable penguintier-dnsd.path" >&2

	# Start it now (only if the preset enabled it). It waits for
	# /var/lib/zerotier-one/authtoken.secret, then starts the DNS daemon.
	if [ "$systemd_running" = true ] && systemctl --quiet is-enabled penguintier-dnsd.path; then
		systemctl start penguintier-dnsd.path ||
			echo "penguintier: could not start penguintier-dnsd.path" >&2
	fi
else
	# Upgrade: restart the daemon only if it is running ("try-restart").
	if [ "$systemd_running" = true ]; then
		systemctl try-restart penguintier-dnsd.service || true
	fi
fi

exit 0
