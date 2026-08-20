# LocalGate 执行计划（Execution Plan）

> 历史设计文档：早期步骤中的 `workspace`、`workspaces[]` 和 `workspace_id` 已被废弃。现行 LocalGate 采用单 Root 模型：一个实例只配置一个 `root`，需要多个安全域时运行多个实例。

目标：把 LocalGate 按“可交付的小步快跑”拆成多个可实现步骤，每一步尽量控制在 **500–1000 行代码**的变更量级，并且每步都能跑通、可验证、可回滚。

说明：
- 本计划按“模块边界 + 可运行验收”拆分，不绑定具体语言/框架；实现时可映射到你的实际技术栈。
- 500–1000 行为指导性上限：包含新增文件与必要测试/脚手架，不包含依赖安装与生成代码（如 openapi 生成）产生的噪音。

---

## Step 01：仓库骨架 + 配置（含 workspace）+ 统一错误模型（~600–900 LOC）

**目标**
- 跑起一个空服务/进程，有版本号、配置加载、统一错误返回，为后续模块提供共同约定。

**实现范围**
- 目录骨架：`core/` `capabilities/` `security/` `adapters/` `cli/` `tests/`
- 配置：本地配置文件 + 环境变量覆盖（先把“workspace 配置”作为一等公民）
  - 配置优先级：CLI flags > env > config file > defaults
  - 配置文件位置：先支持 `./localgate.yaml`（或 `~/.config/localgate/config.yaml` 二选一即可）
  - Server 配置（最小）：`bind`、`log_store`、`max_concurrency`、`default_timeout_ms`、`max_output_bytes`
  - Workspace 配置（最小）：用 `workspace_id` 将不同项目/目录隔离成不同策略域
    - `workspaces[].id`：稳定标识（用于请求参数与 task context）
    - `workspaces[].roots`：允许访问的根目录列表（Path Sandbox 的 allowlist）
    - `workspaces[].path_denylist`：禁止路径（覆盖/补充全局 denylist）
    - `workspaces[].command_allowlist`：允许命令（或允许前缀/模板，后续 Step 08 才实现执行校验）
    - `workspaces[].cwd`：默认工作目录（`run_command` 默认 cwd）
    - `workspaces[].env_allowlist`：允许透传/覆盖的环境变量名（避免泄露）；服务进程 `PATH` 作为执行基础环境始终继承，请求若要覆盖 `PATH` 仍需显式 allowlist
- 统一错误：错误码、可读消息、可选 details（不含敏感信息）
- 健康检查：`healthz`（或 CLI `--healthcheck`）

**配置示例（示意）**

```yaml
bind: "127.0.0.1:3737"
max_concurrency: 4
default_timeout_ms: 600000
max_output_bytes: 10485760

workspaces:
  - id: "localgate"
    roots:
      - "/Users/bytedance/project"
    path_denylist:
      - "/Users/bytedance/.ssh"
    cwd: "/Users/bytedance/project/LocalGate"
    command_allowlist:
      - "git"
      - "npm"
      - "pnpm"
      - "go"
    env_allowlist:
      - "PATH"
      - "HOME"
```

**验收标准**
- 可启动并输出版本与关键配置摘要
- 健康检查返回成功
- 任意错误都能以统一格式输出（CLI/HTTP 任选其一先落地）
- 可从配置中加载至少 1 个 workspace，并能在启动日志中列出 `workspace_id -> roots`（不展开敏感 denylist 细节）

---

【进度备注】已完成（Go 实现）：
- go.mod 初始化；最小依赖：yaml v3
- 可执行入口：`cmd/localgate/main.go`，支持 `-config/-bind/-print-config/-version`
- 配置模块：`internal/config`（defaults、YAML 加载、ENV/FLAG 覆盖、SafeSummary）
- 最小 HTTP 服务：`internal/server` 暴露 `/healthz`
- 本地验证：`go build` / `go run` 成功，`curl /healthz` 返回 `{"status":"ok",...}`

【延期/后续项】已记录在 write_todo.md：
- 配置文件多路径搜索策略（XDG 路径、系统路径）
- 结构化日志与可插拔日志后端

## Step 02：Task 领域模型 + 状态机 + 内存任务表（~700–1000 LOC）

**目标**
- 把“执行”抽象为 Task：可创建、可查询、可取消（先做状态与句柄，不做具体工具）。

**实现范围**
- `Task`：`task_id/status/timestamps/requester/context/result/error`
- 状态机：`queued -> running -> succeeded/failed/canceled/timeout`
- `TaskManager`：`create/get/cancel`（先用内存存储）
- ID 生成：UUID/ULID（择一）

**验收标准**
- 创建“空任务”可走通生命周期（无工具执行也能完成/失败）
- cancel 能把 running 任务打到 canceled（先模拟即可）

---

【进度备注】已完成（Go 实现）：
- 包：`internal/core`，实现 Task/Status 与 `Manager`（内存 tasks + cancel 通道）
- 能创建“空任务”并按定时完成；支持取消；状态从 queued→running→succeeded/canceled
- 单测：`internal/core/task_test.go` 覆盖成功与取消路径，`go test ./...` 通过

【延期/后续项】已记录在 write_todo.md：
- Task 错误码细化与失败路径（failed/timeout）的可复现单测
- `requester/workspace_id` 等上下文字段的来源与绑定（待接入 HTTP/MCP）

## Step 03：事件模型 + 结构化日志管道（内存版）（~700–1000 LOC）

**目标**
- 统一所有运行时输出为事件流，并支持按 task_id 查询/订阅（后面工具与协议都复用）。

**实现范围**
- `Event`：`TaskCreated/Started/ToolInvoked/Stdout/Stderr/Completed/Failed/Canceled/Timeout`
- `EventSink`：事件写入与按 task_id 拉取（ring buffer 或 append list）
- `LogStore`：stdout/stderr 分流、cursor/offset 查询（内存）

**验收标准**
- 任何任务至少产生 created/started/completed 等事件
- 能按 task_id 拉取事件与日志片段（用于后续 HTTP/SSE）

---

【进度备注】已完成（Go 实现）：
- 包：`internal/core/events.go` 定义 `Event/EventType` 与 `EventSink`（Append/List）
- 与 `Manager` 集成：创建/开始/结束/取消时写入事件
- 单测：`internal/core/events_test.go` 覆盖 Append/List 与 Manager 触发事件

【延期/后续项】已记录在 write_todo.md：
- 日志事件 cursor/offset 的跨进程语义（与持久化对齐）
- stdout/stderr 的分片大小上限与节流策略

## Step 04：Tool Registry + 调用约定（~600–900 LOC）

**目标**
- 建立“工具即插件”的统一入口：后续 file/shell 都走同一个 `invoke()`。

**实现范围**
- `Tool` 接口：`name/schema/execute(ctx,args)->result`
- `Registry`：注册、列出、查找、调用
- `InvokeContext`：携带 task_id、工作区根目录、logger、取消信号等

**验收标准**
- 注册一个 demo 工具（如 `echo`）可被调用并产生日志事件
- 工具调用失败可映射为统一错误模型

---

【进度备注】已完成（Go 实现）：
- 包：`internal/core/registry.go`，定义 `Tool/Registry/InvokeContext`
- 能 `Register/Get/List/Invoke`，并在 Invoke 时写入 ToolInvoked 事件
- 单测：`internal/core/registry_test.go` 覆盖注册/调用与事件写入

【延期/后续项】已记录在 write_todo.md：
- Tool schema 的 JSON Schema 表达与导出（供 MCP/HTTP 使用）
- InvokeContext 的日志/取消信号对接（待接入 Runner）

## Step 05：最小持久化（Task 元信息 + 事件追加）（~700–1000 LOC）

**目标**
- 服务重启后不“失忆”：至少能查历史任务元信息与事件（用于回查、审计的地基）。

**实现范围**
- 存储选择：本地文件（append log + index）或 SQLite（二选一，尽量简单）
- 持久化内容：
  - task 元信息（创建、状态变化、结果摘要）
  - 事件追加（至少保存时间戳、类型、payload 摘要）
- 启动恢复：加载未完成任务（标记为 failed/unknown 或重入策略先不做）

**验收标准**
- 创建任务并产生日志，重启后仍能查询到任务与日志
- 存储文件/库不会无限增长（最小保留策略：按条数或按时间裁剪）

---

【进度备注】已完成（Go 实现）：
- 包：`internal/persist/store.go`，文件型存储：`tasks/<id>.json` 与 `events/<id>.log`（JSON Lines）
- 能保存/读取任务与事件；单测覆盖 `SaveTask/LoadTasks/AppendEvent/LoadEvents`

【延期/后续项】已记录在 write_todo.md：
- 与内存 EventSink/Manager 的实时接线（hook OnAppend 或持久化写入器）
- 保留策略与清理：按天/按条数/按总大小裁剪

## Step 06：Path Sandbox（安全底座 1/2）（~500–800 LOC）

**目标**
- 文件能力在 MVP 阶段也不裸奔：限定只能访问允许的 workspace roots，拒绝敏感目录与符号链接穿透。

**实现范围**
- 配置：`workspace_roots`、`path_denylist`、`follow_symlink` 策略
- 统一路径解析：realpath、规范化、相对路径处理
- 校验函数：`validate_path(op, path)->allowed|deny(reason)`

**验收标准**
- 允许目录可访问，禁止目录返回一致错误码
- 通过 `../`、symlink 等方式无法逃逸允许根目录

---

【进度备注】已完成（Go 实现）：
- 包：`internal/security/sandbox.go`，提供 `ValidatePath`（roots/denylist/symlink 策略）
- 单测：`internal/security/sandbox_test.go` 覆盖在根内允许与命中 denylist 的拒绝

【延期/后续项】已记录在 write_todo.md：
- 更严格的符号链接策略（跨文件系统、循环链接检测）与拒绝原因分类
- 与 Workspace 配置打通 follow_symlink 的默认值与覆盖

## Step 07：Filesystem Tools（~700–1000 LOC）

**目标**
- Agent 有“手”：能读/写/列目录/搜索文件，并且所有操作都走 sandbox 校验与事件日志。

**实现范围**
- `read_file/write_file/list_directory/search_files`
- 大小限制：最大文件大小、最大结果数
- 事件：ToolInvoked + 结果摘要（不记录大段内容到审计事件里）

**验收标准**
- 对一个真实项目目录：可读文件、可写临时文件、可搜索匹配
- 所有操作都产生可回查事件

---

【进度备注】已完成（Go 实现）：
- 包：`internal/capabilities/filesystem/files.go`，实现 `read_file/write_file/list_directory/search_files`
- 路径全部走 `ValidatePath` 校验；单测覆盖读写、列表与搜索
- 后续将通过 Registry 暴露给协议层（待接入 HTTP/MCP）

【延期/后续项】已记录在 write_todo.md：
- 大文件/二进制文件的读取策略（大小上限与截断、编码）
- search 的跨目录深度限制与忽略规则（如 .git/node_modules）

## Step 08：Command Policy（安全底座 2/2）（~500–800 LOC）

**目标**
- 给 shell “加护栏”：先用轻量策略避免明显高危命令/参数。

**实现范围**
- 配置：`command_allowlist`（命令名/前缀/模板择一落地）
- 基础拒绝规则：高危模式（如 `rm -rf /`）拦截（规则要可配置可扩展）
- 校验函数：`validate_command(command, cwd, env)->allowed|deny`

**验收标准**
- allowlist 内命令可执行
- 明确禁止的命令稳定拒绝，并输出原因（不泄露敏感信息）

---

【进度备注】已完成（Go 实现）：
- 包：`internal/security/command_policy.go`，提供 `ValidateCommand`（首 token allowlist）
- 单测：`internal/security/command_policy_test.go` 覆盖允许/未配置/明显高危命令被拒绝

【延期/后续项】已记录在 write_todo.md：
- 更细粒度的参数模板化白名单与黑名单规则
- 命令解析采用 argv 切分而非空格（跨平台 shell 差异）

## Step 09：Shell Runner（同步版）+ stdout/stderr 事件化（~800–1000 LOC）

**目标**
- Agent 有“脚”：能稳定运行命令并实时产生 stdout/stderr 事件与 exit code。

**实现范围**
- `run_command`（同步执行）：cwd、env（白名单）、timeout（先软超时）
- stdout/stderr 捕获：边读边发事件（避免一次性读爆内存）
- 结果：exit_code、signal、duration

**验收标准**
- 跑 `echo/ls` 等命令可实时看到日志
- 非 0 退出码能被正确记录并返回

---

【进度备注】已完成（Go 实现）：
- 包：`internal/capabilities/shell/runner.go`（同步 Runner，SSE 友好的事件生产）
- 工具：`internal/capabilities/shell/tool.go`（run_command；cwd/env 白名单、可选 timeout_ms）
- 与事件：stdout/stderr 逐行写入 EventSink；`Registry.Invoke` 支持 ic.Sink 透传
- 单测：`tool_test.go` 覆盖 echo 正常输出与 timeout 路径

【延期/后续项】已记录在 write_todo.md：
- 子进程组治理与硬终止（Step 10）
- argv 解析与跨平台 shell 差异处理

## Step 10：取消/超时的“硬终止”与子进程治理（~600–900 LOC）

**目标**
- 让任务控制真正有效：cancel/timeout 能终止运行中的命令（含子进程组兜底）。

**实现范围**
- 取消信号：TaskManager 发出 cancel，Runner 监听并终止
- 超时：到点自动 cancel + 标记 timeout
- 进程组/树终止：平台相关实现（macOS/Unix 优先）

**验收标准**
- `sleep 60` 类命令能被 cancel 在短时间内停止
- timeout 能触发并收敛到 timeout 状态

---

【进度备注】已完成（Go 实现）：
- Runner：进程组启动（setpgid），取消/超时对进程组执行 SIGTERM→SIGKILL，避免遗留子进程
- run_command：取消/超时会追加 TaskCanceled/TaskTimeout 事件（用于后续协议层订阅）
- 单测：新增测试验证 cancel 后子进程 pid 被回收（kill(pid,0) 不存在）

【延期/后续项】已记录在 write_todo.md：
- Windows 平台适配（Job Object）与跨平台抽象
- 超时/取消的错误码与用户可读信息规范化

## Step 11：HTTP Adapter（任务式 API + 日志拉取）（~800–1000 LOC）

**目标**
- 任意客户端可用 HTTP 接入 LocalGate：创建任务、查状态、拉日志、取消。

**实现范围**
- `POST /tasks`：提交 `tool_name + args` 或 `action`（先支持单工具）
- `GET /tasks/{id}`：状态/结果
- `GET /tasks/{id}/logs?cursor=`：日志分页
- `POST /tasks/{id}/cancel`
- 错误格式与 Step 01 对齐

**验收标准**
- curl 可创建 `run_command` 任务并获取日志与最终结果
- 断线后可继续用 task_id 拉取后续日志

---

【进度备注】已完成（Go 实现）：
- HTTP API：`POST /tasks`、`GET /tasks/{id}`、`POST /tasks/{id}/cancel`、`GET /tasks/{id}/logs?cursor=`
- 任务执行：创建任务后在后台调用 Registry 的工具执行；stdout/stderr 事件可通过 logs 接口回查
- Workspace 选择：请求可指定 workspace_id；缺省使用配置的 default workspace
- 测试：`internal/httpapi/api_test.go` 覆盖创建任务、查询状态、拉取日志与取消任务

【延期/后续项】已记录在 write_todo.md：
- HTTP 鉴权与请求者身份注入（requester 的可信来源）
- 返回结构中补齐 tool 结果（artifacts/result）与错误码规范化

## Step 12：HTTP 流式事件（SSE/WebSocket 二选一）（~600–900 LOC）

**目标**
- 提供“实时可观测”的开发体验：UI/Agent 能订阅任务事件流。

**实现范围**
- `GET /tasks/{id}/events/stream`：SSE（推荐先做）或 WebSocket
- cursor 语义：断线重连不丢事件（至少 best-effort）

**验收标准**
- 创建任务后可实时看到 stdout/stderr 事件
- 断线重连能从 cursor 继续（或明确告知从最新开始）

---

【进度备注】已完成（Go 实现）：
- SSE：`GET /tasks/{id}/events/stream`，支持 query `cursor` 与 `Last-Event-ID` 恢复
- 断线重连：cursor 以事件 offset 为语义，重连后从指定 offset 继续输出
- 测试：新增 SSE 基础流与 cursor resume 测试（a→断线→b）

【延期/后续项】已记录在 write_todo.md：
- EventSink 的实时通知机制（避免轮询），以及 per-connection 背压/缓冲控制
- SSE 心跳与连接保活策略（避免中间层超时）

## Step 13：MCP Adapter（工具列表 + 调用 + 任务管理工具）（~800–1000 LOC）

**目标**
- 成为“给现有 Agent 插上手脚”的主入口：MCP 客户端可直接调用 file/shell 与任务管理。

**实现范围**
- MCP server：列出工具与 schema、处理调用
- 工具映射：
  - 直接工具：`read_file/write_file/run_command`
  - 管理工具：`task_status/task_logs/task_cancel`
- 统一错误/日志：与 Core 事件对齐

**验收标准**
- 任意 MCP 客户端能成功调用 read/write/run_command
- 长命令可返回 task_id 并用 task_logs 追踪

---

【进度备注】已完成（Go 实现）：
- MCP（stdio）：`-mcp` 模式启动，使用 JSON-RPC 2.0 newline framing
- 生命周期：支持 `initialize` / `ping` / `notifications/initialized`（忽略通知）
- Tools：支持 `tools/list` / `tools/call`
- 管理工具：通过工具暴露 `task_status` / `task_logs` / `task_cancel`
- 测试：`internal/mcp/stdio_test.go` 覆盖 tools/call 执行与日志回查

【延期/后续项】已记录在 write_todo.md：
- Tool inputSchema 的真实 JSON Schema 导出（目前为最小 `{type:object}`）
- Streamable HTTP MCP transport（与现有 HTTP API 的关系与复用）

## Step 14：Killer Demo（自动修复闭环）+ 最小回归测试集（~800–1000 LOC）

**目标**
- 做出对外可演示的端到端闭环：读→改→测→diff/report；并建立基本回归测试防止后续迭代破坏核心链路。

**实现范围**
- Demo runner（CLI 或脚本）：串联工具调用（不做复杂 Agent，只做稳定流程）
- 输出：
  - 修改文件列表与 diff（或 patch 文件）
  - 测试命令结果与摘要
  - task_id 与日志链接（HTTP/MCP 任选）
- 回归测试：
  - sandbox 不可逃逸
  - command policy 拦截生效
  - run_command 可取消/超时

**验收标准**
- demo 可重复运行并稳定成功（失败可诊断）
- 核心能力有最小自动化测试覆盖

---

【进度备注】已完成（Go 实现）：
- Demo runner：`cmd/localgate-demo`，在临时目录构造一个可复现失败的 Go 项目
- 闭环：初次 `go test` 失败 → `read_file` 读取源码 → `write_file` 应用修复 → `go test` 通过 → `diff -u` 输出 patch
- 输出：包含 initial/final 测试结果与对应 task_id，以及 diff 的 task_id

【延期/后续项】已记录在 write_todo.md：
- 更贴近真实项目的 demo（多文件修改、lint/test 多步骤、失败诊断报告）

## Step 15（可选）：审批/审计能力落地的第一刀（~800–1000 LOC）

**目标**
- 在不引入复杂 UI 的情况下，先把 approval/audit 的技术闭环跑通，为 Phase 2 铺路。

**实现范围**
- Policy hook：在工具执行前判定是否需要 approval
- Approval store：pending/approved/rejected（可先用本地存储）
- Audit 记录：请求、审批、执行结果（脱敏摘要）
- CLI 审批：用 CLI 先替代 UI（避免一次做太大）

**验收标准**
- 高风险操作会进入 pending 并阻塞执行，批准后继续，拒绝则失败
- 重启后仍能查到审批与审计记录

---

【进度备注】已完成（Go 实现）：
- Approval/Audit：`internal/approval` 提供本地文件存储（approvals/*.json + audit/events.jsonl）
- Policy hook：当前对高风险 `run_command`（如 rm/sudo/curl/chmod/git push/commit 等）进入 pending_approval
- HTTP：新增 `GET /approvals/{id}`、`POST /approvals/{id}/approve`、`POST /approvals/{id}/reject`
- CLI：新增 `cmd/localgate-approve`，通过 HTTP 执行 approve/reject
- 测试：新增 approval store 单测与 HTTP approval flow 测试；`go test ./...` 通过

【延期/后续项】已记录在 write_todo.md：
- approval policy 仍是代码内置规则，后续应改为配置化策略

---

## Step 16：安全命令执行（argv/模板化）（~700–1000 LOC）

**目标**
- 为高安全场景提供“无 shell”的命令执行工具（如 `run_argv`），并支持模板化参数规则；逐步减少对 `/bin/sh -lc` 的依赖。

**实现范围**
- Runner 增加 argv 路径：`exec.CommandContext(ctx, argv[0], argv[1:]...)`，禁用 shell 元字符
- 新增 `run_argv` 工具：入参为 `argv: string[]`；接入 command policy 的模板化白名单
- Policy 扩展：模板规则（命令名 + 允许参数模式）
- 事件与日志：沿用 stdout/stderr 产出

**验收标准**
- 可运行典型命令（如 `["git","status"]`）并产出事件
- 含元字符的 argv 会被拒绝或净化，策略命中有效

---

【进度备注】已完成（Go 实现）：
- Runner：新增 `RunArgv`，对 argv 执行路径禁用 shell 元字符、沿用事件与超时/取消
- 工具：新增 `run_argv` 并注册到 Registry（每 workspace）
- 测试：`internal/capabilities/shell/argv_tool_test.go` 覆盖 echo 正常执行与 timeout 路径；`go test ./...` 通过

【延期/后续项】已记录在 write_todo.md：
- command policy 模板化白名单与参数模式的配置化
- 对 run_command 的迁移指导与兼容层

---
## Step 17：事件管道的背压与保留策略（~600–900 LOC）

**目标**
- 让 EventSink 在高并发/高输出场景下可控：有上限、有清理、有背压与心跳。

**实现范围**
- 每 task 事件上限（可配）与截断策略；全局上限与按 task 驱逐
- SSE 心跳与保活；连接关闭策略；基础背压（队列/缓冲控制）
- 事件实时通知（条件变量/通道）替代轮询

**验收标准**
- 压测下连接不堆积、事件不无限增长；断线重连可继续

---

【进度备注】已完成（Go 实现）：
- EventSink 增加每 task 事件上限与截断（默认 1000，可调用 NewEventSinkWithLimit 设置）
- SSE 心跳与保活：无事件时仍推送 `: heartbeat`，保持连接活跃
- 单测：新增事件上限截断用例；全量测试通过

【延期/后续项】已记录在 write_todo.md：
- 全局上限与按 task 驱逐策略；连接级背压与缓冲控制；心跳频率配置化

---
## Step 18：工具 Schema 导出（MCP/HTTP 同源）（~600–900 LOC）

**目标**
- 为 read/write/run_command/task_* 输出真实的 JSON Schema（input/output），提升生态接入体验。

**实现范围**
- Registry 生成工具 inputSchema/outputSchema
- MCP tools/list 返回 schema；HTTP 文档（可选）同步
- 结构化结果：对 run_command 输出中补 structured 内容与错误码规范

**验收标准**
- 工具 schema 可被客户端解析并用于参数校验；示例客户端接入成功

---

【进度备注】已完成（Go 实现）：
- Filesystem 工具：为 `read_file/write_file/list_directory/search_files` 提供 inputSchema
- Shell 工具：为 `run_command/run_argv` 提供 inputSchema（包含 cwd/timeout/env/argv 等）
- MCP：`tools/list` 现在返回 Registry 工具的真实 schema；任务管理工具返回固定 schema
- 测试：全量测试通过

【延期/后续项】已记录在 write_todo.md：
- outputSchema 与结构化错误码规范；HTTP 文档同步
- Schema 版本与校验错误映射（Protocol vs Tool execution errors）

---
## Step 19：持久化接线与最小恢复（~700–1000 LOC）

**目标**
- 将事件与任务的关键字段持续写入 Store，并提供最小的重启恢复策略。

**实现范围**
- EventSink Append hook → Store.AppendEvent
- Task 结束写 Store.SaveTask；按天/条数/大小做保留
- 重启最小恢复：加载任务元信息，状态解释与查询可用

**验收标准**
- 重启后可查询历史任务与事件；保留策略生效

---

【进度备注】已完成（Go 实现，写入路径）：
- EventSink：新增 writer hook（EventWriter），在 Append 时写 Store.AppendEvent
- TaskManager：新增 task writer（TaskWriter），在 finishTask 时写 Store.SaveTask
- App：默认以 `log_store` 为 base 初始化 Store，并挂载到 Sink/Manager；终态任务及事件日志保留 48 小时，启动时和每小时执行清理
- 测试：全量测试通过

【延期/后续项】已记录在 write_todo.md：
- 启动最小恢复策略（加载历史任务元信息并解释状态）
- 保留策略配置化（按天/条数/大小），事件与任务清理任务

---
【补充】已完成最小恢复：
- App 启动时加载 `tasks/*.json` 历史任务并注入 TaskManager；可查询历史状态（不做重入执行）
## Step 20：审批策略配置化与审计查询（~700–1000 LOC）

**目标**
- 把 approval policy 从硬编码改为 workspace 级配置，并提供审计查询/导出接口。

**实现范围**
- config 增加 approval policy 规则：按 tool/path/command/argv 模式
- HTTP 审计查询：按 approval_id/task_id/requester 过滤与分页
- 文档：策略定义与示例配置

**验收标准**
- 高风险操作能按配置触发/放行；审计查询可用并返回结构化结果

---

【进度备注】已完成（Go 实现，配置与查询）：
- Approval policy 配置化：workspace 配置新增 `approval_policy`（commands/write_paths），按配置决定是否 pending
- HTTP 审计查询：新增 `GET /audits?approval_id=&task_id=&requester=&limit=&offset=` 返回结构化记录
- 测试：更新 HTTP approval flow 用例以验证配置驱动的审批触发；全量测试通过

【延期/后续项】已记录在 write_todo.md：
- 审计导出（CSV/JSONL）、分页优化与时间范围过滤
- 更丰富的策略表达（正则/模板），以及策略文档与示例

---
## Step 21：统一审批执行流与审批后台（分 4 个子阶段落地）

> Step 15 和 Step 20 已完成审批存储、基础策略、单条审批接口与审计查询的原型闭环；本步骤负责把审批升级为所有工具/API 调用共享的执行安全边界。动态 Web 托管不在本步骤范围内。

### 21.0 已锁定的产品与架构决策

- 所有可能执行工具的协议入口统一经过一个执行提交服务，不允许协议层直接调用 `App.InvokeTool`、`CreateToolTask` 或 `Registry.Invoke` 绕过审批。
- 不需要审批的调用继续走原有快速路径：不创建 approval、不进入等待状态、不要求用户额外交互。
- 需要审批的调用在服务端保存原始请求并创建 gated task；审批通过后自动释放任务继续执行，调用方和模型不得重新提交原操作。
- `pending_approval` 是合法的执行状态，不作为普通工具失败返回。
- 服务返回 `approval_id`、`approval_url` 和 `task_id`；模型向用户展示审批链接，用户批准后通过现有 `task_status` / `task_result` 或 HTTP task API 获取结果。
- 当前没有独立客户端和审批通知系统；第一版采用“模型提示审批链接 + 用户回到对话确认 + 模型查询 task_id”的拉取式流程。
- 同步调用只在有限窗口内等待；仍未批准或仍未执行完成时返回 `202 Accepted` / `pending_approval`，后台任务生命周期不依赖 HTTP 连接生命周期。
- 审批必须绑定 workspace、requester、tool 和规范化参数摘要；一个审批只能消费一次，不能被用于另一组参数或另一个任务。
- 审批链接的 GET 只展示信息，状态变更必须通过 POST；审批人身份来自服务端认证上下文，不接受请求体伪造 `actor`。
- `approval.base_url` 表示审批页面的外部访问前缀，不表示 LocalGate 额外监听一个独立端口；审批路由仍由现有 HTTP 服务承载。
- `approval_url` 必须由 `approval.base_url + /approval-ui/{approval_id}` 生成，不允许隐式回退到 `http.base_url`，避免模型拿到错误或不可访问的审批地址。

### 21.1 目标执行模型

```text
HTTP /tasks
HTTP /tools/{tool}/call
MCP stdio tools/call
MCP Go tools/call
HTTP MCP runtime
        ↓
ExecutionService.Submit
        ↓
参数校验 / 规范化 / 摘要
        ↓
ApprovalPolicy.Evaluate
   ┌────────────┼──────────────────┐
   ↓            ↓                  ↓
 allow          deny          require_approval
   ↓            ↓                  ↓
创建并执行任务  返回策略拒绝       创建 gated task
                                  ↓
                              创建 approval
                                  ↓
                    返回 approval_url + task_id
                                  ↓
                           用户在网页批准
                                  ↓
                       原子消费 approval 记录
                                  ↓
                            release task gate
                                  ↓
                              自动执行原请求
```

建议新增统一模型：

```go
type ExecutionRequest struct {
    WorkspaceID    string
    Requester      Principal
    Tool           string
    Args           map[string]any
    IdempotencyKey string
    WaitDuration   time.Duration
}

type Submission struct {
    Status      string
    TaskID      string
    ApprovalID  string
    ApprovalURL string
    ExpiresAt   time.Time
    Result      map[string]any
}
```

统一返回状态：

- `succeeded`：已完成并带最终结果
- `queued` / `running`：任务已开始或等待调度
- `pending_approval`：任务在审批 gate 前等待
- `rejected` / `expired`：审批终止，工具未执行
- `failed` / `canceled` / `timeout`：任务执行终态

---

### Step 21A：统一执行网关与 gated task（~900–1300 LOC）

**目标**

- 先解决审批绕过和“审批更新前任务已经开始”的竞态问题。
- 让所有协议入口获得一致的 allow / deny / pending_approval / task 行为。

**建议文件**

```text
internal/execution/service.go
internal/execution/types.go
internal/execution/gated_task.go
internal/execution/digest.go
internal/execution/service_test.go
```

**实现范围**

- 新增 `ExecutionService.Submit(ctx, ExecutionRequest)`，作为外部工具执行唯一入口。
- 将真实工具调用收口到内部方法，例如 `executeAuthorizedTool`；协议层不再直接使用 `App.InvokeTool`。
- 从 `mcpRuntime.createApprovedTask` 提取通用 gated task：任务可以创建，但在 approval gate 释放前不得进入工具代码。
- 为等待审批的任务增加明确状态或可查询元信息：`awaiting_approval`、`approval_id`、`approval_url`、`expires_at`。
- 所有入口接入统一网关：
  - `POST /tasks`
  - `POST /tools/{tool}/call`
  - MCP stdio `tools/call`
  - MCP Go `tools/call`
  - HTTP MCP runtime
  - 创建高风险 session 的 `exec_command`
- 保留 task 管理工具和只读查询入口的直接查询路径，避免它们递归触发审批。
- 支持调用级等待窗口：快速执行完成时返回最终结果；超时则返回 task/approval 状态，不取消后台任务。
- 增加 `Idempotency-Key` 或等价内部字段，为后续防止模型超时重试造成重复执行打基础。

**同步调用约定**

- HTTP 支持 `Prefer: wait=N` 或等价 `wait_ms`，最大值由服务端配置限制。
- 等待窗口内完成：返回 `200` 和最终结果。
- 等待窗口内仍需审批或仍在运行：返回 `202` 和 `task_id`；审批通过后任务自动续跑。
- 客户端断开默认不取消任务；显式取消仍通过 task cancel API。
- MCP 同步调用若短时间内不能完成，返回结构化 `pending_approval` 或异步 task 信息，不将其包装成普通执行错误。

**验收标准**

- 任一需要审批的调用，在批准前都不能进入具体工具 `Execute`。
- 普通 App 审批不再存在“先启动 task、后更新 approval”的竞态。
- `/tools/{tool}/call`、MCP Go 非 deploy 工具等现有直调路径不能绕过审批。
- 同一工具通过不同协议调用时，审批决策和返回结构一致。
- 无需审批的工具不创建审批文件，不进入 gate，现有成功路径与结果结构保持兼容。

---

### Step 21B：审批后台、审批链接与可靠状态机（~900–1400 LOC）

**目标**

- 提供无需独立客户端即可使用的审批页面和列表后台。
- 让批准、拒绝、过期和一次性消费具备明确、可靠的状态迁移。

**审批状态机**

```text
pending
  ├─ approve → consumed → task released
  ├─ reject  → rejected → task canceled
  ├─ timeout → expired  → task canceled
  └─ cancel  → canceled → task canceled
```

第一版可将 `approved` 和 `consumed` 合并成一次原子状态迁移，避免“已批准但未绑定执行”的中间状态。若保留 `approved`，必须保证只有绑定 task 并准备释放 gate 后才能进入该状态。

**审批数据模型补充**

```text
id
status
task_id
workspace_id
requester_id
tool
args / redacted_args
args_digest
policy_id
policy_version
reason_code
risk_level
summary
created_at
expires_at
decided_at
consumed_at
approver_id
decision_comment
```

**HTTP API**

```text
GET  /approvals?status=pending&workspace_id=&tool=&limit=&cursor=
GET  /approvals/{approval_id}
POST /approvals/{approval_id}/approve
POST /approvals/{approval_id}/reject
POST /approvals/{approval_id}/cancel
GET  /approval-ui/{approval_id}
```

**审批页面要求**

- 展示工具、workspace、请求者、风险等级、操作摘要、关键参数、到期时间和 `task_id`。
- 对部署操作展示站点名、来源目录、是否覆盖、是否删除源目录、文件数量和大小等事实。
- GET 页面绝不改变审批状态；批准和拒绝按钮提交 POST。
- 页面默认放在现有 Cloudflare Access / HTTP Auth 后；审批身份由认证上下文产生。
- 暂无身份系统时才考虑一次性 bearer capability link，并要求高强度随机 token、只存 hash、短 TTL、一次性消费和 `Referrer-Policy: no-referrer`。
- 页面批准后显示“任务已自动继续，请返回对话并让模型查询 task_id”。
- 页面链接统一使用 `approval.base_url` 生成；该值可以包含反向代理路径前缀，例如 `https://example.com/localgate-approval`。
- URL 拼接必须清理重复斜杠，但不得改写 scheme、host 或已有路径前缀。
- 当全局审批开启且存在可能触发审批的规则时，`approval.base_url` 为空应在启动配置校验阶段报错；关闭全局审批时允许为空。

**服务返回格式**

```json
{
  "status": "pending_approval",
  "approval_id": "appr_xxx",
  "task_id": "task_xxx",
  "approval_url": "https://macgate.pushan.eu.org/approval-ui/appr_xxx",
  "expires_at": "2026-06-29T10:00:00Z",
  "summary": "覆盖静态站点 suzhou_exam_quiz",
  "risk_level": "high"
}
```

**存储与并发要求**

- 审批文件使用临时文件 + fsync + rename 的原子写入方式，目录权限 `0700`、记录权限 `0600`。
- 状态更新必须是条件迁移：只有 `pending` 可以被批准或拒绝；重复操作返回稳定的冲突错误。
- 批准时校验 TTL、workspace、tool、requester、`args_digest` 和 task 绑定关系。
- approval 只能消费一次；审批成功后才释放 gate。
- 审批或任务创建失败时不得留下可被误执行的半成品状态。
- 若后续需要多进程共享 `log_store`，迁移到 SQLite 或加入跨进程锁；第一版先明确单进程约束并补原子文件写入。

**验收标准**

- 审批后台可以列出全部 pending 请求并完成查看、批准和拒绝。
- 链接预览或浏览器 GET 不会触发批准。
- 重复批准、过期批准、参数摘要不匹配都不能释放任务。
- 审批通过后无需客户端重新提交，原 task 自动进入 queued/running。
- 拒绝和过期后任务明确终止，`task_status` / `task_result` 能返回可理解的最终原因。

---

### Step 21C：模型 Instruction、协议文档与 task 结果闭环（~500–900 LOC）

**目标**

- 让模型正确理解审批不是失败，并能向用户展示链接、避免重复调用、在用户确认后查询执行结果。

**Instruction 统一规则**

在 startup prompt、MCP skill、HTTP API skill 和部署工具说明中加入统一约定：

```text
当工具返回 status=pending_approval 时：
1. 不要把它描述为工具失败。
2. 向用户展示 approval_url，并简要说明待审批操作、风险和过期时间。
3. 不要重复提交原工具调用；原任务会在批准后自动继续。
4. 告诉用户批准后回复“已批准”。
5. 用户确认后，使用 task_id 调用 task_status；任务结束后调用 task_result 获取结果。
6. queued/running 时只报告当前状态，不要创建第二个任务。
7. rejected/expired/canceled/failed 时明确报告原因。
```

**任务查询闭环**

- 复用并统一现有 MCP 工具：`task_status`、`task_logs`、`task_cancel`、`task_result`。
- HTTP 复用 `GET /tasks/{task_id}`、日志和事件接口；必要时补充 approval 元信息。
- `task_status` 对等待审批的任务返回：

```json
{
  "task_id": "task_xxx",
  "status": "awaiting_approval",
  "approval_id": "appr_xxx",
  "approval_url": "https://...",
  "expires_at": "..."
}
```

- `task_result` 在任务未完成时保持 `ready=false`；审批被拒绝或过期时返回稳定的终态与错误码。
- 工具描述中明确：`pending_approval` 后查询 task，不要重放原调用。

**文档更新范围**

```text
internal/capabilities/meta/prompt.j2
skills/localgate-mcp-tools/SKILL.md
skills/localgate-http-api/SKILL.md
docs/mcp_http_execution_api_plan.md
api-command-collection.md
```

文档至少包含：

- 各协议的 `pending_approval` 返回示例
- 审批链接的安全语义
- 同步等待与 `202` 降级规则
- 批准后自动续跑规则
- task 查询示例
- 禁止重复提交和幂等键说明
- 拒绝、过期和失败的错误码

**验收标准**

- 模型看到 `pending_approval` 后能够稳定展示链接，而不是自动重试原工具。
- 用户回复“已批准”后，模型只通过 `task_id` 查询并报告最终结果。
- HTTP、MCP stdio 和 MCP Go 文档使用相同字段和状态术语。
- Instruction 有单元测试，确保关键规则不会在模板修改中丢失。

---

### Step 21D：审批规则配置化与无打断快速路径（~700–1100 LOC）

**目标**

- 将审批从工具内硬编码判断升级为统一、可解释的配置决策。
- 确保不需要审批的工具不受额外交互影响。

**建议配置**

```yaml
approval:
  base_url: "https://approve.example.com"
  enabled: true
  default_ttl_seconds: 600
  max_sync_wait_seconds: 30

  rules:
    - id: deny-workspace-root-delete
      effect: deny
      tools: [deploy_tool]
      when:
        preserve_source: false
        source_is_workspace_root: true

    - id: approve-deploy-overwrite
      effect: require_approval
      tools: [deploy_tool]
      when:
        overwrite: true
      risk: high

    - id: approve-source-removal
      effect: require_approval
      tools: [deploy_tool]
      when:
        preserve_source: false
      risk: critical

    - id: approve-destructive-shell
      effect: require_approval
      tools: [run_command, run_argv, exec_command]
      when:
        command_categories: [filesystem_delete, network_upload, privilege_change]
      risk: high

    - id: allow-agent-tools
      effect: allow
      tools: [read_file, list_directory, search_files]
```

**决策模型**

```go
type Decision struct {
    Effect       Effect // allow, deny, require_approval
    PolicyID     string
    PolicyVersion string
    ReasonCode   string
    RiskLevel    string
    Summary      string
    Facts        map[string]any
}
```

**实现范围**

- `approval.enabled=false` 时跳过可选审批策略，但硬性安全拒绝仍然有效。
- `approval.base_url` 仅用于生成审批链接，不用于普通 API、静态站点、上传下载或 MCP 地址。
- 配置加载时标准化 `approval.base_url` 的尾部斜杠；启用审批时校验其为可用的 `http` 或 `https` URL。
- 支持 `allow`、`deny`、`require_approval` 三类结果；deny 不允许通过人工审批覆盖。
- 保留现有 workspace `approval_policy.commands/write_paths` 的兼容读取，并提供迁移到新规则格式的路径。
- 工具通过统一风险描述接口输出规范化事实，策略不直接解析每种工具的任意原始参数。
- `deploy_tool` 至少输出 `overwrite`、`preserve_source`、站点名、源目录和来源是否为 workspace root。
- shell 工具至少输出 executable、是否删除/写入、是否联网、是否使用 shell control/redirect/background 等事实。
- 策略决策写入 approval 和 audit，使用稳定 `reason_code`，避免只保存自然语言。
- 不需要审批的调用直接创建正常任务，不写 approval store、不进入 `awaiting_approval`、不返回 approval 字段。

**减少打断的后续能力（本步骤只预留模型，不要求首版全部实现）**

- 相同请求摘要在 pending 状态下去重，返回同一个 approval/task。
- 支持 `Idempotency-Key`，相同 key + 相同参数返回原任务；参数不同返回冲突。
- 后续可增加短期、有限范围 grant，例如“10 分钟内允许覆盖同一站点，但不允许删除源目录”。
- grant 必须绑定 principal、workspace、tool、资源范围、最大风险和过期时间；风险升级必须重新审批。

**验收标准**

- 配置为 `allow` 的工具不出现审批链接，执行行为与改造前一致。
- 配置为 `require_approval` 的工具在所有协议入口统一挂起。
- 配置为 `deny` 的操作不能通过审批绕过。
- 关闭审批后可选规则不再打断调用，但 workspace/path 等硬性安全边界保持生效。
- 策略命中结果可在审批页面和审计记录中解释到具体 rule ID。

---

### 21.2 推荐实施顺序

1. **21A 第一批**：建立 `ExecutionService`、通用 gated task，先接入 `/tasks`、`/tools/{tool}/call` 和 MCP Go，消除绕过与竞态。
2. **21B 最小闭环**：补审批列表、审批页面、TTL、一次性消费；批准后自动 release task。
3. **21C 模型闭环**：更新 instruction/skills/docs，统一 `pending_approval` 与 `task_status/task_result` 行为。
4. **21A 第二批**：接入 MCP stdio、HTTP MCP runtime、session 创建等剩余执行入口。
5. **21D 配置化**：引入统一规则模型，迁移现有 deploy/command/write path 特例。
6. **可靠性收尾**：幂等、pending 去重、原子文件写入、审计完整性和全协议回归测试。

### 21.3 测试矩阵

**核心正确性**

- allow：工具立即执行，未创建 approval。
- deny：工具未执行，返回稳定策略错误。
- require approval：批准前工具调用计数为 0；批准后只执行 1 次。
- reject / expire / cancel：工具调用计数始终为 0。
- 重复批准：只有一个请求成功，不会创建或释放第二个任务。
- approval 与参数、tool、workspace、requester 不匹配：拒绝执行。

**协议一致性**

- `/tasks`
- `/tools/{tool}/call`
- MCP stdio
- MCP Go
- HTTP MCP runtime

以上入口对同一请求返回相同的 `status`、`approval_id`、`approval_url`、`task_id` 语义。

**同步与重试**

- 等待窗口内批准并完成时可返回最终结果。
- 等待超时返回 `202`，后续批准仍自动执行。
- HTTP 客户端断开不取消 gated task。
- 相同幂等键重试不重复创建任务；相同键不同参数返回冲突。

**安全与后台**

- GET 审批页面不改变状态。
- 未认证或无审批权限的用户不能批准。
- 页面和审计对 token/password/secret/authorization/cookie/private_key 等字段脱敏。
- 过期链接、已消费链接和被拒绝链接不能再次执行。
- `approval_url` 必须以配置的 `approval.base_url` 为前缀，且不能意外使用 `http.base_url`。
- `approval.base_url` 带路径前缀、尾部斜杠或反向代理子路径时，生成链接仍然正确。

### 21.4 完成定义

- 需要审批的操作在批准前绝不执行，批准后无需重新提交并自动续跑。
- 所有工具/API 执行入口统一走审批决策，不存在协议级绕过。
- 审批后台能列出、查看、批准和拒绝请求，并返回可访问的 `approval_url`。
- 所有审批链接均由独立的 `approval.base_url` 生成；普通 `http.base_url` 的修改不会改变审批链接。
- 模型有统一 instruction，能展示审批链接并通过 `task_id` 获取最终结果。
- 审批规则可配置；不需要审批的工具不受额外交互影响。
- approval 与执行请求强绑定、短期有效、一次性消费，并记录完整审计事件。
- 拒绝、过期、取消、失败和成功都可以通过 task API / MCP task 工具查询。
- 相关单元测试、协议集成测试、竞态测试和 `go test ./...` 通过。

### 21.5 本阶段明确不做

- 动态 Web / WSGI / ASGI 应用托管。
- 手机推送、邮件、Slack 等主动审批通知。
- 多租户权限系统和复杂组织角色模型。
- 跨节点审批协调和分布式任务恢复。
- 无限期保持同步 HTTP 请求等待审批。
- 默认开放宽泛的“本次会话批准所有操作”授权。

【进度备注】截至 2026-06-29，Step 21 的可用闭环与安全收尾已经完成以下部分：

**21A：统一提交与 gated task**

- 当前统一入口采用 `App.SubmitToolTask`，没有额外创建 `internal/execution` 包；REST `/tasks`、工具 HTTP、MCP stdio、MCP Go、session `exec_command` 和专用 HTTP MCP Runtime 均通过该入口做审批决策。
- 专用 `POST /mcp/tools/execute` 已删除部署专用 `requiresApproval` 预判，统一支持 `allow`、`deny`、`require_approval`，并使用稳定的 `policy_denied`、`approval_required`、`task_pending` 返回语义。
- 需要审批时立即创建 `awaiting_approval` gated task，返回 `approval_id`、`approval_url`、`task_id` 和 `expires_at`；批准后释放原 gate，不要求调用方重放工具。
- `InvokeContext.Authorized` 用作统一提交后的可信内部标记，避免直接 Registry 调用绕过部署工具保护，同时允许显式 `allow` 规则正常执行。
- 已补 gated task 独立测试：批准前不执行、并发重复 release 只执行一次、取消后不执行、release/cancel 竞态最多执行一次。
- 测试发现并修复“先取消、后 release 时 select 误选 gate 导致执行”的竞态；gate 释放后会再次检查 context 和任务状态。

**21B：审批后台、安全展示与重启语义**

- 已实现审批列表、详情、批准、拒绝、取消和 `/approval-ui/{id}` 页面；`approval_url` 只由独立 `approval.base_url` 生成，支持反向代理路径前缀。
- 浏览器审批 POST 要求 Origin 的 scheme/host 与 `approval.base_url` 精确匹配，并拒绝 `Sec-Fetch-Site: cross-site`；CLI/API 无 Origin 请求保持兼容。
- 浏览器批准、拒绝和取消后返回友好 HTML 结果页；JSON API 调用保持 `202` JSON 响应。
- approval args 与 audit details 在落盘和查询展示时递归脱敏 token/password/secret/authorization/cookie/private_key 等字段；参数摘要仍基于原始参数计算。
- `approved`、`rejected`、`canceled`、`expired` 均写审计事件。
- 服务启动时扫描遗留 pending approval：已超时的标记为 expired，其余标记为 canceled（actor=`system:restart`）；关联 `awaiting_approval` task 标记为 canceled，`error_code=approval_restart`。当前明确不尝试跨重启恢复内存 gate。
- 审批 actor 已改为服务端 principal：Bearer 鉴权成功记录 `http-bearer`/`bearer`，未启用鉴权记录 `local-unauthenticated`/`none`；客户端 `X-LocalGate-Actor` 不再参与决策或审计。
- 批准前已强校验 approval/task 的 approval ID、task ID、workspace、requester、tool、args digest 和 `awaiting_approval` 状态；失败返回 `approval_binding_mismatch` 并记录 `approval_validation_failed`。
- task 已区分 `approval_rejected`、`approval_expired`、`approval_canceled`、`approval_restart`、`approval_release_failed`，REST 与 MCP task 结果保留这些错误码。

**21C：模型与协议文档**

- startup prompt、`localgate-mcp-tools` skill、`localgate-http-api` skill、HTTP MCP Runtime 文档和 API 命令集均已同步 `pending_approval`、不重放原调用、批准后查询原 `task_id` 的统一约定。
- 文档已补浏览器 Origin 规则、HTML/JSON 响应差异、敏感字段脱敏和重启取消语义。

**21D：当前规则能力**

- 已实现有序、首条命中的工具级规则：`allow`、`deny`、`require_approval`，支持 `id`、`tools` 和 `risk`；`localgate.yaml` 已加入正式示例。
- 保留 `deployments.static.require_approval` 和 workspace `approval_policy.commands/write_paths` 作为无全局规则命中时的兼容兜底。
- 尚未实现基于规范化 facts 的 `when` 条件、policy version、幂等键、pending 去重和有限范围 grant；这些仍属于后续增强，不应标记为 Step 21D 全量完成。

**验收**

- `env -u LOCALGATE_HTTP_BEARER_TOKEN go test ./...` 通过。
- `go vet ./...` 通过。
- `git diff --check` 通过。
- `TestInvokeHandlerLongRunningDefaultWaitIsNotError` 已改为取消后等待 task 进入终态，连续运行 10 次通过，消除测试临时目录清理竞态。

**仍需后续加固**

- approval 状态落盘与内存 gate release 仍是两个步骤；若要提供更强的崩溃一致性，需要引入 consuming/consumed 状态或可恢复执行协调器。
- exactly-once、半创建/孤儿修复、审批查询分页、条件规则、幂等去重、有限范围 grant、跨重启恢复和多进程事务存储已登记到 `todo.md`。

---
## 建议的节奏控制（保证每步 500–1000 LOC）

- 每步都坚持“只引入一个新能力面”：例如 Step 09 只做同步 runner，不在同一步加 cancel/timeout 硬终止。
- 每步都交付一个可运行的最小端点/CLI：哪怕只有一个 demo 命令，也保证功能可验证。
- 每步都补最小测试：优先覆盖安全边界与任务控制，避免后期返工。
