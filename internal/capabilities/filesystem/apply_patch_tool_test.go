package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/config"
	"localgate/internal/core"
)

func TestApplyPatchAddUpdateDelete(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(existing, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	removeTarget := filepath.Join(dir, "remove.txt")
	if err := os.WriteFile(removeTarget, []byte("bye\n"), 0o644); err != nil {
		t.Fatalf("write remove fixture: %v", err)
	}

	res, err := mustApplyPatchTool(t, dir).Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Add File: new.txt",
			"+new file",
			"*** Update File: a.txt",
			"@@",
			"-hello",
			"+world",
			"*** Delete File: remove.txt",
			"*** End Patch",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	if got := readFileText(t, filepath.Join(dir, "new.txt")); got != "new file\n" {
		t.Fatalf("unexpected new.txt: %q", got)
	}
	if got := readFileText(t, existing); got != "world\n" {
		t.Fatalf("unexpected updated file: %q", got)
	}
	if _, err := os.Stat(removeTarget); !os.IsNotExist(err) {
		t.Fatalf("expected remove.txt to be deleted, err=%v", err)
	}
	if res["added"] != 1 || res["updated"] != 1 || res["deleted"] != 1 {
		t.Fatalf("unexpected result summary: %#v", res)
	}
}

func TestApplyPatchAtomicOnMismatch(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(b, []byte("two\n"), 0o644); err != nil {
		t.Fatalf("write b: %v", err)
	}

	_, err := mustApplyPatchTool(t, dir).Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: a.txt",
			"@@",
			"-one",
			"+ONE",
			"*** Update File: b.txt",
			"@@",
			"-missing",
			"+TWO",
			"*** End Patch",
		}, "\n"),
	})
	if err == nil {
		t.Fatalf("expected patch failure")
	}
	if got := readFileText(t, a); got != "one\n" {
		t.Fatalf("a.txt should be unchanged, got %q", got)
	}
	if got := readFileText(t, b); got != "two\n" {
		t.Fatalf("b.txt should be unchanged, got %q", got)
	}
}

func TestApplyPatchPreservesExecutableMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(target, []byte("echo old\n"), 0o751); err != nil {
		t.Fatalf("write script: %v", err)
	}
	_, err := mustApplyPatchTool(t, dir).Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: script.sh",
			"@@",
			"-echo old",
			"+echo new",
			"*** End Patch",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o751 {
		t.Fatalf("executable mode changed: %o", info.Mode().Perm())
	}
}

func TestApplyPatchAddDoesNotOverwriteConcurrentCreate(t *testing.T) {
	dir := t.TempDir()
	tool := applyPatchTool{fsToolBase: fsToolBase{ws: config.Root{Path: dir}}}
	staged, err := tool.prepare([]patchOp{{action: "add", path: "new.txt", lines: []string{"ours"}}})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	target := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(target, []byte("racer"), 0o600); err != nil {
		t.Fatalf("write concurrent target: %v", err)
	}
	if err := commitStagedFiles(staged); err == nil {
		t.Fatal("expected concurrent create conflict")
	}
	if got := readFileText(t, target); got != "racer" {
		t.Fatalf("concurrent target overwritten: %q", got)
	}
}

func TestApplyPatchRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Add File: " + outside,
		"+escape",
		"*** End Patch",
	}, "\n")
	_, err := mustApplyPatchTool(t, dir).Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"patch": patch,
	})
	if err == nil {
		t.Fatalf("expected outside root rejection")
	}
}

func TestApplyPatchRejectsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink not supported")
	}
	_, err := mustApplyPatchTool(t, dir).Execute(context.Background(), core.InvokeContext{}, map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: link.txt",
			"@@",
			"-hello",
			"+world",
			"*** End Patch",
		}, "\n"),
	})
	if err == nil {
		t.Fatalf("expected symlink rejection")
	}
}

func TestApplyPatchToolSchemaDocumentsPatchFormat(t *testing.T) {
	tool := mustApplyPatchTool(t, t.TempDir())
	schema, ok := tool.Schema().(map[string]any)
	if !ok {
		t.Fatalf("unexpected schema type: %#v", tool.Schema())
	}
	props, _ := schema["properties"].(map[string]any)
	patchProp, _ := props["patch"].(map[string]any)
	desc, _ := patchProp["description"].(string)
	if !strings.Contains(desc, "*** Begin Patch") || !strings.Contains(desc, "*** Update File:") {
		t.Fatalf("patch schema description should explain patch format: %q", desc)
	}
	if !strings.Contains(desc, "best-effort in-process rollback") || !strings.Contains(desc, "not crash-atomic") {
		t.Fatalf("patch schema description should state transaction limits: %q", desc)
	}
}

func mustApplyPatchTool(t *testing.T, root string) core.Tool {
	t.Helper()
	ws := config.Root{Path: root}
	for _, tl := range NewTools(ws) {
		if tl.Name() == "apply_patch" {
			return tl
		}
	}
	t.Fatal("apply_patch tool not found")
	return nil
}

func readFileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return string(b)
}
