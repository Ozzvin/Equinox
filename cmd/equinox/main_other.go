//go:build !windows

// Command equinox is only available on Windows; use cmd/equinox elsewhere.
package main

import "fmt"

func main() { fmt.Println("equinox is Windows-only; run cmd/equinox instead") }
