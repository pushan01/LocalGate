package opencli_gemini

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	Name                      = "opencli_gemini"
	defaultModel              = "Pro"
	defaultTimeout            = 120 * time.Second
	maxCapturedBytes          = 8 << 20
	pendingFingerprintHint    = "opencli_gemini.pending_message_fingerprint_v1"
	pendingFingerprintLenHint = "opencli_gemini.pending_message_fingerprint_len_v1"
	lastObservedTurnCountHint = "opencli_gemini.last_observed_turn_count"
	maxFingerprintSuffixRunes = 32
	defaultSubmissionTimeout  = 8
	maxArtifactBytes          = 16 << 20
)

type ErrorKind string

const (
	ErrorConfiguration ErrorKind = "configuration"
	ErrorAuthRequired  ErrorKind = "auth_required"
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
	BrowserExecutable string
	ArtifactDir       string
	DefaultModel      string
	Timeout           time.Duration
	SubmitInterval    time.Duration
}

type Provider struct {
	executable        string
	browserExecutable string
	artifactDir       string
	defaultModel      string
	timeout           time.Duration
	submitInterval    time.Duration
	surfaceSerial     chan struct{}
	submitSerial      chan struct{}
	lastSubmitDoneAt  time.Time
}

func New(cfg Config) (*Provider, error) {
	executable, err := resolveExecutable(cfg.Executable)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	artifactDir := strings.TrimSpace(cfg.ArtifactDir)
	if artifactDir != "" {
		artifactDir, err = filepath.Abs(artifactDir)
		if err != nil {
			return nil, &ProviderError{Kind: ErrorConfiguration, Message: "resolve Gemini artifact directory", Cause: err}
		}
		if err := os.MkdirAll(artifactDir, 0o700); err != nil {
			return nil, &ProviderError{Kind: ErrorConfiguration, Message: "create Gemini artifact directory", Cause: err}
		}
	}
	configuredDefaultModel := strings.TrimSpace(cfg.DefaultModel)
	if configuredDefaultModel == "" {
		configuredDefaultModel = defaultModel
	}
	return &Provider{
		executable:        executable,
		browserExecutable: strings.TrimSpace(cfg.BrowserExecutable),
		artifactDir:       artifactDir,
		defaultModel:      configuredDefaultModel,
		timeout:           timeout,
		submitInterval:    cfg.SubmitInterval,
		surfaceSerial:     make(chan struct{}, 1),
		submitSerial:      make(chan struct{}, 1),
	}, nil
}

func (p *Provider) Create(ctx context.Context, req subagent.ProviderCreateRequest) (subagent.ProviderState, error) {
	prompt := strings.TrimSpace(req.Instruction)
	if prompt == "" {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorConfiguration, Message: "opencli Gemini subagent instruction is required"}
	}
	model := strings.TrimSpace(req.Options["model"])
	if model == "" {
		model = p.defaultModel
	}
	row, err := p.submit(ctx, "", prompt, true, model)
	if err != nil {
		return subagent.ProviderState{}, err
	}
	state, err := stateFromSubmitRow(row)
	if err != nil {
		return subagent.ProviderState{}, subagent.NewOutcomeUnknownError(err)
	}
	if strings.TrimSpace(state.Model) == "" {
		state.Model = model
	}
	state.Hints = withPendingMarker(state.Hints, prompt)
	return state, nil
}

func (p *Provider) Send(ctx context.Context, state subagent.ProviderState, message string) (subagent.ProviderState, error) {
	conversationID := strings.TrimSpace(state.ConversationID)
	if conversationID == "" {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorConfiguration, Message: "opencli Gemini conversation id is required"}
	}
	prompt := strings.TrimSpace(message)
	if prompt == "" {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorConfiguration, Message: "opencli Gemini message is required"}
	}
	pendingState := cloneState(state)
	pendingState.Hints = withPendingMarker(pendingState.Hints, prompt)
	row, err := p.submit(ctx, conversationID, prompt, false, "")
	if err != nil {
		if errors.Is(err, subagent.ErrOutcomeUnknown) {
			return pendingState, err
		}
		return state, err
	}
	if strings.TrimSpace(row.ConversationID) != conversationID {
		err := &ProviderError{
			Kind:             ErrorInvalidOutput,
			Message:          "opencli Gemini submit returned a different conversation id",
			MayHaveSubmitted: true,
		}
		return pendingState, subagent.NewOutcomeUnknownError(err)
	}
	pendingState.ConversationURL = strings.TrimSpace(row.ConversationURL)
	if model := strings.TrimSpace(row.Model); model != "" {
		pendingState.Model = model
	}
	return pendingState, nil
}

func (p *Provider) Poll(ctx context.Context, state subagent.ProviderState) (subagent.ProviderState, subagent.Observation, error) {
	conversationID := strings.TrimSpace(state.ConversationID)
	if conversationID == "" {
		return state, subagent.Observation{}, &ProviderError{Kind: ErrorConfiguration, Message: "opencli Gemini conversation id is required"}
	}
	payload, err := p.run(ctx, []string{"gemini", "inspect", conversationID, "--format", "json"}, false)
	if err != nil {
		return state, subagent.Observation{}, err
	}
	row, err := parseInspectRow(payload)
	if err != nil {
		return state, subagent.Observation{}, &ProviderError{Kind: ErrorInvalidOutput, Message: err.Error(), Cause: err}
	}
	if err := p.validateArtifacts(row); err != nil {
		return state, subagent.Observation{}, &ProviderError{Kind: ErrorInvalidOutput, Message: err.Error(), Cause: err}
	}
	if strings.TrimSpace(row.ConversationID) != conversationID {
		return state, subagent.Observation{}, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli Gemini inspect conversation id does not match requested conversation"}
	}
	return observationFromInspect(state, row)
}

func (p *Provider) Wait(ctx context.Context, state subagent.ProviderState) (subagent.ProviderState, subagent.Observation, error) {
	// Manager.Wait owns the polling cadence so all providers share the same
	// per-agent/global throttling policy. Keep this method non-blocking for the
	// Provider interface and for any direct callers.
	return p.Poll(ctx, state)
}

func (p *Provider) Recover(_ context.Context, state subagent.ProviderState) (subagent.ProviderState, error) {
	if strings.TrimSpace(state.ConversationID) == "" {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorConfiguration, Message: "opencli Gemini conversation id is required"}
	}
	return cloneState(state), nil
}

func (p *Provider) Close(context.Context, subagent.ProviderState) error {
	// Local close is intentionally logical-only. Gemini conversation history is
	// retained in the user's account.
	return nil
}

func (p *Provider) submit(ctx context.Context, conversationID, prompt string, fresh bool, model string) (submitRow, error) {
	args := []string{"gemini", "submit", prompt}
	if fresh {
		args = append(args, "--new", "true")
		if model = strings.TrimSpace(model); model != "" {
			args = append(args, "--model", model)
		}
	} else {
		args = append(args, "--conversation", conversationID)
	}
	args = append(args,
		"--submission-timeout", strconv.Itoa(defaultSubmissionTimeout),
		"--format", "json",
	)
	payload, err := p.runSubmit(ctx, args)
	if err != nil {
		if mayHaveSubmitted(err) {
			return submitRow{}, subagent.NewOutcomeUnknownError(err)
		}
		return submitRow{}, err
	}
	row, err := parseSubmitRow(payload)
	if err != nil {
		return submitRow{}, subagent.NewOutcomeUnknownError(&ProviderError{
			Kind:             ErrorInvalidOutput,
			Message:          err.Error(),
			Cause:            err,
			MayHaveSubmitted: true,
		})
	}
	if !strings.EqualFold(strings.TrimSpace(row.Status), "submitted") {
		return submitRow{}, subagent.NewOutcomeUnknownError(&ProviderError{
			Kind:             ErrorInvalidOutput,
			Message:          fmt.Sprintf("opencli Gemini submit returned status %q", row.Status),
			MayHaveSubmitted: true,
		})
	}
	return row, nil
}

type submitRow struct {
	Status          string `json:"Status"`
	ConversationID  string `json:"ConversationId"`
	ConversationURL string `json:"ConversationUrl"`
	Model           string `json:"Model"`
	Generating      bool   `json:"Generating"`
}

type inspectTurn struct {
	Index     int                 `json:"Index"`
	Role      string              `json:"Role"`
	Text      string              `json:"Text"`
	Artifacts []subagent.Artifact `json:"Artifacts,omitempty"`
}

type inspectRow struct {
	ConversationID  string        `json:"ConversationId"`
	ConversationURL string        `json:"ConversationUrl"`
	Generating      bool          `json:"Generating"`
	Turns           []inspectTurn `json:"Turns"`
}

func stateFromSubmitRow(row submitRow) (subagent.ProviderState, error) {
	id := strings.TrimSpace(row.ConversationID)
	if id == "" {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli Gemini submit JSON is missing ConversationId", MayHaveSubmitted: true}
	}
	url := strings.TrimSpace(row.ConversationURL)
	if url == "" || !strings.Contains(url, "/app/"+id) {
		return subagent.ProviderState{}, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli Gemini submit JSON has an invalid ConversationUrl", MayHaveSubmitted: true}
	}
	return subagent.ProviderState{ConversationID: id, ConversationURL: url, Model: strings.TrimSpace(row.Model)}, nil
}

func parseSubmitRow(payload []byte) (submitRow, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return submitRow{}, errors.New("opencli Gemini submit returned empty JSON output")
	}
	var rows []submitRow
	if payload[0] == '[' {
		if err := json.Unmarshal(payload, &rows); err != nil {
			return submitRow{}, fmt.Errorf("decode opencli Gemini submit JSON: %w", err)
		}
		if len(rows) != 1 {
			return submitRow{}, fmt.Errorf("opencli Gemini submit returned %d rows, expected 1", len(rows))
		}
		return rows[0], nil
	}
	var row submitRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return submitRow{}, fmt.Errorf("decode opencli Gemini submit JSON: %w", err)
	}
	return row, nil
}

func parseInspectRow(payload []byte) (inspectRow, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return inspectRow{}, errors.New("opencli Gemini inspect returned empty JSON output")
	}
	var rows []inspectRow
	if payload[0] == '[' {
		if err := json.Unmarshal(payload, &rows); err != nil {
			return inspectRow{}, fmt.Errorf("decode opencli Gemini inspect JSON: %w", err)
		}
		if len(rows) != 1 {
			return inspectRow{}, fmt.Errorf("opencli Gemini inspect returned %d rows, expected 1", len(rows))
		}
		if err := validateInspectRow(rows[0]); err != nil {
			return inspectRow{}, err
		}
		return rows[0], nil
	}
	var row inspectRow
	if err := json.Unmarshal(payload, &row); err != nil {
		return inspectRow{}, fmt.Errorf("decode opencli Gemini inspect JSON: %w", err)
	}
	if err := validateInspectRow(row); err != nil {
		return inspectRow{}, err
	}
	return row, nil
}

func validateInspectRow(row inspectRow) error {
	if strings.TrimSpace(row.ConversationID) == "" {
		return errors.New("opencli Gemini inspect JSON is missing ConversationId")
	}
	for i, turn := range row.Turns {
		if turn.Index <= 0 {
			return fmt.Errorf("opencli Gemini inspect turn %d has invalid Index", i)
		}
		if turn.Role != "User" && turn.Role != "Assistant" && turn.Role != "System" {
			return fmt.Errorf("opencli Gemini inspect turn %d has invalid Role %q", i, turn.Role)
		}
	}
	return nil
}

func observationFromInspect(state subagent.ProviderState, row inspectRow) (subagent.ProviderState, subagent.Observation, error) {
	next := cloneState(state)
	if url := strings.TrimSpace(row.ConversationURL); url != "" {
		next.ConversationURL = url
	}
	if next.Hints == nil {
		next.Hints = map[string]string{}
	}
	next.Hints[lastObservedTurnCountHint] = strconv.Itoa(len(row.Turns))

	fingerprint := strings.TrimSpace(state.Hints[pendingFingerprintHint])
	fingerprintLen, hasFingerprintLen := parsePositiveInt(state.Hints[pendingFingerprintLenHint])
	matchedUserIndex := 0
	if fingerprint != "" {
		for _, turn := range row.Turns {
			if turn.Role != "User" {
				continue
			}
			if messageFingerprint(turn.Text) != fingerprint &&
				(!hasFingerprintLen || !messageFingerprintPrefixMatches(turn.Text, fingerprint, fingerprintLen)) {
				continue
			}
			if turn.Index > matchedUserIndex {
				matchedUserIndex = turn.Index
			}
		}
	}

	latestAssistant, latestAssistantIndex := assistantAfter(row.Turns, matchedUserIndex)
	if fingerprint == "" {
		latestAssistant, latestAssistantIndex = latestAssistantTurn(row.Turns)
	}
	pending := row.Generating
	if fingerprint != "" {
		pending = pending || matchedUserIndex == 0 || latestAssistantIndex <= matchedUserIndex
	} else if len(row.Turns) > 0 && row.Turns[len(row.Turns)-1].Role == "User" {
		pending = true
	}

	if !pending {
		delete(next.Hints, pendingFingerprintHint)
		delete(next.Hints, pendingFingerprintLenHint)
	}
	if len(next.Hints) == 0 {
		next.Hints = nil
	}
	artifacts := artifactsForTurn(row.Turns, latestAssistantIndex)
	return next, subagent.Observation{
		Generating: pending,
		Reply: subagent.Reply{
			Text:      latestAssistant,
			Artifacts: artifacts,
		},
	}, nil
}

func artifactsForTurn(turns []inspectTurn, turnIndex int) []subagent.Artifact {
	if turnIndex <= 0 {
		return nil
	}
	for _, turn := range turns {
		if turn.Index == turnIndex && turn.Role == "Assistant" {
			if turn.Artifacts == nil {
				return nil
			}
			out := make([]subagent.Artifact, len(turn.Artifacts))
			copy(out, turn.Artifacts)
			return out
		}
	}
	return nil
}

func (p *Provider) validateArtifacts(row inspectRow) error {
	for turnIndex := range row.Turns {
		for artifactIndex := range row.Turns[turnIndex].Artifacts {
			artifact := &row.Turns[turnIndex].Artifacts[artifactIndex]
			if p.artifactDir == "" {
				return errors.New("opencli Gemini inspect returned artifacts while artifact storage is disabled")
			}
			if !regexp.MustCompile(`^art_[a-f0-9]{24}$`).MatchString(strings.TrimSpace(artifact.ID)) {
				return fmt.Errorf("opencli Gemini inspect artifact has invalid id %q", artifact.ID)
			}
			if artifact.Type != "image" || artifact.MIMEType != "image/png" {
				return fmt.Errorf("opencli Gemini inspect artifact %s has unsupported type or MIME", artifact.ID)
			}
			if artifact.Size <= 0 || artifact.Size > maxArtifactBytes {
				return fmt.Errorf("opencli Gemini inspect artifact %s has invalid size", artifact.ID)
			}
			if artifact.Width <= 0 || artifact.Height <= 0 {
				return fmt.Errorf("opencli Gemini inspect artifact %s has invalid dimensions", artifact.ID)
			}
			if filepath.Base(artifact.FileName) != artifact.FileName || strings.TrimSpace(artifact.FileName) == "" {
				return fmt.Errorf("opencli Gemini inspect artifact %s has invalid file name", artifact.ID)
			}
			artifactPath, err := filepath.Abs(strings.TrimSpace(artifact.Path))
			if err != nil {
				return fmt.Errorf("resolve opencli Gemini artifact %s path: %w", artifact.ID, err)
			}
			resolvedRoot, err := filepath.EvalSymlinks(p.artifactDir)
			if err != nil {
				return fmt.Errorf("resolve opencli Gemini artifact directory: %w", err)
			}
			resolvedPath, err := filepath.EvalSymlinks(artifactPath)
			if err != nil {
				return fmt.Errorf("resolve opencli Gemini artifact %s file path: %w", artifact.ID, err)
			}
			rel, err := filepath.Rel(resolvedRoot, resolvedPath)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				return fmt.Errorf("opencli Gemini inspect artifact %s path escapes artifact directory", artifact.ID)
			}
			info, err := os.Lstat(artifactPath)
			if err != nil {
				return fmt.Errorf("stat opencli Gemini artifact %s: %w", artifact.ID, err)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() != artifact.Size {
				return fmt.Errorf("opencli Gemini inspect artifact %s file metadata mismatch", artifact.ID)
			}
			payload, err := os.ReadFile(artifactPath)
			if err != nil {
				return fmt.Errorf("read opencli Gemini artifact %s: %w", artifact.ID, err)
			}
			sum := sha256.Sum256(payload)
			if fmt.Sprintf("%x", sum[:]) != strings.ToLower(strings.TrimSpace(artifact.SHA256)) {
				return fmt.Errorf("opencli Gemini inspect artifact %s sha256 mismatch", artifact.ID)
			}
			artifact.Path = artifactPath
			artifact.SHA256 = strings.ToLower(strings.TrimSpace(artifact.SHA256))
		}
	}
	return nil
}

func assistantAfter(turns []inspectTurn, userIndex int) (string, int) {
	if userIndex == 0 {
		return "", 0
	}
	text := ""
	index := 0
	for _, turn := range turns {
		if turn.Role == "Assistant" && turn.Index > userIndex && turn.Index >= index {
			index = turn.Index
			text = turn.Text
		}
	}
	return text, index
}

func latestAssistantTurn(turns []inspectTurn) (string, int) {
	text := ""
	index := 0
	for _, turn := range turns {
		if turn.Role == "Assistant" && turn.Index >= index {
			index = turn.Index
			text = turn.Text
		}
	}
	return text, index
}

func withPendingMarker(hints map[string]string, message string) map[string]string {
	out := cloneHints(hints)
	if out == nil {
		out = map[string]string{}
	}
	normalized := normalizeFingerprintText(message)
	out[pendingFingerprintHint] = fingerprintNormalized(normalized)
	out[pendingFingerprintLenHint] = strconv.Itoa(len([]rune(normalized)))
	return out
}

func parsePositiveInt(raw string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	return value, err == nil && value > 0
}

func messageFingerprint(message string) string {
	return fingerprintNormalized(normalizeFingerprintText(message))
}

func fingerprintNormalized(normalized string) string {
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
	return fingerprintNormalized(string(normalized[:prefixLen])) == fingerprint
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

func (p *Provider) run(ctx context.Context, args []string, maySubmit bool) ([]byte, error) {
	runCtx := ctx
	cancel := func() {}
	if p.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, p.timeout)
	}
	defer cancel()

	select {
	case p.surfaceSerial <- struct{}{}:
		defer func() { <-p.surfaceSerial }()
	case <-runCtx.Done():
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, &ProviderError{Kind: ErrorTimeout, Message: "opencli Gemini command timed out waiting for provider surface", Cause: runCtx.Err()}
		}
		return nil, &ProviderError{Kind: ErrorCanceled, Message: "opencli Gemini command canceled while waiting for provider surface", Cause: runCtx.Err()}
	}

	var stdout, stderr limitedBuffer
	stdout.max = maxCapturedBytes
	stderr.max = 256 << 10
	cmd := exec.CommandContext(runCtx, p.executable, args...)
	processutil.ConfigureCommand(cmd)
	if p.browserExecutable != "" || p.artifactDir != "" {
		env := os.Environ()
		if p.browserExecutable != "" {
			env = append(env, "LOCALGATE_BROWSER_EXECUTABLE="+p.browserExecutable)
		}
		if p.artifactDir != "" {
			env = append(env, "LOCALGATE_ARTIFACT_DIR="+p.artifactDir)
		}
		cmd.Env = env
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if runCtx.Err() != nil {
			return nil, runCtx.Err()
		}
		return nil, &ProviderError{Kind: ErrorExecution, Message: "start opencli Gemini command", Cause: err}
	}
	if err := cmd.Wait(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, &ProviderError{Kind: ErrorTimeout, Message: "opencli Gemini command timed out", Cause: runCtx.Err(), Stderr: stderr.String(), MayHaveSubmitted: maySubmit}
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			return nil, &ProviderError{Kind: ErrorCanceled, Message: "opencli Gemini command canceled", Cause: runCtx.Err(), Stderr: stderr.String(), MayHaveSubmitted: maySubmit}
		}
		exitCode := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		kind := classifyFailure(stderr.String())
		mayHaveSubmitted := maySubmit && kind != ErrorAuthRequired && kind != ErrorChallenge && kind != ErrorQuota
		return nil, &ProviderError{
			Kind:             kind,
			Message:          failureMessage(kind),
			ExitCode:         exitCode,
			Stderr:           stderr.String(),
			Cause:            err,
			MayHaveSubmitted: mayHaveSubmitted,
		}
	}
	if stdout.truncated {
		return nil, &ProviderError{Kind: ErrorInvalidOutput, Message: "opencli Gemini JSON output exceeded capture limit", MayHaveSubmitted: maySubmit}
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

func classifyFailure(stderr string) ErrorKind {
	text := strings.ToLower(stderr)
	switch {
	case strings.Contains(text, "captcha"), strings.Contains(text, "challenge"):
		return ErrorChallenge
	case strings.Contains(text, "authrequired"), strings.Contains(text, "auth required"), strings.Contains(text, "sign in"), strings.Contains(text, "log in"), strings.Contains(text, "google session cookies"), strings.Contains(text, "401"), strings.Contains(text, "403"):
		return ErrorAuthRequired
	case strings.Contains(text, "quota"), strings.Contains(text, "rate limit"), strings.Contains(text, "too many requests"), strings.Contains(text, "usage limit"), strings.Contains(text, "usage cap"):
		return ErrorQuota
	case strings.Contains(text, "timed out"), strings.Contains(text, "timeout"):
		return ErrorTimeout
	default:
		return ErrorExecution
	}
}

func failureMessage(kind ErrorKind) string {
	switch kind {
	case ErrorChallenge:
		return "Gemini browser session requires challenge handling"
	case ErrorAuthRequired:
		return "Gemini browser session requires login"
	case ErrorQuota:
		return "Gemini browser session is rate or quota limited"
	case ErrorTimeout:
		return "opencli Gemini command timed out"
	default:
		return "opencli Gemini command failed"
	}
}

func mayHaveSubmitted(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.MayHaveSubmitted
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

func cloneState(state subagent.ProviderState) subagent.ProviderState {
	state.Hints = cloneHints(state.Hints)
	return state
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

var markdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)

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
