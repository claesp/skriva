#!/bin/sh
# Before removal of the Skriva package. An update does not stop the
# server here: the new package restarts it.
# Debian: $1 is "remove" or "upgrade". RPM: $1 is 0 for a removal.
set -e

if [ -d /run/systemd/system ] && { [ "$1" = remove ] || [ "$1" = 0 ]; }; then
	systemctl disable --now skriva.service >/dev/null 2>&1 || true
fi
