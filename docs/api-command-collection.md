# LocalGate.mcp 命令集合文档

本文档汇总 `LocalGate.mcp` 当前对外暴露的全部 HTTP API 与 MCP API，便于调用方快速接入、联调和排障。

如果服务启用了 HTTP Bearer Token 认证，除 `allow_paths` 放行路径外，其余 HTTP 请求都需要带上：

```bash
-H "Authorization: Bearer <your-token>"
```

如果需要浏览器直接跨域访问 LocalGate，可在配置中开启：

```yaml
http:
  base_url: "http://127.0.0.1:8000"
  allow_cors: true
```

开启后，服务会统一返回 `Access-Control-Allow-Origin: *`，并处理 `OPTIONS` 预检请求。

如果希望 HTTP MCP `/mcp` 整体兼容无 `SessionID` 的调用，可以开启：

```yaml
mcp_http:
  stateless: true
```

开启后：

- `/mcp` 使用 stateless 模式
- 服务端不再要求 `Mcp-Session-Id`
- 客户端也不需要保存和回传 `initialize` 返回的 SessionID

如果希望工具参数额外支持 base64 字符串形式，可以开启：

```yaml
tool_arguments:
  enable_base64: true
```

开启后：

- HTTP `POST /tools/{name}/call` 可以直接提交 base64 字符串，请求体解码后必须是该工具原本要求的 JSON object
- HTTP `POST /tasks` 的 `args` 字段也可以是 base64 字符串
- MCP `tools/call` 的 `arguments` 也可以是 base64 字符串

## 概览

### HTTP API

当前 HTTP 路由入口定义在 [api.go](file:///Users/bytedance/project/LocalGate.mcp/internal/httpapi/api.go)。

分为 8 类：

- public 部署只读页面：`https://deploys.{public-domain}/`
- private 部署只读页面：`https://deploys.{private-domain}/`
- 部署管理页面：`approval.base_url/deploy-mgr`
- 工具发现与直调：`/tools`、`/tools/{name}`、`/tools/{name}/call`
- 任务提交与查询：`/tasks`、`/tasks/{id}/*`
- 会话执行：`/sessions/*`
- 审批：`/approvals/{id}/*`
- 审计：`/audits`
- SSE：`/tasks/{id}/events/stream`
- 系统接口：`/system/uploads`、`/system/downloads`、`/system/upgrades`

### MCP API

当前仓库同时支持两种 MCP 访问方式：

- `stdio MCP`：适合被 MCP Client 作为子进程启动
- `HTTP MCP`：挂载在 `/mcp`，可直接使用 `curl`

相关实现：

- stdio 版本：[stdio.go](file:///Users/bytedance/project/LocalGate.mcp/internal/mcp/stdio.go)
- HTTP MCP 挂载：[http.go](file:///Users/bytedance/project/LocalGate.mcp/internal/mcpgo/http.go)
- MCP 工具桥接：[server.go](file:///Users/bytedance/project/LocalGate.mcp/internal/mcpgo/server.go)

支持的 JSON-RPC 方法：

- `initialize`
- `ping`
- `tools/list`
- `tools/call`

其中 `tools/call` 可调用两类工具：

- 普通工具：走 task 异步包装
- session 工具：直接调用

session 直调工具包括：

- `exec_command`
- `write_stdin`
- `read_thread_terminal`
- `close_session`

### ChatGPT / Apps 文件入参

如果 MCP 客户端是 ChatGPT Apps 兼容宿主，工具可以通过 MCP `_meta` 声明文件参数。当前 `upload_file` 已声明：

```json
{
  "_meta": {
    "openai/fileParams": ["file"]
  }
}
```

这表示参数 `file` 可以接收 ChatGPT 风格文件对象，例如：

```json
{
  "download_url": "https://...",
  "file_id": "file_123",
  "mime_type": "application/pdf",
  "file_name": "report.pdf"
}
```

`download_url` 是临时地址。`upload_file` 会读取这个 URL，把内容保存到 `path` 指定的、位于配置 root 内的绝对路径。

## 工具 HTTP 化模式

当服务启用了：

```yaml
tool_http_mode:
  enabled: true
```

LocalGate 会切换到工具 HTTP 化模式。

这个模式下有两个关键变化：

1. MCP 只暴露四个 bootstrap 工具：
   - `get_startup_instructions`
   - `get_skill`
   - `get_temporary_access_token`
   - `get_tool_schema`
2. 业务工具不再通过 MCP `tools/call` 调用，而是通过 `/tools/{tool}/call` 直接访问

### 调用顺序

1. 先读取 `get_startup_instructions`
2. 如果 startup 中的 built-in skill 与当前任务匹配，调用 `get_skill` 按需加载完整工作流说明
3. 调用 `get_temporary_access_token`
4. 读取返回中的：
   - `auth_value`
   - `auth_header_name`
   - `base_url`
   - `call_path_pattern`
5. 从 `get_startup_instructions` 返回的 `tools` 字段里读取每个工具的：
   - `name`
   - `description`
6. 对目标工具调用 `get_tool_schema`
7. 再用 `curl` 或其他 HTTP 客户端访问 `/tools/{tool}/call`

如果目标工具是 `upload_file`，先调用 `get_tool_schema(tool=upload_file)`，然后传：

- `path`：目标绝对路径
- `file`：文件对象，至少包含 `download_url`

### `get_temporary_access_token` 返回结构

典型返回：

```json
{
  "auth_value": "lgta_xxx",
  "auth_header_name": "X-LocalGate-Tool-Access",
  "base_url": "http://127.0.0.1:8000",
  "call_path_pattern": "/tools/{tool}/call",
  "expires_at": "2026-06-26T12:34:56Z"
}
```

注意：

- 字段刻意不使用 `token`
- `auth_value` 是当前 LocalGate 实例的短期访问凭证
- 若服务启用了全局 Bearer 鉴权，工具 HTTP 请求仍然要额外带 `Authorization: Bearer ...`

### 工具 HTTP 路由

#### 1. 列出工具

- `GET /tools`

返回当前 LocalGate 实例可见工具的：

- `name`
- `description`
- `schema`

#### 2. 查看单个工具定义

- `GET /tools/{name}`

#### 3. 直接调用工具

- `POST /tools/{name}/call`

这个接口直接把请求体作为 tool args 传给本地 registry 中的同名工具，并返回原始 tool result。

请求要求：

- body：该工具原始 JSON 参数；若 `tool_arguments.enable_base64=true`，也可以直接传 base64 字符串，解码后仍需是 JSON object
- header：
  - `Authorization: Bearer ...`
  - `X-LocalGate-Tool-Access: <auth_value>`

### 工具模式鉴权示例

先拿 bootstrap token：

```bash
curl "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": { "name": "get_temporary_access_token", "arguments": {} }
  }'
```

再直接调工具：

```bash
curl "http://127.0.0.1:8000/tools/read_file/call" \
  -H "Authorization: Bearer <your-token>" \
  -H "X-LocalGate-Tool-Access: <auth_value>" \
  -H "Content-Type: application/json" \
  -d '{"path":"/abs/path/to/file.txt"}'
```

若开启 `tool_arguments.enable_base64=true`，也可以这样调用：

```bash
printf '%s' '{"path":"/abs/path/to/file.txt"}' | base64 | \
xargs -I{} curl "http://127.0.0.1:8000/tools/read_file/call" \
  -H "Authorization: Bearer <your-token>" \
  -H "X-LocalGate-Tool-Access: <auth_value>" \
  --data '{}'
```

## HTTP API

默认响应为 JSON。若启用了 `http_auth.enabled=true`，除放行路径外，HTTP API 与 `/mcp` 都需要携带 Bearer Token。

### 部署查看与管理页面

查看与管理页面按 Host、Path 和 listener 完全分离：

- `GET https://deploys.{public-domain}/`：只读查看 public 部署。
- `GET https://deploys.{private-domain}/`：只读查看 private 部署。
- `GET {approval.base_url}/deploy-mgr`：部署管理页面。
- `POST {approval.base_url}/deploy-mgr/actions/{type}/{name}/stop`
- `POST {approval.base_url}/deploy-mgr/actions/{type}/{name}/start`
- `POST {approval.base_url}/deploy-mgr/actions/{type}/{name}/delete`

public `deploys` 子域只挂载在 `deployments.public_bind`，private `deploys` 子域只挂载在 `deployments.private_bind`；控制面不提供部署列表。`/deploy-mgr` 及其操作只允许通过 `approval.base_url` 的 Host 访问。交叉访问统一返回 `404`。旧 `/deployment-ui` 路径不再提供服务。

管理操作通过现有 task/approval 管线执行，不会绕过 `approval.rules`、`deployments.static.require_approval` 或 `deployments.dynamic.require_approval`；需要审批时浏览器会跳转到对应审批页面，批准后原任务继续执行。

删除表单必须包含 `confirm=delete`。浏览器管理操作要求 `Origin` 与 `approval.base_url` 的 scheme 和 host 一致，跨站请求返回 `403`。两个页面都兼容各自 base URL 中的反向代理路径前缀。

```bash
curl "https://deploys.apps.example.com/"
curl "https://deploys.private-apps.example.com/" \
  -H "Authorization: Bearer <your-token>"
```

### 应用入口与动态网页部署

仓库提供完整占位模板 `localgate.example.yaml`。首次配置可执行：

```bash
cp localgate.example.yaml localgate.yaml
```

控制面 Host 直接由 `http.base_url` 派生；`deployments.public_base_url` 和 `deployments.private_base_url` 用于静态站点与动态应用的 Host 路由。部署参数 `options.access` 支持 `public` 或 `private`，省略时使用 `default_access`。静态部署不再提供控制面 `/static/docs/` 路径。

管理页面 `/deploy-mgr` 可以把现有静态或动态 deployment 在 public/private 之间转换。转换会进入现有 task/approval 管线，成功后旧域名立即返回 `404`。

启用动态进程托管前，还必须配置 `deployments.dynamic.allowed_executables`；生产环境建议为 executable 使用绝对路径。

启用 `deployments.dynamic` 后，可通过统一工具部署长期运行的 HTTP 应用。进程必须读取 LocalGate 注入的 `HOST=127.0.0.1` 和 `PORT`，并且 `argv[0]` 必须命中 `allowed_executables`。

```bash
curl -X POST "http://127.0.0.1:8000/tasks" \
  -H "Authorization: Bearer <your-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "deploy_tool",
    "args": {
      "type": "dynamic",
      "name": "blog",
      "source": "/Users/yatta/Documents/blog",
      "runtime": {
        "argv": ["node", "server.js"],
        "cwd": ".",
        "env": {"NODE_ENV": "production"}
      },
      "options": {
        "overwrite": true,
        "preserve_source": true,
        "health_path": "/healthz",
        "restart_policy": "on-failure"
      }
    }
  }'
```

部署通过健康检查后，地址由 `options.access` 选择的 base URL 派生。例如 `public_base_url=https://apps.example.com` 时，`blog` 的 public 地址为 `https://blog.apps.example.com/`。

生命周期工具：`get_deployment`、`restart_deployment`、`get_deployment_logs`、`stop_deployment`、`start_deployment`、`list_deployment_releases`、`rollback_deployment`。动态部署、覆盖、删除和 rollback 默认进入高风险审批；审批前不会启动进程。

动态部署使用托管 release 快照运行。日志按 `max_log_bytes` 和 `max_log_files` 轮转，release 按 `release_retention` 保留；可使用 `list_deployment_releases` 查看历史版本，并使用 `rollback_deployment` 切换到仍被保留且健康检查通过的 release。部署已激活但源目录清理失败时，结果会返回 `status=deployed_with_warning`、`cleanup_pending=true` 和稳定 warning code，而不是把在线应用报告为完全失败。

### 1. 列出工具

**接口**

- `GET /tools`

**查询参数**

- `summary=1`：可选，仅返回 `name + description`

**示例**

```bash
curl "http://127.0.0.1:8000/tools"
```

### 2. 查看单个工具定义

**接口**

- `GET /tools/{name}`

**示例**

```bash
curl "http://127.0.0.1:8000/tools/read_file"
```

### 3. 直接调用工具

**接口**

- `POST /tools/{name}/call`

**请求头**

- `Authorization: Bearer <token>`，若启用了全局 HTTP 鉴权
- `X-LocalGate-Tool-Access: <auth_value>`，仅在 `tool_http_mode.enabled=true` 时需要

**示例**

```bash
curl "http://127.0.0.1:8000/tools/read_file/call" \
  -H "Authorization: Bearer <your-token>" \
  -H "X-LocalGate-Tool-Access: <auth_value>" \
  -H "Content-Type: application/json" \
  -d '{"path":"/abs/path/to/file.txt"}'
```

### `upload_file`

用途：

- 当宿主给的是文件对象，而不是当前机器上的本地路径
- 需要把 `download_url` 指向的文件落到 LocalGate 配置 root 内

参数：

```json
{
  "path": "/abs/path/in/root/report.pdf",
  "file": {
    "download_url": "https://example.com/tmp/report.pdf",
    "file_id": "file_123",
    "mime_type": "application/pdf",
    "file_name": "report.pdf"
  }
}
```

返回：

```json
{
  "path": "/abs/path/in/root/report.pdf",
  "bytes": 12345,
  "file_id": "file_123",
  "file_name": "report.pdf",
  "mime_type": "application/pdf"
}
```

### 4. 提交任务

**接口**

- `POST /tasks`

**请求体**

```json
{
  "tool": "run_command",
  "args": {
    "command": "go test ./..."
  },
  "requester": "cli"
}
```

**返回**

- 正常创建：`201`
- 需要审批：`202`，返回 `status=pending_approval`、`approval_id`、`approval_url`、`task_id`、`expires_at`
- 若 `?stream=1`：直接切换为 SSE 输出

需要审批时 task 已经创建，只是停在 approval gate 前。批准后原 task 自动继续，客户端不能重复提交原工具调用。

**示例**

```bash
curl -X POST "http://127.0.0.1:8000/tasks" \
  -H "Content-Type: application/json" \
  -d '{
    "tool": "run_command",
    "args": { "command": "pwd" },
    "requester": "cli"
  }'
```

### 4. 查询任务状态

已结束任务及其事件日志固定保留 48 小时。LocalGate 启动时会先清理一次，运行期间每小时清理一次；排队中、运行中和等待审批的任务不会按该策略删除。

**接口**

- `GET /tasks/{task_id}`

**示例**

```bash
curl "http://127.0.0.1:8000/tasks/<task_id>"
```

### 5. 取消任务

**接口**

- `POST /tasks/{task_id}/cancel`

**示例**

```bash
curl -X POST "http://127.0.0.1:8000/tasks/<task_id>/cancel"
```

### 6. 查询任务结果

**接口**

- `GET /tasks/{task_id}/result`

**返回特点**

- 返回 `task_id`、`status`、`ready`
- 若已有结果，返回 `result`
- 自动附带 `stdout`、`stderr`

**示例**

```bash
curl "http://127.0.0.1:8000/tasks/<task_id>/result"
```

### 7. 查询任务日志

**接口**

- `GET /tasks/{task_id}/logs`

**查询参数**

- `cursor`：可选，默认 `0`

**返回**

```json
{
  "cursor": 12,
  "events": []
}
```

**示例**

```bash
curl "http://127.0.0.1:8000/tasks/<task_id>/logs?cursor=0"
```

### 8. 订阅任务事件流

**接口**

- `GET /tasks/{task_id}/events/stream`

**查询参数 / Header**

- `cursor`
- `Last-Event-ID`

**返回特点**

- SSE 流
- 先回放 backlog，再进入实时订阅
- 每 10 秒发送一次 heartbeat

**示例**

```bash
curl -N "http://127.0.0.1:8000/tasks/<task_id>/events/stream"
```

### 9. 启动后台会话

**接口**

- `POST /sessions/exec`

**请求体**

```json
{
  "cmd": "npm run dev",
  "workdir": ".",
  "yield_time_ms": 1000,
  "max_output_items": 200,
  "shell": "/bin/zsh",
  "login": false,
  "tty": true,
  "requester": "cli",
  "env": {
    "NODE_ENV": "development"
  }
}
```

**返回字段**

- `session_id`
- `status`
- `running`
- `completed`
- `output`
- `items`
- `next_offset`
- `base_offset`
- `truncated`
- `cursor_expired`
- `output_error`
- `exit_code`

`next_offset` 是绝对 cursor。后续读取或写入 stdin 时，应把它作为 `cursor` 传回。若旧输出已被缓冲裁剪，服务端返回当前 `base_offset`，并设置 `truncated=true`、`cursor_expired=true`。

**示例**

```bash
curl -X POST "http://127.0.0.1:8000/sessions/exec" \
  -H "Content-Type: application/json" \
  -d '{
    "cmd": "cat",
    "yield_time_ms": 100,
    "max_output_items": 200
  }'
```

### 10. 查询单个 session

**接口**

- `GET /sessions/{session_id}`

**示例**

```bash
curl "http://127.0.0.1:8000/sessions/<session_id>"
```

### 11. 向 session 写入 stdin

**接口**

- `POST /sessions/{session_id}/stdin`

**请求体**

```json
{
  "chars": "help\n",
  "yield_time_ms": 1000,
  "max_output_items": 200
}
```

**说明**

- `chars=""` 可用于仅轮询新增输出

**示例**

```bash
curl -X POST "http://127.0.0.1:8000/sessions/<session_id>/stdin" \
  -H "Content-Type: application/json" \
  -d '{
    "chars": "",
    "yield_time_ms": 1000,
    "max_output_items": 200
  }'
```

### 12. 读取 session 终端缓冲

**接口**

- `GET /sessions/{session_id}/terminal`

**查询参数**

- `max_output_items`
- `wait_ms`：可选，等待新输出或 session 终态；旧 `max_output_tokens` 仍作为兼容 alias

**示例**

```bash
curl "http://127.0.0.1:8000/sessions/<session_id>/terminal?max_output_items=200&wait_ms=1000"
```

### 13. 关闭 session

**接口**

- `POST /sessions/{session_id}/close`

**示例**

```bash
curl -X POST "http://127.0.0.1:8000/sessions/<session_id>/close"
```

### 14. 审批管理与详情

审批管理入口为：

```text
{approval.base_url}/approval-ui
```

管理页支持 pending、all、approved、rejected、canceled 和 expired 状态筛选，并可直接打开详情、批准或拒绝 pending 请求。

**接口**

- `GET /approval-ui`（浏览器审批管理列表页，默认显示 pending）
- `GET /approval-ui?status=all`
- `GET /approval-ui/{approval_id}`（浏览器审批详情页）
- `GET /approvals?status=pending`（JSON 列表）
- `GET /approvals/{approval_id}`（JSON 详情）

所有 `/approval-ui` 和 `/approvals` 路由只允许通过 `approval.base_url` 配置的 Host 访问。通过控制面域名、localhost 或其他 Host 访问时统一返回 `404`。当审批 Host 与 `http.base_url` 不同时，审批 Host 上的 `/tasks`、`/mcp` 等非审批路由同样返回 `404`。

**示例**

```bash
APPROVAL_BASE_URL="https://approve.example.com"
curl "$APPROVAL_BASE_URL/approvals/<approval_id>"
```

### 15. 通过审批

**接口**

- `POST /approvals/{approval_id}/approve`

**示例**

```bash
curl -X POST "$APPROVAL_BASE_URL/approvals/<approval_id>/approve" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <localgate-bearer-token>"
```

批准成功后，原 `task_id` 自动从 `awaiting_approval` 进入 `queued` / `running`。继续查询 `/tasks/{task_id}` 和 `/tasks/{task_id}/result`，不要重新提交原操作。

### 16. 拒绝审批

**接口**

- `POST /approvals/{approval_id}/reject`

```bash
curl -X POST "$APPROVAL_BASE_URL/approvals/<approval_id>/reject" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <localgate-bearer-token>"
```

### 17. 取消审批

```bash
curl -X POST "$APPROVAL_BASE_URL/approvals/<approval_id>/cancel" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <localgate-bearer-token>"
```

浏览器表单审批要求 `Origin` 的 scheme 和 host 与 `approval.base_url` 精确一致，跨站 POST 会返回 `403`。浏览器成功后返回友好的 HTML 结果页，CLI/API 请求仍返回 JSON。

审批参数与审计详情会递归脱敏常见 secret 字段。服务重启时，未决 pending 审批会被过期或取消，关联 task 返回 `error_code=approval_restart`。

审批人由服务端认证上下文确定，客户端不能通过 `X-LocalGate-Actor` 指定。启用 Bearer 鉴权时 actor 为可信 principal `http-bearer`、`auth_method=bearer`；未启用鉴权时明确记录为 `local-unauthenticated`。

批准前会重新校验 approval 与 task 的 `approval_id`、`task_id`、requester、tool、`args_digest` 以及 task 必须仍为 `awaiting_approval`。不一致返回 `409 approval_binding_mismatch`，并记录 `approval_validation_failed` 审计。

审批导致的 task 终态错误码包括：`approval_rejected`、`approval_expired`、`approval_canceled`、`approval_restart`、`approval_release_failed`。

### 18. 查询审计记录

**接口**

- `GET /audits`

**查询参数**

- `approval_id`
- `operation_id`
- `task_id`
- `requester`
- `tool`
- `action`
- `limit`
- `offset`

**示例**

```bash
curl "http://127.0.0.1:8000/audits?requester=cli&limit=20&offset=0"
```

## HTTP 工具集合

当前工具由单一 LocalGate Registry 注册，当前实例包含以下工具能力。

### 文件系统工具

- `read_file`
- `write_file`
- `apply_patch`

### 一次性命令工具

- `run_command`

### 会话工具

- `exec_command`
- `write_stdin`
- `read_thread_terminal`

## MCP API

### 启动方式

#### 1. 启动 HTTP + MCP `/mcp`

这个模式会同时提供：

- 普通 HTTP API
- HTTP MCP `/mcp`

启动命令：

```bash
go run ./cmd/localgate -config ./localgate.yaml
```

或直接使用脚本：

```bash
./scripts/start-http.sh
```

默认地址：

```text
http://127.0.0.1:8000
http://127.0.0.1:8000/mcp
```

Bearer Token 配置示例：

```yaml
http_auth:
  enabled: true
  bearer_token: "replace-with-your-token"
  allow_paths:
    - /healthz
```

#### 2. 启动 stdio MCP

这个模式只启动 MCP stdio server，不提供 HTTP。

启动命令：

```bash
go run ./cmd/localgate -config ./localgate.yaml -mcp
```

或直接使用脚本：

```bash
./scripts/start-mcp.sh
```

说明：

- `stdio MCP` 不能直接用 `curl`
- `HTTP MCP /mcp` 可以直接用 `curl`

### 编译脚本

默认编译到 `bin/localgate`：

```bash
./scripts/build.sh
```

常用参数：

```bash
VERSION=0.1.0 ./scripts/build.sh
OUTPUT=./bin/localgate-dev ./scripts/build.sh
GOOS=linux GOARCH=arm64 ./scripts/build.sh
```


控制面、public deployment 和 private deployment 使用三个独立 listener：

```yaml
bind: "127.0.0.1:3737"
deployments:
  public_bind: "127.0.0.1:3738"
  private_bind: "127.0.0.1:3740"
```

三个配置必须使用不同 TCP 端口。控制面 listener 不注册任何 deployment handler；public/private listener 分别只服务对应 access 的部署和各自的 `deploys` 子域列表，也不注册 `/tasks`、`/mcp`、管理后台或控制面 `/healthz`。

### 启动脚本参数

HTTP 启动脚本支持：

```bash
CONFIG=./localgate.yaml ./scripts/start-http.sh
BIND=127.0.0.1:9000 PUBLIC_DEPLOYMENTS_BIND=127.0.0.1:9001 PRIVATE_DEPLOYMENTS_BIND=127.0.0.1:9003 ./scripts/start-http.sh
SKIP_BUILD=1 ./scripts/start-http.sh
DAEMON=1 ./scripts/start-http.sh
DAEMON=1 LOG_FILE=./.localgate/run/http.log PID_FILE=./.localgate/run/http.pid ./scripts/start-http.sh
```

其中 daemon 模式仅适用于 HTTP 启动脚本：

- `DAEMON=1`：后台启动 LocalGate HTTP 服务
- `RUN_DIR`：默认 `./.localgate/run`
- `PID_FILE`：默认 `$RUN_DIR/localgate-http.pid`
- `LOG_FILE`：默认 `$RUN_DIR/localgate-http.log`

LocalGate 使用固定的内置隐藏工具列表。这些工具不会出现在 `GET /tools` 或 MCP `tools/list` 中，也不能通过通用任务入口 `POST /tasks` 调用：

- `run_argv`
- `search_files`
- `list_directory`
- `create_upload_token`
- `create_download_token`

这些隐藏项不可通过 root 配置动态增删。其中前三项可由模型通过 `run_command` 组合实现；上传和下载令牌工具保留给内部或专用传输流程。

脚本会在启动成功后输出 pid、pid 文件和日志文件路径；若 pid 文件已存在且进程仍存活，脚本会直接失败，避免重复启动。

daemon 启动的子进程会自动注入以下 runtime 环境变量，供自升级流程复用：

- `LOCALGATE_RUNTIME_MODE`
- `LOCALGATE_RUNTIME_BIN`
- `LOCALGATE_RUNTIME_CONFIG`
- `LOCALGATE_RUNTIME_BIND`
- `LOCALGATE_RUNTIME_PUBLIC_DEPLOYMENT_BIND`
- `LOCALGATE_RUNTIME_PRIVATE_DEPLOYMENT_BIND`
- `LOCALGATE_RUNTIME_PID_FILE`
- `LOCALGATE_RUNTIME_LOG_FILE`
- `LOCALGATE_RUNTIME_RUN_DIR`

HTTP 停止脚本支持：

```bash
./scripts/stop-http.sh
PID_FILE=./.localgate/run/http.pid ./scripts/stop-http.sh
STOP_TIMEOUT_SECONDS=15 ./scripts/stop-http.sh
```

停止脚本参数：

- `RUN_DIR`：默认 `./.localgate/run`
- `PID_FILE`：默认 `$RUN_DIR/localgate-http.pid`
- `STOP_TIMEOUT_SECONDS`：默认 `10`

停止脚本会优先发送 `SIGTERM`，等待进程退出；若超时未退出，再发送 `SIGKILL`，最后清理 pid 文件。

### 自动升级接口

仅支持 **HTTP daemon** 模式，且需要额外配置环境变量：

```bash
export LOCALGATE_UPGRADE_TOKEN=replace-me
```

升级接口使用独立请求头：

```bash
-H "X-LocalGate-Upgrade-Token: replace-me"
```

#### 1. 上传升级二进制

**接口**

- `POST /system/upgrades`

**请求要求**

- `multipart/form-data`
- 表单字段 `file`
- 若全局 HTTP Bearer 鉴权开启，仍需同时携带 `Authorization: Bearer ...`

**示例**

```bash
curl -X POST "http://127.0.0.1:8000/system/upgrades" \
  -H "X-LocalGate-Upgrade-Token: replace-me" \
  -F "file=@./bin/localgate"
```

**返回**

```json
{
  "upgrade_id": "01...",
  "task_id": "01...",
  "status": "accepted"
}
```

说明：

- 上传接口本身是异步的
- `task_id` 表示“文件已接收并移交给 upgrade runner”的任务
- 最终跨重启升级结果请查询升级状态接口

#### 2. 查询升级状态

**接口**

- `GET /system/upgrades/{upgrade_id}`

**示例**

```bash
curl "http://127.0.0.1:8000/system/upgrades/<upgrade_id>"
```

**状态说明**

- `uploaded`
- `validated`
- `handoff_started`
- `stopping_old`
- `replacing_binary`
- `starting_new`
- `health_checking`
- `succeeded`
- `rollback_started`
- `rolled_back`
- `failed`

stdio MCP 启动脚本支持：

```bash
CONFIG=./localgate.yaml ./scripts/start-mcp.sh
SKIP_BUILD=1 ./scripts/start-mcp.sh
```

说明：

- `start-mcp.sh` 不支持 `DAEMON=1`
- 原因是 stdio MCP 需要和调用它的客户端进程保持 stdin/stdout 连接，脱离前台后不再具备正常交互语义

### HTTP MCP `/mcp` curl 示例

以下命令默认服务已通过：

```bash
go run ./cmd/localgate -config ./localgate.yaml
```

启动，并监听在 `http://127.0.0.1:8000/mcp`。

#### 1. initialize

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
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

#### 2. ping

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "ping"
  }'
```

#### 3. tools/list

如果配置了下游 MCP server 中继，返回结果里还会包含前缀化的下游工具名，格式为 `server__tool`，例如 `github__search_repositories`。

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/list"
  }'
```

#### 4. tools/call: `read_file`

调用中继工具时同样使用 `tools/call`，只需把 `params.name` 换成 `server__tool`。这类工具的参数和返回值由下游 MCP server 决定，不应假设它们遵循 LocalGate 本地 root 的文件 sandbox 约束。

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 4,
    "method": "tools/call",
    "params": {
      "name": "read_file",
      "arguments": {
        "path": "plan.md"
      }
    }
  }'
```

#### 5. tools/call: `run_command`

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 5,
    "method": "tools/call",
    "params": {
      "name": "run_command",
      "arguments": {
        "command": "pwd"
      }
    }
  }'
```

#### 5.1 tools/call: `apply_patch`

不支持 unified diff。多文件 patch 会先完整验证，再依次提交；提交失败时执行进程内回滚。更新会保留文件权限并拒绝已被并发替换的目标，但不承诺进程崩溃或多个 LocalGate 进程之间的事务原子性。

#### 5.2 `create_upload_token` / `create_download_token` 返回字段

这两个工具为了减少自动脱敏对模型读取结果的影响，返回体里的结构化字段避免使用 `token` 命名：

- 上传工具返回 `upload_url`、`auth_header_name`、`auth_value`
- 下载工具返回 `download_url`、`auth_query_key`、`auth_value`

注意这只影响工具返回字段名；底层 HTTP 协议本身不变，上传仍使用 `X-LocalGate-Upload-Token` 请求头，下载 URL 仍使用 `token` query 参数。

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 51,
    "method": "tools/call",
    "params": {
      "name": "apply_patch",
      "arguments": {
        "patch": "*** Begin Patch\n*** Update File: plan.md\n@@\n-old line\n+new line\n*** End Patch"
      }
    }
  }'
```

#### 6. tools/call: `exec_command`

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-token>" \
  -d '{
    "jsonrpc": "2.0",
    "id": 6,
    "method": "tools/call",
    "params": {
      "name": "exec_command",
      "arguments": {
        "cmd": "echo hello",
        "yield_time_ms": 200,
        "max_output_items": 200
      }
    }
  }'
```

#### 7. tools/call: 异步任务

普通工具支持 `async: true`，先返回 `task_id`，再继续查询。

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 7,
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

#### 8. tools/call: `task_status`

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 8,
    "method": "tools/call",
    "params": {
      "name": "task_status",
      "arguments": {
        "task_id": "your-task-id"
      }
    }
  }'
```

#### 9. tools/call: `task_logs`

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 9,
    "method": "tools/call",
    "params": {
      "name": "task_logs",
      "arguments": {
        "task_id": "your-task-id",
        "cursor": 0
      }
    }
  }'
```

#### 10. tools/call: `task_result`

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 10,
    "method": "tools/call",
    "params": {
      "name": "task_result",
      "arguments": {
        "task_id": "your-task-id"
      }
    }
  }'
```

#### 11. tools/call: `wait_task_finish`

等待指定任务进入终态，仅返回任务状态，不返回具体执行结果。

```bash
curl -X POST "http://127.0.0.1:8000/mcp" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 11,
    "method": "tools/call",
    "params": {
      "name": "wait_task_finish",
      "arguments": {
        "task_id": "your-task-id",
        "timeout_ms": 60000
      }
    }
  }'
```

### stdio MCP JSON-RPC 说明

stdio 模式仍然使用单行 JSON-RPC，请求格式与下文一致，但应通过 MCP Client 或 stdin/stdout 交互，不是 `curl`。

### 1. initialize

**请求**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-11-25"
  }
}
```

### 2. ping

**请求**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "ping"
}
```

### 3. tools/list

**请求**

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/list"
}
```

**返回内容**

- 当前实例 Registry 中所有注册工具
- 3 个管理工具：
  - `task_status`
  - `task_logs`
  - `task_cancel`
  - `wait_task_finish`
- 如果配置了下游 MCP server 中继，还会出现 `server__tool` 形式的下游工具名；这类工具由 LocalGate 转发到下游 MCP `tools/call`

### 4. tools/call

**请求格式**

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "tools/call",
  "params": {
    "name": "read_file",
    "arguments": {
      "path": "README.md"
    }
  }
}
```

**返回格式**

- `result.content[0].text`：文本化 JSON
- `result.structuredContent`：结构化结果
- `result.isError`：是否错误
- 当 `params.name` 是 `server__tool` 时，参数和返回体遵循下游工具自己的 schema，不应假设它与本地 `read_file` / `run_command` 等工具行为一致

## MCP 工具清单

### 普通工具

普通工具通过 `tools/call` 进入 task 提交链路。

- `read_file`
- `write_file`
- `apply_patch`
- `run_command`

### session 直调工具

这 3 个工具不会进入 task 包装，而是直接调用。

- `exec_command`
- `write_stdin`
- `read_thread_terminal`

### MCP 管理工具

- `task_status`
- `task_logs`
- `task_cancel`
- `wait_task_finish`

## MCP 工具参数速查

### `read_file`

```json
{
  "path": "/absolute/root/path"
}
```

只接受普通文本文件，默认最大 8 MiB；目录、特殊文件、二进制文件和超限文件会被拒绝。

### `write_file`

```json
{
  "path": "relative/or/absolute/path",
  "content": "file content"
}
```

### `list_directory`

```json
{
  "path": "/absolute/root/path",
  "recursive": true,
  "max_results": 1000
}
```

达到结果上限时返回 `truncated=true`。

### `search_files`

```json
{
  "root": "/absolute/root/path",
  "glob": "*.go",
  "max_results": 50
}
```

非法 glob 会被拒绝；达到结果上限时返回 `truncated=true`。

### `apply_patch`

```json
{
  "patch": "*** Begin Patch\n*** Update File: path/to/file\n@@\n-old line\n+new line\n*** End Patch"
}
```

说明：
- 支持 `Add File` / `Update File` / `Delete File`
- 整个 patch 会先验证，再依次提交；提交失败时执行进程内回滚
- 更新保留权限并拒绝已被并发替换的目标
- 不承诺进程崩溃或多进程场景的事务原子性

### `run_command`

```json
{
  "command": "go test ./...",
  "cwd": ".",
  "timeout_ms": 600000,
  "env": {
    "GOFLAGS": "-count=1"
  }
}
```

### `run_argv`

```json
{
  "argv": ["go", "test", "./..."],
  "cwd": ".",
  "timeout_ms": 600000,
  "env": {
    "GOFLAGS": "-count=1"
  }
}
```

### `exec_command`

```json
{
  "cmd": "npm run dev",
  "workdir": ".",
  "yield_time_ms": 1000,
  "max_output_items": 200,
  "shell": "/bin/zsh",
  "login": false,
  "tty": true,
  "env": {
    "NODE_ENV": "development"
  }
}
```

### `write_stdin`

```json
{
  "session_id": "session-id",
  "chars": "",
  "yield_time_ms": 1000,
  "max_output_items": 200,
  "cursor": 120
}
```

### `read_thread_terminal`

```json
{
  "session_id": "session-id",
  "max_output_items": 200,
  "wait_ms": 1000,
  "cursor": 120
}
```

### `close_session`

```json
{
  "session_id": "session-id"
}
```

每次保存返回的 `next_offset` 并作为下一次 `cursor`；`has_more=true` 时继续读取，`completed=true` / `output_complete=true` 表示终态 Session 的保留输出已追平。`buffer_truncated=true` 表示旧缓冲已被裁剪，`cursor_expired=true` 表示传入 cursor 已失效。优先使用 `max_output_items`；`max_output_tokens` 仅是兼容 alias，并不按模型 token 计数。Session 属于当前 LocalGate 实例，后续读写只需要 `session_id` 和 cursor。

### `task_status`

```json
{
  "task_id": "task-id"
}
```

### `task_logs`

```json
{
  "task_id": "task-id",
  "cursor": 0
}
```

### `task_cancel`

```json
{
  "task_id": "task-id"
}
```

### `wait_task_finish`

```json
{
  "task_id": "task-id",
  "timeout_ms": 60000
}
```

## 典型工作流

### 场景 1：HTTP 提交一次性任务

1. `POST /tasks`
2. `GET /tasks/{task_id}`
3. `GET /tasks/{task_id}/result`
4. 如需增量日志，调用 `GET /tasks/{task_id}/logs`

### 场景 2：HTTP 启动后台命令并持续交互

1. `POST /sessions/exec`
2. 从返回中拿到 `session_id`
3. `POST /sessions/{id}/stdin`
4. `GET /sessions/{id}/terminal`
5. 完成后 `POST /sessions/{id}/close`

### 场景 3：MCP 调用后台命令

1. `initialize`
2. `tools/list`
3. `tools/call(name=exec_command)`
4. `tools/call(name=write_stdin)`
5. `tools/call(name=read_thread_terminal)`

## 备注

- `exec_command`、`write_stdin`、`read_thread_terminal` 是本仓库里最适合“长运行进程 / REPL / dev server”的调用方式。
- `run_command`、`run_argv` 更适合一次性命令。
- 若命中审批策略，HTTP `/tasks` 和 MCP 普通工具调用可能返回审批态；session 直调工具当前走直接调用路径。
