//go:build windows

package shell

import (
	"context"
	"os"
	"os/exec"
	"strconv"
)

func configureProcessGroup(_ *exec.Cmd) {}

func terminateProcessGroupOnContext(ctx context.Context, pid int, processDone <-chan struct{}) {
	select {
	case <-processDone:
		return
	case <-ctx.Done():
	}
	terminateProcess(pid)
}

func terminateProcess(pid int) {
	if pid <= 0 {
		return
	}
	if err := exec.Command("taskkill.exe", "/PID", strconv.Itoa(pid), "/T", "/F").Run(); err == nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err == nil {
		_ = proc.Kill()
	}
}

func isSessionOutputClosedError(error) bool {
	return false
}
