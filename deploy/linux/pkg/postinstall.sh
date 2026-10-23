#!/bin/sh
# Runs after install and upgrade (deb: "configure"; rpm: 1 or 2).
set -e
if command -v systemd-sysusers >/dev/null 2>&1; then
	systemd-sysusers equinox-server.conf
elif ! getent passwd equinox >/dev/null; then
	useradd --system --user-group --home-dir /var/lib/equinox --no-create-home --shell /usr/sbin/nologin equinox
fi
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload || true
	# an upgrade restarts a running server on the new binary; a fresh install starts nothing
	systemctl try-restart equinox-server.service || true
fi
