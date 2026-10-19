#!/bin/sh
# preremove: runs before the package's files are deleted, on a real removal
# and also during an upgrade (when the old version is replaced).
#
# On a real removal it stops and disables the DNS daemon's units. Stopping
# penguintier-dnsd reverts the DNS settings it applied. During an upgrade it
# does nothing: postinstall.sh restarts the daemon instead.
#
# nfpm uses this one script for both package formats, and they pass
# different arguments:
#   .deb (dpkg "prerm"): $1 = "remove" on a real removal; "upgrade",
#        "deconfigure" or "failed-upgrade" otherwise.
#   .rpm (rpm "%preun"): $1 = how many copies of the package are left after
#        this step: 0 = real removal, 1 = upgrade.
set -e

case "$1" in
	remove | 0)
		;;
	*)
		exit 0
		;;
esac

if ! command -v systemctl >/dev/null 2>&1; then
	exit 0
fi

# "disable" removes the "enabled" symlinks; "--now" also stops the units,
# which needs a running systemd (not the case in most containers).
# A failure here must not block the removal, hence "|| true".
if [ -d /run/systemd/system ]; then
	systemctl disable --now penguintier-dnsd.path penguintier-dnsd.service || true
else
	systemctl disable penguintier-dnsd.path penguintier-dnsd.service || true
fi

exit 0
