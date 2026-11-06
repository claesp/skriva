#!/bin/sh
# After installation or update of the Skriva package.
# Debian: $1 is "configure" and $2 is the old version for an update.
# RPM: $1 is 1 for an installation and 2 or more for an update.
set -e

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	update=false
	if [ "$1" = configure ] && [ -n "${2:-}" ]; then
		update=true
	elif [ "$1" -ge 2 ] 2>/dev/null; then
		update=true
	fi
	if [ "$update" = true ]; then
		# The new server applies the database migrations at start.
		systemctl try-restart skriva.service >/dev/null 2>&1 || true
	else
		echo "Skriva: edit /etc/skriva/skriva.env, then run: systemctl enable --now skriva"
	fi
fi
