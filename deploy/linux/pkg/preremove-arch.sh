#!/bin/sh
# pacman runs pre_remove only on a real removal (an upgrade runs pre_upgrade), and passes it the version, not
# "remove" or 0 as deb and rpm do: so this one stops the service without asking.
if [ -d /run/systemd/system ]; then
	systemctl disable --now equinox-server.service || true
fi
