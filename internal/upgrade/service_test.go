package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"
)

func TestServiceCreateAndDispatch(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "bin", "localgate")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	svc := NewService(filepath.Join(tmp, "upgrades"), "upgrade-token", config.Runtime{
		Mode: "http_daemon",
		Bin:  target,
		Bind: "127.0.0.1:18000",
	})
	svc.startRunner = func(statePath, runnerLog string) error {
		return os.WriteFile(runnerLog, []byte(statePath), 0o644)
	}

	state, err := svc.Create(strings.NewReader("#!/bin/sh\necho 1.2.3\n"), "localgate")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if state.Status != StatusUploaded {
		t.Fatalf("unexpected status: %s", state.Status)
	}
	if _, err := os.Stat(state.CandidateBinary); err != nil {
		t.Fatalf("candidate not created: %v", err)
	}

	res, err := svc.Dispatch(context.Background(), state.ID, "task-1")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res["upgrade_id"] != state.ID {
		t.Fatalf("unexpected dispatch result: %#v", res)
	}

	stored, err := svc.Get(state.ID)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if stored.Status != StatusHandoffStarted {
		t.Fatalf("unexpected stored status: %#v", stored)
	}
	if stored.TaskID != "task-1" {
		t.Fatalf("unexpected task id: %#v", stored)
	}
	if stored.VersionAfter != "1.2.3" {
		t.Fatalf("unexpected version after: %#v", stored)
	}
}

func TestServiceRejectsActiveUpgrade(t *testing.T) {
	tmp := t.TempDir()
	svc := NewService(filepath.Join(tmp, "upgrades"), "upgrade-token", config.Runtime{
		Mode: "http_daemon",
		Bin:  "/tmp/localgate",
	})
	if _, err := svc.Create(strings.NewReader("first"), "first"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.Create(strings.NewReader("second"), "second"); err != ErrActiveUpgrade {
		t.Fatalf("expected ErrActiveUpgrade, got %v", err)
	}
}

func TestRuntimeEnvIncludesServiceBinds(t *testing.T) {
	env := runtimeEnv(config.Runtime{
		Mode:                  "http_daemon",
		Bind:                  "127.0.0.1:8000",
		AdminBind:             "127.0.0.1:8002",
		PublicDeploymentBind:  "127.0.0.1:8001",
		PrivateDeploymentBind: "127.0.0.1:8003",
	})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "LOCALGATE_RUNTIME_BIND=127.0.0.1:8000") {
		t.Fatalf("control runtime bind missing: %s", joined)
	}
	if !strings.Contains(joined, "LOCALGATE_RUNTIME_PUBLIC_DEPLOYMENT_BIND=127.0.0.1:8001") {
		t.Fatalf("public deployment runtime bind missing: %s", joined)
	}
	if !strings.Contains(joined, "LOCALGATE_RUNTIME_PRIVATE_DEPLOYMENT_BIND=127.0.0.1:8003") {
		t.Fatalf("private deployment runtime bind missing: %s", joined)
	}
	if !strings.Contains(joined, "LOCALGATE_RUNTIME_ADMIN_BIND=127.0.0.1:8002") {
		t.Fatalf("admin runtime bind missing: %s", joined)
	}
}
