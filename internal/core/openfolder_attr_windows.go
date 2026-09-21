//go:build windows

package core

import "syscall"

// explorerAttr passes the command line through untouched, because Explorer parses it itself.
func explorerAttr(cmdline string) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CmdLine: "explorer.exe " + cmdline}
}
