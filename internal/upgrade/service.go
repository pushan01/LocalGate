package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"localgate/internal/config"
)

var ErrActiveUpgrade = errors.New("active upgrade exists")

type Service struct {
	store       *Store
	token       string
	runtime     config.Runtime
	executable  string
	now         func() time.Time
	startRunner func(statePath, runnerLog string) error
}

func NewService(baseDir, token string, runtime config.Runtime) *Service {
	executable := strings.TrimSpace(runtime.Bin)
	if executable == "" {
		if path, err := os.Executable(); err == nil {
			executable = path
		}
	}
	svc := &Service{
		store:      NewStore(baseDir),
		token:      strings.TrimSpace(token),
		runtime:    runtime,
		executable: executable,
		now:        func() time.Time { return time.Now().UTC() },
	}
	svc.startRunner = svc.startRunnerProcess
	return svc
}

func (s *Service) Token() string {
	return s.token
}

func (s *Service) Enabled() bool {
	return s.token != ""
}

func (s *Service) RuntimeMode() string {
	return strings.TrimSpace(s.runtime.Mode)
}

func (s *Service) Get(id string) (State, error) {
	return s.store.Get(id)
}

func (s *Service) Create(reader io.Reader, filename string) (State, error) {
	if reader == nil {
		return State{}, errors.New("upgrade reader required")
	}
	id := newID()
	if err := s.store.AcquireActive(id); err != nil {
		return State{}, err
	}
	state, err := s.store.InitState(State{
		ID:               id,
		Status:           StatusUploaded,
		Message:          "upload received",
		OriginalFilename: filename,
		TargetBinary:     s.targetBinary(),
		CandidateBinary:  s.store.CandidatePath(id),
		BackupBinary:     s.store.BackupPath(id),
		RunnerLogFile:    s.store.RunnerLogPath(id),
		OldPID:           os.Getpid(),
		Runtime:          s.runtime,
		StartArgs:        append([]string(nil), os.Args[1:]...),
	})
	if err != nil {
		_ = s.store.ReleaseActive(id)
		return State{}, err
	}

	out, err := os.OpenFile(state.CandidateBinary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		_ = s.failState(state.ID, "failed to create candidate", err)
		return State{}, err
	}
	defer out.Close()
	written, err := io.Copy(out, reader)
	if err != nil {
		_ = s.failState(state.ID, "failed to write candidate", err)
		return State{}, err
	}
	if written == 0 {
		_ = s.failState(state.ID, "candidate file is empty", errors.New("empty candidate file"))
		return State{}, errors.New("empty candidate file")
	}

	state, err = s.store.Update(state.ID, func(st *State) error {
		st.Message = "candidate file staged"
		st.VersionBefore = currentVersion(st.TargetBinary)
		return nil
	})
	if err != nil {
		_ = s.store.ReleaseActive(state.ID)
		return State{}, err
	}
	return state, nil
}

func (s *Service) Dispatch(ctx context.Context, id, taskID string) (map[string]any, error) {
	if s.RuntimeMode() != "http_daemon" {
		return nil, errors.New("upgrade only supported for http daemon mode")
	}
	st, err := s.store.Update(id, func(st *State) error {
		st.TaskID = taskID
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := os.Chmod(st.CandidateBinary, 0o755); err != nil {
		_ = s.failState(id, "failed to chmod candidate", err)
		return nil, err
	}
	versionAfter, err := binaryVersion(st.CandidateBinary)
	if err != nil {
		_ = s.failState(id, "candidate validation failed", err)
		return nil, err
	}

	st, err = s.store.Update(id, func(st *State) error {
		st.Status = StatusValidated
		st.Message = "candidate validated"
		st.VersionAfter = versionAfter
		return nil
	})
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		_ = s.failState(id, "upgrade canceled before runner handoff", ctx.Err())
		return nil, ctx.Err()
	default:
	}

	if err := s.startRunner(s.store.StatePath(id), st.RunnerLogFile); err != nil {
		_ = s.failState(id, "failed to start upgrade runner", err)
		return nil, err
	}

	st, err = s.store.Update(id, func(st *State) error {
		st.Status = StatusHandoffStarted
		st.Message = "upgrade runner started"
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"upgrade_id": st.ID,
		"status":     st.Status,
		"task_id":    st.TaskID,
	}, nil
}

func (s *Service) RunFromStatePath(statePath string) error {
	st, err := s.store.GetByPath(statePath)
	if err != nil {
		return err
	}

	if err := s.updateStatus(st.ID, StatusStoppingOld, "stopping current daemon"); err != nil {
		return err
	}
	_ = stopPID(st.OldPID, 10*time.Second)

	if err := s.updateStatus(st.ID, StatusReplacingBinary, "replacing target binary"); err != nil {
		return err
	}
	if err := replaceBinary(st.TargetBinary, st.BackupBinary, st.CandidateBinary); err != nil {
		_ = s.failAndRelease(st.ID, "failed to replace target binary", err)
		return err
	}

	if err := s.updateStatus(st.ID, StatusStartingNew, "starting new daemon"); err != nil {
		return err
	}
	newPID, err := s.startDaemon(st)
	if err != nil {
		return s.rollback(st.ID, 0, fmt.Errorf("failed to start new daemon: %w", err))
	}
	if _, err := s.store.Update(st.ID, func(state *State) error {
		state.NewPID = newPID
		return nil
	}); err != nil {
		return err
	}

	if err := s.updateStatus(st.ID, StatusHealthChecking, "waiting for new daemon to accept connections"); err != nil {
		return err
	}
	if err := waitForRuntimeBinds(st.Runtime, 15*time.Second); err != nil {
		return s.rollback(st.ID, newPID, fmt.Errorf("new daemon health check failed: %w", err))
	}

	_, err = s.store.Update(st.ID, func(state *State) error {
		state.Status = StatusSucceeded
		state.Message = "upgrade completed successfully"
		return nil
	})
	_ = s.store.ReleaseActive(st.ID)
	return err
}

func (s *Service) rollback(id string, newPID int, cause error) error {
	_, _ = s.store.Update(id, func(st *State) error {
		st.Status = StatusRollbackStarted
		st.Message = cause.Error()
		return nil
	})
	if newPID > 0 {
		_ = stopPID(newPID, 5*time.Second)
	}
	st, err := s.store.Get(id)
	if err != nil {
		return err
	}
	if err := restoreBinary(st.TargetBinary, st.BackupBinary); err != nil {
		return s.failAndRelease(id, "rollback restore failed", err)
	}
	oldPID, err := s.startDaemon(st)
	if err != nil {
		return s.failAndRelease(id, "rollback restart failed", err)
	}
	if err := waitForRuntimeBinds(st.Runtime, 15*time.Second); err != nil {
		return s.failAndRelease(id, "rollback health check failed", err)
	}
	_, err = s.store.Update(id, func(state *State) error {
		state.Status = StatusRolledBack
		state.Message = cause.Error()
		state.NewPID = oldPID
		return nil
	})
	_ = s.store.ReleaseActive(id)
	return err
}

func (s *Service) failAndRelease(id, msg string, err error) error {
	_ = s.failState(id, msg, err)
	_ = s.store.ReleaseActive(id)
	return err
}

func (s *Service) failState(id, msg string, err error) error {
	_, updateErr := s.store.Update(id, func(st *State) error {
		st.Status = StatusFailed
		if err != nil {
			st.Message = fmt.Sprintf("%s: %v", msg, err)
		} else {
			st.Message = msg
		}
		return nil
	})
	if updateErr != nil {
		return updateErr
	}
	return s.store.ReleaseActive(id)
}

func (s *Service) updateStatus(id string, status Status, msg string) error {
	_, err := s.store.Update(id, func(st *State) error {
		st.Status = status
		st.Message = msg
		return nil
	})
	return err
}

func (s *Service) startRunnerProcess(statePath, runnerLog string) error {
	if s.executable == "" {
		return errors.New("runner executable path empty")
	}
	if err := os.MkdirAll(filepath.Dir(runnerLog), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(runnerLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(s.executable, "-upgrade-runner", "-upgrade-state", statePath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureDetachedProcess(cmd)
	return cmd.Start()
}

func (s *Service) startDaemon(st State) (int, error) {
	target := st.TargetBinary
	if target == "" {
		return 0, errors.New("target binary empty")
	}
	if err := os.MkdirAll(filepath.Dir(st.Runtime.LogFile), 0o755); err != nil && st.Runtime.LogFile != "" {
		return 0, err
	}
	var logFile *os.File
	var err error
	if st.Runtime.LogFile != "" {
		logFile, err = os.OpenFile(st.Runtime.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		defer logFile.Close()
	}

	cmd := exec.Command(target, st.StartArgs...)
	configureDetachedProcess(cmd)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.Env = runtimeEnv(st.Runtime)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	if st.Runtime.PIDFile != "" {
		if err := os.MkdirAll(filepath.Dir(st.Runtime.PIDFile), 0o755); err == nil {
			_ = os.WriteFile(st.Runtime.PIDFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0o644)
		}
	}
	return cmd.Process.Pid, nil
}

func (s *Service) targetBinary() string {
	if s.runtime.Bin != "" {
		return s.runtime.Bin
	}
	return s.executable
}

func runtimeEnv(rt config.Runtime) []string {
	env := os.Environ()
	pairs := map[string]string{
		"LOCALGATE_RUNTIME_MODE":                    rt.Mode,
		"LOCALGATE_RUNTIME_BIN":                     rt.Bin,
		"LOCALGATE_RUNTIME_CONFIG":                  rt.ConfigPath,
		"LOCALGATE_RUNTIME_BIND":                    rt.Bind,
		"LOCALGATE_RUNTIME_ADMIN_BIND":              rt.AdminBind,
		"LOCALGATE_RUNTIME_PUBLIC_DEPLOYMENT_BIND":  rt.PublicDeploymentBind,
		"LOCALGATE_RUNTIME_PRIVATE_DEPLOYMENT_BIND": rt.PrivateDeploymentBind,
		"LOCALGATE_RUNTIME_PID_FILE":                rt.PIDFile,
		"LOCALGATE_RUNTIME_LOG_FILE":                rt.LogFile,
		"LOCALGATE_RUNTIME_RUN_DIR":                 rt.RunDir,
	}
	for k, v := range pairs {
		env = append(env, k+"="+v)
	}
	return env
}

func currentVersion(path string) string {
	version, err := binaryVersion(path)
	if err != nil {
		return ""
	}
	return version
}

func binaryVersion(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("binary path empty")
	}
	out, err := exec.Command(path, "-version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func replaceBinary(target, backup, candidate string) error {
	if target == "" || backup == "" || candidate == "" {
		return errors.New("binary paths must not be empty")
	}
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(candidate, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	return nil
}

func restoreBinary(target, backup string) error {
	if target == "" || backup == "" {
		return errors.New("binary paths must not be empty")
	}
	_ = os.Remove(target)
	if err := os.Rename(backup, target); err != nil {
		return err
	}
	return nil
}

func waitForRuntimeBinds(runtime config.Runtime, timeout time.Duration) error {
	if err := waitForBind(runtime.Bind, timeout); err != nil {
		return fmt.Errorf("control bind %s: %w", runtime.Bind, err)
	}
	if strings.TrimSpace(runtime.AdminBind) != "" {
		if err := waitForBind(runtime.AdminBind, timeout); err != nil {
			return fmt.Errorf("admin bind %s: %w", runtime.AdminBind, err)
		}
	}
	if strings.TrimSpace(runtime.PublicDeploymentBind) != "" {
		if err := waitForBind(runtime.PublicDeploymentBind, timeout); err != nil {
			return fmt.Errorf("public deployment bind %s: %w", runtime.PublicDeploymentBind, err)
		}
	}
	if strings.TrimSpace(runtime.PrivateDeploymentBind) != "" {
		if err := waitForBind(runtime.PrivateDeploymentBind, timeout); err != nil {
			return fmt.Errorf("private deployment bind %s: %w", runtime.PrivateDeploymentBind, err)
		}
	}
	return nil
}

func waitForBind(bind string, timeout time.Duration) error {
	address := normalizeBind(bind)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(300 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("bind probe timeout")
	}
	return lastErr
}

func normalizeBind(bind string) string {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return bind
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
