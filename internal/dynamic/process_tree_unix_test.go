//go:build !windows

package dynamic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestDeleteTerminatesManagedProcessTree(t *testing.T) {
	service, source, executable := newDynamicTestService(t, filepath.Join(t.TempDir(), "managed"))
	t.Cleanup(func() { _ = service.Close() })
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	service.config.EnvAllowlist = append(service.config.EnvAllowlist, "LOCALGATE_DYNAMIC_CHILD_PID_FILE")
	_, err := service.Deploy(context.Background(), DeployRequest{
		Name: "tree", Source: source,
		Runtime: RuntimeSpec{Argv: []string{executable, "-test.run=^TestDynamicHelperProcess$"}, Env: map[string]string{
			"LOCALGATE_DYNAMIC_HELPER": "1", "LOCALGATE_DYNAMIC_CHILD_PID_FILE": pidFile,
		}},
		Options: DeployOptions{PreserveSource: true, HealthPath: "/healthz", RestartPolicy: "never"},
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			childPID, readErr = strconv.Atoi(string(data))
			if readErr == nil && childPID > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("child pid was not reported: %v", readErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := service.Delete(context.Background(), "tree"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deadline = time.Now().Add(4 * time.Second)
	for {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child process %d survived process-group termination: %v", childPID, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
