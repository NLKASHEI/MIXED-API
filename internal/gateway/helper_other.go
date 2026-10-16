//go:build !windows

package gateway

import "os/exec"

func hideHelperWindow(cmd *exec.Cmd) {}
