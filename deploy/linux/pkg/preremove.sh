#!/bin/sh
# Stops the service only on a real removal (deb: "remove"; rpm: 0), not on an upgrade.
set -e
if [ "$1" = remove ] || [ "$1" = 0 ]; then
	if [ -d /run/systemd/system ]; then
		systemctl disable --now equinox-server.service || true
	fi
fi
