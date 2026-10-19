#!/bin/sh
# preinstall: runs before the package's files are unpacked.
#
# Its only job is to tell postinstall.sh "this is a first install", because
# postinstall cannot always tell on its own (see below).
#
# nfpm uses this one script for both package formats, and they pass
# different arguments:
#   .deb (dpkg "preinst"): $1 = "install" for a new install,
#        "upgrade" when replacing an installed version.
#   .rpm (rpm "%pre"):     $1 = how many copies of the package will be
#        installed after this step: 1 = new install, 2 = upgrade.
#
# Why the marker file: after "apt remove" (not "apt purge"), dpkg keeps a
# record of the package (state "rc"). Installing it again then passes the
# old version to postinstall, exactly like an upgrade. preremove.sh disabled
# the DNS units on removal, so postinstall must enable them again. Only this
# script sees the difference ("install", not "upgrade").
#
# The marker lives in /run, which is emptied on every boot, so it can never
# outlive a half-finished install for long.
set -e

case "$1" in
	install | 1)
		# ":" is the shell's do-nothing command; "> file" creates an
		# empty file. If /run is missing, postinstall falls back to its
		# own arguments, so a failure here must not stop the install.
		: >/run/penguintier-first-install 2>/dev/null || true
		;;
esac

exit 0
