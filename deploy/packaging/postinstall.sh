#!/bin/sh
# After installation or update of the Skriva package.
# Debian: $1 is "configure" and $2 is the old version for an update.
# RPM: $1 is 1 for an installation and 2 or more for an update.
set -e

# The system user of the service. It has no home and no shell.
if ! getent passwd skriva >/dev/null; then
	useradd --system --user-group --no-create-home --home-dir /nonexistent \
		--shell /usr/sbin/nologin --comment "Skriva wiki server" skriva
fi

# The TLS certificates: a private CA in /etc/skriva/ca, and the
# certificates of Skriva, Keycloak and PostgreSQL in /etc/skriva/tls. The
# command never replaces a file. Replace them with your own files.
if [ ! -f /etc/skriva/tls/skriva.crt ]; then
	host=$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)
	if /usr/bin/skriva certs -dir /etc/skriva/tls -ca-dir /etc/skriva/ca \
		-skriva-hosts "$host" -keycloak-hosts "$host" -postgres-hosts "$host" >/dev/null; then
		chmod 755 /etc/skriva/tls
		# The service reads its key as the group skriva.
		chgrp skriva /etc/skriva/tls/skriva.key
		chmod 640 /etc/skriva/tls/skriva.key
		echo "Skriva: made a private CA (/etc/skriva/ca/ca.crt) and TLS certificates in /etc/skriva/tls."
	else
		echo "Skriva: could not make the TLS certificates. Run: skriva certs -dir /etc/skriva/tls -ca-dir /etc/skriva/ca" >&2
	fi
fi

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
