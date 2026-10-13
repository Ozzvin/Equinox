//go:build !windows && !linux

// Command equinox is only available on Windows and Linux; use cmd/equinox-server elsewhere.
package main

import "fmt"

func main() { fmt.Println("equinox runs on Windows and Linux; run equinox-server instead") }
