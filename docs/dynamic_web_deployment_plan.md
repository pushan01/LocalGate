# LocalGate 动态 Web 部署实施计划

状态：P0/P1 核心正确性、统一架构和可靠性能力已实现，生产级专项验收仍在进行中
适用分支：`dev/add_mcp_http`  
最后更新：2026-07-01

## 实现状态

本文件描述的是完整动态部署计划，不仅是首个 MVP。当前实现可用于部署和托管可信的长期 HTTP 应用，但尚未达到完整架构和生产加固目标。

整体状态：

| 范围 | 状态 | 说明 |
|---|---|---|
| 核心动态运行链路 | 已完成 | 快照、进程启动、健康检查、Host 代理、生命周期工具可用 |
| 工具和协议接入 | 已完成 | REST、MCP、task、approval、生命周期审计和稳定错误码已接入 |
| 统一部署架构 | 已完成 | `deployment.Manager` 已统一 static/dynamic，工具和总览页只依赖 manager，共享 snapshot 已接入 |
| 可靠性 | 基本完成 | 蓝绿覆盖、正确的 on-failure、状态事务、日志轮转、release GC/history/rollback 和恢复已实现 |
| 安全加固 | 部分完成 | executable 固定、转发头重建、proxy body/header/concurrency/time limits 已实现；强资源隔离和完整 fuzz/race 仍待完成 |
| 完整验收 | 部分完成 | 全量 `go test -race ./...` 已通过；正常退出、健康跳转、状态持久化、代理头/body、日志轮转、release rollback 和审计已有专项测试；WebSocket/SSE、进程树和 crash consistency 仍待补齐 |

当前分支已经实现：

- `deployments.dynamic` 配置、默认关闭和启动校验；
- 控制面 Host 白名单与应用 Host 隔离；
- workspace 源码安全快照和托管 release；
- argv 启动、loopback 端口、健康检查和进程组终止；
- Host 反向代理，支持普通请求、SSE、streaming 和 WebSocket upgrade；
- `deploy_tool type=dynamic`、静态/动态统一 list/delete 入口；
- status、restart、stop、start 和日志工具；
- 覆盖部署、异常退出重启和 LocalGate 启动恢复；
- 部署总览页面同时显示静态和动态 deployment；
- 动态部署参数级审批风险和稳定 task error code。

当前实现仍保留以下缺口和边界：

- registry 使用单进程 JSON 原子写入，没有跨进程租约、数据库事务或多实例协调；
- HTTP 进程拥有 dynamic supervisor，MCP stdio 进程不会重复接管运行时；
- Unix 使用 process group 终止进程树，Windows 尚未接入 Job Object；
- LocalGate 正常关闭和蓝绿退役失败可回收受管进程，但宿主机断电或 LocalGate 被强制杀死后的跨进程 orphan 仍无法安全识别；
- 日志已经轮转和限制保留数量，但应用日志内容尚未做通用敏感信息脱敏；
- proxy 已有 body/header/concurrency/upstream timeout 限制，仍缺 deployment 级速率、连接数和指标；
- 非法 Host fuzz、完整 IDNA、WebSocket/SSE 专项、process tree 和 crash consistency 验证尚未完成；
- 第一版只允许持久化非敏感 env，名称包含 secret、token、password 等的变量会被拒绝；
- 动态进程与 LocalGate 使用同一操作系统用户，不构成不可信代码沙箱。

## 1. 背景

LocalGate 当前已经具备：

- 统一的 `deploy_tool`、`list_deployments`、`delete_deployment` 工具入口；
- 静态站点的安全复制、原子激活和注册表；静态与动态部署现统一使用 所选 base URL 主机下的 `{name}` 单层子域名路由，不再提供 `/static/{site}/` 服务；
- 动态 deployment 注册表只保存稳定运行事实，不持久化 `hostname` 或 `url`；公开地址始终由当前 deployment name 与所选 base URL 派生，因此修改 base URL 并重启后不需要迁移动态注册表；
- deployment 持久化 `access: public | private`，并分别映射到 `public_base_url` 与 `private_base_url`；旧记录缺省按 public 兼容，新部署默认 private，管理页面可通过受审批操作转换访问级别；
- task、approval、audit 和统一错误返回；
- HTTP、MCP stdio、MCP Go、HTTP MCP Runtime 等多种调用入口；
- shell task 和交互 session；
- Cloudflare Tunnel 必须将控制面 Host 转发到 `bind`，将 public 应用 Host 转发到 `deployments.public_bind`，将 private 应用 Host 转发到 `deployments.private_bind`。

本计划提出时主要缺少：

- 控制面和应用面使用独立监听端口，并在各自端口继续执行 Host 校验；
- 长期运行进程的独立 supervisor；
- 动态 deployment 注册表、健康检查、日志和恢复；
- 动态应用反向代理；
- 动态部署的覆盖、回滚和重启策略。

当前分支已经实现 Host 隔离、统一 deployment manager、共享快照、supervisor、registry、健康检查、日志轮转、恢复、反向代理、蓝绿覆盖、release history/rollback 和重启策略；多实例协调、强资源隔离和完整生产专项验收仍未完成。

动态 Web 部署不能直接复用普通 task 或 session：

- task 是有限执行单元，部署完成后应进入终态；
- 动态网站进程在部署 task 完成后仍需持续运行；
- session 只有内存状态，并会被 idle janitor 清理；
- 动态进程需要持久化期望状态、崩溃处理和 LocalGate 重启恢复。

因此需要新增独立的 deployment manager 和 process supervisor。

## 2. 目标

通过现有统一部署工具部署长期运行的 HTTP 应用：

```json
{
  "type": "dynamic",
  "name": "blog",
  "source": "/Users/yatta/Documents/blog",
  "runtime": {
    "argv": ["node", "server.js"]
  }
}
```

部署成功后获得独立 Host：

```text
https://blog.pushan.eu.org/
```

推荐的生产流量结构：

```text
*.pushan.eu.org
        ↓
Cloudflare Tunnel
        ↓
LocalGate 0.0.0.0:8000
        ↓ 按 Host 分流
macgate.pushan.eu.org → LocalGate 控制面
blog.pushan.eu.org    → blog 动态应用
shop.pushan.eu.org    → shop 动态应用
未知 Host             → 404
```

新增或删除动态网站时，不需要修改 Cloudflare Tunnel 配置或 DNS；LocalGate 只更新自己的 deployment registry 和代理目标。

## 3. 非目标

第一版明确不做：

- Docker、containerd 或 Kubernetes；
- 自动检测 Node、Django、FastAPI、Rails 等框架；
- 自动安装依赖或 buildpack；
- 任意 shell command 部署；
- 数据库、消息队列等依赖编排；
- 多实例负载均衡；
- 自定义域名和自动证书签发；
- 多节点部署和分布式协调；
- CPU、内存、磁盘、网络的强隔离；
- 将不可信代码视为已沙箱化。

第一版动态进程仍与 LocalGate 使用同一个操作系统用户。它是进程托管能力，不是安全沙箱。

## 4. 核心设计决策

### 4.1 默认使用 Host 路由

动态应用默认使用独立 hostname：

```text
{name}.<selected-base-host>
```

例如：

```text
blog.pushan.eu.org
```

请求路径原样转发：

```text
/assets/app.js → /assets/app.js
/api/users     → /api/users
/socket        → /socket
```

这样客户端继续运行在 `/`，通常不需要 base path 或 path rewrite。

### 4.2 控制面和应用面必须按端口与 Host 双重隔离

控制面和 deployment 应用面不能共享 listener：

```text
bind
    → 只挂载 LocalGate 控制面、MCP、任务、审批和管理后台
    → 普通控制面路由仅接受 `http.base_url` 的 Host；管理路由仅接受 `admin.base_url` 的 Host

deployments.public_bind
    → 只挂载 public deployment handler
    → 接受 public 根域 `/deploys` 和 public deployment 子域

deployments.private_bind
    → 只挂载 private deployment handler
    → 接受 private 根域 `/deploys` 和 private deployment 子域
```

`bind`、`deployments.public_bind` 和 `deployments.private_bind` 必须使用不同 TCP 端口。public/private handler 固定绑定 access，即使错误 Host 被转发到另一个端口，也不能跨 access 提供应用。

### 4.3 动态进程由独立 supervisor 管理

不复用：

- `core.Manager` task 生命周期；
- `core.SessionManager` session 生命周期。

新增 supervisor 负责：

- 启动和停止进程组；
- 捕获 stdout/stderr；
- 健康检查；
- 异常退出处理；
- 重启退避；
- LocalGate 关闭时优雅停止；
- LocalGate 重启后恢复期望运行的 deployment。

### 4.4 只接受 argv，不接受 shell command

允许：

```json
{"argv": ["node", "server.js"]}
```

不允许：

```json
{"command": "npm install && npm start"}
```

构建步骤可先通过现有 `run_command` 完成。动态部署只负责启动已经可运行的应用。

### 4.5 从托管快照运行

动态应用不能直接从 workspace 原目录长期运行。

流程：

```text
workspace source
    ↓ 安全检查和复制
managed release snapshot
    ↓
从托管 release 启动
```

好处：

- 运行内容固定；
- 覆盖发布可蓝绿切换；
- workspace 后续修改不会直接改变线上服务；
- 可做 release 历史和回滚；
- 删除和恢复边界更清晰。

## 5. 目标架构

```text
REST / MCP / Task
        ↓
deployment.Manager
        ├── static.Backend
        └── dynamic.Service
                ├── registry.Store
                ├── snapshot.Manager
                ├── supervisor
                ├── health.Checker
                ├── reverse proxy
                └── log store
```

建议新增目录：

```text
internal/deployment/
  manager.go
  types.go
  errors.go
  snapshot.go

internal/dynamic/
  service.go
  registry.go
  supervisor.go
  process.go
  health.go
  proxy.go
  logs.go
  recovery.go
```

现有 `internal/static` 保持独立，通过 adapter 接入 `deployment.Manager`。

## 6. 配置模型

静态与动态部署统一放在 `deployments` 下。共享入口和名称空间配置位于父级，类型专属配置位于 `static`、`dynamic` 子结构：

```yaml
deployments:
  public_bind: 127.0.0.1:3738
  private_bind: 127.0.0.1:3740
  public_base_url: https://apps.example.com
  private_base_url: https://private-apps.example.com
  default_access: private
  reserved_names: [localgate, control, www, api, admin]

  static:
    max_files: 10000
    max_total_bytes: 52428800
    max_file_bytes: 52428800
    require_approval: true

  dynamic:
    enabled: false
    require_approval: true
    allowed_executables:
      - /absolute/path/to/node
      - /absolute/path/to/python3
    env_allowlist: [NODE_ENV, APP_ENV]
    max_apps: 20
    max_files: 20000
    max_total_bytes: 536870912
    max_file_bytes: 104857600
    startup_timeout_seconds: 20
    shutdown_timeout_seconds: 5
    restart_policy: on-failure
    restart_max_attempts: 3
    restart_backoff_seconds: 2
    restart_reset_seconds: 60
    max_log_bytes: 10485760
    max_log_files: 3
    release_retention: 3
    max_request_body_bytes: 33554432
    max_request_header_bytes: 1048576
    max_concurrent_requests: 100
    upstream_header_timeout_seconds: 30
    max_response_header_bytes: 1048576
```

`deployments.public_bind`、`deployments.private_bind`、控制面 `bind` 和 `admin.bind` 必须使用不同 TCP 端口。只有启用 `deployments.dynamic.enabled` 时才强制要求 `allowed_executables`。

## 7. Host 路由和安全边界

### 7.1 Host 规范化

对 `r.Host`：

- 转为小写；
- 去掉合法端口；
- 去掉末尾的单个点；
- 拒绝空 Host；
- 拒绝非法 IDNA/ASCII hostname；
- 应用 Host 第一版只允许一个 deployment label；
- 不允许通过 `X-Forwarded-Host` 覆盖真实 Host。

### 7.2 Host 分类

伪代码：

```go
host := normalizeHost(r.Host)

switch {
case controlHosts.Contains(host):
    serveControlPlane(w, r)
case deploymentName, ok := dynamicRegistry.LookupHost(host):
    serveDynamicDeployment(w, r, deploymentName)
default:
    http.NotFound(w, r)
}
```

不应只通过字符串 suffix 推断 deployment 存在；必须同时查注册表。

### 7.3 控制面认证

控制面继续使用：

- Bearer auth；
- CORS；
- approval Origin 校验。

应用面：

- 不使用 LocalGate 控制面 Bearer；
- 应用自行负责登录和权限；
- `/tasks`、`/mcp`、`/approvals` 等 path 在应用 Host 下属于应用，不属于 LocalGate 控制面。

### 7.4 Cloudflare Tunnel

public 和 private 应用分别配置入口：

```text
apps.example.com, *.apps.example.com → http://127.0.0.1:3738
private-apps.example.com, *.private-apps.example.com → http://127.0.0.1:3740
```

控制面可以使用一个精确 hostname：

```text
macgate.pushan.eu.org → http://127.0.0.1:3737
```

Cloudflare 侧不得固定覆盖发送给 LocalGate 的 HTTP Host header，否则 LocalGate 无法分流。

## 8. 部署抽象

建议类型：

```go
type DeploymentType string

const (
    DeploymentStatic  DeploymentType = "static"
    DeploymentDynamic DeploymentType = "dynamic"
)

type Manager interface {
    Deploy(context.Context, Request) (Result, error)
    Delete(context.Context, DeleteRequest) error
    Restart(context.Context, RestartRequest) (Result, error)
    Get(context.Context, GetRequest) (Deployment, error)
    List(context.Context, ListRequest) ([]Deployment, error)
}
```

迁移顺序：

1. 先实现 static adapter；
2. 将现有三个 deployment 工具改为依赖 manager；
3. 保证静态部署测试全部不变；
4. 再注册 dynamic backend。

## 9. 动态部署请求

建议输入：

```json
{
  "type": "dynamic",
  "name": "blog",
  "source": "/Users/yatta/Documents/blog",
  "runtime": {
    "argv": ["node", "server.js"],
    "cwd": ".",
    "env": {
      "NODE_ENV": "production"
    }
  },
  "options": {
    "overwrite": true,
    "preserve_source": true,
    "health_path": "/healthz",
    "startup_timeout_ms": 20000,
    "restart_policy": "on-failure"
  }
}
```

校验要求：

- `name` 使用与静态站点一致或更严格的 label 规则；
- `name` 不能在 `reserved_names` 中；
- `source` 必须位于目标 workspace；
- `argv` 非空；
- executable 必须在 allowlist；
- `cwd` 必须位于托管 release 内；
- env key 必须在 allowlist；
- health path 必须是站内绝对 path，不能是完整 URL；
- 动态 deployment hostname 不得与控制面或其他 deployment 冲突。

## 10. 源码快照和存储结构

建议：

```text
.localgate/
  dynamic/
    deployments.json
    apps/
      blog/
        releases/
          20260630-abc123/
            source/
            metadata.json
            stdout.log
            stderr.log
```

快照规则：

- 禁止符号链接；
- 禁止 socket、device、FIFO 等特殊文件；
- 限制文件数、总大小和单文件大小；
- source 和 managed root 不能互相包含；
- 复制到 staging 目录；
- 写入 metadata；
- 原子 rename 成 release；
- `preserve_source=false` 使用现有 stash/restore 语义；
- 任何失败不得留下 active 半成品。

静态服务当前已有的安全复制逻辑应抽成共享组件，避免两套规则逐渐分叉。

## 11. 进程启动

每个动态应用绑定 LocalGate 分配的 loopback 端口：

```text
127.0.0.1:<allocated-port>
```

LocalGate 注入：

```text
HOST=127.0.0.1
PORT=<allocated-port>
LOCALGATE_APP_HOST=blog.pushan.eu.org
LOCALGATE_PUBLIC_URL=https://blog.pushan.eu.org
LOCALGATE_BASE_PATH=/
```

启动要求：

- `exec.Command`，不经过 shell；
- `SysProcAttr.Setpgid=true`；
- cwd 指向 release 内目录；
- 继承最小必要环境；
- 用户 env 经过 allowlist；
- stdout/stderr 写入独立日志；
- `Wait()` 异步监听进程退出；
- task context 只控制部署过程，不能在 task 完成时取消已激活进程。

## 12. 端口分配

MVP 可采用：

1. `127.0.0.1:0` 获取空闲端口；
2. 关闭临时 listener；
3. 启动应用；
4. 如果 bind 竞争失败，分配新端口并有限重试。

该方案有竞争窗口，但可以作为第一版。后续可考虑 socket activation 或应用继承 listener。

用户不能指定：

- `0.0.0.0`；
- 公网 IP；
- 固定公网端口；
- LocalGate 控制面端口。

## 13. Supervisor

运行时状态：

```go
type RuntimeState struct {
    DeploymentName string
    ReleaseID      string
    PID            int
    Port           int
    Status         string
    RestartCount   int
    StartedAt      time.Time
    LastExitAt     time.Time
    LastError      string
}
```

Supervisor 负责：

- start；
- stop；
- restart；
- process group termination；
- stdout/stderr 生命周期；
- exit 状态收集；
- restart policy；
- shutdown；
- startup recovery。

停止流程：

```text
SIGTERM process group
    ↓ 等待 shutdown timeout
SIGKILL process group
```

LocalGate 关闭时必须停止所有由当前进程监督的动态应用。

## 14. 生命周期模型

期望状态：

```text
running
stopped
deleted
```

实际状态：

```text
staging
starting
healthy
active
stopping
stopped
failed
crashed
deleting
```

期望状态和实际状态必须分开。例如：

```text
desired_state = running
status        = crashed
```

这表示 supervisor 应根据重启策略处理，而不是把 deployment 当作用户主动停止。

## 15. 健康检查

默认健康检查：

```text
GET http://127.0.0.1:<port>/
```

用户可配置：

```json
{"health_path": "/healthz"}
```

成功条件：

- TCP 可连接；
- HTTP 状态码为 200–399；
- 在 startup timeout 内至少一次成功；
- 单次请求使用短超时；
- 响应体最多读取少量字节或直接丢弃；
- health path 只能指向当前 loopback backend。

健康检查未通过时：

- 终止新进程；
- deployment 标记 `failed`；
- 不切换 active release；
- 覆盖部署时旧版本继续服务。

## 16. Host 反向代理

动态 handler 根据已规范化 Host 查 active target：

```text
blog.pushan.eu.org
    → deployment blog
    → active release
    → 127.0.0.1:41023
```

代理要求：

- 保留原始 path 和 query；
- 支持 GET、POST、PUT、DELETE 等普通方法；
- 支持 WebSocket upgrade；
- 支持 SSE 和 streaming response；
- 正确清理 hop-by-hop headers；
- 添加 `X-Forwarded-Host`、`X-Forwarded-Proto`、`X-Forwarded-For`；
- 可添加 `X-LocalGate-Deployment`；
- backend 不可用返回稳定 502；
- deployment 未激活返回 503 或 404；
- 未知 Host 一律 404；
- 不解析或改写 HTML、JavaScript、CSS 响应正文。

Host 模式下默认不需要 path rewrite。

## 17. 蓝绿覆盖发布

`overwrite=true` 的流程：

```text
创建新 release snapshot
    ↓
分配新端口
    ↓
启动新进程
    ↓
健康检查通过
    ↓
原子切换 active target
    ↓
停止旧进程
    ↓
清理或保留旧 release
```

失败语义：

- 新 release 启动失败：旧版本继续服务；
- 新 release 健康失败：旧版本继续服务；
- active target 切换失败：新版本停止，旧版本继续；
- 旧进程停止失败：新版本保持 active，但记录清理错误并进入待回收状态。

## 18. 注册表

建议持久化结构：

```go
type Deployment struct {
    Name          string            `json:"name"`
    Type          string            `json:"type"`
    WorkspaceID   string            `json:"workspace_id"`
    Hostname      string            `json:"hostname"`
    ActiveRelease string            `json:"active_release"`
    DesiredState  string            `json:"desired_state"`
    Status        string            `json:"status"`
    Argv          []string          `json:"argv"`
    WorkingDir    string            `json:"working_dir"`
    Env           map[string]string `json:"env"`
    HealthPath    string            `json:"health_path"`
    CreatedAt     string            `json:"created_at"`
    UpdatedAt     string            `json:"updated_at"`
    StartedAt     string            `json:"started_at,omitempty"`
    RestartCount  int               `json:"restart_count"`
    LastError     string            `json:"last_error,omitempty"`
}
```

要求：

- env value 不在 list/status API 明文返回；
- token、password、secret、authorization、cookie、private key 等字段递归脱敏；
- 第一版可使用 JSON + temp file + fsync + rename；
- 服务内使用互斥锁保证单进程一致性；
- 跨进程事务、租约和多实例协调放入 TODO。

## 19. 重启策略

支持：

```text
never
on-failure
```

第一版不建议默认 `always`，避免配置错误导致无限重启。

`on-failure`：

- 非零退出或异常信号触发；
- 指数或分段退避；
- 最大重试次数；
- 超过上限标记 `crashed`；
- 成功稳定运行一段时间后可重置 restart count。

用户主动 stop/delete 不触发自动重启。

## 20. LocalGate 启动恢复

启动时：

1. 读取动态 registry；
2. 忽略持久化的旧 PID 和旧 port；
3. 找出 `desired_state=running` 的 deployment；
4. 验证 active release 仍存在；
5. 分配新 port；
6. 重新启动进程；
7. 健康检查；
8. 成功后恢复 Host 路由；
9. 失败则标记 `failed` 并记录审计。

绝不能因为 registry 中记录了 PID 就直接向该 PID 发信号，因为 PID 可能已被其他进程复用。

## 21. 日志

每个 release：

```text
stdout.log
stderr.log
```

要求：

- 单文件大小限制；
- 简单轮转或截断；
- 查询时支持 tail 和最大字节数；
- 不将无限日志全部装入内存；
- 日志读取不能越过 release root；
- API 输出继续执行敏感信息脱敏。

建议新增工具：

```text
get_deployment_logs
```

参数示例：

```json
{
  "type": "dynamic",
  "name": "blog",
  "stream": "stderr",
  "tail_bytes": 65536
}
```

## 22. MCP 工具变化

### 22.1 `deploy_tool`

支持：

```text
static | dynamic
```

动态返回：

```json
{
  "status": "deployed",
  "type": "dynamic",
  "site": "blog",
  "hostname": "blog.pushan.eu.org",
  "url": "https://blog.pushan.eu.org/",
  "release_id": "20260630-abc123",
  "runtime_status": "active",
  "health": "healthy",
  "source_preserved": true
}
```

### 22.2 `list_deployments`

统一列出静态和动态 deployment。

动态项目至少包含：

```json
{
  "type": "dynamic",
  "site": "blog",
  "url": "https://blog.pushan.eu.org/",
  "status": "active",
  "health": "healthy",
  "release_id": "20260630-abc123",
  "restart_count": 0,
  "updated_at": "2026-06-30T00:00:00Z"
}
```

### 22.3 `delete_deployment`

动态删除流程：

```text
停止进程
→ 移除代理目标
→ 更新 registry
→ 删除 release 文件
→ 写审计
```

### 22.4 新增工具

建议新增：

```text
get_deployment
restart_deployment
get_deployment_logs
stop_deployment
start_deployment
list_deployment_releases
rollback_deployment
```

MVP 至少实现：

```text
get_deployment
restart_deployment
get_deployment_logs
```

## 23. 审批和审计

动态部署属于长期代码执行，默认风险应高于静态部署。

建议：

- deploy dynamic：critical；
- overwrite dynamic：critical；
- delete dynamic：critical；
- restart dynamic：high；
- stop/start dynamic：high；
- status/logs：read-only。

现有审批规则只能按 tool 匹配，MVP 可让所有 `deploy_tool` 都走统一高风险审批。后续增加按参数匹配：

```yaml
admin:
  approval:
    rules:
      - id: dynamic-deploy
        effect: require_approval
        tools: [deploy_tool]
        args:
          type: dynamic
```

审计事件建议：

```text
deployment_staging_started
deployment_process_started
deployment_health_passed
deployment_activated
deployment_activation_failed
deployment_process_exited
deployment_restart_scheduled
deployment_restart_exhausted
deployment_stopped
deployment_deleted
deployment_recovered
```

审计内容可包含：

- deployment name；
- type；
- workspace；
- requester；
- release ID；
- executable；
- hostname；
- health path；
- restart count；
- exit code；
- task ID；
- approval ID。

不得持久化明文 secret env value。

## 24. 稳定错误码

建议：

```text
dynamic_hosting_disabled
deployment_name_invalid
deployment_host_conflict
deployment_not_found
deployment_already_exists
deployment_source_invalid
deployment_executable_denied
deployment_env_denied
deployment_snapshot_failed
deployment_port_allocation_failed
deployment_start_failed
deployment_health_failed
deployment_activation_failed
deployment_stop_failed
deployment_crashed
deployment_restart_exhausted
deployment_proxy_unavailable
```

这些错误码应在以下入口保持一致：

- REST；
- task result；
- MCP stdio；
- MCP Go；
- HTTP MCP Runtime。

## 25. 测试计划

### 25.1 Host 路由

- control host 能访问控制面；
- app host 不能访问控制面；
- app host 的 `/tasks` 转发给应用；
- 未注册 Host 返回 404；
- Host 大小写、端口和末尾点规范化；
- `X-Forwarded-Host` 不能绕过；
- reserved name 拒绝；
- 精确 control host 优先于 app domain；
- 非法 Host fuzz 测试。

### 25.2 部署

- 成功部署最小 HTTP server；
- executable allowlist；
- source 越界拒绝；
- symlink 和特殊文件拒绝；
- env allowlist；
- startup timeout；
- health failure；
- task cancellation；
- approval 前不启动进程；
- approval 后只启动一次。

### 25.3 生命周期

- delete 停止整个进程组；
- crash 自动重启；
- 达到上限进入 crashed；
- LocalGate Close 停止应用；
- LocalGate 重启恢复 active deployment；
- 旧 PID 不被错误终止；
- 用户 stop 不触发 restart。

### 25.4 代理

- GET/POST；
- query string；
- request body；
- response headers；
- WebSocket；
- SSE；
- streaming；
- backend crash 返回稳定 502；
- 蓝绿切换。

### 25.5 覆盖发布

- 新版本健康后切换；
- 新版本失败时旧版本继续；
- 激活后旧进程停止；
- 并发 overwrite 只有一个成功；
- registry 和 active target 不出现半切换状态。

### 25.6 回归

- 所有静态部署测试；
- `/mcp`；
- `/tools`；
- `/tasks`；
- `/approvals`；
- Bearer auth；
- CORS；
- approval Origin；
- upgrade runtime。

### 25.7 当前测试覆盖状态

已覆盖：

- control/app Host 隔离、未知 Host 404 和 Bearer 仅作用于控制面；
- 最小动态 HTTP 应用的部署、健康检查、代理、query 和日志读取；
- start、stop、restart、delete 和 LocalGate 启动恢复；
- executable allowlist、敏感 env 名称拒绝；
- dynamic deploy/delete/lifecycle 的审批风险；
- HTTP MCP runtime 的动态错误码传播；
- 全仓 `go test ./...`、`go vet ./...` 和真实 HTTP 服务主链路验证。

部分覆盖：

- Host 规范化和保留名称；
- source/snapshot 安全规则和共享 snapshot；
- restart policy、正常退出不重启、restart limit/reset；
- 蓝绿覆盖成功路径、release history/rollback 和 retention；
- protocol error code 一致性。

新增专项验证：

- 正常 exit code 0 不触发 `on-failure`；
- health redirect 不跟随到外部地址；
- 客户端伪造转发头被清除并由 LocalGate 重建；
- proxy request body 限制；
- stop intent 必须持久化成功后才终止进程；
- 日志轮转、release retention、release history 和 rollback；
- manager static 生命周期、workspace 根目录删除拒绝和共享 snapshot symlink/配额/取消；
- 部署 staging、process started、health passed 和 activated 生命周期审计；
- 全量 `go test -race ./...`，并修复 task 创建返回共享可变指针导致的数据竞争。

尚未完成专项验证：

- 非法 Host fuzz、完整 IDNA 和真实 Cloudflare wildcard；
- 特殊文件完整矩阵、startup timeout、health failure 和 task cancellation；
- approval 后只启动一次；
- 孙进程和整个 process tree 终止、旧 PID 复用保护；
- restart exhaustion 的完整时间序列和用户 stop 不重启专项；
- WebSocket、SSE、streaming 和 backend crash 502；
- 覆盖失败保留旧版本、并发 overwrite 和 registry 半切换；
- crash consistency 和硬崩溃后的 orphan 清理。

## 26. 分阶段实施

### Phase 0：冻结模型和文档（已完成，持续维护）

输出：

- 配置 schema；
- deployment request/response schema；
- registry schema；
- Host 分类规则；
- 生命周期状态；
- 错误码；
- 安全边界。

本文件即 Phase 0 的初始基线。

### Phase 0A：部署总览页面（已实现）

已增加只读页面：

```text
/deploys
```

当前页面：

- 同时读取静态和动态部署服务；
- 按名称和类型整理展示；
- 展示 type、status、健康状态、更新时间和公开 URL；
- 可直接打开每个 deployment；
- 不创建 task、approval 或 audit；
- dynamic hosting 关闭或不可用时显示稳定的 disabled/unavailable 状态。

当前页面已经通过统一 `deployment.Manager.List` 读取，不存在第二套动态总览路径。

### Phase 1：Host 隔离（基本完成）

实现：

- 控制面 Host 由 `http.base_url` 派生；
- selected base URL host；
- Host normalize；
- 控制面 Host 白名单；
- 未知 Host 404；
- Bearer/CORS 只应用于控制面；
- app Host 的所有 path 保留给应用。

当前已完成 control host、app host、未知 Host 404、控制面 Bearer/CORS 隔离和 `X-Forwarded-Host` 绕过防护。仍需补充非法 Host fuzz、完整 IDNA 规则和真实 Cloudflare wildcard 验证。

完成上述专项验收后，才视为可安全启用生产 Cloudflare 顶级 wildcard。

### Phase 2：部署抽象重构（已完成）

实现：

- `internal/deployment.Manager`；
- static backend adapter；
- 三个现有 deployment 工具迁移；
- 静态部署行为和测试保持不变。

当前已经实现：

- `internal/deployment.Manager` 统一 static/dynamic deploy、delete、list 和动态生命周期操作；
- deploy/list/delete/lifecycle 工具和 deployment UI 只依赖 manager；
- 旧构造器保留为兼容包装，不再作为主依赖路径；
- `internal/deployment/snapshot` 同时服务 static 和 dynamic，统一 symlink、特殊文件、配额、取消和路径边界规则；
- manager 和共享 snapshot 均有独立回归测试。

### Phase 3：动态进程主链路（基本完成）

实现：

- dynamic registry；
- source snapshot；
- argv 启动；
- loopback port；
- health check；
- Host reverse proxy；
- stop/delete；
- stdout/stderr logs；
- graceful shutdown。

当前已经额外实现完整主要生命周期状态、状态先持久化后变更进程、自动重启、启动恢复、可执行文件绝对路径固定和首次健康失败状态记录。剩余工作主要是端口竞争专项验证、Windows Job Object 和完整进程树测试。

### Phase 4：工具、审批和协议（基本完成）

实现：

- `deploy_tool type=dynamic`；
- list/delete 支持 dynamic；
- status/logs/restart 工具；
- approval；
- audit；
- 稳定错误码；
- REST/MCP 协议一致性；
- prompt、skill 和命令文档。

当前工具和协议入口、参数级审批、部署生命周期审计和主要错误码映射已完成，并新增 release list/rollback 工具。剩余工作主要是所有错误码入口矩阵，以及 approval 后只启动一次的更强并发测试。

### Phase 5：可靠性（基本完成）

实现：

- 蓝绿 overwrite；
- restart policy；
- backoff；
- restart limit；
- startup recovery；
- release 清理；
- log rotation。

当前已完成蓝绿主流程、正确的 `on-failure`、指数退避、restart limit/reset、startup recovery、状态事务、日志轮转、release metadata/history/retention/rollback、启动 GC、cleanup pending 和退役进程重试。尚未完成硬崩溃后的跨进程 orphan 识别、SQLite 事务和完整 crash consistency 验证。

### Phase 6：安全加固（部分完成）

实现：

- deployment 数量和存储限制；
- proxy body/header/time limits；
- executable path 强校验；
- process tree termination 验证；
- Host fuzz；
- race tests；
- crash consistency；
- secret provider 设计。

当前已完成 deployment 数量/文件/字节配额、共享安全快照、executable allowlist 与绝对路径持久化、env allowlist、敏感 env 名称拒绝、转发头清理重建、proxy body/header/concurrency/upstream timeout 限制和 Unix 进程组终止。资源强限制、Host fuzz、完整 race/crash consistency、Windows Job Object 和 secret provider 尚未完成。

## 27. 提交记录和后续拆分

原计划建议按以下提交拆分：

```text
feat: add host-based control plane routing
refactor: introduce deployment manager
feat: add dynamic deployment process runtime
feat: proxy dynamic deployments by host
feat: expose dynamic deployment MCP tools
feat: add dynamic deployment recovery and restarts
docs: document Cloudflare dynamic hosting setup
```

当前实际落地提交：

```text
a98187c feat: add deployment overview and dynamic hosting plan
6333eae feat: add managed dynamic web deployments
```

后续不再为了匹配原建议拆分重写历史；剩余工作应按 Phase 2、Phase 5、Phase 6 独立提交。

每个后续提交都应保持：

- `go test ./...` 通过；
- `go vet ./...` 通过；
- `git diff --check` 通过；
- 静态部署可独立回归。

## 28. 核心动态部署验收标准

以下标准用于验收核心动态部署能力，不代表完整计划全部完成：

- Cloudflare 只需一个应用通配符 Tunnel 入口；
- 控制面只在显式 control host 上可访问；
- app Host 不能访问 LocalGate 控制面；
- 未注册 Host 返回 404；
- 客户端应用可继续使用根路径 `/`；
- 动态进程只监听分配的 loopback 端口；
- approval 前进程绝不启动；
- 健康检查通过后才公开流量；
- deployment task 完成后应用继续运行；
- 覆盖失败时旧版本继续服务；
- delete 能终止整个进程组；
- LocalGate 关闭时不遗留受管进程；
- LocalGate 重启后可恢复 active deployment；
- WebSocket 和 SSE 正常；
- 日志和状态可查询；
- 静态部署无回归；
- 所有协议返回一致状态和错误码。

## 29. 完整计划剩余工作

P0/P1 主体已在当前工作区实现。剩余工作重新归类如下。

优先级 P0：生产启用前专项验收

- WebSocket、SSE、streaming 和 backend crash 502 端到端测试；
- 覆盖健康失败保留旧版本、并发 overwrite 和 registry 半切换故障注入；
- restart exhaustion、用户 stop 不重启和进程树终止专项测试；
- Host fuzz、完整 IDNA 和真实 Cloudflare wildcard 验证；
- crash consistency 和异常断电/强杀故障注入测试；
- Windows 使用 Job Object 终止进程树，或明确限制完整 supervisor 支持平台。

优先级 P1：进一步生产运维增强

- deployment 级请求速率、WebSocket 连接数和运行指标；
- 应用日志内容脱敏和跨 release 聚合查询；
- SQLite 或其他事务存储；
- 硬崩溃后的跨进程 orphan 识别和安全回收；
- socket activation 或 listener 继承，消除端口分配竞争窗口。

优先级 P2：扩展能力

- path-based routing 兼容模式；
- custom domains；
- 自动 DNS/Tunnel 管理；
- runtime presets；
- build argv；
- secret provider；
- 多实例和负载均衡；
- resource limits；
- 独立 OS 用户；
- container/sandbox；
- 多进程和多节点协调。
