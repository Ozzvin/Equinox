//go:build !linux

package app

import "github.com/Ozzvin/equinox/internal/api"

// withAddedMounts: folders mounted into a container are a Linux matter (see mounts_linux.go).
func withAddedMounts(_ string, places []api.Place) []api.Place { return places }
