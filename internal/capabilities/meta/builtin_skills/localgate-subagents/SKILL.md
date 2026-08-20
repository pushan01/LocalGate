---
name: localgate-subagents
description: Use for delegating work to persistent LocalGate subagents, continuing an existing subagent conversation, checking subagent state, and closing a subagent without losing provider history.
---

# LocalGate Subagents

Use this skill when the current tool list contains `subagent_spawn` and the task benefits from a separate persistent reasoning thread.

## Identity and reuse

- `subagent_spawn` creates a new logical subagent and returns a stable `subagent_id` such as `sg_*`.
- `subagent_type` is optional logical role metadata such as `coder`, `reviewer`, or `researcher`. It is independent from `provider`; use `provider` to choose ChatGPT/Gemini and `subagent_type` to describe what role the logical subagent plays.
- When multiple providers are enabled, pass `provider` explicitly when the delegated role should use a non-default backend. Current browser-backed providers include `opencli_chatgpt` and `opencli_gemini`.
- Spawn is asynchronous: success means the initial instruction was submitted and the new subagent is normally `busy`; it does not mean the assistant reply is finished.
- Keep that `subagent_id` and use `subagent_send` for later turns after the current turn becomes `ready`. Do not spawn a replacement merely to continue the same delegated task.
- Provider conversation IDs, URLs, and Project IDs returned by `subagent_get` or `subagent_list` are diagnostics. The LocalGate `subagent_id` is the identity callers should retain.
- A busy subagent rejects another send. Use distinct subagents when independent work should run concurrently.
- Distinct `sg_*` IDs must never share one provider conversation. If an operation returns `subagent_conversation_conflict`, treat it as an isolation failure: do not continue sending through the conflicting ID or respawn blindly against the same conversation.

## Tool choice

- Use `subagent_spawn` once when creating a new delegated role or independent conversation. It submits the initial turn and returns without waiting for the assistant response.
- Use `subagent_poll` to check progress without waiting for model completion. Remote refreshes are paced; if the configured interval has not elapsed, LocalGate returns the cached local state with `refreshed=false` and `retry_after_ms` instead of contacting the provider again. `busy` means the current remote turn is still pending; `reply` may contain visible partial assistant text when a refresh actually ran. Completed non-text outputs such as Gemini-generated images are returned separately in `artifacts`.
- Use `subagent_wait` only when the parent agent needs the current turn's completed reply before continuing. Wait respects the same polling pace and sleeps until the next allowed remote check instead of hammering the provider. Keep `reply` as text; use the returned `artifacts` array for generated images/files.
- Use `subagent_send` for follow-up questions, refinements, or additional work in that same conversation after poll/wait reports `ready`. Send submits the message and returns without waiting for the new reply.
- Use `subagent_get` for one subagent's status and provider mapping diagnostics. By default, get performs one remote refresh when the local state is `busy` or `degraded` and the configured polling interval allows it; otherwise it returns the cached state with `refreshed=false`. Pass `refresh=false` only when a strictly local snapshot is required.
- Use `subagent_list` when the caller has lost track of available subagent IDs in the current workspace.
- Use `subagent_archive` when the logical subagent should be removed from LocalGate while retaining its backing conversation. ChatGPT archive first verifies the remote turn is no longer generating and optionally moves it to `archive_project`; the project-move DOM/network verifier is bundled with LocalGate and only needs the generic OpenCLI Browser Bridge. Gemini currently has no remote archive/move implementation, so `opencli_gemini` returns archive unsupported; use `subagent_close` when a Gemini subagent should no longer receive messages.
- Use `subagent_close` when the logical subagent should no longer receive messages. Closing is idempotent and does not delete the backing ChatGPT conversation or Project history.

## Execution and safety

- Spawn/send operations contact an external provider but return after submission, not after model generation. Poll is non-blocking; wait is the explicit blocking operation. If the transport itself returns a task ID, pending approval, or other LocalGate task lifecycle state, follow that lifecycle rather than resubmitting the operation.
- OpenCLI browser providers serialize their own browser-surface operations because each persistent site session uses one navigable tab. This still allows remote conversations to generate concurrently after submit. When coordinating several subagents, prefer poll over a long blocking wait so the shared browser surface stays available for other status checks and submissions.
- Treat provider errors as the result of the current turn. Do not silently create a new subagent because a send failed; the existing conversation mapping may still be valid.
- If spawn or send fails with `outcome_unknown`, the provider may already have created/submitted remote work even though LocalGate could not confirm the result. Do not automatically replay the same operation; for an existing subagent use poll/wait to reconcile its current conversation first.
- If `subagent_send` returns `subagent_busy` or `subagent_not_ready`, do not retry in a tight loop. Poll or wait until the current turn is resolved, then send the next message.
- If `subagent_archive` returns `subagent_archive_busy`, retry later after the current turn finishes. If it returns `outcome_unknown`, keep the `sg_*` mapping and do not manually delete it; the remote move may already have happened and the provider's explicit retry path must reconcile that state first.
- Do not infer that a subagent is stuck merely because `busy` has been visible for a long wall-clock interval or because `updated_at` has not moved. `updated_at` is LocalGate's local state-change time, not a remote heartbeat. Check `last_observed_at`, `phase`, and `next_action`, then refresh/poll before making any recovery decision.
- Never close and respawn a subagent solely to clear a long-lived `busy` state. Close is a deliberate logical lifecycle action; stale state should be reconciled with get/poll/wait first so the existing conversation is not duplicated.
- A single subagent also has a bounded local operation queue. If it is full, wait for existing work to finish instead of creating duplicate operations against the same conversation.
- Do not send secrets or unrelated workspace content to a subagent merely because the provider is available. Delegate only the information needed for the requested task.
- A configured ChatGPT Project affects where a new provider conversation is created. Follow-up turns continue through the saved conversation mapping automatically; callers should not manually manage browser tabs.
- ChatGPT `ask/send` use upstream OpenCLI, while LocalGate bundles the ChatGPT DOM `inspect` path used by poll/wait and the `project-move` DOM/network verifier used by archive. These only require the generic OpenCLI Browser Bridge, so callers do not need a LocalGate-specific OpenCLI fork.
- Gemini conversations are identified by their `/app/<conversation_id>` mapping. The Gemini DOM adapter is bundled with LocalGate and uses only the generic OpenCLI Browser Bridge (`browser open/eval/keys`); callers do not need an OpenCLI build containing LocalGate-specific `gemini submit/inspect` commands. Submit remains asynchronous and inspect remains non-blocking, so the same LocalGate busy/ready lifecycle and polling throttle apply. When a completed Gemini turn contains generated images, the adapter materializes them as PNG files under LocalGate's subagent artifact directory and returns provider-independent metadata in `artifacts`; image-only turns can therefore become `ready` without inventing reply text.
- The latest completed turn's artifacts are persisted on the `sg_*` record and remain visible through `subagent_get` / `subagent_list` until the next successful send lifecycle begins. Artifact paths are internal and are not exposed in the MCP artifact list. Use `subagent_artifact_get(subagent_id, artifact_id)` to obtain a short-lived LocalGate download/resource link instead of trying to reuse Gemini's page-local `blob:` URL; LocalGate revalidates artifact ownership, size, and SHA-256 before export. File export approval is disabled by default (`file_exports.require_approval=false`) but can still be enabled explicitly or required by an admin approval rule.
- Each provider may configure `submit_interval_seconds`. It throttles `spawn/create` and `send` across all subagents using that provider; `0` disables it. Poll/inspect/archive are not delayed by provider submit pacing.
