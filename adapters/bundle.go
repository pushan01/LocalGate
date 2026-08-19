package adapters

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed localgate-adapter.mjs
var runner []byte

// Materialize writes the bundled LocalGate browser adapter runtime into base and
// returns an executable path. The content hash keeps different LocalGate builds
// isolated while making repeated startup idempotent.
func Materialize(base string) (string, error) {
	if len(runner) == 0 {
		return "", fmt.Errorf("bundled LocalGate adapter is empty")
	}
	if base == "" {
		return "", fmt.Errorf("adapter runtime base directory is required")
	}
	sum := sha256.Sum256(runner)
	version := hex.EncodeToString(sum[:8])
	dir := filepath.Join(base, "adapters", version)
	path := filepath.Join(dir, "localgate-adapter.mjs")
	if payload, err := os.ReadFile(path); err == nil && bytes.Equal(payload, runner) {
		if chmodErr := os.Chmod(path, 0o700); chmodErr != nil {
			return "", fmt.Errorf("chmod bundled LocalGate adapter: %w", chmodErr)
		}
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create adapter runtime directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".localgate-adapter-*.mjs")
	if err != nil {
		return "", fmt.Errorf("create adapter runtime temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o700); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod adapter runtime temp file: %w", err)
	}
	if _, err := tmp.Write(runner); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write adapter runtime: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync adapter runtime: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close adapter runtime: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("install adapter runtime: %w", err)
	}
	return path, nil
}
