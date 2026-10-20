#!/bin/sh
# Settings, the torrent list and downloads in /var/lib/equinox are kept, even on purge.
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload || true
fi
