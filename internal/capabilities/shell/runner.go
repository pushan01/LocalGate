package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/security"
)

type Runner struct {
	WS config.Workspace
}

func (r Runner) Run(ctx context.Context, taskID string, command string, cwd string, env map[string]string, timeout time.Duration, sink *core.EventSink) (int, error) {
	if d := security.ValidateCommand(command, r.WS); !d.Allowed {
		return -1, errors.New("command not allowed: " + d.Reason)
	}
	resolvedCWD, err := resolveWorkdir(cwd, r.WS)
	if err != nil {
		return -1, err
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.Command("/bin/sh", "-c", command)
	if resolvedCWD != "" {
		cmd.Dir = resolvedCWD
	}
	cmd.Env = mergeEnvAllowed(env, r.WS.EnvAllowlist)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	pgid := cmd.Process.Pid
	go func() {
		<-execCtx.Done()
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream(execCtx, sink, taskID, "stdout", stdout)
	}()
	go func() {
		defer wg.Done()
		stream(execCtx, sink, taskID, "stderr", stderr)
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	exit := exitCode(waitErr)
	if waitErr != nil {
		// context cancellation/timeout can also manifest as error here
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return exit, context.DeadlineExceeded
		}
		if errors.Is(execCtx.Err(), context.Canceled) {
			return exit, context.Canceled
		}
		return exit, waitErr
	}
	return exit, nil
}

func (r Runner) RunArgv(ctx context.Context, taskID string, argv []string, cwd string, env map[string]string, timeout time.Duration, sink *core.EventSink) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("argv empty")
	}
	// Allowlist by first token
	if d := security.ValidateCommand(argv[0], r.WS); !d.Allowed {
		return -1, errors.New("command not allowed: " + d.Reason)
	}
	for _, s := range argv {
		if strings.ContainsAny(s, ";\n\r`$|&") {
			return -1, errors.New("argv contains shell meta")
		}
	}
	resolvedCWD, err := resolveWorkdir(cwd, r.WS)
	if err != nil {
		return -1, err
	}
	execCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(execCtx, argv[0], argv[1:]...)
	if resolvedCWD != "" {
		cmd.Dir = resolvedCWD
	}
	cmd.Env = mergeEnvAllowed(env, r.WS.EnvAllowlist)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	pgid := cmd.Process.Pid
	go func() {
		<-execCtx.Done()
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stream(execCtx, sink, taskID, "stdout", stdout)
	}()
	go func() {
		defer wg.Done()
		stream(execCtx, sink, taskID, "stderr", stderr)
	}()
	waitErr := cmd.Wait()
	wg.Wait()
	exit := exitCode(waitErr)
	if waitErr != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return exit, context.DeadlineExceeded
		}
		if errors.Is(execCtx.Err(), context.Canceled) {
			return exit, context.Canceled
		}
		return exit, waitErr
	}
	return exit, nil
}
func stream(ctx context.Context, sink *core.EventSink, taskID, streamName string, r io.Reader) {
	if sink == nil || taskID == "" {
		io.Copy(io.Discard, r)
		return
	}
	sc := bufio.NewScanner(r)
	// allow longer lines
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		sink.Append(core.Event{
			TaskID: taskID,
			Type:   core.EventType("Task" + capitalize(streamName)),
			Time:   time.Now().UTC(),
			Stream: streamName,
			Data:   sc.Text(),
		})
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-32) + s[1:]
	}
	return s
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return -1
	}
	return -1
}

func mergeEnvAllowed(env map[string]string, allow []string) []string {
	allowed := make(map[string]struct{}, len(allow))
	for _, k := range allow {
		allowed[k] = struct{}{}
	}
	base := map[string]string{}
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		if _, ok := allowed[k]; ok {
			base[k] = kv[i+1:]
		}
	}
	for k, v := range env {
		if _, ok := allowed[k]; ok {
			base[k] = v
		}
	}
	keys := make([]string, 0, len(base))
	for k := range base {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v := base[k]
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}
