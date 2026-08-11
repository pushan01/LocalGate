---
name: localgate-task-session
description: Use for LocalGate task lifecycle, approvals, long-running or interactive command sessions, terminal cursors, logs, cancellation, and retrieving final task results.
---

# LocalGate Task and Session Lifecycle

Use this skill when work spans more than a single immediate tool result.

## Choose task vs session

- Use `run_command` for one-shot, non-interactive commands that should produce a final result.
- Use `exec_command` for dev servers, watch processes, REPL-like programs, commands that keep running, or work that needs later stdin.
- Once a session exists, use the session follow-up tools actually exposed by the host, such as `write_stdin` and `read_thread_terminal`.

Do not start an interactive session when a one-shot command is sufficient.

## Follow asynchronous tasks

A tool can return a `task_id` before work is terminal. When task lifecycle helpers such as `task_status`, `task_logs`, `wait_task_finish`, `task_result`, or `task_cancel` are exposed by the host/transport, keep using the original `task_id`.

- Waiting, queued, or running states are not failures.
- Use logs for incremental diagnostics when useful.
- Read the final result after the task reaches a terminal success state.
- Cancel only when the user asks or continuing the task is no longer appropriate.

These lifecycle helpers can be host/transport additions and therefore may not appear in the LocalGate Registry tool summary. Call only helpers actually exposed by the current host.

## Approval handoff

If a tool returns `pending_approval`, `awaiting_approval`, or an equivalent state with an `approval_url`:

- show the approval URL to the user;
- preserve the returned `task_id` and approval metadata;
- do not resubmit the original tool call;
- after approval, continue following the same task.

If approval is rejected, canceled, or expires, report that terminal outcome rather than silently creating a replacement task.

## Session terminal cursors

When session output APIs return an absolute cursor/offset such as `next_offset`, preserve it and use it for the next read. If the buffer reports fields such as `base_offset`, `truncated`, or `cursor_expired`, treat them as evidence that older buffered output is no longer available and continue from the valid offset reported by the tool.

Keep session access within the workspace/session context that created it, and do not guess a session id or cursor.