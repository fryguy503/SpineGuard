//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func configureReaderProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
