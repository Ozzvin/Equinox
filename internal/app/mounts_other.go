//go:build !linux

package app

import "github.com/Ozzvin/equinox/internal/api"

// addedMountsHere: folders mounted into a container are a Linux matter (see mounts_linux.go).
func addedMountsHere(string, []api.Place) []api.Place { return nil }
