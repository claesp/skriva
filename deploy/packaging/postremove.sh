#!/bin/sh
# After removal of the Skriva package.
set -e

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi
