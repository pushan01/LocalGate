package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/security"
)

type SessionRunner struct {
	WS       config.Workspace
	Sessions *core.SessionManager
}

func (r SessionRunner) StartSession(ctx context.Context, requester, command, cwd string, env map[string]string, shell string, login, tty bool) (core.Session, error) {
	return r.StartSessionWithInvokeContext(ctx, core.InvokeContext{WorkspaceID: r.WS.ID, Requester: requester}, command, cwd, env, shell, login, tty)
}

func (r SessionRunner) StartSessionWithInvokeContext(ctx context.Context, ic core.InvokeContext, command, cwd string, env map[string]string, shell string, login, tty bool) (core.Session, error) {
	if d := security.ValidateCommand(command, r.WS); !d.Allowed {
		return core.Session{}, errors.New("command not allowed: " + d.Reason)
	}
	resolvedCWD, err := resolveWorkdir(cwd, r.WS)
	if err != nil {
		return core.Session{}, err
	}
	shPath, shArgs, err := shellCommand(shell, login, command)
	if err != nil {
		return core.Session{}, err
	}
	execCmd := exec.CommandContext(ctx, shPath, shArgs...)
	execCmd.Dir = resolvedCWD
	execCmd.Env = mergeEnvAllowed(env, r.WS.EnvAllowlist)
	if tty {
		return r.startTTYSession(execCmd, ic, command, resolvedCWD, shPath, login)
	}
	execCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return core.Session{}, err
	}
	stderr, err := execCmd.StderrPipe()
	if err != nil {
		return core.Session{}, err
	}
	stdin, err := execCmd.StdinPipe()
	if err != nil {
		return core.Session{}, err
	}

	session := r.Sessions.Create(core.Session{
		TaskID:          ic.TaskID,
		Requester:       ic.Requester,
		WorkspaceID:     r.WS.ID,
		Transport:       ic.Transport,
		MCPSessionID:    ic.MCPSessionID,
		SessionMode:     ic.SessionMode,
		SessionIDSource: ic.SessionIDSource,
		RequestID:       ic.RequestID,
		Command:         command,
		CWD:             resolvedCWD,
		Shell:           shPath,
		Login:           login,
		TTY:             tty,
	}, core.SessionController{Stdin: stdin})

	if err := execCmd.Start(); err != nil {
		_ = stdin.Close()
		_ = r.Sessions.Finish(session.ID, -1, err)
		return core.Session{}, err
	}
	if err := r.Sessions.Start(session.ID); err != nil {
		return core.Session{}, err
	}
	pgid := execCmd.Process.Pid
	if err := r.Sessions.SetController(session.ID, core.SessionController{
		Stdin: stdin,
		Close: func() error {
			terminateProcess(pgid)
			_ = stdin.Close()
			return nil
		},
	}); err != nil {
		return core.Session{}, err
	}

	go r.streamSessionOutput(session.ID, "stdout", stdout)
	go r.streamSessionOutput(session.ID, "stderr", stderr)
	go func() {
		waitErr := execCmd.Wait()
		_ = stdin.Close()
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

func (r SessionRunner) startTTYSession(execCmd *exec.Cmd, ic core.InvokeContext, command, cwd, shell string, login bool) (core.Session, error) {
	ptyFile, err := pty.Start(execCmd)
	if err != nil {
		return core.Session{}, err
	}
	session := r.Sessions.Create(core.Session{
		TaskID:          ic.TaskID,
		Requester:       ic.Requester,
		WorkspaceID:     r.WS.ID,
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
	go r.streamSessionOutput(session.ID, "stdout", ptyFile)
	go func() {
		waitErr := execCmd.Wait()
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

func (r SessionRunner) WriteStdin(sessionID, chars string, wait time.Duration, maxItems int) (core.Session, core.SessionReadResult, error) {
	return r.WriteStdinForWorkspace(sessionID, r.WS.ID, chars, 0, wait, maxItems)
}

func (r SessionRunner) WriteStdinForWorkspace(sessionID, workspaceID, chars string, cursor int, wait time.Duration, maxItems int) (core.Session, core.SessionReadResult, error) {
	if err := r.Sessions.WriteStdinForWorkspace(sessionID, workspaceID, chars); err != nil {
		return core.Session{}, core.SessionReadResult{}, err
	}
	if wait > 0 {
		time.Sleep(wait)
	}
	read, err := r.Sessions.ReadForWorkspace(sessionID, workspaceID, cursor, maxItems)
	if err != nil {
		return core.Session{}, core.SessionReadResult{}, err
	}
	s, err := r.Sessions.GetForWorkspace(sessionID, workspaceID)
	if err != nil {
		return core.Session{}, core.SessionReadResult{}, err
	}
	return s, read, nil
}

func (r SessionRunner) ReadSession(sessionID string, maxItems int) (core.Session, core.SessionReadResult, error) {
	return r.ReadSessionForWorkspace(sessionID, r.WS.ID, 0, maxItems)
}

func (r SessionRunner) ReadSessionForWorkspace(sessionID, workspaceID string, cursor, maxItems int) (core.Session, core.SessionReadResult, error) {
	read, err := r.Sessions.ReadForWorkspace(sessionID, workspaceID, cursor, maxItems)
	if err != nil {
		return core.Session{}, core.SessionReadResult{}, err
	}
	s, err := r.Sessions.GetForWorkspace(sessionID, workspaceID)
	if err != nil {
		return core.Session{}, core.SessionReadResult{}, err
	}
	return s, read, nil
}

func (r SessionRunner) CloseSession(sessionID string) (core.Session, error) {
	return r.Sessions.CloseForWorkspace(sessionID, r.WS.ID)
}

func (r SessionRunner) streamSessionOutput(sessionID, stream string, src io.Reader) {
	reader := bufio.NewReaderSize(src, 32*1024)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			continues := errors.Is(err, bufio.ErrBufferFull)
			if !continues && fragment[len(fragment)-1] == '\n' {
				fragment = fragment[:len(fragment)-1]
				if len(fragment) > 0 && fragment[len(fragment)-1] == '\r' {
					fragment = fragment[:len(fragment)-1]
				}
			}
			_ = r.Sessions.AppendOutputChunk(sessionID, stream, string(fragment), continues)
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return
		default:
			_ = r.Sessions.SetOutputError(sessionID, stream, err)
			return
		}
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

func resolveWorkdir(cwd string, ws config.Workspace) (string, error) {
	if cwd == "" {
		cwd = ws.CWD
	}
	if cwd == "" {
		return "", nil
	}
	dec := security.ValidatePath(cwd, ws, security.Options{FollowSymlink: true})
	if !dec.Allowed {
		return "", errors.New("workdir not allowed: " + dec.Reason)
	}
	return dec.Path, nil
}

func shellCommand(shell string, login bool, command string) (string, []string, error) {
	if strings.TrimSpace(command) == "" {
		return "", nil, errors.New("cmd required")
	}
	resolvedShell, err := resolveTrustedShell(shell)
	if err != nil {
		return "", nil, err
	}
	switch filepath.Base(resolvedShell) {
	case "sh":
		if login {
			return "", nil, fmt.Errorf("login shell not supported for %s", resolvedShell)
		}
		return resolvedShell, []string{"-c", command}, nil
	case "bash", "zsh":
		if login {
			return resolvedShell, []string{"-lc", command}, nil
		}
		return resolvedShell, []string{"-c", command}, nil
	default:
		return "", nil, fmt.Errorf("unsupported shell: %s", resolvedShell)
	}
}

func resolveTrustedShell(shell string) (string, error) {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		shell = "/bin/sh"
	}
	if !filepath.IsAbs(shell) {
		return "", fmt.Errorf("shell must be an absolute trusted path: %s", shell)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(shell))
	if err != nil {
		return "", fmt.Errorf("resolve shell %s: %w", shell, err)
	}
	trusted := []string{
		"/bin/sh", "/bin/bash", "/bin/zsh",
		"/usr/bin/sh", "/usr/bin/bash", "/usr/bin/zsh",
	}
	for _, candidate := range trusted {
		candidateResolved, candidateErr := filepath.EvalSymlinks(candidate)
		if candidateErr == nil && resolved == candidateResolved {
			return candidateResolved, nil
		}
	}
	return "", fmt.Errorf("shell is not a trusted system shell: %s", shell)
}
