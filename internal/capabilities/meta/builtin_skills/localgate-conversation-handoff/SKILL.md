---
name: localgate-conversation-handoff
description: Use when the user explicitly wants to compact the current conversation and continue in a newly created ordinary ChatGPT conversation.
---

# LocalGate Conversation Handoff

Use this skill only when the user explicitly asks to compact, hand off, or move the current work into a new ordinary ChatGPT conversation. `conversation_handoff` is a progressive Registry capability rather than a top-level MCP tool. In normal MCP mode, inspect it with `get_tool_schema` and execute it through `invoke_tool`.

## Workflow

1. Summarize the current conversation yourself. LocalGate does not have access to the host conversation history and does not perform the semantic compaction.
2. Build a continuation state, not a history recap. Start from what the user is trying to do now, what state the work is actually in, and what the next conversation needs in order to continue correctly.
3. Do not include system or developer instructions, hidden reasoning, authentication material, cookies, tokens, passwords, or unrelated private data.
4. Call `conversation_handoff` with the compact checkpoint in `handoff_context`. Pass `project` only when the user explicitly asks to put the new conversation in a specific ChatGPT Project. Otherwise omit it: LocalGate will create a normal top-level ChatGPT conversation and will not inherit the Subagent provider's default project.
5. On success, return the new `conversation_url` to the user. The new conversation is an ordinary provider conversation: it has no `subagent_id`, LocalGate does not manage later messages, and the user should continue there directly.
6. After a successful handoff, do not keep executing the old task in the current conversation unless the user explicitly asks to remain here.

## Summary quality

The checkpoint should be useful to an agent that must continue the work without seeing the old conversation.

Prioritize, in this order:

1. The user's current goal and latest explicit request.
2. The real current state of the work, including what is already done and what is not.
3. Confirmed decisions and constraints that should not be rediscovered.
4. Important artifacts needed to continue: repository/file paths, branch names, IDs, URLs, configuration choices, or exact error symptoms when they still matter.
5. Open issues, uncertainties, and the immediate next action.
6. A small amount of recent context only when it changes how the next step should be handled.

Do not turn the checkpoint into a changelog. Omit routine tool calls, task IDs, polling history, successful test command transcripts, temporary debugging attempts, and implementation chronology unless one of them directly affects the next action. Summarize evidence instead of replaying it.

Preserve the user's actual intent rather than overfocusing on the most technical part of the conversation. If the conversation contains both a feature implementation and later feedback about how that feature should behave, the later feedback belongs in the checkpoint as the current requirement.

Normally target roughly 1–4 KiB of Markdown. Complex tasks may use more, but try to stay under about 8 KiB unless additional detail is genuinely required. The tool's larger hard limit is a safety ceiling, not a target.

Match the checkpoint language to the user's current conversation language unless the user explicitly requests a different language. If the conversation is primarily Chinese, write the checkpoint and section headings in Chinese; if it is primarily English, keep them in English. Preserve code, commands, paths, configuration keys, identifiers, and proper names in their original form instead of translating them mechanically.

## Recommended checkpoint shape

Use headings such as:

- `Current Goal`
- `Current State`
- `Decisions & Constraints`
- `Important Artifacts`
- `Open Issues`
- `Next Action`
- `Recent Context` only when necessary

For a primarily Chinese conversation, use the equivalent Chinese shape, for example:

- `当前目标`
- `当前状态`
- `已确认的决策与约束`
- `重要资料`
- `待解决问题`
- `下一步`
- `近期上下文` only when necessary

Sections with no useful content may be omitted. Do not add instructions such as "acknowledge and wait"; LocalGate already wraps the checkpoint with continuation instructions.

## Safety and failure handling

- `conversation_handoff` creates a remote conversation and is non-idempotent. Never call it speculatively or retry it in a loop.
- If the operation returns `conversation_handoff_outcome_unknown`, the remote conversation may already have been created even though LocalGate could not confirm its id or URL. Do not automatically retry. Tell the user to check their ChatGPT conversation history first.
- Do not use `subagent_spawn` as a substitute for this workflow. A handoff intentionally creates no persistent LocalGate subagent resource.
- The returned conversation belongs to the ChatGPT account/workspace used by LocalGate's OpenCLI browser session. The user must be signed into an account that can access that conversation URL.
