package subagentcap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"localgate/internal/core"
	runtime "localgate/internal/subagent"
)

type fakeProvider struct{}

var fakeConversationCounter atomic.Uint64

func (fakeProvider) Create(_ context.Context, req runtime.ProviderCreateRequest) (runtime.ProviderState, error) {
	conversationID := fmt.Sprintf("conversation%d", fakeConversationCounter.Add(1))
	return runtime.ProviderState{
		ConversationID:  conversationID,
		ConversationURL: "https://chatgpt.com/g/g-p-deadbeef/c/" + conversationID,
		ProjectID:       req.Options["project"],
		Hints:           map[string]string{"reply": "created:" + req.Instruction},
	}, nil
}

func (fakeProvider) Send(_ context.Context, state runtime.ProviderState, message string) (runtime.ProviderState, error) {
	state.Hints = map[string]string{"reply": "reply:" + message}
	return state, nil
}

func (fakeProvider) Poll(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return state, runtime.Observation{Reply: runtime.Reply{Text: state.Hints["reply"]}}, nil
}

func (fakeProvider) Wait(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return state, runtime.Observation{Reply: runtime.Reply{Text: state.Hints["reply"]}}, nil
}

func (fakeProvider) Recover(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, error) {
	return state, nil
}

func (fakeProvider) Close(context.Context, runtime.ProviderState) error { return nil }

func (fakeProvider) Archive(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, error) {
	state.ProjectID = "cafebabe"
	state.ConversationURL = "https://chatgpt.com/g/g-p-cafebabe/c/" + state.ConversationID
	return state, nil
}

type artifactProvider struct{ fakeProvider }

func (artifactProvider) Poll(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return state, runtime.Observation{Reply: runtime.Reply{
		Text: "",
		Artifacts: []runtime.Artifact{{
			ID:       "art_0123456789abcdef01234567",
			Type:     "image",
			MIMEType: "image/png",
			FileName: "generated.png",
			Path:     "/tmp/generated.png",
			Size:     123,
			SHA256:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Width:    1024,
			Height:   559,
		}},
	}}, nil
}

func (p artifactProvider) Wait(ctx context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return p.Poll(ctx, state)
}

type pollErrorProvider struct{ fakeProvider }

func (pollErrorProvider) Poll(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return state, runtime.Observation{}, errors.New("remote observation failed")
}

type connectorAuthProvider struct{ fakeProvider }

type connectorAuthError struct{}

func (connectorAuthError) Error() string {
	return "ChatGPT connector MacGate1 authorization has expired; open the subagent ChatGPT conversation and complete authorization to continue"
}
func (connectorAuthError) ErrorCode() string { return "connector_auth_required" }

func (connectorAuthProvider) Poll(_ context.Context, state runtime.ProviderState) (runtime.ProviderState, runtime.Observation, error) {
	return state, runtime.Observation{Generating: true}, connectorAuthError{}
}

func newToolTestManager(t *testing.T) *runtime.Manager {
	t.Helper()
	m, err := runtime.NewManager(runtime.NewStore(filepath.Join(t.TempDir(), "subagents")), runtime.Options{
		DefaultProvider: "fake",
		MaxAgents:       8,
		MaxConcurrency:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterProvider("fake", fakeProvider{}); err != nil {
		t.Fatal(err)
	}
	return m
}

func newToolTestManagerWithProvider(t *testing.T, provider runtime.Provider) *runtime.Manager {
	t.Helper()
	m, err := runtime.NewManager(runtime.NewStore(filepath.Join(t.TempDir(), "subagents")), runtime.Options{
		DefaultProvider: "fake",
		MaxAgents:       8,
		MaxConcurrency:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSubagentToolsFlow(t *testing.T) {
	m := newToolTestManager(t)
	tools := NewTools(m)
	byName := make(map[string]core.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	for _, name := range []string{"subagent_spawn", "subagent_send", "subagent_poll", "subagent_wait", "subagent_get", "subagent_list", "subagent_archive", "subagent_close"} {
		if byName[name] == nil {
			t.Fatalf("missing tool %s", name)
		}
		if byName[name].Schema() == nil {
			t.Fatalf("tool %s has nil schema", name)
		}
	}
	ic := core.InvokeContext{Requester: "tester"}
	spawned, err := byName["subagent_spawn"].Execute(context.Background(), ic, map[string]any{
		"name":          "reviewer",
		"subagent_type": "reviewer",
		"instruction":   "review code",
		"project":       "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := spawned["subagent_id"].(string)
	if id == "" || spawned["subagent_type"] != "reviewer" || spawned["provider"] != "fake" || spawned["accepted"] != true || spawned["status"] != string(runtime.StatusBusy) {
		t.Fatalf("unexpected spawn result: %#v", spawned)
	}
	polled, err := byName["subagent_poll"].Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if polled["reply"] != "created:review code" || polled["status"] != string(runtime.StatusReady) {
		t.Fatalf("unexpected poll result: %#v", polled)
	}

	sent, err := byName["subagent_send"].Execute(context.Background(), ic, map[string]any{
		"subagent_id": id,
		"message":     "continue",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sent["accepted"] != true || sent["status"] != string(runtime.StatusBusy) {
		t.Fatalf("unexpected send result: %#v", sent)
	}
	waited, err := byName["subagent_wait"].Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if waited["reply"] != "reply:continue" || waited["status"] != string(runtime.StatusReady) {
		t.Fatalf("unexpected wait result: %#v", waited)
	}

	got, err := byName["subagent_get"].Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := got["subagent"].(map[string]any)
	conversationID, _ := agent["conversation_id"].(string)
	if !ok || conversationID == "" || agent["subagent_type"] != "reviewer" || agent["project_id"] != "deadbeef" {
		t.Fatalf("unexpected get result: %#v", got)
	}

	listed, err := byName["subagent_list"].Execute(context.Background(), ic, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, ok := listed["subagents"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["subagent_id"] != id || items[0]["subagent_type"] != "reviewer" {
		t.Fatalf("unexpected list result: %#v", listed)
	}

	closed, err := byName["subagent_close"].Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if closed["status"] != string(runtime.StatusClosed) {
		t.Fatalf("unexpected close result: %#v", closed)
	}
	if _, err := byName["subagent_send"].Execute(context.Background(), ic, map[string]any{"subagent_id": id, "message": "after"}); !errors.Is(err, runtime.ErrClosed) {
		t.Fatalf("send after close error = %v", err)
	}
	archived, err := byName["subagent_archive"].Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatal(err)
	}
	if archived["archived"] != true || archived["project_id"] != "cafebabe" {
		t.Fatalf("unexpected archive result: %#v", archived)
	}
	if _, err := byName["subagent_get"].Execute(context.Background(), ic, map[string]any{"subagent_id": id}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("archived subagent should be removed locally, got %v", err)
	}
}

func TestSubagentToolsExposePersistedArtifacts(t *testing.T) {
	m := newToolTestManagerWithProvider(t, artifactProvider{})
	tools := NewTools(m)
	byName := make(map[string]core.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name()] = tool
	}
	ic := core.InvokeContext{}
	spawned, err := byName["subagent_spawn"].Execute(context.Background(), ic, map[string]any{"instruction": "draw"})
	if err != nil {
		t.Fatal(err)
	}
	id := spawned["subagent_id"].(string)
	polled, err := byName["subagent_poll"].Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, ok := polled["artifacts"].([]map[string]any)
	if !ok || len(artifacts) != 1 || artifacts[0]["type"] != "image" || artifacts[0]["id"] == "" {
		t.Fatalf("unexpected poll artifacts: %#v", polled)
	}
	if _, exposed := artifacts[0]["path"]; exposed {
		t.Fatalf("artifact path should remain internal: %#v", artifacts[0])
	}
	got, err := byName["subagent_get"].Execute(context.Background(), ic, map[string]any{"subagent_id": id, "refresh": false})
	if err != nil {
		t.Fatal(err)
	}
	agent := got["subagent"].(map[string]any)
	persisted, ok := agent["artifacts"].([]map[string]any)
	if !ok || len(persisted) != 1 || persisted[0]["id"] != "art_0123456789abcdef01234567" {
		t.Fatalf("unexpected persisted artifacts: %#v", got)
	}
}

func TestSubagentToolsUseInstanceManager(t *testing.T) {
	m := newToolTestManager(t)
	spawn := SpawnTool{manager: m}
	get := GetTool{manager: m}
	res, err := spawn.Execute(context.Background(), core.InvokeContext{}, map[string]any{"instruction": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	id := res["subagent_id"].(string)
	if _, err := get.Execute(context.Background(), core.InvokeContext{}, map[string]any{"subagent_id": id, "refresh": false}); err != nil {
		t.Fatalf("get spawned subagent: %v", err)
	}
}

func TestSubagentGetRefreshesBusyByDefaultAndCanStayLocal(t *testing.T) {
	m := newToolTestManager(t)
	spawn := SpawnTool{manager: m}
	get := GetTool{manager: m}
	ic := core.InvokeContext{}

	first, err := spawn.Execute(context.Background(), ic, map[string]any{"instruction": "first"})
	if err != nil {
		t.Fatal(err)
	}
	firstID := first["subagent_id"].(string)
	refreshed, err := get.Execute(context.Background(), ic, map[string]any{"subagent_id": firstID})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed["refreshed"] != true || refreshed["reply"] != "created:first" {
		t.Fatalf("default get did not refresh busy agent: %#v", refreshed)
	}
	refreshedAgent := refreshed["subagent"].(map[string]any)
	if refreshedAgent["status"] != string(runtime.StatusReady) || refreshedAgent["phase"] != "ready" || refreshedAgent["next_action"] != "send" {
		t.Fatalf("unexpected refreshed agent: %#v", refreshedAgent)
	}
	if refreshedAgent["last_observed_at"] == "" {
		t.Fatalf("default get should expose last_observed_at: %#v", refreshedAgent)
	}

	second, err := spawn.Execute(context.Background(), ic, map[string]any{"instruction": "second"})
	if err != nil {
		t.Fatal(err)
	}
	secondID := second["subagent_id"].(string)
	localOnly, err := get.Execute(context.Background(), ic, map[string]any{"subagent_id": secondID, "refresh": false})
	if err != nil {
		t.Fatal(err)
	}
	if localOnly["refreshed"] != false {
		t.Fatalf("refresh=false unexpectedly refreshed: %#v", localOnly)
	}
	localAgent := localOnly["subagent"].(map[string]any)
	if localAgent["status"] != string(runtime.StatusBusy) || localAgent["phase"] != "submitted" || localAgent["next_action"] != "poll" || localAgent["last_observed_at"] != "" {
		t.Fatalf("unexpected local-only busy snapshot: %#v", localAgent)
	}
}

func TestSubagentGetReturnsDiagnosticStateWhenRefreshFails(t *testing.T) {
	m := newToolTestManagerWithProvider(t, pollErrorProvider{})
	spawn := SpawnTool{manager: m}
	get := GetTool{manager: m}
	ic := core.InvokeContext{}
	spawned, err := spawn.Execute(context.Background(), ic, map[string]any{"instruction": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	id := spawned["subagent_id"].(string)
	result, err := get.Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatalf("diagnostic get should survive provider refresh failure: %v", err)
	}
	if result["refreshed"] != true || result["refresh_error"] != "remote observation failed" {
		t.Fatalf("unexpected refresh failure result: %#v", result)
	}
	agent := result["subagent"].(map[string]any)
	conversationID, _ := agent["conversation_id"].(string)
	if agent["status"] != string(runtime.StatusDegraded) || agent["phase"] != "degraded" || agent["next_action"] != "poll" || conversationID == "" {
		t.Fatalf("refresh failure lost diagnostic mapping: %#v", agent)
	}
}

func TestSubagentGetSurfacesConnectorAuthorizeAction(t *testing.T) {
	m := newToolTestManagerWithProvider(t, connectorAuthProvider{})
	spawn := SpawnTool{manager: m}
	get := GetTool{manager: m}
	ic := core.InvokeContext{}
	spawned, err := spawn.Execute(context.Background(), ic, map[string]any{"instruction": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	id := spawned["subagent_id"].(string)
	result, err := get.Execute(context.Background(), ic, map[string]any{"subagent_id": id})
	if err != nil {
		t.Fatalf("diagnostic get should surface connector blocker: %v", err)
	}
	agent := result["subagent"].(map[string]any)
	if agent["status"] != string(runtime.StatusDegraded) || agent["phase"] != "connector_auth_required" || agent["next_action"] != "authorize" || agent["last_error_code"] != "connector_auth_required" {
		t.Fatalf("connector blocker was not surfaced structurally: %#v", agent)
	}
}

func TestSubagentToolInputLimits(t *testing.T) {
	m := newToolTestManager(t)
	spawn := SpawnTool{manager: m}
	if _, err := spawn.Execute(context.Background(), core.InvokeContext{}, map[string]any{"instruction": ""}); err == nil {
		t.Fatal("expected empty instruction error")
	}
	if _, err := spawn.Execute(context.Background(), core.InvokeContext{}, map[string]any{"instruction": "ok", "name": string(make([]byte, maxNameBytes+1))}); err == nil {
		t.Fatal("expected oversized name error")
	}
}
