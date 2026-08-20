---
name: localgate-task-session
description: Use for LocalGate task lifecycle, approvals, long-running or interactive command sessions, terminal cursors, logs, cancellation, and retrieving final task results.
---

# LocalGate Task and Session Lifecycle

Use this skill when work spans more than a single immediate tool result.

In normal MCP mode, `run_command` remains direct, while session and Task lifecycle helpers may be progressive Registry tools. Before calling a non-direct helper, use `get_tool_schema` and then `invoke_tool`. Keep the original task/session identifiers inside the target tool's `arguments` object.

## Choose task vs session

- Use `run_command` for one-shot, non-interactive commands that should produce a final result.
- Use `exec_command` for dev servers, watch processes, REPL-like programs, commands that keep running, or work that needs later stdin.
- Once a session exists, use the session follow-up tools actually exposed by the host, such as `write_stdin`, `read_thread_terminal`, and `close_session`.
- Use `close_session` to terminate a dev server, watcher, REPL, or other session that should no longer run. `task_cancel` cancels task execution; it is not a substitute for closing a session after `exec_command` has already returned successfully.

Do not start an interactive session when a one-shot command is sufficient.

## Follow asynchronous tasks

A tool can return a `task_id` before work is terminal. Use Registry lifecycle helpers such as `task_status`, `task_logs`, `wait_task_finish`, `task_result`, or `task_cancel` through the current transport and keep using the original `task_id`.

- Waiting, queued, or running states are not failures.
- Use logs for incremental diagnostics when useful.
- Read the final result after the task reaches a terminal success state.
- Cancel only when the user asks or continuing the task is no longer appropriate.

These lifecycle helpers do not need to appear as top-level MCP tools. Their availability is disclosed through this skill and `get_tool_schema`.

## Approval handoff

If a tool returns `pending_approval`, `awaiting_approval`, or an equivalent state with an `approval_url`:

- show the approval URL to the user;
- preserve the returned `task_id` and approval metadata;
- do not resubmit the original tool call;
- after approval, continue following the same task.

If approval is rejected, canceled, or expires, report that terminal outcome rather than silently creating a replacement task.

## Session terminal cursors

When session output APIs return an absolute cursor/offset such as `next_offset`, preserve it and use it for the next read.

- Prefer `max_output_items` for paging. `max_output_tokens` may exist as a compatibility alias but also limits output items, not model tokens.
- Continue reading while `has_more=true`. A terminal process status can be observed before all retained output pages have been consumed; `completed=true` / `output_complete=true` means the process is terminal and the returned cursor has caught up with retained output.
- `buffer_truncated=true` (and the compatibility field `truncated=true`) means older retained output was dropped. `cursor_expired=true` means the supplied cursor was older than the current `base_offset`; continue from the returned `next_offset`.
- `read_thread_terminal` can use `wait_ms` to wait for new output instead of polling aggressively.
- `write_stdin` can use `yield_time_ms` to wait for output after input. Empty `chars` performs only the wait/read step, including after the process has exited.
- A canceled stdin write may have been partially delivered. LocalGate closes that session to avoid leaving an interactive protocol in an unknown state.

Keep session access within the LocalGate instance/session context that created it, and do not guess a session id or cursor.