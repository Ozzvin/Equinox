Equinox server for Linux
========================

    ./equinox-server -no-browser

starts the torrent engine and the web interface on http://127.0.0.1:9091/ . The link with the access key is
printed on start; the key itself lives in <state>/api-token (default state directory: "data" next to the
program, or ~/.config/Equinox if that cannot be written). Never show that file to anyone.

Options (./equinox-server -h lists them all):

    -state DIR        where settings, the list of torrents and the log are kept
    -downloads DIR    where a first start saves torrents (default: ~/Downloads)
    -listen ADDR      HTTP address; only loopback addresses unless -open
    -open             run behind a proxy that signs users in (Umbrel, a reverse proxy with a login): listen on
                      any address and ask for no key. Anyone who can reach the port controls the client, so
                      never publish that port to a network you do not trust.

To reach it from another machine without -open, forward the port over SSH:
    ssh -L 9091:127.0.0.1:9091 you@server

Open the torrent port (51413, TCP and UDP by default) in your firewall and router for incoming connections.

Project page: https://github.com/Ozzvin/equinox
