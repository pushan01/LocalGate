//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package processutil

import (
	"os/exec"
	"syscall"
	"time"
)

const defaultWaitDelay = 2 * time.Second

// ConfigureCommand isolates a provider command in its own process group so a
// context cancellation can terminate the whole subprocess tree, not only the
// direct opencli process. This matters for Node/browser helpers that may inherit
// stdout/stderr and otherwise keep Cmd.Wait blocked after the parent exits.
func ConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = defaultWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
}
