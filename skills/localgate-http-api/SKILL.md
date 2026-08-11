---
name: localgate-http-api
description: Use this skill whenever the user wants to operate files, execute commands, run background processes, inspect logs, or drive a LocalGate server through HTTP or HTTP MCP. Prefer this skill when the request is about calling LocalGate endpoints, using `/tasks`, `/sessions`, `/mcp`, listing tools, approving actions, or integrating a client against LocalGate, even if the user does not explicitly say "HTTP API".
---

# LocalGate HTTP API Playbook

Use this skill to guide model behavior when interacting with a running `LocalGate.mcp` service over:

- normal HTTP endpoints such as `/tools`, `/tasks`, `/sessions`
- system HTTP endpoints such as `/system/upgrades`
- HTTP MCP mounted at `/mcp`

This skill is for API-driven operation. It is not for editing LocalGate internals unless the user explicitly asks to change repository code.

For a complete command reference, consult [api-command-collection.md](file:///Users/bytedance/project/LocalGate.mcp/api-command-collection.md).

This skill also covers the **tool HTTP mode** where MCP only exposes the bootstrap tools (`get_startup_instructions`, `get_skill`, `get_temporary_access_token`, and `get_tool_schema`) and the real business tools move to `/tools/{tool}/call`.

## Design Intent

LocalGate is designed as a controlled execution gateway rather than a raw shell wrapper. The model should preserve that design when using the API.

The key design ideas are:

- **Tool-first execution**: clients do not invoke arbitrary internals; they call named tools.
- **Task/session split**: one-shot work goes through tasks, interactive or long-running work goes through sessions.
- **Approval-aware flow**: risky actions may become approval requests instead of executing immediately.
- **Observable execution**: status, logs, SSE, terminal buffers, and audit records are first-class interfaces.
- **Transport separation**: the same capability can be exposed through HTTP APIs or HTTP MCP `/mcp`.

When answering users, keep that mental model stable. Do not blur `task` and `session`, and do not treat HTTP MCP as identical to stdio MCP.

## Interaction Model

Choose the interaction style based on what the user wants:

### 1. Tool discovery

Use when the user asks:

- what tools exist
- what a tool accepts
- how to call a tool

Primary endpoints:

- `GET /tools`
- `GET /tools/{name}`
- MCP `tools/list`

### 2. One-shot execution

Use when the user wants:

- read a file once
- list a directory
- run a single command
- get a final result

Primary path:

- `POST /tasks`
- then `GET /tasks/{id}` or `GET /tasks/{id}/result`

### 3. Interactive or long-running execution

Use when the user wants:

- dev server
- REPL
- watch process
- keep a command alive
- send more input later

Primary path:

- `POST /sessions/exec`
- `POST /sessions/{id}/stdin`
- `GET /sessions/{id}/terminal`
- `POST /sessions/{id}/close`

### 4. Realtime observation

Use when the user wants:

- streaming output
- tail-like behavior
- incremental task logs

Primary path:

- `GET /tasks/{id}/logs`
- `GET /tasks/{id}/events/stream`

### 5. Approval and audit

Use when the user wants:

- inspect pending approval
- approve or reject a request
- query audit trail

Primary path:

- `GET /approvals?status=pending`
- `GET /approvals/{id}`
- `POST /approvals/{id}/approve`
- `POST /approvals/{id}/reject`
- `POST /approvals/{id}/cancel`
- `GET /approval-ui`（审批管理列表，只能通过 `approval.base_url` 的 Host 访问）
- `GET /approval-ui/{id}`（审批详情，只能通过 `approval.base_url` 的 Host 访问）
- `GET /audits`

### 6. Tool HTTP mode

Use when the instance only exposes:

- `get_startup_instructions`
- `get_skill`
- `get_temporary_access_token`
- `get_tool_schema`

Primary path:

- load a matching built-in workflow with `get_skill` when startup recommends one
- call `get_temporary_access_token`
- read `base_url`, `call_path_pattern`, `auth_header_name`, `auth_value`
- read the `tools` list from `get_startup_instructions`
- call `/tools/{tool}/call` with both Bearer and tool access header

## Transport Selection Rules

Use these rules consistently:

1. If the user asks for **plain HTTP API usage**, prefer `/tools`, `/tasks`, `/sessions`, `/approvals`, `/audits`.
2. If the user asks for **MCP integration over HTTP**, prefer `/mcp` with JSON-RPC requests.
3. If the user asks for **stdio MCP**, do not invent `curl`; explain that stdio MCP is a subprocess/stdin-stdout transport.
4. If the user wants a **background process with follow-up interaction**, prefer sessions over tasks.
5. If the user wants a **simple command or file operation**, prefer tasks unless they explicitly need interactivity.
6. If the user wants a **binary self-upgrade of the running HTTP daemon**, use `/system/upgrades` instead of inventing file replacement steps.
7. If the instance exposes only `get_startup_instructions`, `get_skill`, `get_temporary_access_token`, and `get_tool_schema`, switch to the tool HTTP workflow instead of recommending MCP `tools/call`. Load a matching built-in LocalGate skill with `get_skill` before a multi-tool workflow.
8. If a tool accepts a host-provided file object, preserve that structure. For `upload_file`, pass the file object under `file` and the target workspace path under `path`.
9. If `tool_arguments.enable_base64=true`, `/tools/{tool}/call`, `/tasks.args`, and MCP `tools/call.arguments` may be a base64 string; decode it to the tool's normal JSON object shape before reasoning about fields.
10. If `mcp_http.stateless=true`, HTTP MCP `/mcp` does not require `Mcp-Session-Id`; if it is `false`, keep the normal `initialize -> save SessionID -> send Mcp-Session-Id` workflow.

## Required Inputs

You usually need:

- `BASE_URL`, for example `http://127.0.0.1:8000`
- `workspace_id`, usually `default`
- Bearer Token, if HTTP auth is enabled

If they are missing:

- infer `BASE_URL=http://127.0.0.1:8000` only when the user clearly works with local defaults
- otherwise ask

If the user wants you to start the service, provide the exact startup command first.

If HTTP auth is enabled, include:

```bash
-H "Authorization: Bearer <token>"
```

for all protected HTTP and HTTP MCP requests.

## Startup Commands

### Start HTTP API + HTTP MCP

Use this when the user wants `/tools`, `/tasks`, `/sessions`, or `/mcp`.

```bash
go run ./cmd/localgate -config ./localgate.yaml
```

Or use the startup script:

```bash
./scripts/start-http.sh
DAEMON=1 ./scripts/start-http.sh
./scripts/stop-http.sh
```

Default URLs:

- `http://127.0.0.1:8000`
- `http://127.0.0.1:8000/mcp`

### Start stdio MCP only

Use this when the user wants a subprocess MCP server.

```bash
go run ./cmd/localgate -config ./localgate.yaml -mcp
```

Important:

- stdio MCP is not called with `curl`
- HTTP MCP `/mcp` is called with JSON-RPC over HTTP
- `./scripts/start-mcp.sh` does not support `DAEMON=1` because stdio MCP must stay attached to a client process

## Tool Introduction

These are the core tools exposed by LocalGate in the default workspace.

### Filesystem tools

- `read_file`: read a regular text file up to the service limit
- `write_file`: write file content
- `upload_file`: download a host-provided file object from a public HTTP(S) target and save it into the workspace
- `apply_patch`: validate a multi-file patch and commit it with best-effort in-process rollback

For `upload_file`, the expected `file` shape is:

```json
{
  "download_url": "https://...",
  "file_id": "file_123",
  "mime_type": "application/pdf",
  "file_name": "report.pdf"
}
```

The MCP descriptor declares `_meta.openai/fileParams=["file"]` for ChatGPT-compatible hosts.

For `apply_patch`, keep these constraints stable:

- input must be patch syntax starting with `*** Begin Patch`
- supported operations are `Add File`, `Update File`, `Delete File`
- multi-file patches are fully validated before commit and use in-process rollback on commit failure
- updated files preserve their permission bits and concurrently replaced targets are rejected
- crash-atomic and cross-process transactional behavior is not guaranteed
- do not send unified diff and expect it to work

### One-shot command tools

- `run_command`: run a shell command through the task system

By default, these tools are hidden from `/tools` and `tools/list`:

- `list_directory`
- `search_files`
- `run_argv`

Use `run_command` instead when guiding a model through tool discovery under default settings.

### Session tools

- `exec_command`: start a background-capable shell session
- `write_stdin`: send more input and read output from an explicit absolute `cursor`
- `read_thread_terminal`: read buffered terminal output for a known `session_id` from an explicit absolute `cursor`

Preserve the returned `next_offset` and pass it as the next `cursor`. A trimmed buffer is reported through `base_offset`, `truncated`, and `cursor_expired`. Session access must use the workspace that created the session.

### MCP admin tools

- `task_status`
- `task_logs`
- `task_cancel`
- `wait_task_finish`
- `task_result` on HTTP MCP `/mcp`

## HTTP Endpoints You Should Know

Primary HTTP endpoints:

- `GET /tools`
- `GET /tools/{name}`
- `POST /tasks`
- `GET /tasks/{task_id}`
- `POST /tasks/{task_id}/cancel`
- `GET /tasks/{task_id}/logs`
- `GET /tasks/{task_id}/events/stream`
- `GET /tasks/{task_id}/result`
- `POST /sessions/exec`
- `GET /sessions/{session_id}`
- `POST /sessions/{session_id}/stdin`
- `GET /sessions/{session_id}/terminal`
- `POST /sessions/{session_id}/close`
- `POST /system/upgrades`
- `POST /tools/{tool}/call`
- `GET /system/upgrades/{upgrade_id}`
- `GET /approvals/{approval_id}`
- `POST /approvals/{approval_id}/approve`
- `POST /approvals/{approval_id}/reject`
- `GET /audits`

## Tool HTTP Mode

When LocalGate is running in tool HTTP mode, keep this model stable:

- MCP is only the bootstrap/control plane
- HTTP under `/tools/{tool}/call` is the data plane
- detailed interface understanding comes from `get_tool_schema`
- requests must carry:
  - `Authorization: Bearer <localgate-bearer>`
  - `X-LocalGate-Tool-Access: <auth_value>`

Do not tell the model to use filesystem, shell, upload/download, task admin, or session tools in that mode unless they are actually listed by the service.

## Upgrade Workflow

Use `/system/upgrades` only when the user explicitly wants to upgrade a running **HTTP daemon** with a prebuilt binary.

Required headers:

```bash
-H "X-LocalGate-Upgrade-Token: <token>"
```

If global HTTP auth is enabled, still include:

```bash
-H "Authorization: Bearer <token>"
```

Rules:

1. The request must be `multipart/form-data`.
2. The uploaded binary must be sent as form field `file`.
3. Treat the returned `task_id` as the handoff task only.
4. Poll `GET /system/upgrades/{upgrade_id}` for the final cross-restart result.
5. Terminal statuses are `succeeded`, `rolled_back`, and `failed`.

## HTTP MCP Methods You Should Know

For `/mcp`, the important methods are:

- `initialize`
- `ping`
- `tools/list`
- `tools/call`

Treat `/mcp` as JSON-RPC over HTTP, not as REST.

## Standard HTTP Workflow

Use this for normal API-driven execution.

### Step 0: Sanity check the service

Use either a known health endpoint if available in the deployment, or directly test a lightweight endpoint such as:

```bash
curl -sS "$BASE_URL/tools?workspace_id=$WS&summary=1"
```

### Step 1: Discover tools

Fast summary:

```bash
curl -sS "$BASE_URL/tools?workspace_id=$WS&summary=1"
```

Full schema list:

```bash
curl -sS "$BASE_URL/tools?workspace_id=$WS"
```

### Step 2: Inspect one tool schema when arguments matter

```bash
TOOL="run_command"
curl -sS "$BASE_URL/tools/$TOOL?workspace_id=$WS"
```

Do this before constructing complex requests.

### Step 3: Execute one-shot work as a task

```bash
curl -sS -X POST "$BASE_URL/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "run_command",
    "workspace_id": "default",
    "requester": "http",
    "args": {
      "command": "echo hello"
    }
  }'
```

Interpret the response:

- `201` with `task_id`: continue polling
- `202` with `status=pending_approval`, `approval_id`, `approval_url`, and `task_id`: switch to approval flow without resubmitting the tool

### Step 4: Poll task status

```bash
curl -sS "$BASE_URL/tasks/$TASK_ID"
```

Stop polling on:

- `succeeded`
- `failed`
- `canceled`
- `timeout`

### Step 5: Read logs or stream events

Pull logs:

```bash
curl -sS "$BASE_URL/tasks/$TASK_ID/logs?cursor=0"
```

Realtime SSE:

```bash
curl -N "$BASE_URL/tasks/$TASK_ID/events/stream?cursor=0"
```

### Step 6: Read final result

```bash
curl -sS "$BASE_URL/tasks/$TASK_ID/result"
```

## Standard Session Workflow

Use this when the user needs a long-running or interactive process.

### Step 1: Start session

```bash
curl -sS -X POST "$BASE_URL/sessions/exec" \
  -H "Content-Type: application/json" \
  -d '{
    "cmd": "cat",
    "workspace_id": "default",
    "yield_time_ms": 100,
    "max_output_tokens": 200
  }'
```

### Step 2: Write stdin or poll output

```bash
curl -sS -X POST "$BASE_URL/sessions/$SESSION_ID/stdin" \
  -H "Content-Type: application/json" \
  -d '{
    "chars": "",
    "yield_time_ms": 1000,
    "max_output_tokens": 4000
  }'
```

### Step 3: Inspect terminal buffer

```bash
curl -sS "$BASE_URL/sessions/$SESSION_ID/terminal?max_output_tokens=4000"
```

### Step 4: Close session

```bash
curl -sS -X POST "$BASE_URL/sessions/$SESSION_ID/close"
```

## Approval Workflow

Use when `POST /tasks` returns `202`.

The task already exists and is waiting at an approval gate. Approval releases that same task automatically. Do not submit the original tool request again.

### Read approval

```bash
curl -sS "$BASE_URL/approvals/$APPROVAL_ID"
```

### Approve

```bash
curl -sS -X POST "$BASE_URL/approvals/$APPROVAL_ID/approve" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $LOCALGATE_BEARER_TOKEN"
```

### Reject

```bash
curl -sS -X POST "$BASE_URL/approvals/$APPROVAL_ID/reject" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $LOCALGATE_BEARER_TOKEN"
```

### Cancel

```bash
curl -sS -X POST "$BASE_URL/approvals/$APPROVAL_ID/cancel" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $LOCALGATE_BEARER_TOKEN"
```

After approval, query the original task:

```bash
curl -sS "$BASE_URL/tasks/$TASK_ID"
curl -sS "$BASE_URL/tasks/$TASK_ID/result"
```

Browser form submissions must include an `Origin` whose scheme and host exactly match `approval.base_url`; cross-site approval POSTs are rejected. Browser submissions receive an HTML result page, while CLI/API submissions continue to receive JSON.

Approval responses and audit details recursively redact common secret fields. A service restart cancels unresolved pending approvals and marks their waiting tasks with `error_code=approval_restart` because the in-memory gate cannot be recovered.

Approval actor identity is server-derived. With HTTP Bearer authentication enabled, decisions are recorded as principal `http-bearer` with `auth_method=bearer`; with authentication disabled they are recorded as `local-unauthenticated`. Never send or trust `X-LocalGate-Actor`.

Before approval is committed, LocalGate verifies the task is still `awaiting_approval` and that `approval_id`, `task_id`, workspace, requester, tool, and `args_digest` match. A mismatch returns `approval_binding_mismatch` and leaves the approval pending.

Canceled task reasons are stable: `approval_rejected`, `approval_expired`, `approval_canceled`, `approval_restart`, and `approval_release_failed`.

### Query audits

```bash
curl -sS "$BASE_URL/audits?approval_id=$APPROVAL_ID&limit=50&offset=0"
```

## HTTP MCP Workflow

Use this when the user wants MCP over HTTP, not plain REST.

### Initialize

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-11-25",
      "clientInfo": {
        "name": "curl",
        "version": "1.0.0"
      },
      "capabilities": {}
    }
  }'
```

### List tools

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/list"
  }'
```

### Call a tool

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {
      "name": "run_command",
      "arguments": {
        "command": "pwd"
      }
    }
  }'
```

### Call async task flow

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 4,
    "method": "tools/call",
    "params": {
      "name": "run_command",
      "arguments": {
        "command": "go test ./...",
        "async": true
      }
    }
  }'
```

After that, use:

- `task_status`
- `task_logs`
- `wait_task_finish`
- `task_result`

## Error Handling

Expect:

- normal HTTP JSON responses
- SSE `text/event-stream`
- MCP JSON-RPC response envelopes

For HTTP errors, parse and surface:

- `code`
- `message`
- `details`

For MCP errors, parse:

- `error.code`
- `error.message`

Do not hide structured error information. Show the user the actionable part.

Treat `pending_approval` and `task_pending` as nonterminal workflow states, not generic failures. Keep and reuse the returned `task_id`.

## Communication Style

When using this skill:

1. State which transport you chose: HTTP REST, HTTP MCP, or stdio MCP.
2. State whether you are using task flow or session flow.
3. Surface identifiers clearly:
   - `task_id`
   - `session_id`
   - `approval_id`
4. If a request requires follow-up polling, tell the user the next call.
5. If the user is integrating a client, prefer concrete commands and payloads over abstract explanation.

## Minimal Cheatsheet

Set vars:

```bash
export BASE_URL="http://127.0.0.1:8000"
export WS="default"
```

Start service:

```bash
go run ./cmd/localgate -config ./localgate.yaml
```

List tools:

```bash
curl -sS "$BASE_URL/tools?workspace_id=$WS&summary=1"
```

Create task:

```bash
curl -sS -X POST "$BASE_URL/tasks" \
  -H "Content-Type: application/json" \
  -d "{\"tool\":\"run_command\",\"workspace_id\":\"$WS\",\"requester\":\"http\",\"args\":{\"command\":\"echo hello\"}}"
```

Start session:

```bash
curl -sS -X POST "$BASE_URL/sessions/exec" \
  -H "Content-Type: application/json" \
  -d "{\"cmd\":\"cat\",\"workspace_id\":\"$WS\",\"yield_time_ms\":100,\"max_output_tokens\":200}"
```

HTTP MCP list tools:

```bash
curl -X POST "$BASE_URL/mcp" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```
