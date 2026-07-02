package dynamic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type processExit struct {
	Code int
	Err  error
	At   time.Time
}

type managedProcess struct {
	cmd         *exec.Cmd
	releaseID   string
	port        int
	startedAt   time.Time
	done        chan struct{}
	intentional bool
	mu          sync.Mutex
	exit        processExit
}

func (p *managedProcess) markIntentional() {
	p.mu.Lock()
	p.intentional = true
	p.mu.Unlock()
}

func (p *managedProcess) wasIntentional() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.intentional
}

func (p *managedProcess) exitResult() processExit {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exit
}

type rotatingLogWriter struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	maxBytes int64
	maxFiles int
}

func openRotatingLogWriter(path string, maxBytes int64, maxFiles int) (*rotatingLogWriter, error) {
	if maxBytes <= 0 {
		return nil, errors.New("log size limit must be positive")
	}
	if maxFiles <= 0 {
		maxFiles = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	writer := &rotatingLogWriter{path: path, file: file, size: info.Size(), maxBytes: maxBytes, maxFiles: maxFiles}
	if writer.size >= writer.maxBytes {
		if err := writer.rotateLocked(); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	return writer, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	consumed := 0
	for len(p) > 0 {
		if w.file == nil {
			return consumed, os.ErrClosed
		}
		if w.size >= w.maxBytes {
			if err := w.rotateLocked(); err != nil {
				return consumed, err
			}
		}
		remaining := w.maxBytes - w.size
		chunk := p
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		n, err := w.file.Write(chunk)
		w.size += int64(n)
		consumed += n
		p = p[n:]
		if err != nil {
			return consumed, err
		}
		if n != len(chunk) {
			return consumed, io.ErrShortWrite
		}
	}
	return consumed, nil
}

func (w *rotatingLogWriter) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Sync(); err != nil {
			return err
		}
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}
	if w.maxFiles > 1 {
		_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.maxFiles-1))
		for index := w.maxFiles - 2; index >= 1; index-- {
			oldPath := fmt.Sprintf("%s.%d", w.path, index)
			newPath := fmt.Sprintf("%s.%d", w.path, index+1)
			if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		_ = os.Remove(w.path)
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0
	return nil
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func startManagedProcess(ctx context.Context, executable string, deployment Deployment, hostname, publicURL, releaseRoot string, port int, startupTimeout time.Duration, maxLogBytes int64, maxLogFiles int) (*managedProcess, error) {
	workingDir := filepath.Join(releaseRoot, filepath.FromSlash(deployment.WorkingDir))
	if !withinRoot(workingDir, releaseRoot) {
		return nil, errorf("deployment_start_failed", "working directory escaped release root")
	}
	info, err := os.Stat(workingDir)
	if err != nil || !info.IsDir() {
		return nil, errorf("deployment_start_failed", "working directory is invalid: %v", err)
	}
	logsDir := filepath.Dir(releaseRoot)
	stdoutPath := filepath.Join(logsDir, "stdout.log")
	stderrPath := filepath.Join(logsDir, "stderr.log")
	stdout, err := openRotatingLogWriter(stdoutPath, maxLogBytes, maxLogFiles)
	if err != nil {
		return nil, errorf("deployment_start_failed", "open stdout log: %v", err)
	}
	stderr, err := openRotatingLogWriter(stderrPath, maxLogBytes, maxLogFiles)
	if err != nil {
		_ = stdout.Close()
		return nil, errorf("deployment_start_failed", "open stderr log: %v", err)
	}

	args := append([]string(nil), deployment.Argv[1:]...)
	cmd := exec.Command(executable, args...)
	cmd.Dir = workingDir
	cmd.Env = processEnvironment(deployment, hostname, publicURL, port)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, errorf("deployment_start_failed", "start process: %v", err)
	}
	proc := &managedProcess{cmd: cmd, releaseID: deployment.ActiveRelease, port: port, startedAt: time.Now().UTC(), done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		proc.mu.Lock()
		proc.exit = processExit{Code: code, Err: err, At: time.Now().UTC()}
		proc.mu.Unlock()
		close(proc.done)
	}()

	if err := waitForHealth(ctx, proc, hostname, deployment.HealthPath, startupTimeout); err != nil {
		proc.markIntentional()
		_ = terminateProcess(proc, 2*time.Second)
		if bindConflictInLog(stderrPath) {
			return nil, errorf("deployment_port_allocation_failed", "application could not bind allocated port %d", port)
		}
		return nil, err
	}
	return proc, nil
}

func bindConflictInLog(path string) bool {
	data, _, err := readLogTail(path, 32<<10)
	if err != nil {
		return false
	}
	lower := strings.ToLower(data)
	for _, marker := range []string{"address already in use", "eaddrinuse", "only one usage of each socket address"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func processEnvironment(deployment Deployment, hostname, publicURL string, port int) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.TempDir(),
		"HOST=127.0.0.1",
		"PORT=" + strconv.Itoa(port),
		"LOCALGATE_APP_HOST=" + hostname,
		"LOCALGATE_PUBLIC_URL=" + publicURL,
		"LOCALGATE_BASE_PATH=/",
	}
	keys := make([]string, 0, len(deployment.Env))
	for key := range deployment.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+deployment.Env[key])
	}
	return env
}

func waitForHealth(ctx context.Context, proc *managedProcess, hostname, healthPath string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{
		Timeout: 800 * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", proc.port, healthPath)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-proc.done:
			exit := proc.exitResult()
			return errorf("deployment_start_failed", "process exited before becoming healthy: code=%d err=%v", exit.Code, exit.Err)
		case <-deadline.C:
			return errorf("deployment_health_failed", "health check timed out")
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			req.Host = hostname
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			_, _ = io.CopyN(io.Discard, resp.Body, 4096)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode <= 399 {
				return nil
			}
		}
	}
}

func terminateProcess(proc *managedProcess, timeout time.Duration) error {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return nil
	}
	proc.markIntentional()
	if err := signalProcessGroup(proc.cmd, false); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-proc.done:
		return nil
	case <-timer.C:
		if err := signalProcessGroup(proc.cmd, true); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		select {
		case <-proc.done:
			return nil
		case <-time.After(2 * time.Second):
			return errors.New("process did not exit after forced termination")
		}
	}
}

func readLogTail(path string, tailBytes int64) (string, bool, error) {
	if tailBytes <= 0 {
		tailBytes = 64 << 10
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	start := int64(0)
	truncated := false
	if info.Size() > tailBytes {
		start = info.Size() - tailBytes
		truncated = true
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", false, err
	}
	b, err := io.ReadAll(io.LimitReader(file, tailBytes))
	return strings.ToValidUTF8(string(b), "�"), truncated, err
}
