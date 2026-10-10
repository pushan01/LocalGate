//go:build windows

package shell

import (
	"errors"
	"os/exec"

	"localgate/internal/core"
)

func (r SessionRunner) startTTYSession(_ *exec.Cmd, _ core.InvokeContext, _, _, _ string, _ bool) (core.Session, error) {
	return core.Session{}, errors.New("tty sessions are not supported on windows; use tty=false")
}
