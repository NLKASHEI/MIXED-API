//go:build windows

package gateway

import (
	"os/exec"
	"syscall"
)

func hideHelperWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
