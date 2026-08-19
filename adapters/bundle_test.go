package adapters

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMaterializeBundledAdapter(t *testing.T) {
	base := t.TempDir()
	first, err := Materialize(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Materialize(base)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("materialized path changed: %q != %q", first, second)
	}
	if filepath.Base(first) != "localgate-adapter.mjs" {
		t.Fatalf("unexpected adapter filename %q", first)
	}
	payload, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) == 0 || string(payload[:2]) != "#!" {
		t.Fatalf("unexpected adapter payload prefix: %q", payload[:min(16, len(payload))])
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("materialized adapter is not executable: %v", info.Mode())
	}
}

func TestBundledAdapterJavaScriptParsesWhenNodeIsAvailable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	path, err := Materialize(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "--check", path).CombinedOutput()
	if err != nil {
		t.Fatalf("node --check failed: %v\n%s", err, output)
	}
}
