package shell

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/security"
)

type SessionRunner struct {
	WS       config.Root
	Sessions *core.SessionManager
}

func (r SessionRunner) StartSession(ctx context.Context, requester, command, cwd string, env map[string]string, shell string, login, tty bool) (core.Session, error) {
	return r.StartSessionWithInvokeContext(ctx, core.InvokeContext{Requester: requester}, command, cwd, env, shell, login, tty)
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
	stdout := newSessionOutputWriter(r.Sessions, "", "stdout")
	stderr := newSessionOutputWriter(r.Sessions, "", "stderr")
	execCmd.Stdout = stdout
	execCmd.Stderr = stderr
	stdin, err := execCmd.StdinPipe()
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
		CWD:             resolvedCWD,
		Shell:           shPath,
		Login:           login,
		TTY:             tty,
	}, core.SessionController{Stdin: stdin})
	stdout.sessionID = session.ID
	stderr.sessionID = session.ID

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

	go func() {
		waitErr := execCmd.Wait()
		_ = stdin.Close()
		flushErr := errors.Join(stdout.Flush(), stderr.Flush())
		if waitErr == nil && flushErr != nil {
			waitErr = flushErr
		}
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

func (r SessionRunner) WriteStdin(sessionID, chars string, wait time.Duration, maxItems int) (core.Session, core.SessionReadResult, error) {
	return r.WriteStdinContext(context.Background(), sessionID, chars, 0, wait, maxItems)
}

func (r SessionRunner) WriteStdinContext(ctx context.Context, sessionID, chars string, cursor int, wait time.Duration, maxItems int) (core.Session, core.SessionReadResult, error) {
	if err := r.Sessions.WriteStdinContext(ctx, sessionID, chars); err != nil {
		return core.Session{}, core.SessionReadResult{}, err
	}
	return r.Sessions.WaitRead(ctx, sessionID, cursor, maxItems, wait)
}

func (r SessionRunner) ReadSession(sessionID string, maxItems int) (core.Session, core.SessionReadResult, error) {
	return r.ReadSessionAt(sessionID, 0, maxItems)
}

func (r SessionRunner) ReadSessionAt(sessionID string, cursor, maxItems int) (core.Session, core.SessionReadResult, error) {
	return r.Sessions.ReadSnapshot(sessionID, cursor, maxItems)
}

func (r SessionRunner) WaitReadSession(ctx context.Context, sessionID string, cursor, maxItems int, wait time.Duration) (core.Session, core.SessionReadResult, error) {
	return r.Sessions.WaitRead(ctx, sessionID, cursor, maxItems, wait)
}

func (r SessionRunner) CloseSession(sessionID string) (core.Session, error) {
	return r.Sessions.Close(sessionID)
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
		case errors.Is(err, io.EOF), errors.Is(err, os.ErrClosed), errors.Is(err, syscall.EIO):
			return
		default:
			_ = r.Sessions.SetOutputError(sessionID, stream, err)
			return
		}
	}
}

const sessionOutputChunkBytes = 32 * 1024

type sessionOutputWriter struct {
	sessions  *core.SessionManager
	sessionID string
	stream    string
	mu        sync.Mutex
	pending   []byte
}

func newSessionOutputWriter(sessions *core.SessionManager, sessionID, stream string) *sessionOutputWriter {
	return &sessionOutputWriter{sessions: sessions, sessionID: sessionID, stream: stream}
}

func (w *sessionOutputWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	if err := w.emitReadyLocked(); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *sessionOutputWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	fragment := append([]byte(nil), w.pending...)
	w.pending = w.pending[:0]
	return w.sessions.AppendOutputChunk(w.sessionID, w.stream, string(fragment), false)
}

func (w *sessionOutputWriter) emitReadyLocked() error {
	for {
		if newline := bytes.IndexByte(w.pending, '\n'); newline >= 0 {
			line := append([]byte(nil), w.pending[:newline]...)
			w.pending = w.pending[newline+1:]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if err := w.emitLineLocked(line); err != nil {
				return err
			}
			continue
		}
		if len(w.pending) > sessionOutputChunkBytes {
			chunk := append([]byte(nil), w.pending[:sessionOutputChunkBytes]...)
			w.pending = w.pending[sessionOutputChunkBytes:]
			if err := w.sessions.AppendOutputChunk(w.sessionID, w.stream, string(chunk), true); err != nil {
				return err
			}
			continue
		}
		return nil
	}
}

func (w *sessionOutputWriter) emitLineLocked(line []byte) error {
	for len(line) > sessionOutputChunkBytes {
		if err := w.sessions.AppendOutputChunk(w.sessionID, w.stream, string(line[:sessionOutputChunkBytes]), true); err != nil {
			return err
		}
		line = line[sessionOutputChunkBytes:]
	}
	return w.sessions.AppendOutputChunk(w.sessionID, w.stream, string(line), false)
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

func resolveWorkdir(cwd string, ws config.Root) (string, error) {
	if cwd == "" {
		cwd = ws.Path
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
