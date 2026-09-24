# The image only packs a server binary built beforehand (see .github/workflows/docker.yml): the binaries are
# cross-compiled once, with the same flags as the release archives, instead of inside Docker.
#
#   docker run -p 8080:8080 -p 51413:51413 -p 51413:51413/udp -v equinox:/data -v ~/Downloads:/downloads \
#     ghcr.io/ozzvin/equinox -open -listen 0.0.0.0:8080 -no-browser -state /data -downloads /downloads
#
# -open turns the personal key off: publish the web port only where something in front of it signs users in
# (Umbrel's app proxy, a reverse proxy with a login) or bind it to 127.0.0.1 (-p 127.0.0.1:8080:8080).
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETARCH
COPY bin/equinox-server-linux-${TARGETARCH} /equinox-server
EXPOSE 8080 51413/tcp 51413/udp
ENTRYPOINT ["/equinox-server"]
