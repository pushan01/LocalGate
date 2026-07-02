# LocalGate 统一管理后台规划

## 状态

实施中。第一阶段的统一调用元数据、Task 历史查询、管理 JSON API、独立管理前端、管理域隔离和兼容审批链接已经完成。审批配置和公开地址已归入 `admin.approval` 与 `admin.base_url`，不再存在独立审批域；后续继续进行旧模板迁移、前端工程化和增强型实时更新。

本文档定义 LocalGate 统一管理后台的目标、领域模型、后端 API、前端工程、鉴权边界、迁移路径、测试范围和验收标准。该后台统一承载工具调用历史、MCP Session 调用筛选、部署管理和审批管理，并采用前后端分离架构。

## 1. 背景

LocalGate 当前存在多套分散的管理入口：

- public/private 域名下的 `deploys` 子域根路径：按 access 分开的只读部署列表页面
- `/deploy-mgr`：部署管理页面
- `/approval-ui`：审批列表和审批详情页面
- `/audits`：审计 JSON 查询接口
- `/tasks`、`/tasks/{id}`：Task 创建和查询接口

现有管理页面由 Go `html/template` 直接渲染，页面样式、导航、交互和数据模型相互独立。工具执行历史也没有统一的列表视图，MCP 调用无法按 `Mcp-Session-Id` 聚合查询。

本次改造需要解决以下问题：

1. 将历史、部署和审批整合为一个管理后台。
2. 管理后台前后端分离，Go 后端不再承担业务页面渲染。
3. 每次工具调用都形成统一的 Task 历史记录。
4. `session_id` 为可选字段；传入时可按 MCP Session 查询，不传时不影响调用。
5. Task、Approval、Audit、日志和部署生命周期能够相互关联。
6. 保持现有持久化文件和旧客户端兼容。

## 2. 目标

### 2.1 功能目标

统一后台至少包含以下模块：

- Overview：运行概览和关键统计
- History：全局工具调用历史和 Task 详情
- Deployments：静态、动态部署的查看和管理
- Approvals：审批请求查看、批准、拒绝和取消

调用历史需要支持：

- 全局查看全部工具调用
- 按可选 `session_id` 精确筛选
- 按 tool、status、transport、workspace、requester 和时间范围筛选
- 查看 Task 结果、stdout、stderr、事件和审计记录
- 从 Task 跳转到关联 Approval 或 Deployment

### 2.2 架构目标

- 后端仅提供 JSON、SSE 和静态资源服务。
- 前端作为独立 TypeScript 工程开发。
- 前端和后端接口通过稳定的版本化 API 连接。
- 生产环境默认同源部署，避免不必要的跨域和凭据暴露。
- 管理域和 MCP/control 域继续保持 Host 隔离。

### 2.3 非目标

首个版本不包含：

- 多用户 RBAC
- SQLite 或外部数据库迁移
- 跨 LocalGate 实例聚合
- WebSocket 实时总线
- 审计全文检索
- 复杂报表或长期趋势分析
- 独立 OAuth/OIDC 登录

## 3. 总体架构

```text
Browser
  |
  |-- /admin/*
  |     React SPA
  |
  `-- /api/admin/v1/*
        Go JSON/SSE API
          |
          |-- core.Manager / Task Store
          |-- approval.Service / Audit Store
          |-- deployment.Manager
          `-- Event Sink
```

前端和后端在代码和接口上完全分离。生产环境可以由 LocalGate 使用 `go:embed` 托管构建后的前端资源，但前端只能通过 `/api/admin/v1` 获取数据和执行操作。

建议生产地址：

```text
https://gatectl.example.com/admin
https://gatectl.example.com/api/admin/v1
```

开发环境：

```text
Vite:      http://127.0.0.1:5173
LocalGate: http://127.0.0.1:3737
```

开发模式由 Vite 将 `/api/admin/v1` 代理到 LocalGate；生产环境默认同源，不启用 CORS。

## 4. 统一领域模型

### 4.1 Task 是调用历史主记录

一次工具调用对应一个 Task：

```text
Task 1 --- 0..1 Approval
  |
  |--- 0..N Task Events
  `--- 0..N Audit Records
```

历史列表必须以 Task 为数据源，不能直接以 Audit 为数据源，原因如下：

- 一次工具调用可能产生多条审计记录。
- 普通工具目前不一定写入审批审计。
- Audit 表达的是生命周期事件，而不是调用主实体。
- Task 已经具备状态、时间、结果和错误信息。

### 4.2 统一调用元数据

新增统一调用元数据：

```go
type Meta struct {
    Transport       string `json:"transport,omitempty"`
    MCPSessionID    string `json:"session_id,omitempty"`
    SessionMode     string `json:"session_mode,omitempty"`
    SessionIDSource string `json:"session_id_source,omitempty"`
    RequestID       string `json:"request_id,omitempty"`
    Requester       string `json:"requester,omitempty"`
    WorkspaceID     string `json:"workspace_id,omitempty"`
}
```

建议放置在：

```text
internal/invocation/meta.go
internal/invocation/transport.go
```

Transport 稳定枚举：

```text
mcp_http
mcp_stdio
mcp_runtime
rest
tool_http
admin
system
```

Session mode：

```text
stateful
stateless
none
```

Session ID source：

```text
server_managed
client_supplied
none
```

### 4.3 Session ID 规则

`session_id` 不是必选项。

行为规则：

| 场景 | session_id | session_mode | session_id_source |
|---|---|---|---|
| Stateful MCP，服务端生成 | 有值 | `stateful` | `server_managed` |
| Stateless MCP，客户端传入 | 有值 | `stateless` | `client_supplied` |
| Stateless MCP，客户端未传 | 空 | `stateless` | `none` |
| REST、管理后台、系统调用 | 通常为空 | `none` | `none` |

Stateless 模式下的 `Mcp-Session-Id` 只作为客户端提供的关联标识使用，不作为已经认证或由服务端维护的安全会话。

### 4.4 字段贯穿范围

以下结构需要携带统一调用元数据：

- `core.Task`
- `core.InvokeContext`
- `approval.Request`
- `approval.AuditRecord`

新增字段使用 `omitempty`，旧 JSON 文件缺失这些字段时自动按空值处理，不执行强制数据迁移。

## 5. 工具提交入口统一

新增接口：

```go
func (a *App) SubmitToolTaskWithMeta(
    meta invocation.Meta,
    tool string,
    args map[string]any,
) (Submission, error)
```

旧接口在迁移期间保留为兼容包装：

```go
func (a *App) SubmitToolTask(
    wsid, requester, tool string,
    args map[string]any,
) (Submission, error)
```

现有调用入口按以下方式迁移：

| 当前入口 | Transport | Session ID |
|---|---|---|
| 标准 `/mcp` Streamable HTTP | `mcp_http` | 从 mcp-go context 获取 |
| `localgate -mcp` stdio | `mcp_stdio` | 从 mcp-go context 获取 |
| 旧 `internal/mcp/stdio.go` | `mcp_stdio` | 进程级生成或空值 |
| `/mcp/tools/execute` | `mcp_runtime` | 请求显式提供时记录 |
| `/tasks` REST | `rest` | 请求显式提供时记录 |
| `/tools/{tool}/call` | `tool_http` | 通常为空 |
| 管理后台部署操作 | `admin` | 通常为空 |
| Supervisor 和启动恢复 | `system` | 空值 |

标准 MCP handler 使用：

```go
sessionID := ""
if session := server.ClientSessionFromContext(ctx); session != nil {
    sessionID = strings.TrimSpace(session.SessionID())
}
```

## 6. Task 查询和持久化

### 6.1 Task 查询

`core.Manager` 新增：

```go
type TaskFilter struct {
    SessionID     string
    Transport     string
    Tool          string
    Status        Status
    WorkspaceID   string
    Requester     string
    ApprovalID    string
    CreatedAfter  time.Time
    CreatedBefore time.Time
    Limit         int
    Cursor        string
}

type TaskPage struct {
    Items      []Task
    NextCursor string
}

func (m *Manager) List(filter TaskFilter) TaskPage
```

排序固定为：

```text
created_at DESC, id DESC
```

首个版本从 Manager 内存 Task map 查询，返回深拷贝，避免调用方修改内部状态。

### 6.2 Cursor 分页

Cursor 包含最后一项的：

```json
{
  "created_at": "2026-07-01T10:20:30.123Z",
  "id": "task-id"
}
```

编码使用 URL-safe base64。无效 cursor 返回 HTTP 400，不退回 offset 模式。

### 6.3 Task 持久化时机

Task 在以下状态变化时必须保存：

- created
- awaiting_approval
- queued
- running
- result updated
- succeeded
- failed
- canceled
- timeout

持久化操作不应长时间持有 Manager mutex。状态修改时在锁内生成 Task snapshot，解锁后写入 Store。

### 6.4 启动恢复

服务重启后无法恢复执行中的 goroutine，因此历史中的非终态 Task 必须被终结：

```text
queued/running:
  status = canceled
  error_code = service_restart
  error_msg = task was interrupted by service restart

awaiting_approval:
  沿用审批启动恢复规则
  error_code = approval_restart
```

恢复结果需要再次持久化，并产生相应审计事件。

## 7. 后端管理 API

### 7.1 包结构

新增独立管理 API 包：

```text
internal/adminapi/
  api.go
  response.go
  dashboard.go
  tasks.go
  approvals.go
  deployments.go
  events.go
  filters.go
```

不继续向 `internal/httpapi/api.go` 添加大量管理逻辑。

API 统一前缀：

```text
/api/admin/v1
```

错误格式沿用：

```json
{
  "code": "task_not_found",
  "message": "task not found",
  "details": {}
}
```

### 7.2 Overview

```http
GET /api/admin/v1/overview
```

返回：

- 最近 24 小时 Task 总数
- running、queued、failed 数量
- pending Approval 数量
- 静态和动态 Deployment 数量
- 失败 Deployment 数量
- 最近 Task 和 Approval 摘要

### 7.3 History API

```http
GET  /api/admin/v1/tasks
GET  /api/admin/v1/tasks/{task_id}
GET  /api/admin/v1/tasks/{task_id}/events
GET  /api/admin/v1/tasks/{task_id}/audits
POST /api/admin/v1/tasks/{task_id}/cancel
```

列表筛选参数：

```text
session_id
transport
tool
status
workspace_id
requester
approval_id
created_after
created_before
limit
cursor
```

列表 DTO 不返回完整 Result、stdout、stderr、完整 arguments 或全部审计，避免列表请求读取大量数据。

Task 详情返回：

- Task 基本信息
- 调用元数据
- 参数摘要
- 结构化 Result
- Error
- 关联 Approval 摘要
- 关联 Deployment 摘要

Events、Audit 和流式日志使用独立接口加载。

### 7.4 Approval API

```http
GET  /api/admin/v1/approvals
GET  /api/admin/v1/approvals/{approval_id}
POST /api/admin/v1/approvals/{approval_id}/approve
POST /api/admin/v1/approvals/{approval_id}/reject
POST /api/admin/v1/approvals/{approval_id}/cancel
```

筛选参数：

```text
status
tool
workspace_id
requester
session_id
created_after
created_before
limit
cursor
```

审批动作只返回 JSON，不返回 HTML 页面或重定向。

审批通过时返回更新后的 Approval 和关联 Task；审批已经终结时返回 HTTP 409。

### 7.5 Deployment API

```http
GET    /api/admin/v1/deployments
GET    /api/admin/v1/deployments/{type}/{name}
POST   /api/admin/v1/deployments/{type}/{name}/start
POST   /api/admin/v1/deployments/{type}/{name}/stop
POST   /api/admin/v1/deployments/{type}/{name}/restart
POST   /api/admin/v1/deployments/{type}/{name}/rollback
DELETE /api/admin/v1/deployments/{type}/{name}
GET    /api/admin/v1/deployments/dynamic/{name}/logs
GET    /api/admin/v1/deployments/dynamic/{name}/releases
```

所有变更操作必须通过 `SubmitToolTaskWithMeta`，使用：

```text
transport = admin
requester = 当前认证 principal
```

禁止管理 API 直接调用 `deployment.Manager.Start/Stop/Delete` 绕过 Task 和 Approval。

删除请求必须包含显式确认：

```json
{
  "confirmation": "delete"
}
```

### 7.6 异步响应

部署变更和其他长任务不在 HTTP handler 内等待完成。

普通提交：

```json
{
  "task_id": "task-id",
  "status": "queued",
  "approval": null
}
```

需要审批：

```json
{
  "task_id": "task-id",
  "status": "awaiting_approval",
  "approval": {
    "id": "approval-id",
    "url": "/admin/approvals/approval-id"
  }
}
```

前端随后跳转到 Task 或 Approval 详情页面。

### 7.7 实时更新

首个版本采用轮询加 SSE：

- History 列表：5 秒轮询
- Deployment 列表：5 秒轮询
- Pending Approval 数量：5 秒轮询
- 非终态 Task 详情：2 秒轮询
- stdout、stderr 和 Task events：SSE 或现有 cursor 增量读取

后续可增加统一事件流：

```http
GET /api/admin/v1/events
```

事件类型预留：

```text
task.updated
approval.created
approval.updated
deployment.updated
```

## 8. 前端工程

### 8.1 技术选型

建议：

- React
- TypeScript
- Vite
- React Router
- TanStack Query

首个版本不引入大型全局状态库。服务端状态由 TanStack Query 管理，少量登录和界面状态使用 React context。

### 8.2 目录

```text
web/admin/
  package.json
  vite.config.ts
  tsconfig.json
  index.html
  src/
    main.tsx
    app/
      router.tsx
      query-client.ts
    api/
      client.ts
      types.ts
    layouts/
      AdminLayout.tsx
    components/
      StatusBadge.tsx
      SessionBadge.tsx
      EmptyState.tsx
      ConfirmDialog.tsx
    features/
      dashboard/
      history/
      deployments/
      approvals/
```

### 8.3 前端路由

```text
/admin
/admin/history
/admin/history/:taskId
/admin/deployments
/admin/deployments/:type/:name
/admin/approvals
/admin/approvals/:approvalId
```

### 8.4 页面要求

#### Overview

- 最近 24 小时调用量
- 调用成功率和失败数
- 当前运行中 Task
- 待审批数量
- Deployment 状态摘要
- 最近 Task 和 Approval

#### History

筛选项：

- Session ID
- Tool
- Status
- Transport
- Workspace
- Requester
- 时间范围

表格列：

```text
Time | Tool | Status | Session | Transport | Duration | Requester
```

Session 显示规则：

```text
mcp-session-xxx
Stateless / Client supplied
```

无 Session：

```text
No session
```

Task 详情标签：

```text
Overview | Result | Output | Events | Audit
```

#### Deployments

列表展示：

- Name
- Type
- Status
- Health
- URL
- Release
- Updated time
- Actions

动态 Deployment 详情：

```text
Overview | Logs | Releases | History
```

History 自动按 Deployment 相关工具参数或关联字段筛选。

#### Approvals

列表展示：

- Created time
- Summary
- Tool
- Risk
- Status
- Requester
- Session

详情展示：

- 已脱敏参数
- 风险和规则信息
- 调用来源和 Session
- 关联 Task
- 审批操作
- 审计时间线

## 9. 管理配置

新增：

```yaml
admin:
  enabled: true
  base_url: "https://gatectl.example.com"
  ui_prefix: "/admin"
  api_prefix: "/api/admin/v1"
```

审批是 Admin 的子功能，配置统一放在 `admin.approval` 下，公开地址只使用 `admin.base_url`：

```yaml
admin:
  enabled: true
  base_url: "https://gatectl.example.com"
  approval:
    enabled: true
    default_ttl_seconds: 600
    max_sync_wait_seconds: 30
```

新生成的审批 URL 指向：

```text
{admin.base_url}/admin/approvals/{approval_id}
```

旧 `/approval-ui`、`/approvals` 和 `/deploy-mgr` 路径仅作为 Admin Host 下的兼容入口保留，不再存在独立审批域。

## 10. 鉴权和安全边界

### 10.1 鉴权

首个版本复用现有 HTTP Bearer Auth，不同时引入第二套 token 系统。

建议行为：

- `/admin/*` 静态文件可以加载。
- `/api/admin/v1/*` 必须通过 Bearer Auth。
- 前端提供 Token 输入页。
- Token 只保存在内存或 `sessionStorage`。
- 不写入 URL、日志或 `localStorage`。

当：

```text
admin.enabled = true
http_auth.enabled = false
```

启动时输出高优先级安全警告。

### 10.2 Host 隔离

将当前 `wrapApprovalURLBoundary` 演进为统一管理边界：

```go
func wrapAdminURLBoundary(
    next http.Handler,
    adminBaseURL string,
    controlBaseURL string,
) http.Handler
```

管理主机允许：

```text
/admin
/admin/*
/api/admin/v1
/api/admin/v1/*
```

管理主机默认不允许：

```text
/mcp
/tools
/system/uploads
/system/downloads
```

控制主机和应用主机不能访问管理 API。

### 10.3 Origin 和 CSRF

所有管理变更操作必须校验：

- `Origin` 与 `admin.base_url` 精确匹配
- `Sec-Fetch-Site` 不能为 `cross-site`
- Content-Type 必须符合接口要求
- Bearer principal 由服务端认证上下文获取

不能信任客户端传入的 actor、requester 或 approver header。

### 10.4 数据脱敏

- Task 列表不返回参数和完整结果。
- Task 详情参数复用现有递归脱敏逻辑。
- Approval 和 Audit 持久化前继续脱敏。
- Authorization、token、cookie、password、secret 等字段不可进入前端响应。
- Session ID 可以展示和筛选，但不作为身份认证依据。

## 11. 前端构建和托管

新增：

```text
scripts/build-admin.sh
```

流程：

```bash
cd web/admin
npm ci
npm run build
```

构建产物输出到：

```text
web/admin/dist
```

Go 通过 `internal/adminui` 使用 `go:embed` 托管生产资源。

SPA fallback 只对 `/admin/*` 生效：

- 静态文件存在时直接返回文件。
- 前端路由不存在对应文件时返回 `index.html`。
- `/api/admin/v1/*` 永远不能进入 SPA fallback。

根构建流程：

```bash
./scripts/build-admin.sh
./scripts/build.sh
```

CI 中分别执行前端检查和 Go 检查，避免每次 `go build` 隐式执行 `npm ci`。

## 12. 旧路由迁移

### 12.1 兼容期

旧 GET 页面返回 303：

```text
/approval-ui          -> /admin/approvals
/approval-ui/{id}     -> /admin/approvals/{id}
/deploy-mgr           -> /admin/deployments
public/private deploys subdomains remain read-only access-specific lists
```

旧审批和部署 POST 路由保留一个兼容版本，已有链接和客户端仍可工作。新前端不再调用这些路由。

### 12.2 清理期

稳定后删除：

- `approvalManagementTemplate`
- `deploymentOverviewTemplate`
- `deploymentListTemplate`
- 管理页面 HTML response 分支
- 表单 notice query 参数和 redirect helper
- handler 内同步等待 Deployment Task 的逻辑

## 13. 分阶段实施

### Phase 1：统一调用和历史基础

目标：所有新工具调用具备可查询的统一元数据。

工作项：

1. 新增 `invocation.Meta` 和常量。
2. Task、InvokeContext、Approval、Audit 增加字段。
3. 新增 `SubmitToolTaskWithMeta`。
4. 迁移标准 MCP HTTP 和 stdio 入口。
5. 迁移 REST、tool HTTP、MCP runtime 和管理动作入口。
6. 补齐 Task 所有状态持久化。
7. 增加启动恢复。
8. 实现 Task filter、排序和 cursor 分页。

交付结果：

- 全局 Task 历史数据完整。
- `session_id` 可选。
- Stateful 和 Stateless Session 来源可区分。

### Phase 2：管理 JSON API

目标：后端具备完整且稳定的管理 API。

工作项：

1. 新建 `internal/adminapi`。
2. 实现 Overview API。
3. 实现 History 列表和详情 API。
4. 实现 Approval 查询和动作 API。
5. 实现 Deployment 查询和动作 API。
6. 所有变更操作统一返回 Submission。
7. 增加管理 Host、Origin 和权限边界。

交付结果：

- 不依赖 HTML 页面即可完成全部管理操作。
- 部署和审批动作都进入统一 Task 历史。

### Phase 3：统一前端

目标：交付可用的 LocalGate Admin SPA。

工作项：

1. 初始化 `web/admin`。
2. 实现布局、导航、登录和 API client。
3. 实现 Overview。
4. 实现 History 和 Task 详情。
5. 实现 Deployments 和详情页。
6. 实现 Approvals 和审批动作。
7. 加入轮询、增量日志和错误反馈。
8. 增加响应式布局和可访问性基础。

交付结果：

- 历史、部署和审批在同一个后台中完成。
- 前端可以独立运行和构建。

### Phase 4：集成和迁移

目标：新后台成为默认管理入口。

工作项：

1. 新增 admin 配置。
2. 嵌入并托管前端构建产物。
3. 审批 URL 指向新后台。
4. 旧页面改为跳转。
5. 增加 E2E 测试。
6. 更新 README、配置示例和运维文档。
7. 在兼容窗口结束后删除旧模板。

## 14. 测试规划

### 14.1 领域和持久化测试

- Invocation Meta 正确进入 Task。
- Stateful MCP Session 标记为 `server_managed`。
- Stateless 客户端 Session 标记为 `client_supplied`。
- Stateless 无 Session 正常执行。
- 旧 Task、Approval、Audit JSON 可加载。
- Task 每个状态变化都会落盘。
- 运行中 Task 在重启后正确终结。
- Task List 返回副本，不存在数据竞争。
- Filter、排序和 cursor 分页稳定。

### 14.2 管理 API 测试

- History 各筛选参数。
- History 默认按最新时间排序。
- 列表不返回完整 Result 和敏感参数。
- Task 详情能关联 Approval 和 Audit。
- Approval approve、reject、cancel 状态转换。
- 重复审批返回冲突。
- Deployment start、stop、restart、delete、rollback 返回 Submission。
- Delete 必须有确认值。
- 所有管理动作记录 `transport=admin`。
- Host 隔离和 Origin 校验。
- 401、403、404、409 和 500 错误格式一致。

### 14.3 前端测试

- Token 登录和 401 处理。
- History 筛选和 cursor 翻页。
- Session Badge 正确区分来源。
- Task 非终态自动刷新。
- Approval 操作确认和结果反馈。
- Deployment 危险操作确认。
- 日志增量读取和断线恢复。
- 前端路由刷新时 SPA fallback 正常。

### 14.4 E2E 测试

流程一：

```text
MCP tools/call
-> 产生 Task
-> History 可见
-> session_id 筛选命中
-> Task 详情能查看结果和事件
```

流程二：

```text
deploy_tool
-> pending approval
-> Admin 批准
-> 原 Task 进入执行
-> Deployment 页面更新
-> History 中能查看完整链路
```

流程三：

```text
Stateless MCP + client supplied session ID
-> 调用成功
-> History 显示 Client supplied
-> 不把 Session ID 当作认证身份
```

## 15. 验收标准

完成后必须满足：

1. 不传 `session_id` 的所有工具调用正常执行并进入全局历史。
2. 传入 `Mcp-Session-Id` 后可以按该值精确筛选。
3. Stateless 模式中的客户端 Session ID 被记录但明确标记为非服务端管理。
4. History 一行只代表一次 Task，不因多个 Audit 产生重复调用记录。
5. Task 详情可以查看结果、输出、事件、审批和审计。
6. Deployment 所有管理操作都产生 Task，并遵守现有 Approval policy。
7. Approval 可以在统一后台完成批准、拒绝和取消。
8. Go 后端不再渲染新的业务管理页面。
9. 前端可以独立启动、测试和构建。
10. 生产环境可以同源托管前端构建产物。
11. 管理 API 与 MCP、控制面和应用域保持 Host 隔离。
12. 旧持久化数据无需强制迁移即可继续读取。
13. 旧页面在兼容期内能够跳转或继续完成已有审批操作。
14. `go test ./...`、`go test -race ./...` 和前端测试全部通过。

## 16. 推荐首个可交付版本

首个版本包含：

- Invocation Meta 和可选 Session ID
- Task 状态完整持久化和查询
- History、Approval、Deployment JSON API
- React 管理后台三个核心模块
- Overview 基础统计
- Bearer Token 登录
- 同源静态资源托管
- 旧页面跳转
- 核心 E2E 测试

首个版本暂不包含：

- SQLite
- RBAC
- WebSocket
- 跨实例查询
- 复杂报表
- 审计全文搜索

该范围能够完整实现“统一管理后台、前后端分离、全局历史、可选 MCP Session 筛选、部署和审批统一管理”的核心目标。
