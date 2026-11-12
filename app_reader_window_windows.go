//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

func configureReaderProcess(command *exec.Cmd) {
	// A reader is an interactive GUI application and must remain available when
	// its library window closes. Never hide its native window or add a shell.
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
