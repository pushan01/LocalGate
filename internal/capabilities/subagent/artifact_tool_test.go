package subagentcap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/config"
	"localgate/internal/core"
	"localgate/internal/fileexport"
	runtime "localgate/internal/subagent"
)

type artifactFileProvider struct {
	fakeProvider
	artifact runtime.Artifact
}

func (p artifactFileProvider) Poll(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return state, runtime.Observation{Reply: runtime.Reply{Artifacts: []runtime.Artifact{p.artifact}}}, nil
}

func (p artifactFileProvider) Wait(ctx context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return p.Poll(ctx, state)
}

func TestSubagentArtifactGetIssuesValidatedResourceLink(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	payload := buf.Bytes()
	path := filepath.Join(t.TempDir(), "generated.png")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	artifact := runtime.Artifact{
		ID:       "art_0123456789abcdef01234567",
		Type:     "image",
		MIMEType: "image/png",
		FileName: "generated.png",
		Path:     path,
		Size:     int64(len(payload)),
		SHA256:   fmt.Sprintf("%x", sum[:]),
		Width:    1024,
		Height:   559,
		Alt:      "generated image",
	}
	m := newToolTestManagerWithProvider(t, artifactFileProvider{artifact: artifact})
	ic := core.InvokeContext{}
	spawned, err := SpawnTool{manager: m}.Execute(context.Background(), ic, map[string]any{"instruction": "draw"})
	if err != nil {
		t.Fatal(err)
	}
	id := spawned["subagent_id"].(string)
	if _, err := (PollTool{manager: m}).Execute(context.Background(), ic, map[string]any{"subagent_id": id}); err != nil {
		t.Fatal(err)
	}

	exports, err := fileexport.NewService(fileexport.Options{
		BaseURL:    "https://gate.example",
		DefaultTTL: 5 * time.Minute,
		MaxTTL:     10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.FileExports{
		Enabled:           true,
		DefaultTTLSeconds: 300,
		MaxTTLSeconds:     600,
		MaxFileBytes:      1 << 20,
	}
	tool := NewArtifactGetTool(m, cfg, exports)
	result, err := tool.Execute(context.Background(), ic, map[string]any{
		"subagent_id": id,
		"artifact_id": artifact.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ready" || result["artifact_id"] != artifact.ID || result["url"] == "" {
		t.Fatalf("unexpected artifact result: %#v", result)
	}
	links := core.ToolResourceLinks(tool, result)
	if len(links) != 1 || links[0].MIMEType != "image/png" || links[0].Name != "generated.png" {
		t.Fatalf("unexpected artifact resource links: %#v", links)
	}
	if !strings.HasPrefix(links[0].URI, "https://gate.example/system/exports/") {
		t.Fatalf("unexpected resource URI: %q", links[0].URI)
	}
	if content := core.ToolNativeContent(tool, result); len(content) != 2 || content[1]["type"] != "image" {
		t.Fatalf("expected a direct image preview and downloadable link, got %#v", content)
	}

	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), ic, map[string]any{
		"subagent_id": id,
		"artifact_id": artifact.ID,
	}); err == nil || !strings.Contains(err.Error(), "metadata mismatch") {
		t.Fatalf("expected tampered artifact to fail, got %v", err)
	}
}
