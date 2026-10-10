//go:build !windows

package shell

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessGroupOnContext(ctx context.Context, pgid int, processDone <-chan struct{}) {
	select {
	case <-processDone:
		return
	case <-ctx.Done():
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-processDone:
		return
	case <-timer.C:
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

func terminateProcess(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = exec.Command("kill", "-TERM", fmt.Sprintf("%d", pid)).Run()
	time.Sleep(200 * time.Millisecond)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = exec.Command("kill", "-KILL", fmt.Sprintf("%d", pid)).Run()
}

func isSessionOutputClosedError(err error) bool {
	return err == syscall.EIO
}
