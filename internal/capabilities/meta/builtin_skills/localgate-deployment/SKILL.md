---
name: localgate-deployment
description: Use for LocalGate static or dynamic deployment, deployment status and logs, access changes, restart, rollback, release inspection, or deletion. Load before operating deployment tools.
---

# LocalGate Deployment

Use this skill for deployment workflows. Deployment tools are normally Registry capabilities rather than top-level MCP tools. Treat the current capability routing and each tool schema as authoritative; do not assume every management tool is enabled.

## Progressive invocation

In normal MCP mode, call `get_tool_schema` for the selected deployment tool and then execute it through `invoke_tool`. The target tool still owns approval, Task, audit, root policy and deployment semantics. In Tool HTTP mode, follow `localgate-tool-http` instead.

## Deploy

- Use `deploy_tool` for static sites or long-running dynamic web applications sourced from the configured root.
- Read the tool schema before constructing a deployment call. Core fields are `type`, `name`, and `source`.
- `source` must stay inside the configured root and should be an absolute path.
- `type=static` publishes a static directory and does not need `runtime`.
- `type=dynamic` starts a long-running application and requires runtime configuration such as `runtime.argv`; set `runtime.cwd` and `runtime.env` only when the application needs them.
- Dynamic `runtime.argv` is executed directly, not through a shell. `runtime.argv[0]` must match the current instance's configured executable allowlist; use the exact entries in the `Current instance: dynamic runtime` section appended by `get_skill` instead of guessing a package-manager or framework command.
- LocalGate injects `HOST=127.0.0.1` and a dynamically allocated `PORT`. The application must listen on those values. Do not hard-code ports such as 3000, 5173, or 8000 for a managed dynamic deployment.
- LocalGate health-checks `http://127.0.0.1:$PORT<health_path>` and accepts HTTP 2xx/3xx. The default health path is `/`; set `options.health_path` to a lightweight endpoint when `/` is not suitable.
- Only environment keys in the current instance's deployment environment allowlist may be supplied through `runtime.env`; persistent secret-like variables can still be rejected even when a key is allowlisted.
- Use health/startup/restart options only when they are present in the current schema. Do not invent unsupported fields.
- Overwrite an existing deployment only when the user intends replacement. Preserve the source by default unless the user explicitly wants successful deployment to remove it.

Before the first dynamic deployment attempt, inspect the application startup path and make it conform to the injected `HOST`/`PORT` contract. Prefer fixing the app or choosing an already-allowed interpreter over repeatedly retrying deployment with different guessed commands.

## Approval

Deployment changes can require approval. If a call returns `pending_approval` or an equivalent waiting state:

- show the returned `approval_url` and useful approval metadata to the user;
- keep the original `task_id`;
- do not submit the deployment operation again;
- after approval, continue following that same task until it reaches a terminal state.

A rejected, canceled, or expired approval should be reported as such rather than automatically retried.

## Manage existing deployments

Use only tools that are available in the current Registry capability set. Depending on the instance these can include:

- `list_deployments` for the managed deployment inventory;
- `get_deployment` and `get_deployment_logs` for dynamic runtime state and logs;
- `start_deployment`, `stop_deployment`, and `restart_deployment` for lifecycle control;
- `set_deployment_access` for private/public access changes;
- `list_deployment_releases` and `rollback_deployment` for retained releases and rollback;
- `delete_deployment` for destructive removal.

Read the schema before destructive, access-changing, or rollback operations, and inspect current state before repeating an operation.

## Verify completion

Do not treat a successful tool invocation alone as proof that an application is usable. When a deployment returns a URL, verify the URL when possible. For dynamic applications also use runtime status, health information, and logs when available.