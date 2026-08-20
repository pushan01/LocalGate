package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeProvider struct {
	mu            sync.Mutex
	createCount   int
	sendCount     int
	pollCount     int
	waitCount     int
	recoverCount  int
	closeCount    int
	sendErr       error
	pollErr       error
	pollBusy      bool
	pollArtifacts []Artifact
	activeByID    map[string]int
	maxByID       map[string]int
	activeGlobal  int
	maxGlobal     int
	entered       chan string
	release       chan struct{}
}

type archiveFakeProvider struct {
	*fakeProvider
	archiveCount int
	archiveErr   error
}

type collidingFakeProvider struct {
	*fakeProvider
}

func (p *collidingFakeProvider) Create(_ context.Context, req ProviderCreateRequest) (ProviderState, error) {
	p.mu.Lock()
	p.createCount++
	p.mu.Unlock()
	return ProviderState{
		ConversationID:  "shared-conversation",
		ConversationURL: "https://example.test/c/shared-conversation",
	}, nil
}

func (p *archiveFakeProvider) Archive(_ context.Context, state ProviderState) (ProviderState, error) {
	p.mu.Lock()
	p.archiveCount++
	err := p.archiveErr
	p.mu.Unlock()
	if err != nil {
		return state, err
	}
	state.ProjectID = "cafebabe"
	state.ConversationURL = "https://example.test/g/g-p-cafebabe/c/" + state.ConversationID
	return state, nil
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{
		activeByID: map[string]int{},
		maxByID:    map[string]int{},
	}
}

func (p *fakeProvider) Create(_ context.Context, req ProviderCreateRequest) (ProviderState, error) {
	p.mu.Lock()
	p.createCount++
	id := "conv-" + req.Name
	p.mu.Unlock()
	return ProviderState{ConversationID: id, ConversationURL: "https://example.test/c/" + id}, nil
}

func (p *fakeProvider) Send(ctx context.Context, state ProviderState, message string) (ProviderState, error) {
	p.mu.Lock()
	p.sendCount++
	id := state.ConversationID
	p.activeByID[id]++
	if p.activeByID[id] > p.maxByID[id] {
		p.maxByID[id] = p.activeByID[id]
	}
	p.activeGlobal++
	if p.activeGlobal > p.maxGlobal {
		p.maxGlobal = p.activeGlobal
	}
	entered := p.entered
	release := p.release
	sendErr := p.sendErr
	p.mu.Unlock()

	if entered != nil {
		entered <- id
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			sendErr = ctx.Err()
		}
	}

	p.mu.Lock()
	p.activeByID[id]--
	p.activeGlobal--
	p.mu.Unlock()
	if sendErr != nil {
		return state, sendErr
	}
	state.Hints = map[string]string{"last_message": message}
	return state, nil
}

func (p *fakeProvider) Poll(_ context.Context, state ProviderState) (ProviderState, Observation, error) {
	p.mu.Lock()
	p.pollCount++
	err := p.pollErr
	busy := p.pollBusy
	artifacts := append([]Artifact(nil), p.pollArtifacts...)
	p.mu.Unlock()
	if err != nil {
		return state, Observation{}, err
	}
	reply := "created"
	if state.Hints["last_message"] != "" {
		reply = "reply:" + state.Hints["last_message"]
	}
	return state, Observation{Generating: busy, Reply: Reply{Text: reply, Artifacts: artifacts}}, nil
}

func (p *fakeProvider) Wait(_ context.Context, state ProviderState) (ProviderState, Observation, error) {
	p.mu.Lock()
	p.waitCount++
	err := p.pollErr
	artifacts := append([]Artifact(nil), p.pollArtifacts...)
	p.mu.Unlock()
	if err != nil {
		return state, Observation{}, err
	}
	reply := "created"
	if state.Hints["last_message"] != "" {
		reply = "reply:" + state.Hints["last_message"]
	}
	return state, Observation{Reply: Reply{Text: reply, Artifacts: artifacts}}, nil
}

func (p *fakeProvider) Recover(_ context.Context, state ProviderState) (ProviderState, error) {
	p.mu.Lock()
	p.recoverCount++
	p.mu.Unlock()
	state.Hints = map[string]string{"recovered": "true"}
	return state, nil
}

func (p *fakeProvider) Close(_ context.Context, _ ProviderState) error {
	p.mu.Lock()
	p.closeCount++
	p.mu.Unlock()
	return nil
}

func newTestManager(t *testing.T, provider *fakeProvider, options Options) *Manager {
	t.Helper()
	if options.DefaultProvider == "" {
		options.DefaultProvider = "fake"
	}
	store := NewStore(filepath.Join(t.TempDir(), "subagents"))
	m, err := NewManager(store, options)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}
	return m
}

func markReady(t *testing.T, m *Manager, workspaceID, id string) {
	t.Helper()
	result, err := m.Poll(context.Background(), workspaceID, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Subagent.Status != StatusReady {
		t.Fatalf("poll status = %q, want ready", result.Subagent.Status)
	}
}

func newArchiveTestManager(t *testing.T, provider *archiveFakeProvider) (*Manager, *Store) {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "subagents"))
	m, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}
	return m, store
}

func TestStoreLoadsLegacySubagentWithoutType(t *testing.T) {
	base := filepath.Join(t.TempDir(), "subagents")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := `{"id":"sg_legacy1","name":"legacy","provider":"fake","status":"ready","workspace_id":"ws","provider_state":{"conversation_id":"conv-legacy"}}`
	if err := os.WriteFile(filepath.Join(base, "sg_legacy1.json"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewStore(base).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].SubagentType != "" || loaded[0].Name != "legacy" {
		t.Fatalf("unexpected legacy subagent: %#v", loaded)
	}
}

func TestManagerArchiveMovesBeforeDeletingLocalRecord(t *testing.T) {
	provider := &archiveFakeProvider{fakeProvider: newFakeProvider()}
	m, store := newArchiveTestManager(t, provider)
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "archive", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}
	provider.pollBusy = false

	archived, err := m.Archive(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.ProviderState.ProjectID != "cafebabe" || archived.Status != StatusClosed {
		t.Fatalf("unexpected archived snapshot: %#v", archived)
	}
	if _, err := m.Get("ws", created.Subagent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived subagent should be removed from manager, got %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("archived subagent should be removed from store: %#v", loaded)
	}
	if provider.archiveCount != 1 || provider.pollCount != 1 {
		t.Fatalf("archive calls=%d poll calls=%d, want 1/1", provider.archiveCount, provider.pollCount)
	}
}

func TestManagerRejectsTwoSubagentsOwningSameConversation(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "subagents"))
	m, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	provider := &collidingFakeProvider{fakeProvider: newFakeProvider()}
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}

	first, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "first", Instruction: "one"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "second", Instruction: "two"})
	if !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("second create error = %v, want conversation conflict", err)
	}
	var conflict *ConversationConflictError
	if !errors.As(err, &conflict) || conflict.OwnerID != first.Subagent.ID || conflict.ConversationID != "shared-conversation" {
		t.Fatalf("unexpected conflict details: %#v err=%v", conflict, err)
	}

	agent, err := m.Get("ws", first.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != StatusDegraded || !strings.Contains(agent.LastError, "shared-conversation") {
		t.Fatalf("existing owner should be quarantined after provider collision: %#v", agent)
	}
	agents, err := m.List("ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 {
		t.Fatalf("duplicate conversation must not create a second sg_* mapping: %#v", agents)
	}
}

func TestManagerQuarantinesLoadedDuplicateConversationMapping(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "subagents"))
	baseTime := time.Now().UTC().Add(-time.Minute)
	first := Subagent{
		ID:          "sg_first0000000000000000000",
		Provider:    "fake",
		Status:      StatusBusy,
		WorkspaceID: "ws",
		ProviderState: ProviderState{
			ConversationID: "shared-conversation",
		},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
	}
	second := first
	second.ID = "sg_second000000000000000000"
	second.CreatedAt = baseTime.Add(time.Second)
	second.UpdatedAt = second.CreatedAt
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	provider := newFakeProvider()
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}

	owner, err := m.Get("ws", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Status != StatusBusy {
		t.Fatalf("earliest mapping should remain owner, got %#v", owner)
	}
	duplicate, err := m.Get("ws", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Status != StatusDegraded || !strings.Contains(duplicate.LastError, first.ID) {
		t.Fatalf("loaded duplicate was not quarantined: %#v", duplicate)
	}
	if _, err := m.Poll(context.Background(), "ws", second.ID); !errors.Is(err, ErrConversationConflict) {
		t.Fatalf("duplicate poll error = %v, want conversation conflict", err)
	}
}

func TestManagerArchiveRefusesGeneratingConversationAndKeepsMapping(t *testing.T) {
	provider := &archiveFakeProvider{fakeProvider: newFakeProvider()}
	provider.pollBusy = true
	m, _ := newArchiveTestManager(t, provider)
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "busy", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.Archive(context.Background(), "ws", created.Subagent.ID)
	if !errors.Is(err, ErrArchiveBusy) {
		t.Fatalf("archive busy error = %v", err)
	}
	if provider.archiveCount != 0 {
		t.Fatalf("archive provider called while generating: %d", provider.archiveCount)
	}
	if _, err := m.Get("ws", created.Subagent.ID); err != nil {
		t.Fatalf("busy archive must preserve mapping: %v", err)
	}
}

func TestManagerArchiveOutcomeUnknownKeepsDegradedMapping(t *testing.T) {
	provider := &archiveFakeProvider{fakeProvider: newFakeProvider(), archiveErr: NewOutcomeUnknownError(errors.New("lost response"))}
	m, _ := newArchiveTestManager(t, provider)
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "unknown", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}
	provider.pollBusy = false

	_, err = m.Archive(context.Background(), "ws", created.Subagent.ID)
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("archive error = %v, want outcome unknown", err)
	}
	agent, getErr := m.Get("ws", created.Subagent.ID)
	if getErr != nil {
		t.Fatalf("uncertain archive must preserve mapping: %v", getErr)
	}
	if agent.Status != StatusDegraded || agent.ProviderState.ConversationID != created.Subagent.ProviderState.ConversationID {
		t.Fatalf("unexpected preserved agent after uncertain archive: %#v", agent)
	}
}

func TestManagerCreatePersistsAndReloads(t *testing.T) {
	base := filepath.Join(t.TempDir(), "subagents")
	store := NewStore(base)
	m, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	provider := newFakeProvider()
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}
	result, err := m.Create(context.Background(), CreateRequest{
		WorkspaceID:  "ws-a",
		Name:         "reviewer",
		SubagentType: "reviewer",
		Instruction:  "review code",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !validID(result.Subagent.ID) {
		t.Fatalf("unexpected id %q", result.Subagent.ID)
	}
	if result.Subagent.ProviderState.ConversationID != "conv-reviewer" {
		t.Fatalf("unexpected provider state: %#v", result.Subagent.ProviderState)
	}
	if result.Subagent.Status != StatusBusy {
		t.Fatalf("new subagent status = %q, want busy", result.Subagent.Status)
	}
	if !result.Subagent.LastObservedAt.IsZero() {
		t.Fatalf("new subagent last observed = %s, want zero before first poll", result.Subagent.LastObservedAt)
	}

	reloaded, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := reloaded.Get("ws-a", result.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.SubagentType != "reviewer" || agent.Instruction != "review code" || agent.ProviderState.ConversationID != "conv-reviewer" {
		t.Fatalf("reloaded agent mismatch: %#v", agent)
	}
}

func TestManagerPersistsLatestArtifactsAndClearsThemOnSend(t *testing.T) {
	base := filepath.Join(t.TempDir(), "subagents")
	store := NewStore(base)
	m, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	provider := newFakeProvider()
	provider.pollArtifacts = []Artifact{{
		ID:       "art_0123456789abcdef01234567",
		Type:     "image",
		MIMEType: "image/png",
		FileName: "generated.png",
		Path:     "/tmp/generated.png",
		Size:     123,
		SHA256:   strings.Repeat("a", 64),
		Width:    1024,
		Height:   559,
	}}
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "image", Instruction: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	observed, err := m.Poll(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Reply.Artifacts) != 1 || len(observed.Subagent.Artifacts) != 1 {
		t.Fatalf("artifact was not exposed and persisted: %#v", observed)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Artifacts) != 1 || loaded[0].Artifacts[0].ID != provider.pollArtifacts[0].ID {
		t.Fatalf("persisted artifact mismatch: %#v", loaded)
	}
	provider.pollArtifacts = nil
	sent, err := m.Send(context.Background(), "ws", created.Subagent.ID, "next")
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Artifacts) != 0 {
		t.Fatalf("new turn should clear prior artifacts: %#v", sent.Artifacts)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Artifacts) != 0 {
		t.Fatalf("cleared artifacts were not persisted: %#v", loaded)
	}
}

func TestManagerPollBusyThenWaitReadyAndRejectsSendWhileBusy(t *testing.T) {
	provider := newFakeProvider()
	provider.pollBusy = true
	m := newTestManager(t, provider, Options{})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}
	polled, err := m.Poll(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if polled.Subagent.Status != StatusBusy || polled.Reply.Text != "created" {
		t.Fatalf("unexpected busy poll: %#v", polled)
	}
	if polled.Subagent.LastObservedAt.IsZero() || !polled.Subagent.LastObservedAt.Equal(polled.Subagent.UpdatedAt) {
		t.Fatalf("busy poll should record successful remote observation: %#v", polled.Subagent)
	}
	if _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "too early"); !errors.Is(err, ErrBusy) {
		t.Fatalf("send while busy error = %v, want busy", err)
	}
	provider.mu.Lock()
	provider.pollBusy = false
	provider.mu.Unlock()
	waited, err := m.Wait(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waited.Subagent.Status != StatusReady || waited.Reply.Text != "created" {
		t.Fatalf("unexpected wait result: %#v", waited)
	}
	if _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "next"); err != nil {
		t.Fatal(err)
	}
	agent, err := m.Get("ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != StatusBusy {
		t.Fatalf("status after async send = %q, want busy", agent.Status)
	}
	if !agent.LastObservedAt.Before(agent.UpdatedAt) {
		t.Fatalf("new async submission should be newer than last observation: observed=%s updated=%s", agent.LastObservedAt, agent.UpdatedAt)
	}
}

func TestManagerThrottlesRepeatedPollsForSameAgent(t *testing.T) {
	provider := newFakeProvider()
	m := newTestManager(t, provider, Options{
		SameAgentPollInterval:      80 * time.Millisecond,
		DifferentAgentPollInterval: 30 * time.Millisecond,
	})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := m.Poll(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Refreshed {
		t.Fatalf("first poll should refresh: %#v", first)
	}
	second, err := m.Poll(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Refreshed || second.RetryAfter <= 0 {
		t.Fatalf("same-agent poll should be throttled: %#v", second)
	}
	provider.mu.Lock()
	pollCount := provider.pollCount
	provider.mu.Unlock()
	if pollCount != 1 {
		t.Fatalf("provider poll count = %d, want 1 while throttled", pollCount)
	}

	time.Sleep(90 * time.Millisecond)
	third, err := m.Poll(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Refreshed {
		t.Fatalf("poll after same-agent interval should refresh: %#v", third)
	}
}

func TestManagerThrottlesPollsAcrossDifferentAgents(t *testing.T) {
	provider := newFakeProvider()
	m := newTestManager(t, provider, Options{
		SameAgentPollInterval:      80 * time.Millisecond,
		DifferentAgentPollInterval: 40 * time.Millisecond,
	})
	one, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "two", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Poll(context.Background(), "ws", one.Subagent.ID); err != nil {
		t.Fatal(err)
	}
	second, err := m.Poll(context.Background(), "ws", two.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Refreshed || second.RetryAfter <= 0 {
		t.Fatalf("different-agent poll should be throttled: %#v", second)
	}

	time.Sleep(50 * time.Millisecond)
	third, err := m.Poll(context.Background(), "ws", two.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !third.Refreshed {
		t.Fatalf("different-agent poll after interval should refresh: %#v", third)
	}
}

func TestManagerWaitSleepsUntilPollIntervalAllowsRefresh(t *testing.T) {
	provider := newFakeProvider()
	provider.pollBusy = true
	m := newTestManager(t, provider, Options{
		SameAgentPollInterval:      60 * time.Millisecond,
		DifferentAgentPollInterval: 20 * time.Millisecond,
	})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one", Instruction: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Poll(context.Background(), "ws", created.Subagent.ID); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.pollBusy = false
	provider.mu.Unlock()

	started := time.Now()
	waited, err := m.Wait(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 45*time.Millisecond {
		t.Fatalf("wait ignored same-agent poll interval: %s", elapsed)
	}
	if !waited.Refreshed || waited.Subagent.Status != StatusReady {
		t.Fatalf("unexpected waited result: %#v", waited)
	}
	provider.mu.Lock()
	pollCount := provider.pollCount
	waitCount := provider.waitCount
	provider.mu.Unlock()
	if pollCount != 2 || waitCount != 0 {
		t.Fatalf("provider poll/wait counts = %d/%d, want 2/0", pollCount, waitCount)
	}
}

func TestManagerSerializesSameAgent(t *testing.T) {
	provider := newFakeProvider()
	provider.entered = make(chan string, 2)
	provider.release = make(chan struct{})
	m := newTestManager(t, provider, Options{MaxConcurrency: 4})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	markReady(t, m, "ws", created.Subagent.ID)

	done := make(chan error, 2)
	go func() {
		_, err := m.Send(context.Background(), "ws", created.Subagent.ID, "a")
		done <- err
	}()
	select {
	case <-provider.entered:
	case <-time.After(time.Second):
		t.Fatal("first send did not enter provider")
	}
	go func() {
		_, err := m.Send(context.Background(), "ws", created.Subagent.ID, "b")
		done <- err
	}()
	select {
	case <-provider.entered:
		t.Fatal("second send entered provider before first completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrBusy) {
		t.Fatalf("second send error = %v, want busy", err)
	}
	provider.mu.Lock()
	max := provider.maxByID["conv-one"]
	provider.mu.Unlock()
	if max != 1 {
		t.Fatalf("same conversation concurrency = %d, want 1", max)
	}
}

func TestManagerAllowsDifferentAgentsToRunConcurrently(t *testing.T) {
	provider := newFakeProvider()
	provider.entered = make(chan string, 2)
	provider.release = make(chan struct{})
	m := newTestManager(t, provider, Options{MaxConcurrency: 2})
	one, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "two"})
	if err != nil {
		t.Fatal(err)
	}
	markReady(t, m, "ws", one.Subagent.ID)
	markReady(t, m, "ws", two.Subagent.ID)
	done := make(chan error, 2)
	go func() { _, err := m.Send(context.Background(), "ws", one.Subagent.ID, "a"); done <- err }()
	go func() { _, err := m.Send(context.Background(), "ws", two.Subagent.ID, "b"); done <- err }()
	for range 2 {
		select {
		case <-provider.entered:
		case <-time.After(time.Second):
			t.Fatal("different agents did not enter provider concurrently")
		}
	}
	provider.mu.Lock()
	maxGlobal := provider.maxGlobal
	provider.mu.Unlock()
	if maxGlobal != 2 {
		t.Fatalf("global concurrency = %d, want 2", maxGlobal)
	}
	close(provider.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagerBoundsSameAgentQueueAndExposesBusy(t *testing.T) {
	provider := newFakeProvider()
	provider.entered = make(chan string, 3)
	provider.release = make(chan struct{})
	m := newTestManager(t, provider, Options{MaxConcurrency: 4, MaxPendingPerAgent: 1})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	markReady(t, m, "ws", created.Subagent.ID)
	done := make(chan error, 2)
	go func() { _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "first"); done <- err }()
	select {
	case <-provider.entered:
	case <-time.After(time.Second):
		t.Fatal("first send did not enter provider")
	}
	agent, err := m.Get("ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != StatusBusy {
		t.Fatalf("status while sending = %q, want busy", agent.Status)
	}
	go func() { _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "second"); done <- err }()
	entry, err := m.entry("ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for len(entry.queue) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(entry.queue) != 2 {
		t.Fatalf("queue occupancy = %d, want 2", len(entry.queue))
	}
	if _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "third"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("third send error = %v, want queue full", err)
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrBusy) {
		t.Fatalf("queued send error = %v, want busy", err)
	}
}

func TestManagerQueuedSendHonorsContextCancellation(t *testing.T) {
	provider := newFakeProvider()
	provider.entered = make(chan string, 1)
	provider.release = make(chan struct{})
	m := newTestManager(t, provider, Options{MaxConcurrency: 2, MaxPendingPerAgent: 2})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	markReady(t, m, "ws", created.Subagent.ID)
	firstDone := make(chan error, 1)
	go func() { _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "first"); firstDone <- err }()
	<-provider.entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := m.Send(ctx, "ws", created.Subagent.ID, "second"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued send error = %v", err)
	}
	close(provider.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestManagerRecoversReloadedBusyAgentBeforePollThenSend(t *testing.T) {
	base := filepath.Join(t.TempDir(), "subagents")
	store := NewStore(base)
	provider := newFakeProvider()
	m, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterProvider("fake", provider); err != nil {
		t.Fatal(err)
	}
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	reloadedProvider := newFakeProvider()
	reloaded, err := NewManager(store, Options{DefaultProvider: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.RegisterProvider("fake", reloadedProvider); err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Send(context.Background(), "ws", created.Subagent.ID, "must not replay"); !errors.Is(err, ErrBusy) {
		t.Fatalf("send before reconciling persisted busy state = %v, want busy", err)
	}
	reloadedProvider.mu.Lock()
	if reloadedProvider.sendCount != 0 {
		t.Fatalf("provider send count before poll = %d, want 0", reloadedProvider.sendCount)
	}
	reloadedProvider.mu.Unlock()
	polled, err := reloaded.Poll(context.Background(), "ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if polled.Subagent.Status != StatusReady {
		t.Fatalf("poll after restart status = %q, want ready", polled.Subagent.Status)
	}
	if _, err := reloaded.Send(context.Background(), "ws", created.Subagent.ID, "after restart"); err != nil {
		t.Fatal(err)
	}
	reloadedProvider.mu.Lock()
	recoverCount := reloadedProvider.recoverCount
	sendCount := reloadedProvider.sendCount
	reloadedProvider.mu.Unlock()
	if recoverCount != 1 || sendCount != 1 {
		t.Fatalf("recover/send counts = %d/%d, want 1/1", recoverCount, sendCount)
	}
	agent, err := reloaded.Get("ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.ProviderState.Hints["last_message"] != "after restart" {
		t.Fatalf("unexpected recovered state: %#v", agent.ProviderState)
	}
	if agent.Status != StatusBusy {
		t.Fatalf("status after async send = %q, want busy", agent.Status)
	}
}

func TestManagerOutcomeUnknownMarksAgentDegradedWithoutChangingConversation(t *testing.T) {
	provider := newFakeProvider()
	provider.sendErr = NewOutcomeUnknownError(errors.New("reply lost"))
	m := newTestManager(t, provider, Options{})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	markReady(t, m, "ws", created.Subagent.ID)
	_, err = m.Send(context.Background(), "ws", created.Subagent.ID, "hello")
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("send error = %v, want outcome unknown", err)
	}
	agent, err := m.Get("ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != StatusDegraded || agent.ProviderState.ConversationID != "conv-one" {
		t.Fatalf("unexpected degraded state: %#v", agent)
	}
}

func TestManagerProviderErrorPreservesStateAndCanRecover(t *testing.T) {
	provider := newFakeProvider()
	m := newTestManager(t, provider, Options{})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	markReady(t, m, "ws", created.Subagent.ID)
	provider.sendErr = errors.New("provider unavailable")
	if _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "hello"); err == nil {
		t.Fatal("expected send error")
	}
	agent, err := m.Get("ws", created.Subagent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != StatusDegraded || agent.ProviderState.ConversationID != "conv-one" {
		t.Fatalf("unexpected degraded agent: %#v", agent)
	}
	provider.sendErr = nil
	if _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "retry"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("send while degraded error = %v, want not ready", err)
	}
	markReady(t, m, "ws", created.Subagent.ID)
	if _, err := m.Send(context.Background(), "ws", created.Subagent.ID, "retry"); err != nil {
		t.Fatal(err)
	}
	agent, _ = m.Get("ws", created.Subagent.ID)
	if agent.Status != StatusBusy || agent.LastError != "" || agent.ProviderState.ConversationID != "conv-one" {
		t.Fatalf("unexpected recovered agent: %#v", agent)
	}
}

func TestManagerCloseIsIdempotentAndWorkspaceScoped(t *testing.T) {
	provider := newFakeProvider()
	m := newTestManager(t, provider, Options{})
	created, err := m.Create(context.Background(), CreateRequest{WorkspaceID: "ws-a", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("ws-b", created.Subagent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace get error = %v", err)
	}
	if _, err := m.CloseAgent(context.Background(), "ws-a", created.Subagent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CloseAgent(context.Background(), "ws-a", created.Subagent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Send(context.Background(), "ws-a", created.Subagent.ID, "after close"); !errors.Is(err, ErrClosed) {
		t.Fatalf("send after close error = %v", err)
	}
	provider.mu.Lock()
	closeCount := provider.closeCount
	provider.mu.Unlock()
	if closeCount != 1 {
		t.Fatalf("provider close count = %d, want 1", closeCount)
	}
}

func TestStoreRejectsMismatchedID(t *testing.T) {
	base := filepath.Join(t.TempDir(), "subagents")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "sg_good.json"), []byte(`{"id":"sg_other","provider":"fake"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(base).Load(); err == nil {
		t.Fatal("expected mismatched id error")
	}
}
