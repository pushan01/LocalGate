package adapters

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestGeminiQuotaDetectionIgnoresConversationHistoryTitles(t *testing.T) {
	payload, err := os.ReadFile("localgate-adapter.mjs")
	if err != nil {
		t.Fatal(err)
	}
	source := string(payload)
	start := strings.Index(source, "const snapshotScript = String.raw")
	end := strings.Index(source, "function insertPromptScript")
	if start < 0 || end <= start {
		t.Fatal("could not locate Gemini snapshot script")
	}
	geminiSnapshot := source[start:end]
	if !strings.Contains(geminiSnapshot, "document.querySelector('bard-sidenav, side-navigation-content, conversations-list')") {
		t.Fatal("Gemini blocker detection must remove the conversation-history sidebar")
	}
	if strings.Contains(geminiSnapshot, "usage cap|quota|达到") {
		t.Fatal("Gemini blocker detection must not treat a bare occurrence of quota as a quota error")
	}
	if !strings.Contains(geminiSnapshot, "quota\\s+(?:exceeded|reached|limit|exhausted)") {
		t.Fatal("Gemini blocker detection lost explicit quota-error matching")
	}
}
