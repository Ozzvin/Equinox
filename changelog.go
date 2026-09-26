// Package equinox holds what the program carries from the root of the repository.
package equinox

import _ "embed"

// Changelog is CHANGELOG.md, what each version changed (in Russian), for the Updates page of the settings.
//
//go:embed CHANGELOG.md
var Changelog string
