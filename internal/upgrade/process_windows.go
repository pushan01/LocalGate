//go:build windows

package upgrade

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

func configureDetachedProcess(_ *exec.Cmd) {}

func stopPID(pid int, _ time.Duration) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func pidAlive(_ int) bool {
	return false
}
