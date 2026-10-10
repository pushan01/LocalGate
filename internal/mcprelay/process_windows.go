//go:build windows

package mcprelay

import (
	"os/exec"
	"strconv"
)

func configureProcessGroup(_ *exec.Cmd) {}

func signalProcessGroup(cmd *exec.Cmd, force bool) error {
	args := []string{"/PID", strconv.Itoa(cmd.Process.Pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	if err := exec.Command("taskkill.exe", args...).Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
