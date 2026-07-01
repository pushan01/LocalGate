package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreAcquireActiveRejectsNonTerminalUpgrade(t *testing.T) {
	store := NewStore(t.TempDir())
	state, err := store.InitState(State{
		ID:              "u1",
		Status:          StatusUploaded,
		TargetBinary:    "/tmp/bin/localgate",
		CandidateBinary: store.CandidatePath("u1"),
		BackupBinary:    store.BackupPath("u1"),
		RunnerLogFile:   store.RunnerLogPath("u1"),
	})
	if err != nil {
		t.Fatalf("init state: %v", err)
	}
	if err := store.AcquireActive(state.ID); err != nil {
		t.Fatalf("acquire active: %v", err)
	}
	if err := store.AcquireActive("u2"); !errors.Is(err, ErrActiveUpgrade) {
		t.Fatalf("expected ErrActiveUpgrade, got %v", err)
	}
}

func TestStoreAcquireActiveClearsTerminalLock(t *testing.T) {
	store := NewStore(t.TempDir())
	state, err := store.InitState(State{
		ID:              "u1",
		Status:          StatusSucceeded,
		TargetBinary:    "/tmp/bin/localgate",
		CandidateBinary: store.CandidatePath("u1"),
		BackupBinary:    store.BackupPath("u1"),
		RunnerLogFile:   store.RunnerLogPath("u1"),
	})
	if err != nil {
		t.Fatalf("init state: %v", err)
	}
	lockPath := store.ActiveLockPath()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte(state.ID), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := store.AcquireActive("u2"); err != nil {
		t.Fatalf("acquire active should clear stale terminal lock: %v", err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if string(data) != "u2" {
		t.Fatalf("unexpected lock contents: %q", string(data))
	}
}
