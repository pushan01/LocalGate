package opencli_chatgpt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"localgate/internal/subagent"
	"localgate/internal/subagent/providers/processutil"
)

const (
	Name                      = "opencli_chatgpt"
	defaultTimeout            = 120 * time.Second
	waitProbeChunk            = 9 * time.Second
	maxCapturedBytes          = 8 << 20
	pendingHashHint           = "opencli_chatgpt.pending_message_sha256" // v1 compatibility only
	pendingFingerprintHint    = "opencli_chatgpt.pending_message_fingerprint_v2"
	pendingFingerprintLenHint = "opencli_chatgpt.pending_message_fingerprint_len_v2"
	pendingAfterIndexHint     = "opencli_chatgpt.pending_after_index"
	lastObservedIndexHint     = "opencli_chatgpt.last_observed_index"
	maxFingerprintSuffixRunes = 32
)

type ErrorKind string

const (
	ErrorConfiguration ErrorKind = "configuration"
	ErrorAuthRequired  ErrorKind = "auth_required"
	ErrorConnectorAuth ErrorKind = "connector_auth_required"
	ErrorChallenge     ErrorKind = "challenge"
	ErrorQuota         ErrorKind = "quota"
	ErrorTimeout       ErrorKind = "timeout"
	ErrorCanceled      ErrorKind = "canceled"
	ErrorExecution     ErrorKind = "execution_failed"
	ErrorInvalidOutput ErrorKind = "invalid_output"
)

type ProviderError struct {
	Kind             ErrorKind
	Message          string
	ExitCode         int
	Stderr           string
	Cause            error
	MayHaveSubmitted bool
}

func (e *ProviderError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func (e *ProviderError) Unwrap() error { return e.Cause }

func (e *ProviderError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return string(e.Kind)
}

type Config struct {
	Executable        string
	AdapterExecutable string
	Timeout           time.Duration
	SubmitInterval    time.Duration
	DefaultProject    string
	ArchiveProject    string
}

type Provider struct {
	executable        string
	adapterExecutable string
	timeout           time.Duration
	submitInterval    time.Duration
	defaultProject    string
	archiveProject    string
	surfaceSerial     chan struct{}
	submitSerial      chan struct{}
	lastSubmitDoneAt  time.Time
}

func New(cfg Config) (*Provider, error) {
	executable, err := resolveExecutable(cfg.Executable)
	if err != nil {
		return nil, err
	}
	adapterExecutable := ""
	if strings.TrimSpace(cfg.AdapterExecutable) != "" {
		adapterExecutable, err = resolveExecutable(cfg.AdapterExecutable)
		if err != nil {
			return nil, err
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	projectID := ""
	if strings.TrimSpace(cfg.DefaultProject) != "" {
		projectID, err = normalizeProjectID(cfg.DefaultProject)
		if err != nil {
			return nil, &ProviderError{Kind: ErrorConfiguration, Message: err.Error(), Cause: err}
		}
	}
	archiveProjectID := ""
	if strings.TrimSpace(cfg.ArchiveProject) != "" {
		archiveProjectID, err = normalizeProjectID(cfg.ArchiveProject)
		if err != nil {
			return nil, &ProviderError{Kind: ErrorConfiguration, Message: err.Error(), Cause: err}
		}
	}
	return &Provider{
		executable:        executable,
		adapterExecutable: adapterExecutable,
		timeout:           timeout,
		submitInterval:    cfg.SubmitInterval,
		defaultProject:    projectID,
		archiveProject:    archiveProjectID,
		surfaceSerial:     make(chan struct{}, 1),
		submitSerial:      make(chan struct{}, 1),
	}, nil
}

func (p *Provider) Create(ctx context.Context, req subagent.ProviderCreateRequest) (subagent.ProviderState, error) {
	prompt := strings.TrimSpace(req.Instruction)
	if prompt == "" {
		return subagent.ProviderState{}, &ProviderError{
			Kind: ErrorConfiguration, Message: "opencli ChatGPT subagent instruction is required",
		}
	}
	projectID := p.defaultProject
	if raw := strings.TrimSpace(req.Options["project"]); raw != "" {
		var err error
		projectID, err = normalizeProjectID(raw)
		if err != nil {
			return subagent.ProviderState{}, &ProviderError{Kind: ErrorConfiguration, Message: err.Error(), Cause: err}
		}
	}
	args := []string{"chatgpt", "ask"}
	if projectID != "" {
		args = append(args, "--project", projectID)
	} else {
		args = append(args, "--new")
	}
	args = append(args, "--wait", "false", "--format", "json", "--", prompt)
	payload, err := p.runSubmit(ctx, args)
	if err != nil {
		if mayHaveSubmitted(err) {
			return subagent.ProviderState{}, subagent.NewOutcomeUnknownError(err)
		}
		return subagent.ProviderState{}, err
	}
	row, err := parseAskRow(payload)
	if err != nil {
		return subagent.ProviderState{}, subagent.NewOutcomeUnknownError(&ProviderError{
			Kind: ErrorInvalidOutput, Message: err.Error(), Cause: err, MayHaveSubmitted: true,
		})
	}
	state, err := stateFromRow(row, projectID)
	if err != nil {
		return subagent.ProviderState{}, subagent.NewOutcomeUnknownError(err)
	}
	state.Hints = withPendingMarker(state.Hints, prompt)
	return state, nil
}

func (p *Provider) Send(ctx context.Context, state subagent.ProviderState, message string) (subagent.ProviderState, error) {
	conversationID := strings.TrimSpace(state.ConversationID)
	if conversationID == "" {
		return subagent.ProviderState{}, &ProviderError{
			Kind: ErrorConfiguration, Message: "opencli ChatGPT conversation id is required",
		}
	}
	prompt := strings.TrimSpace(message)
	if prompt == "" {
		return subagent.ProviderState{}, &ProviderError{
			Kind: ErrorConfiguration, Message: "opencli ChatGPT message is required",
		}
	}
	pendingState := cloneState(state)
	pendingState.Hints = withPendingMarker(pendingState.Hints, prompt)
	payload, err := p.runSubmit(ctx, []string{
		"chatgpt", "send",
		"--conversation", conversationID,
		"--format", "json",
		"--", prompt,
	})
	if err != nil {
		if mayHaveSubmitted(err) {
			return pendingState, subagent.NewOutcomeUnknownError(err)
		}
		return state, err
	}
	row, err := parseSendRow(payload)
	if err != nil {
		return pendingState, subagent.NewOutcomeUnknownError(&ProviderError{
			Kind: ErrorInvalidOutput, Message: err.Error(), Cause: err, MayHaveSubmitted: true,
		})
	}
	if !strings.EqualFold(strings.TrimSpace(row.Status), "success") {
		return pendingState, subagent.NewOutcomeUnknownError(&ProviderError{
			Kind: ErrorInvalidOutput, Message: fmt.Sprintf("opencli ChatGPT send returned status %q", row.Status), MayHaveSubmitted: true,
		})
	}
	return pendingState, nil
}

func (p *Provider) Poll(ctx context.Context, state subagent.ProviderState) (subagent.ProviderState, subagent.Observation, error) {
	return p.observe(ctx, state, 0)
}

func (p *Provider) Wait(ctx context.Context, state subagent.ProviderState) (subagent.ProviderState, subagent.Observation, error) {
	waitCtx := ctx
	cancel := func() {}
	if p.timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, p.timeout)
	}
	defer cancel()

	current := cloneState(state)
	for {
		chunk := waitProbeChunk
		if deadline, ok := waitCtx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return current, subagent.Observation{}, waitCtx.Err()
			}
			if remaining < chunk {
				chunk = remaining
			}
		}

		next, observation, err := p.observe(waitCtx, current, chunk)
		if err == nil {
			return next, observation, nil
		}
		if providerErrorKind(err) != ErrorTimeout || waitCtx.Err() != nil {
			return current, subagent.Observation{}, err
		}

		// OpenCLI's detail --wait cannot see connector authorization cards because
		// they live outside data-message-author-role turns. After each bounded
		// wait slice, take one non-blocking observation so a stalled turn can be
		// probed for reconnect-required UI instead of waiting the full provider
		// timeout.
		next, observation, err = p.observe(waitCtx, current, 0)
		if err != nil {
			return next, observation, err
		}
		current = next
		if !observation.Generating {
			return next, observation, nil
		}
	}
}

func (p *Provider) observe(ctx context.Context, state subagent.ProviderState, waitFor time.Duration) (subagent.ProviderState, subagent.Observation, error) {
	conversationID := strings.TrimSpace(state.ConversationID)
	if conversationID == "" {
		return state, subagent.Observation{}, &ProviderError{Kind: ErrorConfiguration, Message: "opencli ChatGPT conversation id is required"}
	}
	args := []string{"chatgpt", "detail", conversationID}
	if p.adapterExecutable != "" {
		args = []string{"chatgpt", "inspect", conversationID}
	} else if waitFor > 0 {
		seconds := int((waitFor + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		args = append(args, "--wait", "true", "--timeout", strconv.Itoa(seconds))
	}
	args = append(args, "--format", "json")
	payload, err := p.run(ctx, args, false)
	if err != nil {
		return state, subagent.Observation{}, err
	}
	rows, err := parseDetailRows(payload)
	if err != nil {
		return state, subagent.Observation{}, &ProviderError{Kind: ErrorInvalidOutput, Message: err.Error(), Cause: err}
	}
	next, observation := observationFromRows(state, rows)
	if observation.Generating && !rowsGenerating(rows) && hasPendingMarker(state.Hints) {
		blocker, probeErr := p.probeConversationBlocker(ctx, state)
		if probeErr == nil && blocker.Blocked {
			message := "ChatGPT connector authorization has expired; open the subagent ChatGPT conversation and complete authorization to continue"
			if blocker.Connector != "" {
				message = fmt.Sprintf("ChatGPT connector %s authorization has expired; open the subagent ChatGPT conversation and complete authorization to continue", blocker.Connector)
			}
			return next, observation, &ProviderError{Kind: ErrorConnectorAuth, Message: message}
		}
	}
	return next, observation, nil
}

type conversationBlocker struct {
	Blocked   bool   `json:"blocked"`
	Kind      string `json:"kind"`
	Connector string `json:"connector"`
	Evidence  string `json:"evidence"`
}

func (p *Provider) probeConversationBlocker(ctx context.Context, state subagent.ProviderState) (conversationBlocker, error) {
	conversationID := strings.TrimSpace(state.ConversationID)
	if conversationID == "" {
		return conversationBlocker{}, nil
	}
	conversationURL := strings.TrimSpace(state.ConversationURL)
	if conversationURL == "" {
		conversationURL = "https://chatgpt.com/c/" + url.PathEscape(conversationID)
	}
	sum := sha256.Sum256([]byte(conversationID))
	session := fmt.Sprintf("localgate-subagent-probe-%x", sum[:6])
	if _, err := p.run(ctx, []string{"browser", session, "open", conversationURL, "--window", "background"}, false); err != nil {
		return conversationBlocker{}, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = p.run(closeCtx, []string{"browser", session, "close"}, false)
	}()

	payload, err := p.run(ctx, []string{"browser", session, "eval", connectorBlockerProbeJS}, false)
	if err != nil {
		return conversationBlocker{}, err
	}
	var blocker conversationBlocker
	if err := json.Unmarshal(bytes.TrimSpace(payload), &blocker); err != nil {
		return conversationBlocker{}, fmt.Errorf("decode opencli ChatGPT connector blocker probe: %w", err)
	}
	return blocker, nil
}

const connectorBlockerProbeJS = `(() => {
    const normalize = (value) => String(value || '').replace(/\u00a0/g, ' ').replace(/\s+/g, ' ').trim();
    const visible = (el) => {
        if (!(el instanceof HTMLElement)) return false;
        const style = window.getComputedStyle(el);
        if (style.display === 'none' || style.visibility === 'hidden') return false;
        const rect = el.getBoundingClientRect();
        return rect.width > 0 && rect.height > 0;
    };
    const labelOf = (el) => normalize(el.getAttribute('aria-label') || el.innerText || el.textContent || '');
    const reconnectLabel = (value) => /^(?:重新连接|重新授权|Reconnect|Reauthorize)$/i.test(value);
    const expiryText = (value) => /连接已过期|连接(?:已经)?失效|授权已过期|connection\s+(?:to\s+.+?\s+)?has\s+expired|connection\s+expired|authorization\s+(?:has\s+)?expired|reconnect.+(?:use|continue)/i.test(value);
    const buttons = Array.from(document.querySelectorAll('button,[role="button"]')).filter(visible);
    for (const button of buttons) {
        if (!reconnectLabel(labelOf(button))) continue;
        let node = button;
        for (let depth = 0; node && depth < 10; depth += 1, node = node.parentElement) {
            const text = normalize(node.innerText || node.textContent || '');
            if (!expiryText(text)) continue;
            let connector = '';
            let match = text.match(/你与\s*([^\s，。]{1,80})\s*的连接(?:已过期|已经失效|失效)/);
            if (!match) match = text.match(/重新连接\s+([^\s，。]{1,80})/);
            if (!match) match = text.match(/(?:your\s+)?connection\s+to\s+(.{1,80}?)\s+has\s+expired/i);
            if (!match) match = text.match(/reconnect\s+([^\s,.]{1,80})/i);
            if (match) connector = normalize(match[1]);
            return {
                blocked: true,
                kind: 'connector_auth_required',
                connector,
                evidence: text.slice(0, 600),
            };
        }
    }
    return { blocked: false, kind: '', connector: '', evidence: '' };
})()`

func rowsGenerating(rows []detailRow) bool {
	for _, row := range rows {
		if row.Generating {
			return true
		}
	}
	return false
}

func hasPendingMarker(hints map[string]string) bool {
	if len(hints) == 0 {
		return false
	}
	return strings.TrimSpace(hints[pendingAfterIndexHint]) != "" ||
		strings.TrimSpace(hints[pendingHashHint]) != ""
}

func (p *Provider) Recover(_ context.Context, state subagent.ProviderState) (subagent.ProviderState, error) {
	if strings.TrimSpace(state.ConversationID) == "" {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorConfiguration, Message: "opencli ChatGPT conversation id is required"}
	}
	return state, nil
}

func (p *Provider) Close(context.Context, subagent.ProviderState) error {
	// Closing a LocalGate subagent is intentionally logical-only. The ChatGPT
	// conversation remains in the user's Project/history unless explicitly
	// managed by a future provider-specific lifecycle operation.
	return nil
}

func (p *Provider) Archive(ctx context.Context, state subagent.ProviderState) (subagent.ProviderState, error) {
	conversationID := strings.TrimSpace(state.ConversationID)
	if conversationID == "" {
		return state, &ProviderError{Kind: ErrorConfiguration, Message: "opencli ChatGPT conversation id is required"}
	}
	if p.archiveProject == "" {
		// archive_project is optional. Without one, LocalGate archive is a
		// local lifecycle operation only: keep the remote conversation where it
		// already is and let Manager remove the sg_* mapping after its preflight
		// Poll confirms the current turn is no longer generating.
		return cloneState(state), nil
	}
	payload, err := p.run(ctx, []string{
		"chatgpt", "project-move", conversationID,
		"--project", p.archiveProject,
		"--format", "json",
	}, true)
	if err != nil {
		if mayHaveSubmitted(err) {
			return state, subagent.NewOutcomeUnknownError(err)
		}
		return state, err
	}
	row, err := parseProjectMoveRow(payload)
	if err != nil {
		return state, subagent.NewOutcomeUnknownError(&ProviderError{
			Kind: ErrorInvalidOutput, Message: err.Error(), Cause: err, MayHaveSubmitted: true,
		})
	}
	if strings.TrimSpace(row.ConversationID) != conversationID {
		err := &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT project-move conversationId does not match requested conversation", MayHaveSubmitted: true}
		return state, subagent.NewOutcomeUnknownError(err)
	}
	projectID, normalizeErr := normalizeProjectID(row.ProjectID)
	if normalizeErr != nil || projectID != p.archiveProject {
		err := &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT project-move projectId does not match archive_project", Cause: normalizeErr, MayHaveSubmitted: true}
		return state, subagent.NewOutcomeUnknownError(err)
	}
	if !row.Verified {
		err := &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT project-move did not provide verified Project membership", MayHaveSubmitted: true}
		return state, subagent.NewOutcomeUnknownError(err)
	}
	membershipProjectID, membershipErr := normalizeProjectID(row.MembershipProjectID)
	if membershipErr != nil || membershipProjectID != p.archiveProject {
		err := &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT project-move membershipProjectId does not match archive_project", Cause: membershipErr, MayHaveSubmitted: true}
		return state, subagent.NewOutcomeUnknownError(err)
	}
	conversationURL := strings.TrimSpace(row.URL)
	if conversationURL == "" {
		err := &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT project-move JSON is missing url", MayHaveSubmitted: true}
		return state, subagent.NewOutcomeUnknownError(err)
	}
	urlConversationID, urlProjectID, parseErr := parseConversationURL(conversationURL)
	if parseErr != nil || urlConversationID != conversationID || urlProjectID != p.archiveProject {
		err := &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT project-move returned an invalid archive conversation URL", Cause: parseErr, MayHaveSubmitted: true}
		return state, subagent.NewOutcomeUnknownError(err)
	}
	next := cloneState(state)
	next.ConversationURL = conversationURL
	next.ProjectID = p.archiveProject
	return next, nil
}

type askRow struct {
	ConversationID  string `json:"conversationId"`
	ConversationURL string `json:"conversationUrl"`
	Tool            string `json:"tool"`
	Response        string `json:"response"`
}

type sendRow struct {
	Status       string `json:"Status"`
	InjectedText string `json:"InjectedText"`
}

type projectMoveRow struct {
	ConversationID      string `json:"conversationId"`
	ProjectID           string `json:"projectId"`
	MembershipProjectID string `json:"membershipProjectId"`
	Verified            bool   `json:"verified"`
	URL                 string `json:"url"`
	Status              string `json:"status"`
}

type detailRow struct {
	Index         int    `json:"Index"`
	Role          string `json:"Role"`
	Text          string `json:"Text"`
	Generating    bool   `json:"Generating"`
	StableSeconds int    `json:"StableSeconds"`
}

func (p *Provider) run(ctx context.Context, args []string, maySubmit bool) ([]byte, error) {
	runCtx := ctx
	cancel := func() {}
	if p.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, p.timeout)
	}
	defer cancel()

	// All ChatGPT adapter commands use OpenCLI's persistent site session and can
	// navigate the same browser tab, including nominally read-only `detail`.
	// Serialize browser driving at the provider boundary so create/send/poll
	// cannot navigate that shared surface out from under one another. Remote
	// ChatGPT generations still overlap because create/send return after submit.
	// The operation deadline starts before this acquisition so callers cannot
	// wait forever behind a wedged OpenCLI process.
	select {
	case p.surfaceSerial <- struct{}{}:
		defer func() { <-p.surfaceSerial }()
	case <-runCtx.Done():
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, &ProviderError{Kind: ErrorTimeout, Message: "opencli ChatGPT command timed out waiting for provider surface", Cause: runCtx.Err()}
		}
		return nil, &ProviderError{Kind: ErrorCanceled, Message: "opencli ChatGPT command canceled while waiting for provider surface", Cause: runCtx.Err()}
	}

	var stdout, stderr limitedBuffer
	stdout.max = maxCapturedBytes
	stderr.max = 256 << 10
	executable := p.executable
	useBundledAdapter := len(args) >= 2 && args[0] == "chatgpt" && (args[1] == "project-move" || args[1] == "inspect") && p.adapterExecutable != ""
	if useBundledAdapter {
		executable = p.adapterExecutable
	}
	cmd := exec.CommandContext(runCtx, executable, args...)
	processutil.ConfigureCommand(cmd)
	if useBundledAdapter {
		cmd.Env = append(os.Environ(), "LOCALGATE_BROWSER_EXECUTABLE="+p.executable)
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if runCtx.Err() != nil {
			return nil, runCtx.Err()
		}
		return nil, &ProviderError{Kind: ErrorExecution, Message: "start opencli ChatGPT command", Cause: err}
	}
	err := cmd.Wait()
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, &ProviderError{Kind: ErrorTimeout, Message: "opencli ChatGPT command timed out", Cause: runCtx.Err(), Stderr: stderr.String(), MayHaveSubmitted: maySubmit}
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			return nil, &ProviderError{Kind: ErrorCanceled, Message: "opencli ChatGPT command canceled", Cause: runCtx.Err(), Stderr: stderr.String(), MayHaveSubmitted: maySubmit}
		}
		exitCode := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		kind := classifyFailure(stderr.String())
		mayHaveSubmitted := maySubmit && kind != ErrorAuthRequired && kind != ErrorChallenge && kind != ErrorQuota
		return nil, &ProviderError{
			Kind: kind, Message: failureMessage(kind), ExitCode: exitCode, Stderr: stderr.String(), Cause: err, MayHaveSubmitted: mayHaveSubmitted,
		}
	}
	if stdout.truncated {
		return nil, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT JSON output exceeded capture limit", MayHaveSubmitted: maySubmit}
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func (p *Provider) runSubmit(ctx context.Context, args []string) ([]byte, error) {
	if p.submitInterval <= 0 {
		return p.run(ctx, args, true)
	}
	select {
	case p.submitSerial <- struct{}{}:
		defer func() { <-p.submitSerial }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if !p.lastSubmitDoneAt.IsZero() {
		remaining := p.submitInterval - time.Since(p.lastSubmitDoneAt)
		if remaining > 0 {
			timer := time.NewTimer(remaining)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	payload, err := p.run(ctx, args, true)
	p.lastSubmitDoneAt = time.Now()
	return payload, err
}

func parseAskRow(payload []byte) (askRow, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return askRow{}, errors.New("opencli ChatGPT returned empty JSON output")
	}
	var rows []askRow
	if payload[0] == '[' {
		if err := json.Unmarshal(payload, &rows); err != nil {
			return askRow{}, fmt.Errorf("decode opencli ChatGPT JSON: %w", err)
		}
		if len(rows) != 1 {
			return askRow{}, fmt.Errorf("opencli ChatGPT returned %d rows, expected 1", len(rows))
		}
		return rows[0], nil
	}
	var row askRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return askRow{}, fmt.Errorf("decode opencli ChatGPT JSON: %w", err)
	}
	return row, nil
}

func parseSendRow(payload []byte) (sendRow, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return sendRow{}, errors.New("opencli ChatGPT send returned empty JSON output")
	}
	var rows []sendRow
	if payload[0] == '[' {
		if err := json.Unmarshal(payload, &rows); err != nil {
			return sendRow{}, fmt.Errorf("decode opencli ChatGPT send JSON: %w", err)
		}
		if len(rows) != 1 {
			return sendRow{}, fmt.Errorf("opencli ChatGPT send returned %d rows, expected 1", len(rows))
		}
		return rows[0], nil
	}
	var row sendRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return sendRow{}, fmt.Errorf("decode opencli ChatGPT send JSON: %w", err)
	}
	return row, nil
}

func parseProjectMoveRow(payload []byte) (projectMoveRow, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return projectMoveRow{}, errors.New("opencli ChatGPT project-move returned empty JSON output")
	}
	var rows []projectMoveRow
	if payload[0] == '[' {
		if err := json.Unmarshal(payload, &rows); err != nil {
			return projectMoveRow{}, fmt.Errorf("decode opencli ChatGPT project-move JSON: %w", err)
		}
		if len(rows) != 1 {
			return projectMoveRow{}, fmt.Errorf("opencli ChatGPT project-move returned %d rows, expected 1", len(rows))
		}
		return rows[0], nil
	}
	var row projectMoveRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return projectMoveRow{}, fmt.Errorf("decode opencli ChatGPT project-move JSON: %w", err)
	}
	return row, nil
}

func parseDetailRows(payload []byte) ([]detailRow, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil, errors.New("opencli ChatGPT detail returned empty JSON output")
	}
	var rows []detailRow
	if payload[0] == '[' {
		if err := json.Unmarshal(payload, &rows); err != nil {
			return nil, fmt.Errorf("decode opencli ChatGPT detail JSON: %w", err)
		}
	} else {
		var row detailRow
		if err := json.Unmarshal(payload, &row); err != nil {
			return nil, fmt.Errorf("decode opencli ChatGPT detail JSON: %w", err)
		}
		rows = []detailRow{row}
	}
	if len(rows) == 0 {
		return nil, errors.New("opencli ChatGPT detail returned no messages")
	}
	for _, row := range rows {
		if row.Index <= 0 || (row.Role != "User" && row.Role != "Assistant") {
			return nil, errors.New("opencli ChatGPT detail returned an invalid message row")
		}
	}
	return rows, nil
}

func observationFromRows(state subagent.ProviderState, rows []detailRow) (subagent.ProviderState, subagent.Observation) {
	next := cloneState(state)
	highestIndex := 0
	for _, row := range rows {
		if row.Index > highestIndex {
			highestIndex = row.Index
		}
	}
	if next.Hints == nil {
		next.Hints = map[string]string{}
	}
	next.Hints[lastObservedIndexHint] = strconv.Itoa(highestIndex)

	// v2 pending state is anchored to the highest message index observed before
	// submission. This prevents a stale detail read immediately after send from
	// mistaking an older completed Assistant turn for the new turn's result.
	if baseline, ok := parseIndexHint(state.Hints[pendingAfterIndexHint]); ok {
		fingerprint := strings.TrimSpace(state.Hints[pendingFingerprintHint])
		fingerprintLen, hasFingerprintLen := parseIndexHint(state.Hints[pendingFingerprintLenHint])
		matchedUserIndex := 0
		var candidateUserIndexes []int
		for _, row := range rows {
			if row.Index <= baseline || row.Role != "User" {
				continue
			}
			candidateUserIndexes = append(candidateUserIndexes, row.Index)
			if fingerprint != "" && messageFingerprint(row.Text) != fingerprint &&
				(!hasFingerprintLen || !messageFingerprintPrefixMatches(row.Text, fingerprint, fingerprintLen)) {
				continue
			}
			if row.Index > matchedUserIndex {
				matchedUserIndex = row.Index
			}
		}

		// Persisted v2 states created before pendingFingerprintLenHint existed
		// cannot validate a DOM-appended suffix (for example ChatGPT's localized
		// expand affordance on long prompts). Recover only when exactly one new
		// User turn exists after the submission baseline and it already has a
		// completed Assistant response. This preserves stale-read protection while
		// allowing old stuck states to self-heal without replaying the send.
		if matchedUserIndex == 0 && fingerprint != "" && !hasFingerprintLen && len(candidateUserIndexes) == 1 {
			candidate := candidateUserIndexes[0]
			_, assistantIndex := assistantAfter(rows, candidate)
			generating := false
			for _, row := range rows {
				if row.Index > baseline && row.Generating {
					generating = true
					break
				}
			}
			if !generating && assistantIndex > candidate {
				matchedUserIndex = candidate
			}
		}

		latestAssistant, latestAssistantIndex := assistantAfter(rows, matchedUserIndex)
		generating := false
		for _, row := range rows {
			if row.Index > baseline && row.Generating {
				generating = true
				break
			}
		}
		pending := generating || matchedUserIndex == 0 || latestAssistantIndex <= matchedUserIndex
		if !pending {
			clearPendingHints(next.Hints)
		}
		if len(next.Hints) == 0 {
			next.Hints = nil
		}
		return next, subagent.Observation{Generating: pending, Reply: subagent.Reply{Text: latestAssistant}}
	}

	// v1 compatibility. Older persisted states only carry a hash of the exact
	// submitted string. First try that legacy match. If ChatGPT rendering removed
	// Markdown syntax (for example backticks), reconcile from the latest completed
	// User->Assistant pair. New submissions never use this fallback because they
	// always carry pendingAfterIndexHint above.
	pendingHash := strings.TrimSpace(state.Hints[pendingHashHint])
	matchedUserIndex := 0
	if pendingHash != "" {
		for _, row := range rows {
			if row.Role == "User" && hashMessage(row.Text) == pendingHash {
				matchedUserIndex = row.Index
			}
		}
	}
	if pendingHash != "" && matchedUserIndex == 0 {
		matchedUserIndex = latestUserIndex(rows)
	}
	latestAssistant, latestAssistantIndex := assistantAfter(rows, matchedUserIndex)
	generating := false
	for _, row := range rows {
		generating = generating || row.Generating
	}
	pending := generating
	if pendingHash != "" {
		pending = pending || matchedUserIndex == 0 || latestAssistantIndex <= matchedUserIndex
	} else if len(rows) > 0 && rows[len(rows)-1].Role == "User" {
		pending = true
	}
	if !pending {
		clearPendingHints(next.Hints)
	}
	if len(next.Hints) == 0 {
		next.Hints = nil
	}
	return next, subagent.Observation{Generating: pending, Reply: subagent.Reply{Text: latestAssistant}}
}

func withPendingMarker(hints map[string]string, message string) map[string]string {
	out := cloneHints(hints)
	if out == nil {
		out = map[string]string{}
	}
	baseline := 0
	if parsed, ok := parseIndexHint(out[lastObservedIndexHint]); ok {
		baseline = parsed
	}
	out[pendingAfterIndexHint] = strconv.Itoa(baseline)
	out[pendingFingerprintHint] = messageFingerprint(message)
	out[pendingFingerprintLenHint] = strconv.Itoa(len([]rune(normalizeFingerprintText(message))))
	delete(out, pendingHashHint)
	return out
}

func clearPendingHints(hints map[string]string) {
	delete(hints, pendingHashHint)
	delete(hints, pendingFingerprintHint)
	delete(hints, pendingFingerprintLenHint)
	delete(hints, pendingAfterIndexHint)
}

func parseIndexHint(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func latestUserIndex(rows []detailRow) int {
	index := 0
	for _, row := range rows {
		if row.Role == "User" && row.Index > index {
			index = row.Index
		}
	}
	return index
}

func assistantAfter(rows []detailRow, userIndex int) (string, int) {
	if userIndex == 0 {
		return "", 0
	}
	text := ""
	index := 0
	for _, row := range rows {
		if row.Role == "Assistant" && row.Index > userIndex && row.Index >= index {
			index = row.Index
			text = row.Text
		}
	}
	return text, index
}

func hashMessage(message string) string {
	message = strings.ReplaceAll(message, "\u00a0", " ")
	message = spacesBeforeNewlinePattern.ReplaceAllString(message, "\n")
	message = repeatedNewlinePattern.ReplaceAllString(message, "\n\n")
	sum := sha256.Sum256([]byte(strings.TrimSpace(message)))
	return fmt.Sprintf("%x", sum[:])
}

func messageFingerprint(message string) string {
	normalized := normalizeFingerprintText(message)
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:])
}

func messageFingerprintPrefixMatches(message, fingerprint string, prefixLen int) bool {
	if prefixLen <= 0 {
		return false
	}
	normalized := []rune(normalizeFingerprintText(message))
	if len(normalized) < prefixLen || len(normalized)-prefixLen > maxFingerprintSuffixRunes {
		return false
	}
	sum := sha256.Sum256([]byte(string(normalized[:prefixLen])))
	return fmt.Sprintf("%x", sum[:]) == fingerprint
}

func normalizeFingerprintText(message string) string {
	message = html.UnescapeString(message)
	message = markdownLinkPattern.ReplaceAllString(message, "$1")
	message = strings.ToLower(message)
	var normalized strings.Builder
	normalized.Grow(len(message))
	for _, r := range message {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func cloneState(state subagent.ProviderState) subagent.ProviderState {
	state.Hints = cloneHints(state.Hints)
	return state
}

func stateFromRow(row askRow, projectID string) (subagent.ProviderState, error) {
	id := strings.TrimSpace(row.ConversationID)
	if id == "" {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT JSON is missing conversationId"}
	}
	conversationURL := strings.TrimSpace(row.ConversationURL)
	if conversationURL != "" {
		urlID, urlProject, err := parseConversationURL(conversationURL)
		if err != nil {
			return subagent.ProviderState{}, &ProviderError{Kind: ErrorInvalidOutput, Message: err.Error(), Cause: err}
		}
		if urlID != id {
			return subagent.ProviderState{}, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT conversationUrl id does not match conversationId"}
		}
		if projectID != "" && urlProject != "" && urlProject != projectID {
			return subagent.ProviderState{}, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli ChatGPT conversationUrl project does not match requested project"}
		}
	}
	return subagent.ProviderState{ConversationID: id, ConversationURL: conversationURL, ProjectID: projectID}, nil
}

var projectPathPattern = regexp.MustCompile(`^/g/g-p-([a-fA-F0-9]{8,})(?:[-/]|$)`)
var conversationPathPattern = regexp.MustCompile(`^/(?:g/g-p-([a-fA-F0-9]{8,})(?:-[^/]*)?/)?c/([A-Za-z0-9_-]{8,})/?$`)
var spacesBeforeNewlinePattern = regexp.MustCompile(`[ \t]+\n`)
var repeatedNewlinePattern = regexp.MustCompile(`\n{3,}`)
var markdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)

func normalizeProjectID(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "g-p-") {
		raw = strings.TrimPrefix(raw, "g-p-")
		if idx := strings.IndexByte(raw, '-'); idx >= 0 {
			raw = raw[:idx]
		}
	}
	if regexp.MustCompile(`^[a-fA-F0-9]{8,}$`).MatchString(raw) {
		return strings.ToLower(raw), nil
	}
	u, err := url.Parse(raw)
	if err == nil && u.Scheme == "" && strings.HasPrefix(raw, "/") {
		u, err = url.Parse("https://chatgpt.com" + raw)
	}
	if err != nil || u == nil || u.Scheme != "https" || !trustedChatGPTHost(u.Hostname()) {
		return "", fmt.Errorf("invalid ChatGPT project %q", value)
	}
	match := projectPathPattern.FindStringSubmatch(u.Path)
	if len(match) != 2 {
		return "", fmt.Errorf("invalid ChatGPT project %q", value)
	}
	return strings.ToLower(match[1]), nil
}

func parseConversationURL(raw string) (conversationID, projectID string, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !trustedChatGPTHost(u.Hostname()) {
		return "", "", fmt.Errorf("invalid ChatGPT conversationUrl %q", raw)
	}
	match := conversationPathPattern.FindStringSubmatch(u.Path)
	if len(match) != 3 {
		return "", "", fmt.Errorf("invalid ChatGPT conversationUrl %q", raw)
	}
	return match[2], strings.ToLower(match[1]), nil
}

func trustedChatGPTHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "chatgpt.com" || strings.HasSuffix(host, ".chatgpt.com")
}

func resolveExecutable(value string) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		raw = "opencli"
	}
	resolved, err := exec.LookPath(raw)
	if err != nil {
		return "", &ProviderError{Kind: ErrorConfiguration, Message: fmt.Sprintf("resolve opencli executable %q: %v", raw, err), Cause: err}
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", &ProviderError{Kind: ErrorConfiguration, Message: "resolve opencli executable path", Cause: err}
	}
	if real, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = real
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", &ProviderError{Kind: ErrorConfiguration, Message: fmt.Sprintf("opencli executable is not an executable regular file: %s", resolved), Cause: err}
	}
	return resolved, nil
}

func classifyFailure(stderr string) ErrorKind {
	text := strings.ToLower(stderr)
	switch {
	case strings.Contains(text, "captcha"), strings.Contains(text, "challenge"):
		return ErrorChallenge
	case strings.Contains(text, "authrequired"), strings.Contains(text, "auth required"), strings.Contains(text, "log in"), strings.Contains(text, "logged-in"), strings.Contains(text, "401"), strings.Contains(text, "403"):
		return ErrorAuthRequired
	case strings.Contains(text, "quota"), strings.Contains(text, "rate limit"), strings.Contains(text, "usage limit"), strings.Contains(text, "usage cap"):
		return ErrorQuota
	case strings.Contains(text, "did not finish or stabilize before timeout"), strings.Contains(text, "timed out"), strings.Contains(text, "timeout"):
		return ErrorTimeout
	default:
		return ErrorExecution
	}
}

func failureMessage(kind ErrorKind) string {
	switch kind {
	case ErrorChallenge:
		return "ChatGPT browser session requires challenge handling"
	case ErrorAuthRequired:
		return "ChatGPT browser session requires login"
	case ErrorConnectorAuth:
		return "ChatGPT connector authorization has expired; open the subagent ChatGPT conversation and complete authorization to continue"
	case ErrorQuota:
		return "ChatGPT browser session is quota limited"
	case ErrorTimeout:
		return "opencli ChatGPT command timed out"
	default:
		return "opencli ChatGPT command failed"
	}
}

func mayHaveSubmitted(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.MayHaveSubmitted
}

func providerErrorKind(err error) ErrorKind {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Kind
	}
	return ""
}

type limitedBuffer struct {
	bytes.Buffer
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.max - b.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || original > 0
		return original, nil
	}
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		b.truncated = true
		return original, nil
	}
	_, _ = b.Buffer.Write(p)
	return original, nil
}

func cloneHints(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
