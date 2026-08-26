package subagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound               = errors.New("subagent not found")
	ErrProviderNotFound       = errors.New("subagent provider not found")
	ErrClosed                 = errors.New("subagent is closed")
	ErrManagerClosed          = errors.New("subagent manager is closed")
	ErrMaxAgents              = errors.New("subagent limit reached")
	ErrQueueFull        error = queueFullError{}
)

type Options struct {
	DefaultProvider            string
	MaxAgents                  int
	MaxConcurrency             int
	MaxPendingPerAgent         int
	SameAgentPollInterval      time.Duration
	DifferentAgentPollInterval time.Duration
}

type agentEntry struct {
	mu            sync.Mutex
	agent         Subagent
	serial        chan struct{}
	queue         chan struct{}
	needsRecovery bool
}

type Manager struct {
	mu                         sync.RWMutex
	pollMu                     sync.Mutex
	store                      *Store
	providers                  map[string]Provider
	agents                     map[string]*agentEntry
	conversationOwners         map[string]string
	defaultProvider            string
	maxAgents                  int
	maxPendingPerAgent         int
	sameAgentPollInterval      time.Duration
	differentAgentPollInterval time.Duration
	lastPollByAgent            map[string]time.Time
	lastPollAgentID            string
	lastPollCompletedAt        time.Time
	pollSerial                 chan struct{}
	creating                   int
	sem                        chan struct{}
	closed                     bool
}

func NewManager(store *Store, options Options) (*Manager, error) {
	if store == nil {
		return nil, errors.New("subagent store is required")
	}
	if options.MaxAgents <= 0 {
		options.MaxAgents = 32
	}
	if options.MaxConcurrency <= 0 {
		options.MaxConcurrency = 4
	}
	if options.MaxPendingPerAgent <= 0 {
		options.MaxPendingPerAgent = 8
	}
	loaded, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load subagents: %w", err)
	}
	m := &Manager{
		store:                      store,
		providers:                  map[string]Provider{},
		agents:                     make(map[string]*agentEntry, len(loaded)),
		conversationOwners:         make(map[string]string, len(loaded)),
		defaultProvider:            strings.TrimSpace(options.DefaultProvider),
		maxAgents:                  options.MaxAgents,
		sameAgentPollInterval:      options.SameAgentPollInterval,
		differentAgentPollInterval: options.DifferentAgentPollInterval,
		lastPollByAgent:            make(map[string]time.Time),
		pollSerial:                 make(chan struct{}, 1),
		sem:                        make(chan struct{}, options.MaxConcurrency),
	}
	for _, agent := range loaded {
		needsRecovery := agent.Status != StatusClosed
		key := conversationKey(agent.Provider, agent.ProviderState.ConversationID)
		if key != "" {
			if ownerID, exists := m.conversationOwners[key]; exists {
				conflict := &ConversationConflictError{ConversationID: agent.ProviderState.ConversationID, OwnerID: ownerID}
				agent.Status = StatusDegraded
				agent.LastError = conflict.Error()
				agent.LastErrorCode = errorCode(conflict)
				agent.UpdatedAt = time.Now().UTC()
				needsRecovery = false
				if err := store.Save(agent); err != nil {
					return nil, fmt.Errorf("persist duplicate conversation quarantine for %s: %w", agent.ID, err)
				}
			} else {
				m.conversationOwners[key] = agent.ID
			}
		}
		m.agents[agent.ID] = newAgentEntry(agent, options.MaxPendingPerAgent, needsRecovery)
	}
	m.maxPendingPerAgent = options.MaxPendingPerAgent
	return m, nil
}

func (m *Manager) RegisterProvider(name string, provider Provider) error {
	name = strings.TrimSpace(name)
	if name == "" || provider == nil {
		return errors.New("subagent provider name and implementation are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrManagerClosed
	}
	if _, exists := m.providers[name]; exists {
		return fmt.Errorf("subagent provider %q already registered", name)
	}
	m.providers[name] = provider
	return nil
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	providerName := strings.TrimSpace(req.Provider)
	if providerName == "" {
		providerName = m.defaultProvider
	}
	provider, err := m.reserveCreate(providerName)
	if err != nil {
		return CreateResult{}, err
	}
	defer m.finishCreateReservation()
	if err := m.acquire(ctx); err != nil {
		return CreateResult{}, err
	}
	state, err := provider.Create(ctx, ProviderCreateRequest{
		Name:        strings.TrimSpace(req.Name),
		Instruction: req.Instruction,
		Options:     cloneStringMap(req.Options),
	})
	m.release()
	if err != nil {
		return CreateResult{}, err
	}
	now := time.Now().UTC()
	agent := Subagent{
		ID:            m.newID(),
		Name:          strings.TrimSpace(req.Name),
		SubagentType:  strings.TrimSpace(req.SubagentType),
		Provider:      providerName,
		Status:        StatusBusy,
		Instruction:   req.Instruction,
		ProviderState: cloneProviderState(state),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if ownerID, claimErr := m.claimConversation(agent.Provider, agent.ProviderState.ConversationID, agent.ID); claimErr != nil {
		m.markConversationOwnerDegraded(ownerID, claimErr)
		return CreateResult{}, claimErr
	}
	claimed := conversationKey(agent.Provider, agent.ProviderState.ConversationID) != ""
	if err := m.store.Save(agent); err != nil {
		if claimed {
			m.releaseConversationClaim(agent.Provider, agent.ProviderState.ConversationID, agent.ID)
		}
		_ = provider.Close(context.Background(), state)
		return CreateResult{}, fmt.Errorf("persist subagent: %w", err)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		if claimed {
			m.releaseConversationClaim(agent.Provider, agent.ProviderState.ConversationID, agent.ID)
		}
		_ = provider.Close(context.Background(), state)
		return CreateResult{}, ErrManagerClosed
	}
	m.agents[agent.ID] = newAgentEntry(agent, m.maxPendingPerAgent, false)
	m.mu.Unlock()
	return CreateResult{Subagent: cloneSubagent(agent)}, nil
}

func (m *Manager) Send(ctx context.Context, id, message string) (Subagent, error) {
	if strings.TrimSpace(message) == "" {
		return Subagent{}, errors.New("subagent message is required")
	}
	entry, err := m.entry(id)
	if err != nil {
		return Subagent{}, err
	}
	if err := m.ensureConversationOwner(id, entry); err != nil {
		return Subagent{}, err
	}
	if err := entry.reserveQueue(); err != nil {
		return Subagent{}, err
	}
	defer entry.releaseQueue()
	if err := entry.acquireSerial(ctx); err != nil {
		return Subagent{}, err
	}
	defer entry.releaseSerial()

	entry.mu.Lock()
	if entry.agent.Status == StatusClosed {
		entry.mu.Unlock()
		return Subagent{}, ErrClosed
	}
	if entry.agent.Status == StatusBusy {
		entry.mu.Unlock()
		return Subagent{}, ErrBusy
	}
	if entry.agent.Status != StatusReady {
		entry.mu.Unlock()
		return Subagent{}, ErrNotReady
	}
	providerName := entry.agent.Provider
	state := cloneProviderState(entry.agent.ProviderState)
	needsRecovery := entry.needsRecovery
	entry.mu.Unlock()

	provider, err := m.provider(providerName)
	if err != nil {
		m.markEntryFailure(entry, err)
		return Subagent{}, err
	}
	state, err = m.recoverEntry(ctx, entry, provider, state, needsRecovery)
	if err != nil {
		return Subagent{}, err
	}

	entry.mu.Lock()
	entry.agent.Status = StatusBusy
	entry.agent.Artifacts = nil
	entry.agent.LastError = ""
	entry.agent.LastErrorCode = ""
	entry.agent.UpdatedAt = time.Now().UTC()
	busySnapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	if err := m.store.Save(busySnapshot); err != nil {
		entry.mu.Lock()
		entry.agent.Status = StatusReady
		entry.mu.Unlock()
		return Subagent{}, fmt.Errorf("persist subagent busy state: %w", err)
	}
	if err := m.acquire(ctx); err != nil {
		m.restoreEntryReady(entry)
		return Subagent{}, err
	}
	nextState, callErr := provider.Send(ctx, state, message)
	m.release()
	if callErr != nil {
		if errors.Is(callErr, ErrOutcomeUnknown) && nextState.ConversationID != "" && nextState.ConversationID == state.ConversationID {
			entry.mu.Lock()
			entry.agent.ProviderState = cloneProviderState(nextState)
			entry.mu.Unlock()
		}
		m.markEntryFailure(entry, callErr)
		return Subagent{}, callErr
	}
	entry.mu.Lock()
	entry.agent.ProviderState = cloneProviderState(nextState)
	entry.agent.Status = StatusBusy
	entry.agent.LastError = ""
	entry.agent.LastErrorCode = ""
	entry.agent.UpdatedAt = time.Now().UTC()
	snapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	if err := m.store.Save(snapshot); err != nil {
		return Subagent{}, fmt.Errorf("persist subagent: %w", err)
	}
	return snapshot, nil
}

func (m *Manager) Poll(ctx context.Context, id string) (ObserveResult, error) {
	return m.observe(ctx, id, false)
}

func (m *Manager) Wait(ctx context.Context, id string) (ObserveResult, error) {
	return m.observe(ctx, id, true)
}

func (m *Manager) observe(ctx context.Context, id string, wait bool) (ObserveResult, error) {
	entry, err := m.entry(id)
	if err != nil {
		return ObserveResult{}, err
	}
	if err := m.ensureConversationOwner(id, entry); err != nil {
		return ObserveResult{}, err
	}

	for {
		// Observation requests are deliberately non-queuing at both the global
		// poll gate and the per-agent serial gate. A long-running send/wait must
		// not accumulate poll/get callers that consume upstream task workers.
		retryAfter, acquired, slotErr := m.acquirePollSlot(ctx, id, false)
		if slotErr != nil {
			return ObserveResult{}, slotErr
		}
		if !acquired {
			if retryAfter <= 0 {
				retryAfter = m.observationContentionRetryAfter()
			}
			if !wait {
				return cachedObserveResult(entry, retryAfter), nil
			}
			if err := waitContext(ctx, retryAfter); err != nil {
				return ObserveResult{}, err
			}
			continue
		}

		if !entry.tryAcquireSerial() {
			m.releasePollSlot(id, false)
			retryAfter = m.observationContentionRetryAfter()
			if !wait {
				return cachedObserveResult(entry, retryAfter), nil
			}
			if err := waitContext(ctx, retryAfter); err != nil {
				return ObserveResult{}, err
			}
			continue
		}

		result, attempted, refreshErr := m.refreshEntry(ctx, entry)
		entry.releaseSerial()
		m.releasePollSlot(id, attempted)
		if refreshErr != nil {
			return ObserveResult{}, refreshErr
		}
		if !wait || result.Subagent.Status != StatusBusy {
			return result, nil
		}
	}
}

func (m *Manager) refreshEntry(ctx context.Context, entry *agentEntry) (ObserveResult, bool, error) {
	entry.mu.Lock()
	if entry.agent.Status == StatusClosed {
		entry.mu.Unlock()
		return ObserveResult{}, false, ErrClosed
	}
	providerName := entry.agent.Provider
	state := cloneProviderState(entry.agent.ProviderState)
	needsRecovery := entry.needsRecovery
	entry.mu.Unlock()

	provider, err := m.provider(providerName)
	if err != nil {
		m.markEntryFailure(entry, err)
		return ObserveResult{}, false, err
	}
	state, err = m.recoverEntry(ctx, entry, provider, state, needsRecovery)
	if err != nil {
		return ObserveResult{}, false, err
	}

	if err := m.acquire(ctx); err != nil {
		return ObserveResult{}, false, err
	}
	nextState, observation, pollErr := provider.Poll(ctx, state)
	m.release()
	if pollErr != nil {
		m.markEntryFailure(entry, pollErr)
		return ObserveResult{}, true, pollErr
	}

	observedAt := time.Now().UTC()
	entry.mu.Lock()
	entry.agent.ProviderState = cloneProviderState(nextState)
	entry.agent.Artifacts = cloneArtifacts(observation.Reply.Artifacts)
	if observation.Generating {
		entry.agent.Status = StatusBusy
	} else {
		entry.agent.Status = StatusReady
	}
	entry.agent.LastError = ""
	entry.agent.LastErrorCode = ""
	entry.agent.UpdatedAt = observedAt
	entry.agent.LastObservedAt = observedAt
	snapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	if err := m.store.Save(snapshot); err != nil {
		return ObserveResult{}, true, fmt.Errorf("persist subagent observation: %w", err)
	}
	return ObserveResult{Subagent: snapshot, Reply: observation.Reply, Refreshed: true}, true, nil
}

func cachedObserveResult(entry *agentEntry, retryAfter time.Duration) ObserveResult {
	entry.mu.Lock()
	snapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	return ObserveResult{Subagent: snapshot, Refreshed: false, RetryAfter: retryAfter}
}

func (m *Manager) observationContentionRetryAfter() time.Duration {
	retryAfter := m.concurrentPollRetryAfter()
	if retryAfter <= 0 || retryAfter > 250*time.Millisecond {
		return 100 * time.Millisecond
	}
	return retryAfter
}

func (m *Manager) Get(id string) (Subagent, error) {
	entry, err := m.entry(id)
	if err != nil {
		return Subagent{}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return cloneSubagent(entry.agent), nil
}

func (m *Manager) List() ([]Subagent, error) {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrManagerClosed
	}
	entries := make([]*agentEntry, 0, len(m.agents))
	for _, entry := range m.agents {
		entries = append(entries, entry)
	}
	m.mu.RUnlock()
	out := make([]Subagent, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		out = append(out, cloneSubagent(entry.agent))
		entry.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *Manager) CloseAgent(ctx context.Context, id string) (Subagent, error) {
	entry, err := m.entry(id)
	if err != nil {
		return Subagent{}, err
	}
	if err := m.ensureConversationOwner(id, entry); err != nil {
		return Subagent{}, err
	}
	if err := entry.acquireSerial(ctx); err != nil {
		return Subagent{}, err
	}
	defer entry.releaseSerial()
	entry.mu.Lock()
	if entry.agent.Status == StatusClosed {
		agent := cloneSubagent(entry.agent)
		entry.mu.Unlock()
		return agent, nil
	}
	providerName := entry.agent.Provider
	state := cloneProviderState(entry.agent.ProviderState)
	entry.mu.Unlock()
	provider, err := m.provider(providerName)
	if err != nil {
		return Subagent{}, err
	}
	if err := m.acquire(ctx); err != nil {
		return Subagent{}, err
	}
	callErr := provider.Close(ctx, state)
	m.release()
	entry.mu.Lock()
	if callErr != nil {
		entry.agent.UpdatedAt = time.Now().UTC()
		entry.agent.Status = StatusDegraded
		entry.agent.LastError = callErr.Error()
		snapshot := cloneSubagent(entry.agent)
		entry.mu.Unlock()
		_ = m.store.Save(snapshot)
		return snapshot, callErr
	}
	entry.agent.Status = StatusClosed
	entry.agent.LastError = ""
	entry.agent.LastErrorCode = ""
	entry.agent.UpdatedAt = time.Now().UTC()
	entry.needsRecovery = false
	snapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	if err := m.store.Save(snapshot); err != nil {
		return Subagent{}, fmt.Errorf("persist subagent: %w", err)
	}
	return snapshot, nil
}

// Archive lets the provider perform its configured archival step and only then
// removes the LocalGate sg_* record. A provider may intentionally leave the
// remote conversation in place when no archive destination is configured. Any
// remote or verification failure preserves the local mapping so the operation
// can be inspected or retried safely.
func (m *Manager) Archive(ctx context.Context, id string) (Subagent, error) {
	entry, err := m.entry(id)
	if err != nil {
		return Subagent{}, err
	}
	if err := m.ensureConversationOwner(id, entry); err != nil {
		return Subagent{}, err
	}
	if err := entry.reserveQueue(); err != nil {
		return Subagent{}, err
	}
	defer entry.releaseQueue()
	if err := entry.acquireSerial(ctx); err != nil {
		return Subagent{}, err
	}
	defer entry.releaseSerial()

	entry.mu.Lock()
	providerName := entry.agent.Provider
	state := cloneProviderState(entry.agent.ProviderState)
	needsRecovery := entry.needsRecovery
	entry.mu.Unlock()

	provider, err := m.provider(providerName)
	if err != nil {
		m.markEntryFailure(entry, err)
		return Subagent{}, err
	}
	archiver, ok := provider.(Archiver)
	if !ok {
		return Subagent{}, ErrArchiveUnsupported
	}
	state, err = m.recoverEntry(ctx, entry, provider, state, needsRecovery)
	if err != nil {
		return Subagent{}, err
	}

	// A logical close does not stop an already-running remote generation. Poll
	// immediately before the move so archive never knowingly relocates a turn
	// whose outcome is still in flight.
	if _, _, err := m.acquirePollSlot(ctx, id, true); err != nil {
		return Subagent{}, err
	}
	if err := m.acquire(ctx); err != nil {
		m.releasePollSlot(id, false)
		return Subagent{}, err
	}
	polledState, observation, pollErr := provider.Poll(ctx, state)
	m.release()
	m.releasePollSlot(id, true)
	if pollErr != nil {
		m.markEntryFailure(entry, pollErr)
		return Subagent{}, pollErr
	}
	state = cloneProviderState(polledState)
	entry.mu.Lock()
	entry.agent.ProviderState = cloneProviderState(state)
	entry.agent.LastError = ""
	entry.agent.LastErrorCode = ""
	entry.agent.UpdatedAt = time.Now().UTC()
	if entry.agent.Status != StatusClosed {
		if observation.Generating {
			entry.agent.Status = StatusBusy
		} else {
			entry.agent.Status = StatusReady
		}
	}
	preArchive := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	if err := m.store.Save(preArchive); err != nil {
		return Subagent{}, fmt.Errorf("persist subagent before archive: %w", err)
	}
	if observation.Generating {
		return preArchive, ErrArchiveBusy
	}

	if err := m.acquire(ctx); err != nil {
		return Subagent{}, err
	}
	archivedState, archiveErr := archiver.Archive(ctx, state)
	m.release()
	if archiveErr != nil {
		m.markEntryFailure(entry, archiveErr)
		return Subagent{}, archiveErr
	}

	entry.mu.Lock()
	entry.agent.ProviderState = cloneProviderState(archivedState)
	entry.agent.Status = StatusClosed
	entry.agent.LastError = ""
	entry.agent.LastErrorCode = ""
	entry.agent.UpdatedAt = time.Now().UTC()
	entry.needsRecovery = false
	archived := cloneSubagent(entry.agent)
	entry.mu.Unlock()

	// Persist the provider's final archive state before deletion. If deletion
	// itself fails, the retained record is safe to retry because providers are
	// required to make Archive idempotent, including the no-move case.
	if err := m.store.Save(archived); err != nil {
		return archived, fmt.Errorf("persist archived subagent: %w", err)
	}
	if err := m.store.Delete(id); err != nil {
		return archived, fmt.Errorf("delete archived subagent: %w", err)
	}

	m.mu.Lock()
	if current := m.agents[id]; current == entry {
		delete(m.agents, id)
		key := conversationKey(archived.Provider, archived.ProviderState.ConversationID)
		if key != "" && m.conversationOwners[key] == id {
			delete(m.conversationOwners, key)
		}
	}
	m.mu.Unlock()
	return archived, nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) reserveCreate(providerName string) (Provider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if providerName == "" {
		return nil, ErrProviderNotFound
	}
	provider := m.providers[providerName]
	if provider == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, providerName)
	}
	if len(m.agents)+m.creating >= m.maxAgents {
		return nil, ErrMaxAgents
	}
	m.creating++
	return provider, nil
}

func (m *Manager) finishCreateReservation() {
	m.mu.Lock()
	if m.creating > 0 {
		m.creating--
	}
	m.mu.Unlock()
}

func (m *Manager) entry(id string) (*agentEntry, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrNotFound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	entry := m.agents[id]
	if entry == nil {
		return nil, ErrNotFound
	}
	return entry, nil
}

func (m *Manager) provider(name string) (Provider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	provider := m.providers[name]
	if provider == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, name)
	}
	return provider, nil
}

func conversationKey(providerName, conversationID string) string {
	providerName = strings.TrimSpace(providerName)
	conversationID = strings.TrimSpace(conversationID)
	if providerName == "" || conversationID == "" {
		return ""
	}
	return providerName + "\x00" + conversationID
}

func (m *Manager) claimConversation(providerName, conversationID, agentID string) (string, error) {
	key := conversationKey(providerName, conversationID)
	if key == "" {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", ErrManagerClosed
	}
	if ownerID := m.conversationOwners[key]; ownerID != "" && ownerID != agentID {
		return ownerID, &ConversationConflictError{ConversationID: strings.TrimSpace(conversationID), OwnerID: ownerID}
	}
	m.conversationOwners[key] = agentID
	return agentID, nil
}

func (m *Manager) releaseConversationClaim(providerName, conversationID, agentID string) {
	key := conversationKey(providerName, conversationID)
	if key == "" {
		return
	}
	m.mu.Lock()
	if m.conversationOwners[key] == agentID {
		delete(m.conversationOwners, key)
	}
	m.mu.Unlock()
}

func (m *Manager) ensureConversationOwner(agentID string, entry *agentEntry) error {
	entry.mu.Lock()
	providerName := entry.agent.Provider
	conversationID := entry.agent.ProviderState.ConversationID
	entry.mu.Unlock()
	key := conversationKey(providerName, conversationID)
	if key == "" {
		return nil
	}
	m.mu.RLock()
	ownerID := m.conversationOwners[key]
	m.mu.RUnlock()
	if ownerID != "" && ownerID != agentID {
		conflict := &ConversationConflictError{ConversationID: strings.TrimSpace(conversationID), OwnerID: ownerID}
		m.markEntryFailure(entry, conflict)
		return conflict
	}
	return nil
}

func (m *Manager) markConversationOwnerDegraded(ownerID string, cause error) {
	if strings.TrimSpace(ownerID) == "" || cause == nil {
		return
	}
	m.mu.RLock()
	entry := m.agents[ownerID]
	m.mu.RUnlock()
	if entry != nil {
		m.markEntryFailure(entry, cause)
	}
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) release() {
	<-m.sem
}

func (m *Manager) acquirePollSlot(ctx context.Context, agentID string, wait bool) (time.Duration, bool, error) {
	for {
		delay := m.pollDelay(agentID, time.Now())
		if delay > 0 {
			if !wait {
				return delay, false, nil
			}
			if err := waitContext(ctx, delay); err != nil {
				return 0, false, err
			}
			continue
		}

		if wait {
			select {
			case m.pollSerial <- struct{}{}:
			case <-ctx.Done():
				return 0, false, ctx.Err()
			}
		} else {
			select {
			case m.pollSerial <- struct{}{}:
			default:
				return m.concurrentPollRetryAfter(), false, nil
			}
		}

		delay = m.pollDelay(agentID, time.Now())
		if delay <= 0 {
			return 0, true, nil
		}
		<-m.pollSerial
		if !wait {
			return delay, false, nil
		}
		if err := waitContext(ctx, delay); err != nil {
			return 0, false, err
		}
	}
}

func (m *Manager) releasePollSlot(agentID string, attempted bool) {
	m.pollMu.Lock()
	if attempted {
		now := time.Now()
		m.lastPollByAgent[agentID] = now
		m.lastPollAgentID = agentID
		m.lastPollCompletedAt = now
	}
	<-m.pollSerial
	m.pollMu.Unlock()
}

func (m *Manager) concurrentPollRetryAfter() time.Duration {
	if m.sameAgentPollInterval > m.differentAgentPollInterval {
		return m.sameAgentPollInterval
	}
	return m.differentAgentPollInterval
}

func (m *Manager) pollDelay(agentID string, now time.Time) time.Duration {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()

	var earliest time.Time
	if m.sameAgentPollInterval > 0 {
		if last := m.lastPollByAgent[agentID]; !last.IsZero() {
			earliest = last.Add(m.sameAgentPollInterval)
		}
	}
	if m.differentAgentPollInterval > 0 && m.lastPollAgentID != "" && m.lastPollAgentID != agentID && !m.lastPollCompletedAt.IsZero() {
		cross := m.lastPollCompletedAt.Add(m.differentAgentPollInterval)
		if cross.After(earliest) {
			earliest = cross
		}
	}
	if earliest.IsZero() || !earliest.After(now) {
		return 0
	}
	return earliest.Sub(now)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newAgentEntry(agent Subagent, maxPending int, needsRecovery bool) *agentEntry {
	return &agentEntry{
		agent:         cloneSubagent(agent),
		serial:        make(chan struct{}, 1),
		queue:         make(chan struct{}, maxPending+1),
		needsRecovery: needsRecovery,
	}
}

func (e *agentEntry) reserveQueue() error {
	select {
	case e.queue <- struct{}{}:
		return nil
	default:
		return ErrQueueFull
	}
}

func (e *agentEntry) releaseQueue() { <-e.queue }

func (e *agentEntry) acquireSerial(ctx context.Context) error {
	select {
	case e.serial <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *agentEntry) tryAcquireSerial() bool {
	select {
	case e.serial <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *agentEntry) releaseSerial() { <-e.serial }

func (m *Manager) recoverEntry(ctx context.Context, entry *agentEntry, provider Provider, state ProviderState, needsRecovery bool) (ProviderState, error) {
	if !needsRecovery {
		return state, nil
	}
	if err := m.acquire(ctx); err != nil {
		return ProviderState{}, err
	}
	recovered, recoverErr := provider.Recover(ctx, state)
	m.release()
	if recoverErr != nil {
		m.markEntryFailure(entry, recoverErr)
		return ProviderState{}, recoverErr
	}
	state = cloneProviderState(recovered)
	entry.mu.Lock()
	entry.agent.ProviderState = cloneProviderState(state)
	entry.agent.LastError = ""
	entry.agent.LastErrorCode = ""
	entry.agent.UpdatedAt = time.Now().UTC()
	entry.needsRecovery = false
	recoveredSnapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	if err := m.store.Save(recoveredSnapshot); err != nil {
		persistErr := fmt.Errorf("persist recovered subagent: %w", err)
		m.markEntryFailure(entry, persistErr)
		return ProviderState{}, persistErr
	}
	return state, nil
}

func (m *Manager) markEntryFailure(entry *agentEntry, cause error) {
	entry.mu.Lock()
	entry.agent.Status = StatusDegraded
	entry.agent.LastError = cause.Error()
	entry.agent.LastErrorCode = errorCode(cause)
	entry.agent.UpdatedAt = time.Now().UTC()
	snapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	_ = m.store.Save(snapshot)
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return strings.TrimSpace(coded.ErrorCode())
	}
	return ""
}

func (m *Manager) restoreEntryReady(entry *agentEntry) {
	entry.mu.Lock()
	changed := false
	if entry.agent.Status == StatusBusy {
		entry.agent.Status = StatusReady
		entry.agent.UpdatedAt = time.Now().UTC()
		changed = true
	}
	snapshot := cloneSubagent(entry.agent)
	entry.mu.Unlock()
	if changed {
		_ = m.store.Save(snapshot)
	}
}

func (m *Manager) newID() string {
	for {
		var raw [12]byte
		if _, err := rand.Read(raw[:]); err != nil {
			panic(fmt.Sprintf("generate subagent id: %v", err))
		}
		id := "sg_" + hex.EncodeToString(raw[:])
		m.mu.RLock()
		_, exists := m.agents[id]
		m.mu.RUnlock()
		if !exists {
			return id
		}
	}
}

func cloneSubagent(agent Subagent) Subagent {
	agent.ProviderState = cloneProviderState(agent.ProviderState)
	agent.Artifacts = cloneArtifacts(agent.Artifacts)
	return agent
}

func cloneArtifacts(input []Artifact) []Artifact {
	if input == nil {
		return nil
	}
	out := make([]Artifact, len(input))
	copy(out, input)
	return out
}

func cloneProviderState(state ProviderState) ProviderState {
	state.Hints = cloneStringMap(state.Hints)
	return state
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
