# Ozzvin's Umbrel community app store

Community app store for [umbrelOS](https://umbrel.com). Apps:

- **Equinox** — a fast, tidy BitTorrent client ([source](https://github.com/Ozzvin/equinox)).

## Add the store

In umbrelOS: **App Store → ⋯ (top right) → Community App Stores**, paste this repository's URL, then open the
store and install Equinox.

## Maintainers

`ozzvin-equinox/docker-compose.yml` points at `ghcr.io/ozzvin/equinox:<version>` (built by the *Docker image*
workflow in the Equinox repository; the package must be public). To release a new version, change the tag in the
compose file and `version`/`releaseNotes` in `umbrel-app.yml` together.
