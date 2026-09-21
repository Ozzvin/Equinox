//go:build !windows

package api

import "strings"

const computerName = "Компьютер"

func drives() []fsPlace { return nil }

func hiddenEntry(_, name string) bool { return strings.HasPrefix(name, ".") }
