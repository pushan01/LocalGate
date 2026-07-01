# LocalGate 工作规划（Working Plan）

目标：前期优先实现“给现有 Agent 系统赋予本地执行能力（手和脚）”，让任意 Agent 通过标准协议稳定调用本地文件与命令执行；后期分阶段增强安全治理（审批、审计、沙箱等）。

本规划假设代码尚未落地，内容以“模块划分 + 里程碑交付物 + 验收标准 + 开发顺序”为主，便于直接拆分任务进入开发。

---

## 1. 总体原则

- 先打通闭环：以可工作的最小执行网关为先，优先“可用 + 可集成 + 可观测”，安全能力逐步迭代。
- 内核与协议解耦：执行运行时与能力层保持稳定；MCP/HTTP/Tool schema 只是适配层，随生态变化可替换。
- 工具原子化：Filesystem、Shell 等能力以“原子工具（tool）”提供；更高层任务由 Agent/上层框架编排。
- 默认安全底座：MVP 也必须有最低限度的边界（路径沙箱、命令限制、超时/取消），避免“裸奔执行”。

---

## 2. 模块划分（建议的工程边界）

### 2.1 Core：执行内核（必须先做，所有入口复用）

**A. Tool Registry（工具注册表）**
- 维护工具清单、参数 schema（JSON Schema 或等价定义）、版本与描述
- 支持按名称调用：`invoke(tool_name, args, ctx)`

**B. Execution Runtime（执行运行时）**
- Task 模型：`task_id`、状态机、开始/结束时间、错误、产物（artifacts）
- 调度与并发：队列、并发上限（全局/按工具）
- 取消与超时：可中断的执行协议（best-effort）+ 强制终止兜底

**C. Event & Log Pipeline（事件与日志管道）**
- 统一事件：`TaskCreated/Started/ToolInvoked/Stdout/Stderr/Completed/Failed/Canceled/Timeout`
- 结构化日志：可按 `task_id` 查询、可流式订阅（用于 UI/Agent）

**D. Persistence（可选但建议尽早）**
- 最小存储：任务元信息 + 日志索引（可先用本地文件/SQLite）
- 为后续审计与回放铺路

### 2.2 Capability：能力实现（MVP 只做 File + Shell）

**A. Filesystem Tools**
- `read_file(path)`
- `write_file(path, content, mode=overwrite|append)`
- `list_directory(path, recursive=false)`
- `search_files(root, pattern, glob, max_results)`

**B. Shell Tools**
- `run_command(command, cwd, env?, timeout?, stream_logs=true)`
- 支持 stdout/stderr 流式输出、exit code、signal、执行耗时

### 2.3 Security（MVP 安全底座 + 后期增强）

**MVP 必备（Phase 0/1）**
- Path Sandbox：允许根目录白名单（workspace roots）、拒绝目录黑名单、符号链接策略
- Command Policy（轻量）：命令 allowlist（或按前缀/模板）+ 禁止高危关键字（如 `rm -rf /`）的基础规则
- Resource Limits：超时、并发上限、输出大小上限（防止日志爆炸）

**后期增强（Phase 2+）**
- Approval Flow：策略触发用户确认（写入敏感路径、执行高危命令、网络访问等）
- Audit Trail：谁请求、谁批准、执行了什么、结果如何（可导出）
- Sandbox：Docker/VM/受限用户权限/系统调用拦截（按平台能力择优）

### 2.4 Protocol Adapters：接入层（MCP 优先 + HTTP fallback）

**A. MCP Server Adapter（优先）**
- 将 MCP tool 调用映射到 Core 的 `invoke`
- 支持工具列表、schema、错误规范化、日志流（按 MCP 能力选择）

**B. HTTP API Adapter（通用）**
- 任务式 API（异步）：创建任务、查询状态、取消、拉取/订阅日志
- 直接工具调用 API（可选）：同步/异步调用工具

**C. Tool Schema Export（生态适配）**
- OpenAI / Anthropic / Generic JSON 的工具描述导出（由 Registry 生成）

### 2.5 Packaging：交付形态

- CLI：本地启动/停止、配置、健康检查、调试调用
- Config：workspace roots、policy、并发、日志存储位置
- 可选：系统服务化（launchd/systemd）与桌面端壳（后期）

---

## 3. 开发顺序与里程碑（可直接拆分任务）

### Milestone 0：项目基建与最小骨架（目标：能跑一个空服务）

**交付物**
- 代码结构：`core/`、`capabilities/`、`security/`、`adapters/`、`cli/`、`tests/`
- 配置系统：支持读取本地配置文件 + 环境变量覆盖
- 统一错误模型：错误码、可读消息、可追踪字段（task_id/tool_name）
- 最小健康检查：`healthz` 或等价能力

**验收标准**
- 本地启动成功，输出版本与配置摘要（不包含敏感信息）

---

### Milestone 1：Core Runtime（目标：任务生命周期 + 日志事件闭环）

**交付物**
- Task 状态机：`queued -> running -> succeeded/failed/canceled/timeout`
- `create_task()`、`get_task()`、`cancel_task()`、`list_tasks()`（可选）
- 事件模型与日志写入：按 `task_id` 聚合
- 并发与超时框架（先实现全局级别）

**验收标准**
- 能创建一个“空任务”并完整走通状态流转与事件记录

---

### Milestone 2：Filesystem 能力（目标：Agent 能安全读写项目文件）

**交付物**
- `read_file/write_file/list_directory/search_files` 工具实现
- Path Sandbox（MVP）：workspace roots 白名单 + denylist + symlink 策略
- 对大文件/二进制的策略：大小上限、编码约束、错误返回

**验收标准**
- 在允许目录内：读写/遍历/搜索可用
- 在禁止目录内：明确拒绝，返回一致错误码

---

### Milestone 3：Shell 执行能力（目标：稳定运行命令并可流式看日志）

**交付物**
- `run_command`：支持 cwd、env（受控）、timeout
- stdout/stderr 流式事件写入（与 Core 日志管道对齐）
- 进程终止：取消任务时终止子进程（含子进程组兜底）
- 输出/资源限制：最大输出字节数、最大执行时长

**验收标准**
- 能运行 `test`/`build` 等常规命令并实时看到日志
- 取消任务可在可接受时间内停止命令执行并进入 `canceled`

---

### Milestone 4：HTTP Adapter（目标：任何客户端都能接入）

建议采用“任务式 API”，因为更适合长耗时与流式日志。

**交付物（最小集合）**
- `POST /tasks`：创建任务（指定 tool + args 或 action 列表）
- `GET /tasks/{id}`：状态与结果
- `POST /tasks/{id}/cancel`：取消
- `GET /tasks/{id}/logs`：拉取日志（分页/offset）
- `GET /tasks/{id}/events/stream`：流式事件（SSE 或 WebSocket 二选一）

**验收标准**
- curl 能创建任务、看到流式日志、拿到最终 exit code/错误信息

---

### Milestone 5：MCP Adapter（目标：主流 Agent 框架“一键接入”）

**交付物**
- MCP server：列出工具、schema、处理 tool invocation
- 将 MCP 调用映射到 Core：复用 Task 与日志能力
- 错误与日志格式对齐：让 Agent 能理解失败原因与可恢复建议

**验收标准**
- 任意 MCP 客户端可调用 `read_file/write_file/run_command` 并成功返回

---

### Milestone 6：Killer Demo（目标：可对外演示的端到端闭环）

**Demo 形态（建议）**
- 场景：对一个可复现失败的测试用例，自动定位并修复，最终测试通过
- 流程：`search_files -> read_file -> write_file -> run_command(test) -> 输出 diff + 报告`

**验收标准**
- Demo 可重复运行，成功率高；失败时给出可诊断日志
- 输出包含：修改文件列表、diff、测试结果、耗时、task_id

---

## 4. 安全能力迭代规划（后期分阶段增强）

### Phase 1（可用性 + 基础安全）
- 更细粒度并发控制（按工具/按工作区）
- 更严格的 command policy（模板化、参数白名单）
- 配置热更新（可选）：变更 policy 不需重启

### Phase 2（审批与审计）
- Approval：策略触发、交互式批准/拒绝、可配置“自动批准窗口”
- Audit Trail：请求者身份、批准者身份、变更与执行结果的不可抵赖记录（至少可导出）

### Phase 3（隔离与沙箱）
- 运行在受限用户权限/容器内
- 文件系统隔离（只挂载允许目录）
- 网络访问策略（按需开启、白名单）

---

## 5. 配置与策略建议（MVP 的可落地配置面）

配置建议应该放在工作规划里，但建议只保留“对外契约级别”的内容：明确有哪些配置域、各自控制什么、安全边界在哪里；具体 YAML 示例与逐步落地细节放在执行计划里，避免工作规划被实现细节淹没。

**A. Server（服务级）**
- `bind`: 监听地址
- `max_concurrency`: 全局并发
- `default_timeout_ms`: 默认超时
- `max_output_bytes`: 单任务最大输出
- `log_store`: 日志/事件存储位置与保留策略

**B. Workspace（工作区级，推荐作为一等配置域）**
- 目的：把不同项目/目录隔离成不同策略域，避免“单一全局 allowlist/denylist”难以维护。
- `workspaces[].id`: 稳定标识（由调用方选择/传入）
- `workspaces[].roots`: 允许访问的根目录列表（Path Sandbox allowlist）
- `workspaces[].path_denylist`: 禁止访问路径（敏感目录、用户隐私目录等）
- `workspaces[].command_allowlist`: 允许命令（或允许前缀/模板）
- `workspaces[].cwd`: 默认工作目录（`run_command` 默认 cwd）
- `workspaces[].env_allowlist`: 允许透传的环境变量名（避免泄露）

**C. Security Defaults（默认安全底座）**
- `follow_symlink`: 符号链接策略（默认建议禁用或严格校验）
- `deny_rules`: 高危命令/参数的拒绝规则集合（可配置可扩展）

---

## 6. 依赖关系（决定开发顺序）

- Capability（File/Shell）依赖：Security（path/command 限制）与 Core（日志/任务）
- MCP/HTTP Adapter 依赖：Core Runtime（统一 task/log 模型）与 Tool Registry
- Approval/Audit 依赖：Persistence（至少能保存审计事件）
- Sandbox 依赖：部署形态与平台能力（容器/权限/系统接口）

因此推荐顺序：**Core Runtime → Filesystem → Shell → HTTP → MCP → Demo → 安全增强（approval/audit/sandbox）**

---

## 7. 验收清单（MVP Definition of Done）

- 任意客户端可通过 MCP 或 HTTP 调用本地文件与命令执行能力
- 任务可取消、可超时、可查询状态
- 日志可流式订阅，且可回查（至少在本地持久化一段时间）
- 默认具备最低安全边界：路径沙箱 + 基础命令限制 + 资源限制
- Killer demo 可稳定复现成功闭环（失败可诊断）
