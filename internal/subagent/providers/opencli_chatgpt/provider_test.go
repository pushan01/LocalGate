package opencli_chatgpt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"localgate/internal/subagent"
)

func TestCreateUsesProjectWithoutWaitingAndKeepsPromptAsOneArg(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
cat <<'JSON'
[{"conversationId":"conversation123","conversationUrl":"https://chatgpt.com/g/g-p-deadbeef/c/conversation123","tool":"","response":""}]
JSON
`, capture))
	p, err := New(Config{Executable: executable, DefaultProject: "https://chatgpt.com/g/g-p-deadbeef-localgate"})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "should-not-exist")
	prompt := "review this; touch " + marker + "\n$(uname) \"quoted\""
	state, err := p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if state.ConversationID != "conversation123" || state.ProjectID != "deadbeef" || state.Hints[pendingFingerprintHint] == "" || state.Hints[pendingAfterIndexHint] != "0" {
		t.Fatalf("unexpected create state: %#v", state)
	}
	args := readCapturedArgs(t, capture)
	want := []string{"chatgpt", "ask", "--project", "deadbeef", "--wait", "false", "--format", "json", "--", prompt}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("prompt was interpreted by a shell: stat err=%v", err)
	}
}

func TestCreateWithoutProjectUsesNew(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
printf '%%s\n' '[{"conversationId":"conversation123","conversationUrl":"https://chatgpt.com/c/conversation123","response":""}]'
`, capture))
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: "hello"}); err != nil {
		t.Fatal(err)
	}
	args := readCapturedArgs(t, capture)
	if !containsAdjacent(args, "--new", "--wait") || contains(args, "--project") || !containsAdjacent(args, "--wait", "false") {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestCreateRejectsModelSelection(t *testing.T) {
	executable := writeFakeExecutable(t, "exit 99\n")
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Create(context.Background(), subagent.ProviderCreateRequest{
		Instruction: "hello",
		Options:     map[string]string{"model": "Pro"},
	})
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != ErrorConfiguration || !strings.Contains(providerErr.Message, "model selection is not supported") {
		t.Fatalf("unexpected model selection error: %#v", err)
	}
}

func TestCreateSerializesConcurrentChatGPTConversationCreation(t *testing.T) {
	dir := t.TempDir()
	guard := filepath.Join(dir, "guard")
	overlap := filepath.Join(dir, "overlap")
	counter := filepath.Join(dir, "counter")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
if ! mkdir %q 2>/dev/null; then
  printf 'overlap' > %q
fi
count=0
if [ -f %q ]; then count=$(cat %q); fi
count=$((count + 1))
printf '%%s' "$count" > %q
sleep 0.15
rmdir %q 2>/dev/null || true
printf '[{"conversationId":"conversation%%s","conversationUrl":"https://chatgpt.com/c/conversation%%s"}]\n' "$count" "$count"
`, guard, overlap, counter, counter, counter, guard))
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	states := make([]subagent.ProviderState, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			states[i], errs[i] = p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: fmt.Sprintf("hello %d", i)})
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if states[0].ConversationID == states[1].ConversationID {
		t.Fatalf("concurrent creates reused conversation: %#v", states)
	}
	if _, err := os.Stat(overlap); !os.IsNotExist(err) {
		t.Fatalf("create commands overlapped; overlap marker err=%v", err)
	}
}

func TestSendUsesConversationWithoutWaiting(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
printf '%%s\n' '[{"Status":"Success","InjectedText":"continue"}]'
`, capture))
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{ConversationID: "conversation123", ProjectID: "deadbeef", Hints: map[string]string{"transport": "old"}}
	next, err := p.Send(context.Background(), state, "continue")
	if err != nil {
		t.Fatal(err)
	}
	if next.ConversationID != "conversation123" || next.ProjectID != "deadbeef" || next.Hints["transport"] != "old" || next.Hints[pendingFingerprintHint] == "" || next.Hints[pendingAfterIndexHint] != "0" {
		t.Fatalf("unexpected send state: %#v", next)
	}
	args := readCapturedArgs(t, capture)
	want := []string{"chatgpt", "send", "--conversation", "conversation123", "--format", "json", "--", "continue"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestSubmitIntervalPacesCreateAndSendPerProvider(t *testing.T) {
	executable := writeFakeExecutable(t, `
case "${2:-}" in
  ask)
    printf '%s\n' '[{"conversationId":"conversation123","conversationUrl":"https://chatgpt.com/c/conversation123","response":""}]'
    ;;
  send)
    printf '%s\n' '[{"Status":"Success","InjectedText":"continue"}]'
    ;;
  *) exit 2 ;;
esac
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

func TestPollTracksPendingTurnUntilAssistantCompletes(t *testing.T) {
	busyExecutable := writeFakeExecutable(t, `
printf '%s\n' '[{"Index":1,"Role":"User","Text":"hello","Generating":true,"StableSeconds":0},{"Index":2,"Role":"Assistant","Text":"partial","Generating":true,"StableSeconds":0}]'
`)
	p, err := New(Config{Executable: busyExecutable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{ConversationID: "conversation123", Hints: withPendingMarker(nil, "hello")}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Generating || observation.Reply.Text != "partial" || next.Hints[pendingFingerprintHint] == "" {
		t.Fatalf("unexpected busy observation: next=%#v observation=%#v", next, observation)
	}

	completeExecutable := writeFakeExecutable(t, `
printf '%s\n' '[{"Index":1,"Role":"User","Text":"hello","Generating":false,"StableSeconds":0},{"Index":2,"Role":"Assistant","Text":"done","Generating":false,"StableSeconds":0}]'
`)
	p, err = New(Config{Executable: completeExecutable})
	if err != nil {
		t.Fatal(err)
	}
	next, observation, err = p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "done" || next.Hints[pendingFingerprintHint] != "" || next.Hints[lastObservedIndexHint] != "2" {
		t.Fatalf("unexpected completed observation: next=%#v observation=%#v", next, observation)
	}
}

func TestPollDoesNotMarkReadyBeforePendingUserTurnAppears(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"Index":1,"Role":"User","Text":"older","Generating":false,"StableSeconds":0},{"Index":2,"Role":"Assistant","Text":"old reply","Generating":false,"StableSeconds":0}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{ConversationID: "conversation123", Hints: withPendingMarker(map[string]string{lastObservedIndexHint: "2"}, "new message")}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Generating || observation.Reply.Text != "" || next.Hints[pendingFingerprintHint] == "" {
		t.Fatalf("pending turn should remain busy: next=%#v observation=%#v", next, observation)
	}
}

func TestPendingHashUsesChatGPTDetailTextNormalization(t *testing.T) {
	raw := "hello \t\n\n\nworld\u00a0"
	normalized := "hello\n\nworld"
	if hashMessage(raw) != hashMessage(normalized) {
		t.Fatal("pending message hash should tolerate the same whitespace normalization used by ChatGPT detail")
	}
}

func TestPendingFingerprintIgnoresMarkdownPresentationSyntax(t *testing.T) {
	raw := "读取 `/Users/pushan/PROJECT_HANDOFF.md` 并查看 **reviewer**"
	dom := "读取 /Users/pushan/PROJECT_HANDOFF.md 并查看 reviewer"
	if messageFingerprint(raw) != messageFingerprint(dom) {
		t.Fatal("pending fingerprint should ignore Markdown syntax removed by ChatGPT DOM rendering")
	}
}

func TestPollMatchesPromptWhenDOMAppendsExpandAffordance(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"Index":1,"Role":"User","Text":"older","Generating":false,"StableSeconds":0},{"Index":2,"Role":"Assistant","Text":"old reply","Generating":false,"StableSeconds":0},{"Index":3,"Role":"User","Text":"long prompt展开","Generating":false,"StableSeconds":0},{"Index":4,"Role":"Assistant","Text":"done","Generating":false,"StableSeconds":0}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "conversation123",
		Hints:          withPendingMarker(map[string]string{lastObservedIndexHint: "2"}, "long prompt"),
	}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "done" || next.Hints[pendingFingerprintHint] != "" || next.Hints[pendingFingerprintLenHint] != "" {
		t.Fatalf("DOM-suffixed turn did not converge: next=%#v observation=%#v", next, observation)
	}
}

func TestPollRecoversPersistedV2FingerprintWhenDOMAppendsSuffix(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"Index":1,"Role":"User","Text":"older","Generating":false,"StableSeconds":0},{"Index":2,"Role":"Assistant","Text":"old reply","Generating":false,"StableSeconds":0},{"Index":3,"Role":"User","Text":"long prompt展开","Generating":false,"StableSeconds":0},{"Index":4,"Role":"Assistant","Text":"done","Generating":false,"StableSeconds":0}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "conversation123",
		Hints: map[string]string{
			lastObservedIndexHint:  "2",
			pendingAfterIndexHint:  "2",
			pendingFingerprintHint: messageFingerprint("long prompt"),
		},
	}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "done" || next.Hints[pendingFingerprintHint] != "" {
		t.Fatalf("persisted v2 DOM-suffixed turn did not recover: next=%#v observation=%#v", next, observation)
	}
}

func TestPollMatchesMarkdownPromptAfterDOMFormattingIsRemoved(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"Index":1,"Role":"User","Text":"读取 /Users/pushan/PROJECT_HANDOFF.md 并查看 reviewer","Generating":false,"StableSeconds":0},{"Index":2,"Role":"Assistant","Text":"done","Generating":false,"StableSeconds":0}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "conversation123",
		Hints:          withPendingMarker(nil, "读取 `/Users/pushan/PROJECT_HANDOFF.md` 并查看 **reviewer**"),
	}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "done" || next.Hints[pendingFingerprintHint] != "" {
		t.Fatalf("markdown-normalized turn did not converge: next=%#v observation=%#v", next, observation)
	}
}

func TestPollRecoversLegacyPendingHashWhenMarkdownWasRenderedAway(t *testing.T) {
	executable := writeFakeExecutable(t, `
printf '%s\n' '[{"Index":1,"Role":"User","Text":"读取 PROJECT_HANDOFF.md","Generating":false,"StableSeconds":0},{"Index":2,"Role":"Assistant","Text":"legacy done","Generating":false,"StableSeconds":0}]'
`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID: "conversation123",
		Hints:          map[string]string{pendingHashHint: hashMessage("读取 `PROJECT_HANDOFF.md`")},
	}
	next, observation, err := p.Poll(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "legacy done" || next.Hints[pendingHashHint] != "" {
		t.Fatalf("legacy markdown state did not recover: next=%#v observation=%#v", next, observation)
	}
}

func TestPollDetectsConnectorReconnectBlocker(t *testing.T) {
	executable := writeConnectorAuthFakeExecutable(t)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID:  "conversation123",
		ConversationURL: "https://chatgpt.com/c/conversation123",
		Hints:           withPendingMarker(nil, "hello"),
	}
	_, observation, err := p.Poll(context.Background(), state)
	if errorKind(err) != ErrorConnectorAuth {
		t.Fatalf("connector blocker error kind = %q, err=%v", errorKind(err), err)
	}
	if !observation.Generating {
		t.Fatal("blocked pending turn should remain pending until authorization is restored")
	}
	if !strings.Contains(err.Error(), "MacGate1") {
		t.Fatalf("connector blocker error should identify connector: %v", err)
	}
}

func TestWaitDetectsConnectorReconnectBlockerBetweenWaitSlices(t *testing.T) {
	executable := writeConnectorAuthFakeExecutable(t)
	p, err := New(Config{Executable: executable, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{
		ConversationID:  "conversation123",
		ConversationURL: "https://chatgpt.com/c/conversation123",
		Hints:           withPendingMarker(nil, "hello"),
	}
	_, _, err = p.Wait(context.Background(), state)
	if errorKind(err) != ErrorConnectorAuth {
		t.Fatalf("wait connector blocker error kind = %q, err=%v", errorKind(err), err)
	}
}

func TestWaitUsesDetailWaitAndReturnsCompletedReply(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "args.bin")
	executable := writeFakeExecutable(t, fmt.Sprintf(`
for arg in "$@"; do printf '%%s\0' "$arg"; done > %q
printf '%%s\n' '[{"Index":1,"Role":"User","Text":"hello","Generating":false,"StableSeconds":6},{"Index":2,"Role":"Assistant","Text":"done","Generating":false,"StableSeconds":6}]'
`, capture))
	p, err := New(Config{Executable: executable, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{ConversationID: "conversation123", Hints: withPendingMarker(nil, "hello")}
	next, observation, err := p.Wait(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Generating || observation.Reply.Text != "done" || next.Hints[pendingFingerprintHint] != "" {
		t.Fatalf("unexpected wait result: next=%#v observation=%#v", next, observation)
	}
	args := readCapturedArgs(t, capture)
	want := []string{"chatgpt", "detail", "conversation123", "--wait", "true", "--timeout", "9", "--format", "json"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestProviderClassifiesFailuresAndTimeout(t *testing.T) {
	authExecutable := writeFakeExecutable(t, `
printf '%s\n' 'ChatGPT requires a logged-in session' >&2
exit 2
`)
	p, err := New(Config{Executable: authExecutable})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: "hello"})
	if errorKind(err) != ErrorAuthRequired {
		t.Fatalf("auth error kind = %q, err=%v", errorKind(err), err)
	}

	timeoutExecutable := writeFakeExecutable(t, `exec sleep 5`)
	p, err = New(Config{Executable: timeoutExecutable, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: "hello"})
	if !errors.Is(err, subagent.ErrOutcomeUnknown) {
		t.Fatalf("expected create timeout outcome unknown, got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("timeout did not terminate promptly: %s", time.Since(started))
	}
}

func TestSendTimeoutKeepsPendingStateAndIsOutcomeUnknown(t *testing.T) {
	executable := writeFakeExecutable(t, `exec sleep 5`)
	p, err := New(Config{Executable: executable, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	state := subagent.ProviderState{ConversationID: "conversation123"}
	next, err := p.Send(context.Background(), state, "hello")
	if !errors.Is(err, subagent.ErrOutcomeUnknown) {
		t.Fatalf("expected outcome unknown, got %v", err)
	}
	if next.ConversationID != state.ConversationID || next.Hints[pendingFingerprintHint] == "" {
		t.Fatalf("uncertain send should retain reconcilable pending state: %#v", next)
	}
}

func TestSendMalformedOutputIsOutcomeUnknown(t *testing.T) {
	executable := writeFakeExecutable(t, `printf '%s\n' 'not-json'`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	next, err := p.Send(context.Background(), subagent.ProviderState{ConversationID: "conversation123"}, "hello")
	if !errors.Is(err, subagent.ErrOutcomeUnknown) || next.Hints[pendingFingerprintHint] == "" {
		t.Fatalf("expected malformed send output to be outcome unknown with pending state, next=%#v err=%v", next, err)
	}
}

func TestProviderRejectsMalformedCreateOutputAndProjectURLs(t *testing.T) {
	executable := writeFakeExecutable(t, `printf '%s\n' 'not-json'`)
	p, err := New(Config{Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Create(context.Background(), subagent.ProviderCreateRequest{Instruction: "hello"})
	if !errors.Is(err, subagent.ErrOutcomeUnknown) {
		t.Fatalf("expected malformed create output to be outcome unknown, got %v", err)
	}

	if _, err := New(Config{Executable: executable, DefaultProject: "https://evil.test/g/g-p-deadbeef"}); err == nil || errorKind(err) != ErrorConfiguration {
		t.Fatalf("expected off-domain project configuration error, got %v", err)
	}
	if got, err := normalizeProjectID("/g/g-p-A1B2C3D4-localgate"); err != nil || got != "a1b2c3d4" {
		t.Fatalf("normalize project = %q, err=%v", got, err)
	}
}

func TestParseRowsRequireValidShapes(t *testing.T) {
	if _, err := parseAskRow([]byte(`[]`)); err == nil {
		t.Fatal("expected empty ask row error")
	}
	if _, err := parseSendRow([]byte(`[]`)); err == nil {
		t.Fatal("expected empty send row error")
	}
	if _, err := parseDetailRows([]byte(`[]`)); err == nil {
		t.Fatal("expected empty detail rows error")
	}
	if _, err := stateFromRow(askRow{ConversationURL: "https://chatgpt.com/c/conversation123"}, ""); err == nil {
		t.Fatal("expected missing conversation id error")
	}
	if _, err := stateFromRow(askRow{ConversationID: "conversation123", ConversationURL: "https://evil.test/c/conversation123"}, ""); err == nil {
		t.Fatal("expected off-domain conversation url error")
	}
}

func writeFakeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencli-fake")
	content := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeConnectorAuthFakeExecutable(t *testing.T) string {
	t.Helper()
	return writeFakeExecutable(t, `
if [ "$1" = "chatgpt" ] && [ "$2" = "detail" ]; then
  case " $* " in
    *" --wait true "*)
      printf '%s\n' 'Conversation did not finish or stabilize before timeout.' >&2
      exit 2
      ;;
  esac
  printf '%s\n' '[{"Index":1,"Role":"User","Text":"hello","Generating":false,"StableSeconds":0}]'
  exit 0
fi
if [ "$1" = "browser" ]; then
  case "$3" in
    open)
      printf '%s\n' '{"opened":true}'
      ;;
    eval)
      printf '%s\n' '{"blocked":true,"kind":"connector_auth_required","connector":"MacGate1","evidence":"重新连接 MacGate1 你与 MacGate1 的连接已过期"}'
      ;;
    close)
      printf '%s\n' '{"closed":true}'
      ;;
  esac
  exit 0
fi
printf '%s\n' 'unexpected fake opencli invocation' >&2
exit 2
`)
}

func readCapturedArgs(t *testing.T, path string) []string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.Split(payload, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	out := make([]string, len(parts))
	for i, part := range parts {
		out[i] = string(part)
	}
	return out
}

func errorKind(err error) ErrorKind {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Kind
	}
	return ""
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsAdjacent(items []string, first, second string) bool {
	for i := 0; i+1 < len(items); i++ {
		if items[i] == first && items[i+1] == second {
			return true
		}
	}
	return false
}
