//go:build !windows

package shell

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"

	"localgate/internal/core"
)

func (r SessionRunner) startTTYSession(execCmd *exec.Cmd, ic core.InvokeContext, command, cwd, shell string, login bool) (core.Session, error) {
	ptyFile, err := pty.Start(execCmd)
	if err != nil {
		return core.Session{}, err
	}
	session := r.Sessions.Create(core.Session{
		TaskID:          ic.TaskID,
		Requester:       ic.Requester,
		Transport:       ic.Transport,
		MCPSessionID:    ic.MCPSessionID,
		SessionMode:     ic.SessionMode,
		SessionIDSource: ic.SessionIDSource,
		RequestID:       ic.RequestID,
		Command:         command,
		CWD:             cwd,
		Shell:           shell,
		Login:           login,
		TTY:             true,
	}, core.SessionController{Stdin: ptyFile})
	if err := r.Sessions.Start(session.ID); err != nil {
		_ = ptyFile.Close()
		return core.Session{}, err
	}
	pid := execCmd.Process.Pid
	if err := r.Sessions.SetController(session.ID, core.SessionController{
		Stdin: ptyFile,
		Close: func() error {
			terminateProcess(pid)
			if err := ptyFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				return err
			}
			return nil
		},
	}); err != nil {
		_ = ptyFile.Close()
		return core.Session{}, err
	}
	var outputWG sync.WaitGroup
	outputWG.Add(1)
	go func() {
		defer outputWG.Done()
		r.streamSessionOutput(session.ID, "stdout", ptyFile)
	}()
	go func() {
		waitErr := execCmd.Wait()
		outputWG.Wait()
		_ = ptyFile.Close()
		if waitErr != nil {
			if errors.Is(waitErr, context.Canceled) {
				_ = r.Sessions.Finish(session.ID, -1, waitErr)
				return
			}
			_ = r.Sessions.Finish(session.ID, exitCode(waitErr), waitErr)
			return
		}
		_ = r.Sessions.Finish(session.ID, 0, nil)
	}()
	return r.Sessions.Get(session.ID)
}
