#!/bin/sh
# postremove: runs after the package's files are deleted, on a real removal
# and also at the end of an upgrade.
#
# It tells systemd to re-read its unit files, so it forgets the removed
# penguintier-dnsd units (or loads the upgraded ones).
#
# The arguments differ between formats (.deb: "remove", "purge", "upgrade",
# ...; .rpm: 0 = removal, 1 = upgrade), but a reload is right in every case,
# so this script ignores them.
#
# The penguintier-dns system user is kept on purpose: deleting a system user
# could leave files owned by a number that a new user later reuses.
set -e

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
fi

exit 0
