# MCP HTTP Execution Runtime

> Historical note: references to a default workspace or workspace roots below describe the retired multi-workspace model. The current runtime is bound to one configured root per LocalGate instance.

## Status

Implemented as a single dedicated runtime. There is no legacy or `/mcp/v2` compatibility route.

## Endpoints

### Tool discovery

```http
GET /mcp/tools
```

### Tool execution

```http
POST /mcp/tools/execute
Content-Type: application/json
Authorization: Bearer <token>
```

Example request:

```json
{
  "tool": "deploy_tool",
  "requester": "release-client",
  "input": {
    "type": "static",
    "name": "site-a",
    "source": "/absolute/workspace/path/dist",
    "options": {
      "overwrite": false,
      "preserve_source": true
    }
  }
}
```

`options.preserve_source` defaults to `true`. When set to `false`, LocalGate stages the source directory by renaming it to a temporary sibling, commits the deployment, and then removes the staged source. If deployment fails before commit, the source directory is moved back. Removing a workspace root is always rejected.

Successful direct execution returns:

```json
{
  "success": true,
  "data": {
    "status": "deployed",
    "type": "static",
    "site": "site-a",
    "url": "https://site-a.apps.example.com/",
    "hostname": "site-a.apps.example.com",
    "file_count": 12,
    "total_bytes": 40960,
    "source_preserved": true
  }
}
```

Every execution request goes through the same `App.SubmitToolTask` path used by REST, stdio MCP, and standard HTTP MCP. The decision order is:

1. first matching global `approval.rules` entry
2. dynamic mutation fallback when `deployments.dynamic.require_approval` is enabled
3. static deployment fallback when `deployments.static.require_approval` is enabled
4. legacy workspace command/write-path approval policy

An `allow` decision creates and executes a normal task. A `deny` decision returns HTTP 403 with `code=policy_denied`. A `require_approval` decision creates a gated task immediately and returns HTTP 202 with `approval_id`, `approval_url`, and `task_id`; approving the request releases that same task without requiring the client to replay the tool call.

If an allowed task does not complete inside `approval.max_sync_wait_seconds`, the runtime returns HTTP 202 with `code=task_pending` and the task can be queried through the existing task endpoints.

### Static content

Static deployments use the same host-based routing model as dynamic deployments:

```http
GET {selected_base_url with site prepended as a subdomain}/...
HEAD {selected_base_url with site prepended as a subdomain}/...
```

The control-plane `/static/{site}/...` route is not registered. Application hosts bypass control-plane Bearer/CORS middleware, so external access control must be enforced by the app ingress or a future deployment access policy. Directory listing is disabled and SPA fallback uses the configured site index.

### Deployment overview page

```http
GET https://deploys.{public-domain}/
GET https://deploys.{private-domain}/
```

Each read-only HTML overview is mounted at the `deploys` subdomain on its matching deployment listener and lists only deployments with that access level. The control listener does not expose a deployment list. The pages read static and dynamic deployments through the unified `deployment.Manager` and do not create tasks, approvals, or audit events.

### Dynamic web deployments

When `deployments.dynamic.enabled=true`, `deploy_tool` accepts `type=dynamic` with a runtime argv, relative cwd, allowlisted environment, health path, and restart policy. The HTTP LocalGate process supervises the application, assigns a loopback port, waits for health, and publishes it by prepending `{name}` as a single subdomain label to the selected public/private base URL host.

Dynamic lifecycle tools are `get_deployment`, `restart_deployment`, `get_deployment_logs`, `stop_deployment`, `start_deployment`, `list_deployment_releases`, and `rollback_deployment`. Managed releases include metadata, retention, history, health-checked rollback, rotating logs, lifecycle audit events, and partial-success cleanup warnings.

Control-plane Bearer/CORS middleware only runs for the exact Host derived from `http.base_url`. Application hosts bypass LocalGate control authentication and proxy all paths to the registered application. Unknown hosts return 404.

## Deployment safety

- Source must be an absolute directory inside the configured default workspace roots.
- Source and managed static storage must not contain each other.
- Source symlinks and non-regular files are rejected.
- Site names allow letters, numbers, hyphens, and underscores only.
- Deployments use a staging directory and rename activation.
- Overwrite must be explicitly enabled.
- Source deletion must be explicitly enabled with `preserve_source: false`.
- A workspace root cannot be selected for source deletion.
- Source deletion is staged and restored if deployment does not commit.
- Context cancellation is checked during traversal, copying, activation, and registry commit.
- Default limits are 10,000 files, 50 MiB total, and 50 MiB per file.

## Approval and audit

Approval behavior is controlled by global `approval.enabled`, ordered `approval.rules`, and static/dynamic deployment fallbacks. Dynamic deploy, overwrite, and delete requests use critical risk by default. Audit details include `overwrite` and the effective `preserve_source` value. Execution records use:

- `execution_started`
- `execution_succeeded`
- `execution_failed`
- `execution_canceled`

Audit queries support `approval_id`, `operation_id`, `task_id`, `requester`, `tool`, and `action`.

Approval request arguments and audit details are recursively redacted before persistence and presentation. Browser approval POSTs require an exact Origin match with `admin.base_url`; cross-site submissions are rejected. Browser approval actions return a human-readable HTML result page, while API callers receive JSON.

The approver is derived from the server authentication context rather than request headers or bodies. A valid configured Bearer credential maps to the fixed trusted principal `http-bearer` (`auth_method=bearer`); an instance without HTTP authentication records `local-unauthenticated` (`auth_method=none`). `X-LocalGate-Actor` is ignored.

Approval commits revalidate the live task binding: approval ID, task ID, workspace, requester, tool, argument digest, and `awaiting_approval` status must all match. A mismatch leaves the approval pending, returns `approval_binding_mismatch`, and appends an `approval_validation_failed` audit event.

Approval terminal task reasons are preserved across REST and MCP task-result surfaces: `approval_rejected`, `approval_expired`, `approval_canceled`, `approval_restart`, and `approval_release_failed`.

Pending approvals cannot recover their in-memory gates across a process restart. Startup reconciliation marks expired entries as `expired`, cancels all other pending approvals as `system:restart`, and marks related `awaiting_approval` tasks as canceled with `error_code=approval_restart`.
