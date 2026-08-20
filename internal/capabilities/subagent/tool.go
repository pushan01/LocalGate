package subagentcap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"localgate/internal/core"
	runtime "localgate/internal/subagent"
)

const (
	maxNameBytes         = 256
	maxSubagentTypeBytes = 128
	maxProviderBytes     = 128
	maxProjectBytes      = 2048
	maxInstructionBytes  = 256 << 10
	maxMessageBytes      = 256 << 10
)

func NewTools(manager *runtime.Manager) []core.Tool {
	return []core.Tool{
		SpawnTool{manager: manager},
		SendTool{manager: manager},
		PollTool{manager: manager},
		WaitTool{manager: manager},
		GetTool{manager: manager},
		ListTool{manager: manager},
		ArchiveTool{manager: manager},
		CloseTool{manager: manager},
	}
}

type SpawnTool struct{ manager *runtime.Manager }

func (SpawnTool) Name() string { return "subagent_spawn" }

func (SpawnTool) Description() string {
	return "Create one persistent LocalGate subagent and submit its initial instruction without waiting for the assistant response. Keep and reuse the returned subagent_id; use subagent_poll or subagent_wait to read the result."
}

func (SpawnTool) Schema() any {
	return objectSchema(map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "Optional short display name for this subagent, such as security-reviewer-1.",
		},
		"subagent_type": map[string]any{
			"type":        "string",
			"description": "Optional logical subagent type independent of provider, such as coder, reviewer, researcher, or general.",
		},
		"provider": map[string]any{
			"type":        "string",
			"description": "Optional provider name. Omit to use LocalGate's configured default provider.",
		},
		"instruction": map[string]any{
			"type":        "string",
			"description": "Initial instruction sent as the first provider conversation turn.",
		},
		"project": map[string]any{
			"type":        "string",
			"description": "Optional provider-specific ChatGPT Project ID or /g/g-p-... URL override for this new subagent.",
		},
	}, []string{"instruction"})
}

func (SpawnTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"subagent_id":   map[string]any{"type": "string"},
		"name":          map[string]any{"type": "string"},
		"subagent_type": map[string]any{"type": "string"},
		"provider":      map[string]any{"type": "string"},
		"status":        map[string]any{"type": "string"},
		"accepted":      map[string]any{"type": "boolean"},
	}, []string{"subagent_id", "provider", "status", "accepted"})
}

func (SpawnTool) ToolAnnotations() map[string]any { return mutatingAnnotations(false) }

func (t SpawnTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	instruction := stringArg(args, "instruction")
	if err := requireText("instruction", instruction, maxInstructionBytes); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(stringArg(args, "name"))
	if err := optionalText("name", name, maxNameBytes); err != nil {
		return nil, err
	}
	subagentType := strings.TrimSpace(stringArg(args, "subagent_type"))
	if err := optionalText("subagent_type", subagentType, maxSubagentTypeBytes); err != nil {
		return nil, err
	}
	provider := strings.TrimSpace(stringArg(args, "provider"))
	if err := optionalText("provider", provider, maxProviderBytes); err != nil {
		return nil, err
	}
	project := strings.TrimSpace(stringArg(args, "project"))
	if err := optionalText("project", project, maxProjectBytes); err != nil {
		return nil, err
	}
	options := map[string]string(nil)
	if project != "" {
		options = map[string]string{"project": project}
	}
	result, err := t.manager.Create(ctx, runtime.CreateRequest{
		Name:         name,
		SubagentType: subagentType,
		Provider:     provider,
		Instruction:  instruction,
		Options:      options,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"subagent_id":   result.Subagent.ID,
		"name":          result.Subagent.Name,
		"subagent_type": result.Subagent.SubagentType,
		"provider":      result.Subagent.Provider,
		"status":        string(result.Subagent.Status),
		"accepted":      true,
	}, nil
}

type SendTool struct{ manager *runtime.Manager }

func (SendTool) Name() string { return "subagent_send" }

func (SendTool) Description() string {
	return "Submit one follow-up message to a ready LocalGate subagent without waiting for the assistant response. Use subagent_poll or subagent_wait afterward; a busy subagent rejects another send."
}

func (SendTool) Schema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{
			"type":        "string",
			"description": "Stable sg_* id returned by subagent_spawn.",
		},
		"message": map[string]any{
			"type":        "string",
			"description": "Follow-up message for the existing subagent conversation.",
		},
	}, []string{"subagent_id", "message"})
}

func (SendTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{"type": "string"},
		"status":      map[string]any{"type": "string"},
		"accepted":    map[string]any{"type": "boolean"},
	}, []string{"subagent_id", "status", "accepted"})
}

func (SendTool) ToolAnnotations() map[string]any { return mutatingAnnotations(false) }

func (t SendTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(stringArg(args, "subagent_id"))
	if err := requireText("subagent_id", id, 128); err != nil {
		return nil, err
	}
	message := stringArg(args, "message")
	if err := requireText("message", message, maxMessageBytes); err != nil {
		return nil, err
	}
	agent, err := t.manager.Send(ctx, id, message)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"subagent_id": id,
		"status":      string(agent.Status),
		"accepted":    true,
	}, nil
}

type PollTool struct{ manager *runtime.Manager }

func (PollTool) Name() string { return "subagent_poll" }

func (PollTool) Description() string {
	return "Check an existing LocalGate subagent's generation state without waiting for model completion. Remote refreshes are paced; calls made too soon return the cached local state with refreshed=false and retry_after_ms."
}

func (PollTool) Schema() any { return observeSchema() }

func (PollTool) OutputSchema() any { return observeOutputSchema() }

func (PollTool) ToolAnnotations() map[string]any { return remoteReadAnnotations() }

func (t PollTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(stringArg(args, "subagent_id"))
	if err := requireText("subagent_id", id, 128); err != nil {
		return nil, err
	}
	result, err := t.manager.Poll(ctx, id)
	if err != nil {
		return nil, err
	}
	return observeResultMap(id, result), nil
}

type WaitTool struct{ manager *runtime.Manager }

func (WaitTool) Name() string { return "subagent_wait" }

func (WaitTool) Description() string {
	return "Wait until the current LocalGate subagent turn finishes and return the completed assistant reply. Remote status checks respect the configured same-agent and different-agent polling intervals."
}

func (WaitTool) Schema() any { return observeSchema() }

func (WaitTool) OutputSchema() any { return observeOutputSchema() }

func (WaitTool) ToolAnnotations() map[string]any { return remoteReadAnnotations() }

func (t WaitTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(stringArg(args, "subagent_id"))
	if err := requireText("subagent_id", id, 128); err != nil {
		return nil, err
	}
	result, err := t.manager.Wait(ctx, id)
	if err != nil {
		return nil, err
	}
	return observeResultMap(id, result), nil
}

type GetTool struct{ manager *runtime.Manager }

func (GetTool) Name() string { return "subagent_get" }

func (GetTool) Description() string {
	return "Get one LocalGate subagent's status and provider conversation mapping summary. By default a busy or degraded subagent is remotely refreshed when its polling interval allows; otherwise the cached local state is returned with refreshed=false. Set refresh=false for a strictly local snapshot."
}

func (GetTool) Schema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{"type": "string", "description": "Stable sg_* id."},
		"refresh": map[string]any{
			"type":        "boolean",
			"description": "Whether busy/degraded state should be non-blockingly refreshed from the provider. Defaults to true.",
		},
	}, []string{"subagent_id"})
}

func (GetTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"subagent":       agentOutputSchema(),
		"refreshed":      map[string]any{"type": "boolean"},
		"retry_after_ms": map[string]any{"type": "integer"},
		"reply":          map[string]any{"type": "string"},
		"artifacts":      artifactArraySchema(),
		"refresh_error":  map[string]any{"type": "string"},
	}, []string{"subagent", "refreshed"})
}

func (GetTool) ToolAnnotations() map[string]any { return remoteReadAnnotations() }

func (t GetTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(stringArg(args, "subagent_id"))
	if err := requireText("subagent_id", id, 128); err != nil {
		return nil, err
	}
	agent, err := t.manager.Get(id)
	if err != nil {
		return nil, err
	}
	refresh := true
	if value, ok := args["refresh"].(bool); ok {
		refresh = value
	}
	result := map[string]any{
		"subagent":  agentMap(agent),
		"refreshed": false,
		"artifacts": artifactMaps(agent.Artifacts),
	}
	if !refresh || (agent.Status != runtime.StatusBusy && agent.Status != runtime.StatusDegraded) {
		return result, nil
	}
	observed, refreshErr := t.manager.Poll(ctx, id)
	if refreshErr == nil {
		result["subagent"] = agentMap(observed.Subagent)
		result["refreshed"] = observed.Refreshed
		if observed.Refreshed {
			result["reply"] = observed.Reply.Text
		}
		result["artifacts"] = artifactMaps(observed.Subagent.Artifacts)
		if observed.RetryAfter > 0 {
			result["retry_after_ms"] = observed.RetryAfter.Milliseconds()
		}
		return result, nil
	}
	if errors.Is(refreshErr, context.Canceled) || errors.Is(refreshErr, context.DeadlineExceeded) {
		return nil, refreshErr
	}
	latest, getErr := t.manager.Get(id)
	if getErr != nil {
		return nil, refreshErr
	}
	result["subagent"] = agentMap(latest)
	result["refreshed"] = true
	result["refresh_error"] = refreshErr.Error()
	return result, nil
}

type ListTool struct{ manager *runtime.Manager }

func (ListTool) Name() string { return "subagent_list" }

func (ListTool) Description() string {
	return "List LocalGate subagents owned by the current LocalGate instance, including status and non-secret provider conversation mapping summaries."
}

func (ListTool) Schema() any { return objectSchema(map[string]any{}, nil) }

func (ListTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"subagents": map[string]any{"type": "array", "items": agentOutputSchema()},
	}, []string{"subagents"})
}

func (ListTool) ToolAnnotations() map[string]any { return readOnlyAnnotations() }

func (t ListTool) Execute(_ context.Context, ic core.InvokeContext, _ map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	agents, err := t.manager.List()
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(agents))
	for _, agent := range agents {
		items = append(items, agentMap(agent))
	}
	return map[string]any{"subagents": items}, nil
}

type CloseTool struct{ manager *runtime.Manager }

type ArchiveTool struct{ manager *runtime.Manager }

func (ArchiveTool) Name() string { return "subagent_archive" }

func (ArchiveTool) Description() string {
	return "Archive and delete a local sg_* mapping after confirming its current remote turn is finished. If archive_project is configured, first move and verify the backing conversation there; if it is empty, leave the remote conversation where it is."
}

func (ArchiveTool) Schema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{"type": "string", "description": "Stable sg_* id to archive and remove locally."},
	}, []string{"subagent_id"})
}

func (ArchiveTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"subagent_id":      map[string]any{"type": "string"},
		"archived":         map[string]any{"type": "boolean"},
		"project_id":       map[string]any{"type": "string"},
		"artifacts":        artifactArraySchema(),
		"conversation_url": map[string]any{"type": "string"},
	}, []string{"subagent_id", "archived", "project_id", "conversation_url"})
}

func (ArchiveTool) ToolAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    false,
		"destructiveHint": true,
		"idempotentHint":  true,
		"openWorldHint":   true,
	}
}

func (t ArchiveTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(stringArg(args, "subagent_id"))
	if err := requireText("subagent_id", id, 128); err != nil {
		return nil, err
	}
	agent, err := t.manager.Archive(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"subagent_id":      id,
		"archived":         true,
		"project_id":       agent.ProviderState.ProjectID,
		"artifacts":        artifactMaps(agent.Artifacts),
		"conversation_url": agent.ProviderState.ConversationURL,
	}, nil
}

func (CloseTool) Name() string { return "subagent_close" }

func (CloseTool) Description() string {
	return "Logically close one LocalGate subagent so it no longer accepts messages. This does not delete the backing ChatGPT conversation or Project history."
}

func (CloseTool) Schema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{"type": "string", "description": "Stable sg_* id."},
	}, []string{"subagent_id"})
}

func (CloseTool) OutputSchema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{"type": "string"},
		"status":      map[string]any{"type": "string"},
	}, []string{"subagent_id", "status"})
}

func (CloseTool) ToolAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    false,
		"destructiveHint": true,
		"idempotentHint":  true,
		"openWorldHint":   true,
	}
}

func (t CloseTool) Execute(ctx context.Context, ic core.InvokeContext, args map[string]any) (map[string]any, error) {
	if err := requireManager(t.manager); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(stringArg(args, "subagent_id"))
	if err := requireText("subagent_id", id, 128); err != nil {
		return nil, err
	}
	agent, err := t.manager.CloseAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"subagent_id": id, "status": string(agent.Status)}, nil
}

func agentMap(agent runtime.Subagent) map[string]any {
	return map[string]any{
		"subagent_id":      agent.ID,
		"name":             agent.Name,
		"subagent_type":    agent.SubagentType,
		"provider":         agent.Provider,
		"status":           string(agent.Status),
		"conversation_id":  agent.ProviderState.ConversationID,
		"conversation_url": agent.ProviderState.ConversationURL,
		"project_id":       agent.ProviderState.ProjectID,
		"artifacts":        artifactMaps(agent.Artifacts),
		"last_error":       agent.LastError,
		"last_error_code":  agent.LastErrorCode,
		"phase":            agentPhase(agent),
		"next_action":      nextAction(agent),
		"created_at":       formatTime(agent.CreatedAt),
		"updated_at":       formatTime(agent.UpdatedAt),
		"last_observed_at": formatTime(agent.LastObservedAt),
	}
}

func agentOutputSchema() map[string]any {
	properties := map[string]any{
		"subagent_id":      map[string]any{"type": "string"},
		"name":             map[string]any{"type": "string"},
		"subagent_type":    map[string]any{"type": "string"},
		"provider":         map[string]any{"type": "string"},
		"status":           map[string]any{"type": "string"},
		"conversation_id":  map[string]any{"type": "string"},
		"conversation_url": map[string]any{"type": "string"},
		"project_id":       map[string]any{"type": "string"},
		"artifacts":        artifactArraySchema(),
		"last_error":       map[string]any{"type": "string"},
		"last_error_code":  map[string]any{"type": "string"},
		"phase":            map[string]any{"type": "string"},
		"next_action":      map[string]any{"type": "string"},
		"created_at":       map[string]any{"type": "string"},
		"updated_at":       map[string]any{"type": "string"},
		"last_observed_at": map[string]any{"type": "string"},
	}
	return objectSchema(properties, []string{"subagent_id", "provider", "status", "phase", "next_action", "created_at", "updated_at", "last_observed_at"})
}

func agentPhase(agent runtime.Subagent) string {
	switch agent.Status {
	case runtime.StatusBusy:
		if agent.LastObservedAt.IsZero() || agent.LastObservedAt.Before(agent.UpdatedAt) {
			return "submitted"
		}
		return "generating"
	case runtime.StatusReady:
		return "ready"
	case runtime.StatusDegraded:
		if agent.LastErrorCode == "connector_auth_required" {
			return "connector_auth_required"
		}
		return "degraded"
	case runtime.StatusClosed:
		return "closed"
	default:
		return string(agent.Status)
	}
}

func nextAction(agent runtime.Subagent) string {
	switch agent.Status {
	case runtime.StatusReady:
		return "send"
	case runtime.StatusDegraded:
		if agent.LastErrorCode == "connector_auth_required" {
			return "authorize"
		}
		return "poll"
	case runtime.StatusBusy:
		return "poll"
	case runtime.StatusClosed:
		return "none"
	default:
		return "get"
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func readOnlyAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   false,
	}
}

func remoteReadAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   true,
	}
}

func observeSchema() any {
	return objectSchema(map[string]any{
		"subagent_id": map[string]any{"type": "string", "description": "Stable sg_* id."},
	}, []string{"subagent_id"})
}

func observeOutputSchema() any {
	return objectSchema(map[string]any{
		"subagent_id":    map[string]any{"type": "string"},
		"status":         map[string]any{"type": "string"},
		"reply":          map[string]any{"type": "string"},
		"artifacts":      artifactArraySchema(),
		"refreshed":      map[string]any{"type": "boolean"},
		"retry_after_ms": map[string]any{"type": "integer"},
	}, []string{"subagent_id", "status", "reply", "refreshed"})
}

func observeResultMap(id string, result runtime.ObserveResult) map[string]any {
	artifacts := result.Reply.Artifacts
	if len(artifacts) == 0 {
		artifacts = result.Subagent.Artifacts
	}
	out := map[string]any{
		"subagent_id": id,
		"status":      string(result.Subagent.Status),
		"reply":       result.Reply.Text,
		"artifacts":   artifactMaps(artifacts),
		"refreshed":   result.Refreshed,
	}
	if result.RetryAfter > 0 {
		out["retry_after_ms"] = result.RetryAfter.Milliseconds()
	}
	return out
}

func artifactArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": objectSchema(map[string]any{
			"id":        map[string]any{"type": "string"},
			"type":      map[string]any{"type": "string"},
			"mime_type": map[string]any{"type": "string"},
			"file_name": map[string]any{"type": "string"},
			"size":      map[string]any{"type": "integer"},
			"sha256":    map[string]any{"type": "string"},
			"width":     map[string]any{"type": "integer"},
			"height":    map[string]any{"type": "integer"},
			"alt":       map[string]any{"type": "string"},
		}, []string{"id", "type"}),
	}
}

func artifactMaps(artifacts []runtime.Artifact) []map[string]any {
	out := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, map[string]any{
			"id":        artifact.ID,
			"type":      artifact.Type,
			"mime_type": artifact.MIMEType,
			"file_name": artifact.FileName,
			"size":      artifact.Size,
			"sha256":    artifact.SHA256,
			"width":     artifact.Width,
			"height":    artifact.Height,
			"alt":       artifact.Alt,
		})
	}
	return out
}

func mutatingAnnotations(idempotent bool) map[string]any {
	return map[string]any{
		"readOnlyHint":    false,
		"destructiveHint": false,
		"idempotentHint":  idempotent,
		"openWorldHint":   true,
	}
}

func requireManager(manager *runtime.Manager) error {
	if manager == nil {
		return errors.New("subagent runtime is not enabled")
	}
	return nil
}

func requireText(name, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return optionalText(name, value, maxBytes)
}

func optionalText(name, value string, maxBytes int) error {
	if len([]byte(value)) > maxBytes {
		return fmt.Errorf("%s exceeds %d UTF-8 bytes", name, maxBytes)
	}
	return nil
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
