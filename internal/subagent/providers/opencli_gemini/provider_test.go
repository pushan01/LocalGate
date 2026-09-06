package opencli_gemini

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"localgate/internal/subagent"
)

func TestCreateUsesAsyncGeminiSubmit(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
printf '%%s\n' '[{"Status":"Submitted","ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Model":"3.1 Pro","Generating":true}]'
`, capture))
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state, err := p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if state.ConversationID != "abc123def4567890" || state.ConversationURL == "" || state.Model != "3.1 Pro" || state.Hints[pendingFingerprintHint] == "" {
		t.Fatalf("unexpected create state: %#v", state)
	}
	args := readCapturedArgs(t, capture)
	want := []string{"gemini", "submit", "hello", "--new", "true", "--model", "Pro", "--submission-timeout", "8", "--format", "json"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestCreateExplicitModelOverridesDefaultModel(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
printf '%%s\n' '[{"Status":"Submitted","ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Model":"3.7 Flash","Generating":true}]'
`, capture))
	p, err := New(Config{Executable: executable, DefaultModel: "Pro"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := p.Create(context.Background(), subagent.ProviderCreateRequest{
		Instruction: "hello",
		Options:     map[string]string{"model": "Flash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Model != "3.7 Flash" {
		t.Fatalf("selected model = %q, want 3.7 Flash", state.Model)
	}
	args := readCapturedArgs(t, capture)
	want := []string{"gemini", "submit", "hello", "--new", "true", "--model", "Flash", "--submission-timeout", "8", "--format", "json"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestCreatePassesRequestedModelAndPersistsSelectedModel(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
printf '%%s\n' '[{"Status":"Submitted","ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Model":"3.1 Pro","Generating":true}]'
`, capture))
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state, err := p.Create(context.Background(), subagent.ProviderCreateRequest{
		Instruction: "hello",
		Options:     map[string]string{"model": "Pro"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Model != "3.1 Pro" {
		t.Fatalf("selected model = %q, want 3.1 Pro", state.Model)
	}
	args := readCapturedArgs(t, capture)
	want := []string{"gemini", "submit", "hello", "--new", "true", "--model", "Pro", "--submission-timeout", "8", "--format", "json"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSendTargetsExistingConversation(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
printf '%%s\n' '[{"Status":"Submitted","ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Generating":true}]'
`, capture))
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{ConversationID: "abc123def4567890", ConversationURL: "https://gemini.google.com/app/abc123def4567890", Model: "3.1 Pro"}
	next, err := p.Send(context.Background(), state, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if next.Hints[pendingFingerprintHint] == "" {
		t.Fatalf("send did not preserve pending marker: %#v", next)
	}
	if next.Model != "3.1 Pro" {
		t.Fatalf("send changed selected model: %#v", next)
	}
	args := readCapturedArgs(t, capture)
	want := []string{"gemini", "submit", "continue", "--conversation", "abc123def4567890", "--submission-timeout", "8", "--format", "json"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSubmitIntervalPacesCreateAndSendPerProvider(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"Status":"Submitted","ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Generating":true}]'
`)
	p, err := New(Config{Executable: executable, SubmitInterval: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	state, err := p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := p.Send(context.Background(), state, "continue"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 60*time.Millisecond {
		t.Fatalf("send was not provider-paced: elapsed=%s", elapsed)
	}
}

func TestPollTracksPendingGeminiTurnUntilComplete(t *testing.T) {
	busyExecutable := writeFakeExecutable(t, `
printf '%s\n' '[{"ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Generating":true,"Turns":[{"Index":1,"Role":"User","Text":"hello"},{"Index":2,"Role":"Assistant","Text":"partial"}]}]'
`)
	p, err := New(Config{Executable: busyExecutable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{ConversationID: "abc123def4567890", Hints: withPendingMarker(nil, "hello")}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Generating || observation.Reply.Text != "partial" || next.Hints[pendingFingerprintHint] == "" {
		t.Fatalf("unexpected busy observation: next=%#v observation=%#v", next, observation)
	}

	completeExecutable := writeFakeExecutable(t, `
printf '%s\n' '[{"ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Generating":false,"Turns":[{"Index":1,"Role":"User","Text":"hello"},{"Index":2,"Role":"Assistant","Text":"done"}]}]'
`)
	p, err = New(Config{Executable: completeExecutable})
	if err != nil {
		t.Fatal(err)
	}
	next, observation, err = p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "done" || next.Hints[pendingFingerprintHint] != "" {
		t.Fatalf("unexpected completed observation: next=%#v observation=%#v", next, observation)
	}
}

func TestPollMatchesGeminiRenderedUserText(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"ConversationId":"abc123def4567890","Generating":false,"Turns":[{"Index":1,"Role":"User","Text":"You said\n\n读取 /Users/pushan/PROJECT_HANDOFF.md"},{"Index":2,"Role":"User","Text":"读取 /Users/pushan/PROJECT_HANDOFF.md"},{"Index":3,"Role":"Assistant","Text":"done"}]}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "abc123def4567890",
		Hints:          withPendingMarker(nil, "读取 `/Users/pushan/PROJECT_HANDOFF.md`"),
	}
	_, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "done" {
		t.Fatalf("rendered Gemini user turn did not converge: %#v", observation)
	}
}

func TestPollDoesNotReturnOlderAssistantBeforePendingGeminiUserAppears(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"ConversationId":"abc123def4567890","Generating":false,"Turns":[{"Index":1,"Role":"User","Text":"older"},{"Index":2,"Role":"Assistant","Text":"old reply"}]}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "abc123def4567890",
		Hints:          withPendingMarker(nil, "new message"),
	}
	_, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Generating || observation.Reply.Text != "" {
		t.Fatalf("stale Gemini DOM should stay busy without old reply: %#v", observation)
	}
}

func TestPollCompletesOnEmptyAssistantTurnForGeneratedMedia(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"ConversationId":"abc123def4567890","Generating":false,"Turns":[{"Index":1,"Role":"User","Text":"draw an image"},{"Index":2,"Role":"Assistant","Text":""}]}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "abc123def4567890",
		Hints:          withPendingMarker(nil, "draw an image"),
	}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating {
		t.Fatalf("generated-media Assistant turn should complete the pending turn: %#v", observation)
	}
	if observation.Reply.Text != "" {
		t.Fatalf("generated-media completion should not invent reply text: %#v", observation)
	}
	if next.Hints[pendingFingerprintHint] != "" {
		t.Fatalf("pending marker was not cleared after generated-media completion: %#v", next)
	}
}

func TestPollReturnsValidatedImageArtifact(t *testing.T) {
	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "artifacts")
	artifactPath := filepath.Join(artifactDir, "abc123def4567890", "turn-2", "image.png")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("\x89PNG\r\n\x1a\nlocalgate-test-image")
	if err := os.WriteFile(artifactPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	executable := writeFakeExecutable(t, fmt.Sprintf(`
printf '%%s\n' '[{"ConversationId":"abc123def4567890","Generating":false,"Turns":[{"Index":1,"Role":"User","Text":"draw an image"},{"Index":2,"Role":"Assistant","Text":"","Artifacts":[{"id":"art_0123456789abcdef01234567","type":"image","mime_type":"image/png","file_name":"gemini-image.png","path":"%s","size":%d,"sha256":"%x","width":1024,"height":559,"alt":"generated image"}]}]}]'
`, artifactPath, len(payload), sum))
	p, err := New(Config{Executable: executable, ArtifactDir: artifactDir})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "abc123def4567890",
		Hints:          withPendingMarker(nil, "draw an image"),
	}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || len(observation.Reply.Artifacts) != 1 {
		t.Fatalf("unexpected artifact observation: %#v", observation)
	}
	artifact := observation.Reply.Artifacts[0]
	if artifact.Path != artifactPath || artifact.SHA256 != fmt.Sprintf("%x", sum) || artifact.Width != 1024 || artifact.Height != 559 {
		t.Fatalf("unexpected artifact metadata: %#v", artifact)
	}
	if next.Hints[pendingFingerprintHint] != "" {
		t.Fatalf("pending marker was not cleared: %#v", next)
	}
}

func TestPollRejectsArtifactOutsideConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "artifacts")
	outside := filepath.Join(dir, "outside.png")
	payload := []byte("image")
	if err := os.WriteFile(outside, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	executable := writeFakeExecutable(t, fmt.Sprintf(`
printf '%%s\n' '[{"ConversationId":"abc123def4567890","Generating":false,"Turns":[{"Index":1,"Role":"User","Text":"draw"},{"Index":2,"Role":"Assistant","Text":"","Artifacts":[{"id":"art_0123456789abcdef01234567","type":"image","mime_type":"image/png","file_name":"image.png","path":"%s","size":%d,"sha256":"%x","width":1,"height":1}]}]}]'
`, outside, len(payload), sum))
	p, err := New(Config{Executable: executable, ArtifactDir: artifactDir})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = p.Poll(context.Background(), subagent.ProviderState{ConversationID: "abc123def4567890"})
	if err == nil || !strings.Contains(err.Error(), "escapes artifact directory") {
		t.Fatalf("expected artifact path rejection, got %v", err)
	}
}

func TestPollRejectsArtifactEscapingThroughSymlinkParent(t *testing.T) {
	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "artifacts")
	outsideDir := filepath.Join(dir, "outside")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("png-bytes")
	outsidePath := filepath.Join(outsideDir, "image.png")
	if err := os.WriteFile(outsidePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(artifactDir, "escape")
	if err := os.Symlink(outsideDir, linkDir); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(linkDir, "image.png")
	sum := sha256.Sum256(payload)
	executable := writeFakeExecutable(t, fmt.Sprintf(`
printf '%%s\n' '[{"ConversationId":"abc123def4567890","Generating":false,"Turns":[{"Index":1,"Role":"User","Text":"draw"},{"Index":2,"Role":"Assistant","Text":"","Artifacts":[{"id":"art_0123456789abcdef01234567","type":"image","mime_type":"image/png","file_name":"image.png","path":"%s","size":%d,"sha256":"%x","width":1,"height":1}]}]}]'
`, artifactPath, len(payload), sum))
	p, err := New(Config{Executable: executable, ArtifactDir: artifactDir})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "abc123def4567890",
		Hints:          withPendingMarker(nil, "draw"),
	}
	if _, _, err := p.Poll(context.Background(), state); err == nil || !strings.Contains(err.Error(), "escapes artifact directory") {
		t.Fatalf("expected symlink-parent escape to fail, got %v", err)
	}
}

func TestProviderClassifiesAuthAndTimeout(t *testing.T) {
	authExecutable := writeFakeExecutable(t, `
printf '%s\n' 'Google session cookies missing; sign in' >&2
exit 2
`)
	p, err := New(Config{Executable: authExecutable})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = p.Poll(context.Background(), subagent.ProviderState{ConversationID: "abc123def4567890"})
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != ErrorAuthRequired {
		t.Fatalf("auth error = %#v, err=%v", providerErr, err)
	}

	timeoutExecutable := writeFakeExecutable(t, `exec sleep 5`)
	p, err = New(Config{Executable: timeoutExecutable, Timeout: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, _, err = p.Poll(context.Background(), subagent.ProviderState{ConversationID: "abc123def4567890"})
	if !errors.As(err, &providerErr) || providerErr.Kind != ErrorTimeout {
		t.Fatalf("timeout error = %#v, err=%v", providerErr, err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("timeout did not terminate promptly: %s", time.Since(started))
	}
}

func TestProviderPassesBrowserExecutableToBundledAdapter(t *testing.T) {
	executable := writeFakeExecutable(t, `
if [ "${LOCALGATE_BROWSER_EXECUTABLE:-}" != "/opt/local/bin/opencli-browser" ]; then
  printf '%s\n' "unexpected browser executable: ${LOCALGATE_BROWSER_EXECUTABLE:-}" >&2
  exit 9
fi
printf '%s\n' '[{"ConversationId":"abc123def4567890","ConversationUrl":"https://gemini.google.com/app/abc123def4567890","Generating":false,"Turns":[]}]'
`)
	p, err := New(Config{Executable: executable, BrowserExecutable: "/opt/local/bin/opencli-browser"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Poll(context.Background(), subagent.ProviderState{ConversationID: "abc123def4567890"}); err != nil {
		t.Fatal(err)
	}
}

func writeFakeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencli-fake")
	content := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCapturedArgs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
