//go:build !windows

package core

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}

func bindToJob(int) {}

// KillStray 只在 Windows 上有意义。
func KillStray(string) int { return 0 }
