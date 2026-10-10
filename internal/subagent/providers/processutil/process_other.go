//go:build !windows && !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package processutil

import (
	"os/exec"
	"time"
)

const defaultWaitDelay = 2 * time.Second

// ConfigureCommand keeps a bounded Wait on platforms where LocalGate does not
// currently provide process-group termination. exec.CommandContext's default
// direct-process cancellation remains in effect.
func ConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.WaitDelay = defaultWaitDelay
}
