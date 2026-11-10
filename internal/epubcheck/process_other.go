//go:build !windows

package epubcheck

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
