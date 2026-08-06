# Plan

## CH-NOTIFY-002：收敛系统通知触发策略

- **状态**：已完成
- **规划日期**：2026-08-03
- **开始日期**：2026-08-03
- **完成日期**：2026-08-03
- **优先级**：P1
- **目标**：审批仅在待处理、拒绝、取消和过期时发送 Bark 系统通知；批准不发送；任何工具 Task 失败或超时均不发送系统通知。
- **非目标**：不改变模型主动调用 `send_notification` 的行为，不取消动态部署监管自身的错误通知，不调整 Bark 传输、加密或配置结构。
- **涉及范围**：`internal/app` 通知事件筛选、相关单元测试和 README。
- **预计规模**：约 30–80 行有效 diff。
- **验收条件**：
  - pending、rejected、canceled、expired 审批状态会进入通知发送路径。
  - approved 审批状态不会进入通知发送路径。
  - `EventTaskFailed` 和 `EventTaskTimeout` 不再挂接 Bark 系统通知观察器。
  - 动态部署错误通知和 `send_notification` 工具保持原行为。
- **验证方式**：`go test ./internal/app ./internal/approval`、`go test ./...`、`go vet ./...`、`git diff --check`。
- **实际实现**：App 的审批观察器增加状态筛选，仅允许 pending、rejected、canceled 和 expired；删除 Task failed/timeout 的 Bark 观察器及相关消息构造逻辑；同步更新 README 和单元测试。
- **验证结果**：定向测试、`go test ./...`、`go vet ./...` 和 `git diff --check` 均通过。

## CH-SEC-001：工具与 Session 安全加固

- **状态**：已完成
- **规划日期**：2026-07-30
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **优先级**：P0
- **目标**：修复文件和执行工具中已确认的环境隔离、Shell 信任、Session 所有权、远程上传、终端游标和文件 I/O 一致性问题，并把已复现场景固化为回归测试。
- **非目标**：本阶段不新增 Files 终端 UI、不接入 SSE / WebSocket、不扩展命令策略语法，也不重构审批状态机。
- **涉及范围**：`internal/capabilities/shell`、`internal/core/session_manager.go`、`internal/capabilities/filesystem`、`internal/httpapi`、相关安全辅助模块、测试和文档。
- **依赖与风险**：该阶段修改命令执行、Session 授权和文件写入等核心安全边界，必须按高风险变更验证。`CH-FB-004` 在本阶段完成前保持阻塞，避免 Files 终端 UI 暴露现有缺陷。
- **实施顺序**：
  1. `CH-SEC-001A`：执行环境与可信 Shell。
  2. `CH-SEC-001B`：Session 所有权、游标与输出可靠性。
  3. `CH-SEC-001C`：`upload_file` SSRF 防护。
  4. `CH-SEC-001D`：原子写入、权限保留与 Patch 事务语义。
  5. `CH-SEC-001E`：读取和目录遍历资源限制。
- **总体验收条件**：
  - 命令子进程只能获得 workspace `env_allowlist` 允许的环境变量，调用方省略 `env` 时也不得继承 LocalGate 全部进程环境。
  - `exec_command.shell` 只能使用明确允许的可信真实路径，不能通过 workspace 内伪造的 `sh`、`bash` 或 `zsh` 绕过命令策略。
  - Session 的读取、stdin、关闭和所有 `/sessions/*` 路由统一校验 workspace 所有权，并保留 requester 和调用关联信息。
  - 终端输出使用客户端持有的单调递增 cursor；缓冲裁剪不会重复或跳过输出，cursor 过期和截断有明确响应。
  - 超长单行和底层读取错误不会被静默丢弃；输出采用受限字节块或等价的可靠分片机制。
  - `upload_file` 拒绝 loopback、private、link-local、multicast 和 unspecified 目标，重定向后重新校验，并具有完整超时。
  - `write_file` 和 `/system/uploads` 使用不会在失败时截断原文件的原子写入流程。
  - `apply_patch` 更新现有文件时保留权限位，并准确描述多文件提交在崩溃和多进程场景下的保证。
  - `read_file`、`list_directory` 和 `search_files` 具有文件类型、大小、数量、取消和错误传播保护。
  - 所有已确认问题均有永久回归测试，相关并发路径通过 race 测试。
- **总体验证方式**：各子 Change 定向回归测试、相关包 `go test -race`、`git diff --check`、`go test ./...`。
- **总体结果**：五个子 Change 均已完成。命令环境、Session 授权和 cursor、远程上传网络边界、文件提交一致性以及读取/遍历资源限制均已落地，并同步更新 README、HTTP/MCP Skills 和 API 示例文档。
- **最终验证结果**：`go test ./...`、`go vet ./...`、相关包竞态测试、`web/admin` 的 `npm run check` 和 `git diff --check` 均通过。
- **保留边界**：普通文件工具已显著缩小路径替换窗口并拒绝已检测到的并发目标变化，但完整的 descriptor-relative 文件访问仍作为后续 Unix 专项；`apply_patch` 提供进程内回滚，不承诺崩溃原子性或多进程事务。

### CH-SEC-001A：执行环境与可信 Shell

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **预计规模**：约 250–450 行有效 diff。
- **范围**：统一构造 `run_command`、`run_argv`、`exec_command` 的子进程环境；限制 Shell 为配置允许的 canonical path；处理符号链接、`PATH`、`HOME`、`TMPDIR` 和 login shell 行为。
- **验收**：环境变量泄漏和伪造 Shell 绕过的复现测试在修复前失败、修复后通过；现有命令 allowlist / denylist 测试保持通过。
- **实际实现**：三个命令入口始终显式构造 allowlist 环境；环境输出稳定排序；交互 Session 只接受解析后等同于系统 `sh`、`bash` 或 `zsh` 的绝对路径，拒绝 workspace 内同名伪造程序。
- **验证结果**：`go test ./internal/capabilities/shell` 通过，覆盖未授权环境变量不泄漏、allowlist `PATH` 保留和伪造 Shell 拒绝。

### CH-SEC-001B：Session 所有权、游标与输出可靠性

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **预计规模**：约 350–650 行有效 diff。
- **范围**：为 Session 读、写、关闭增加 workspace 和调用主体约束；保存 requester、transport 和请求关联元数据；使用绝对 cursor 与 retained-buffer base offset；替换会静默丢失长行的 Scanner 路径。
- **验收**：跨 workspace Session 访问被拒绝；分页不会重复第一页；缓冲裁剪不跳过未读输出；超长单行和读取错误有明确输出或截断状态。
- **实际实现**：Session 保存 Task、requester、transport 和 MCP 请求关联信息；工具与四类 `/sessions/*` 路由统一校验 workspace；输出改用绝对 cursor、`base_offset`、`cursor_expired` 和 `truncated`；终端读取改为 32 KiB 受限分片，并显式报告底层读取错误。
- **验证结果**：Core、Shell、HTTP API 定向测试通过，覆盖跨 workspace 403、分页不重复、缓冲裁剪、2 MiB 单行和读取错误保留。

### CH-SEC-001C：远程上传 SSRF 防护

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **预计规模**：约 250–450 行有效 diff。
- **范围**：为 `upload_file` 引入专用 HTTP Client、连接和请求超时、目标 IP 策略、重定向复检以及 IPv4 / IPv6 覆盖。
- **验收**：loopback、RFC1918/private、link-local、multicast、unspecified 和重定向到内网均被拒绝；允许的公网下载和大小上限仍正常工作。
- **实际实现**：`upload_file` 使用无代理专用 HTTP Client、总请求/连接/TLS/响应头超时、DNS 与字面 IP 校验、重定向复检和拨号前再次解析；任一 DNS 结果为非公网地址时整体拒绝。
- **验证结果**：`go test ./internal/capabilities/filesystem` 通过，覆盖 IPv4/IPv6 内网类别、混合 DNS、loopback 上传和重定向目标复检。

### CH-SEC-001D：原子写入、权限与 Patch 事务语义

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **预计规模**：约 350–700 行有效 diff。
- **范围**：抽取统一原子文件写入流程供 `write_file`、上传端点和文件浏览器复用；保留已有文件权限；缩小 symlink / 路径替换 TOCTOU；修正 `apply_patch` 权限和事务描述。
- **验收**：失败写入不截断旧内容；Patch 后可执行位保持；并发创建、rename 失败、rollback 失败和磁盘写入失败均有测试和明确行为。
- **实际实现**：新增 `internal/fileio` 同目录临时文件、同步落盘、权限保留、目标身份复检和无覆盖创建流程；`write_file`、`upload_file`、`/system/uploads` 与文件浏览器共用该实现。`apply_patch` 保留更新目标权限、拒绝并发替换与并发创建覆盖、汇总 rollback 错误，并把事务保证准确描述为进程内 best-effort rollback。
- **验证结果**：File I/O、Filesystem、HTTP Upload、File Browser 和 Patch 定向测试通过，覆盖失败源读取保留旧文件、可执行位、并发创建与目标替换语义。

### CH-SEC-001E：读取与目录遍历资源限制

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **预计规模**：约 200–400 行有效 diff。
- **范围**：为 `read_file` 增加普通文件、大小和二进制策略；为递归列表和搜索增加最大结果数、glob 校验、context cancellation 和遍历错误传播。
- **验收**：超大文件、目录、特殊文件、非法 glob、权限错误、取消和结果截断均返回明确结果，不再静默返回不完整成功。
- **实际实现**：`read_file` 限制为最大 8 MiB 的普通 UTF-8 文本文件；`list_directory` 与 `search_files` 增加默认/硬上限、显式 `truncated`、非法 glob 拒绝、context cancellation、普通文件过滤和遍历错误传播。
- **验证结果**：Filesystem 定向测试通过，覆盖目录、二进制、超限文件、取消、非法 glob、结果裁剪和权限错误传播。

## CH-FB-004：终端与任务联动

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **优先级**：P1
- **目标**：让 Admin Files 页面能从当前 workspace/root/目录启动受控 Session，持续读取终端输出、发送 stdin 和关闭 Session，并在命令需要审批时保留 Files 上下文、展示关联 Task / Approval 状态和返回入口。
- **非目标**：本轮不增加 SSE / WebSocket、不实现多终端标签、不持久化终端到浏览器重启后、不增加 Git/LSP/Monaco，也不绕过现有 `exec_command` 审批和命令策略。
- **涉及范围**：`internal/filebrowser` 目录解析、`internal/adminapi` 终端 API、`web/admin/src` 终端状态与 UI、Admin 构建产物、测试和文件浏览器文档。
- **依赖与风险**：必须复用已完成的 `CH-SEC-001` Session workspace 校验与绝对 cursor；Admin 与控制面监听端口分离，因此浏览器不能直接调用 `/sessions/*`，需要 Admin 同源代理接口。终端轮询不得重渲染或覆盖编辑器中的未保存内容。
- **预计规模**：约 650–1000 行有效 diff；若超过护栏，实时流、多终端和持久化继续拆到后续 Change。
- **验收条件**：
  - Files 当前目录可作为经过 file-browser sandbox 校验的绝对 workdir 启动 `exec_command` Task。
  - 启动、stdin 和 close 使用 Admin 同源 API，统一执行 Origin、workspace 和 Session 所有权校验。
  - 终端使用绝对 cursor 增量轮询；输出不重复，cursor 过期和截断有明确 UI 提示。
  - 启动操作需要审批时，Files 展示 Task / Approval 状态；批准、拒绝或取消后可返回原 workspace/root/path 与终端 Task 上下文。
  - Task 成功产生 `session_id` 后自动接入终端；失败、取消和超时显示可操作错误，并提供 Task 详情入口。
  - 终端轮询和输入不会导致 Files 页面整体重渲染，不丢失打开标签或未保存内容。
  - 后端、前端状态模块、生成资源和全量测试均通过。
- **验证方式**：Admin API 和 File Browser 定向 Go 测试、终端状态 Node 单元测试、`npm run check`、相关包 race 测试、`git diff --check`、`go test ./...`。
- **实际实现**：
  - 新增 Admin 同源 `/api/admin/v1/file-browser/terminals` 接口，支持从 file-browser sandbox 校验后的当前目录创建审批型 `exec_command` Task，以及按 workspace 读取、stdin 和关闭 Session。
  - Files URL 使用 `terminal_task`、`terminal_session`、`terminal_path` 保存关联；Task 成功后自动取得 `session_id`，终端面板使用绝对 cursor 增量轮询并显示 cursor 过期与截断提示。
  - 新增独立 `FileTerminalState` 前端状态模块，按 offset 去重输出并限制内存保留；轮询只更新终端 DOM，不重渲染文件编辑器。
  - 审批页面支持经过同源 `/admin` 校验的 `return_to`；Files 发起的终端和文件变更审批完成后可返回原 workspace/root/path，并保留内存中的 dirty 标签。
  - 修复显式关闭 Session 后进程退出回调把 `closed` 覆盖成 `failed` 的竞态。
- **验证结果**：
  - Admin API、File Browser、Core 和 Admin 资源定向测试通过，覆盖当前目录 workdir、输出读取、stdin、关闭、跨 workspace 拒绝、审批 Task 绑定和 Origin 校验。
  - Node 终端状态与文件标签共 10 个测试通过；Admin `dist` 连续两次构建 SHA-256 一致。
  - 无头 Chrome 普通终端冒烟通过：未保存编辑内容在 Task/Session 轮询期间保持不变，终端输出、状态和 Task 链接正确。
  - 无头 Chrome 审批往返冒烟通过：`return_to` 带回原 Files 和 terminal Task 上下文，批准后自动接入 Session，dirty 编辑内容保持不变。
  - `go test ./...`、`go vet ./...`、`go test -race ./internal/core ./internal/capabilities/shell ./internal/adminapi ./internal/filebrowser ./internal/app` 和 `git diff --check` 均通过。
- **范围说明**：工作树同时包含尚未提交的 `CH-FB-003` 和 `CH-SEC-001` 改动，因此未伪造单独的实际 diff 行数；实时 SSE/WebSocket、多终端标签和浏览器重启后的完整状态持久化已拆出后续事项。

## CH-FB-005A：只读 Git status / diff

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-30
- **优先级**：P1
- **目标**：在 Files 当前目录中展示受控的 Git 分支、ahead/behind、工作区状态以及 staged / unstaged diff，并允许从状态项返回对应文件。
- **非目标**：本轮不提供 stage、commit、checkout、reset、clean、pull、push 等写操作，不实现实时终端流、多终端、code-server、LSP 或 Monaco。
- **涉及范围**：`internal/filebrowser` 只读 Git 服务、`internal/adminapi` 同源查询接口、Files Git 面板、前端构建产物、测试和文档。
- **安全边界**：worktree 根、Git dir 和 common dir 必须位于选中的 file-browser root 内；Git 使用受限环境和固定参数，禁用 pager、external diff、textconv、fsmonitor、可选锁和用户/系统配置；命令有超时、输出大小及状态项数量上限。
- **验收条件**：
  - 当前目录位于 Git 仓库内时返回分支、upstream、ahead/behind 和 porcelain v2 状态；非仓库返回明确状态而不是 500。
  - 仓库根位于选中 root 之外时拒绝，不能借由父仓库读取 root 外信息。
  - staged 与 unstaged diff 可按当前目录或单文件查询，路径继续经过 file-browser sandbox 校验。
  - 仓库配置中的 external diff、textconv、pager 和 fsmonitor 不会执行。
  - 大输出、超时和 context cancellation 返回明确错误，不静默截断为完整成功。
  - Files Git 面板展示状态、刷新、工作区/staged diff 和打开文件入口，不破坏现有标签与 dirty 状态。
  - 定向 Go/Node 测试、Admin 构建、全量测试、vet、相关 race 测试和 diff 校验通过。
- **验证方式**：`go test ./internal/filebrowser ./internal/adminapi ./web/admin`、前端 Node 测试与 `npm run check`、无头浏览器冒烟、`go test -race ./internal/filebrowser ./internal/adminapi`、`go test ./...`、`go vet ./...`、`git diff --check`。
- **实际实现**：
  - 新增 `internal/filebrowser` 只读 Git 服务，使用 porcelain v2 `-z` 解析分支、upstream、ahead/behind、普通、rename/copy、conflict 和 untracked 状态，并将结果统一转换为 file-browser root 相对路径。
  - staged / unstaged diff 支持当前目录、现存文件和已删除文件；删除目标仅在现存父目录通过 sandbox 校验后生成 pathspec，不放宽通用文件解析规则。
  - Git worktree 根、Git dir 和 common dir 都必须位于所选 root 内；父仓库、外置 `.git` 和 linked worktree metadata 越界均拒绝。
  - Git 只从受信任系统目录解析，使用最小环境，禁用 pager、external diff、textconv、fsmonitor、optional locks 以及用户/系统配置；命令限制 10 秒，status 4 MiB/5000 项，diff 2 MiB。
  - 新增 Admin 同源 status / diff API 和 Files Git 面板，支持刷新、当前目录或单文件 working/staged diff、状态项打开文件；Git 面板状态独立于文件标签，导航不丢失 dirty 编辑内容。
- **验证结果**：
  - File Browser 测试覆盖 staged/unstaged、已删除文件、目录作用域、嵌套仓库路径、非仓库、父仓库越界、外置 Git metadata、external diff/fsmonitor 禁用、取消、rename 解析和输出上限。
  - Admin API 测试覆盖 status、diff、非仓库和参数校验；前端 Git 状态模块与既有文件/终端模块共 13 个 Node 测试通过。
  - 无头 Chrome 真实工作树冒烟通过：显示 `plan.md` working diff，打开 `internal/filebrowser/git.go` 后原 `plan.md` dirty 标签仍保留，未发现运行时异常。
  - Admin `dist` 连续两次构建 SHA-256 一致。
  - `go test ./...`、`go vet ./...`、`go test -race ./internal/filebrowser ./internal/adminapi ./web/admin`、`npm run check` 和 `git diff --check` 均通过。

下一项：独立评估实时终端流、多终端 UX 和 code-server private dynamic deployment 模板。

## CH-FB-005B：实时终端流

- **状态**：已完成
- **开始日期**：2026-07-30
- **完成日期**：2026-07-31
- **优先级**：P1
- **目标**：为 Files 当前终端增加 Admin 同源 SSE 输出流，在输出或 Session 状态变化时实时更新 UI，并在流不可用或中断时自动回退现有绝对 cursor 轮询。
- **非目标**：本轮不实现多终端标签、浏览器重启后的完整终端恢复、WebSocket、终端历史持久化，也不改变 `exec_command`、Task、Approval 或 Session 的权限策略。
- **涉及范围**：`internal/core` Session 变更订阅、`internal/adminapi` SSE 代理、`web/admin/src` 流解析与终端更新、生成资源、测试和文档。
- **安全边界**：SSE 继续要求 Admin 鉴权并按 workspace 校验 Session 所有权；浏览器使用可附加 Bearer Header 的 `fetch` 流而非原生 `EventSource`；连接随页面、终端或请求 context 取消，不允许无限遗留 goroutine。
- **验收条件**：
  - `GET /api/admin/v1/file-browser/terminals/{id}/stream` 使用 SSE 返回初始 Session 快照，并在输出、错误或状态变化时推送后续事件。
  - SSE 使用绝对 cursor；cursor 过期、base offset、截断和 output error 与轮询接口保持同一契约。
  - Session 结束且缓冲输出追平后发送最终状态并关闭流；客户端断开会及时释放订阅。
  - 跨 workspace 访问拒绝，未知 Session、非法 cursor 和不支持 flush 的响应有明确错误。
  - Files 默认使用带 Admin Authorization Header 的 fetch SSE；连接失败或意外中断时显示降级提示并继续轮询，不丢失或重复输出。
  - 离开 Files、重置终端或切换 Session 时取消旧流；终端更新不重渲染编辑器，不影响 dirty 标签。
  - Core/Admin/前端定向测试、全量测试、vet、相关 race 测试、重复构建和浏览器冒烟通过。
- **验证方式**：`go test ./internal/core ./internal/adminapi ./web/admin`、`npm run check`、无头浏览器 SSE 与轮询降级冒烟、`go test -race ./internal/core ./internal/adminapi ./web/admin`、`go test ./...`、`go vet ./...`、`git diff --check`。
- **实现结果**：
  - SessionManager 新增按 workspace 授权的变更订阅；输出、状态、关闭和 `output_error` 变化会通知 watcher，取消与清理均幂等释放。
  - Admin 新增同源 SSE 终端流，发送初始快照、绝对 cursor 增量、状态/错误变化和心跳；最终输出追平后关闭连接。
  - Files 使用带 Admin Authorization Header 的 `fetch` 读取 SSE，解析分片和多行 data；流失败时显示原因并自动回退绝对 cursor 轮询。
  - 离开 Files、重置终端和切换 Session 会取消旧流；终端局部刷新不重建编辑器，不影响 dirty 标签、Git 面板或 Approval 返回上下文。
- **验证结果**：
  - `go test ./...`、`go vet ./...`、`go test -race ./internal/core ./internal/adminapi ./web/admin`、`npm run check` 和 `git diff --check` 均通过。
  - 前端 18 个 Node 测试通过，连续两次 Admin 构建 SHA-256 一致。
  - 无头 Chrome 冒烟通过：正常路径在命令结束前收到首段 SSE；阻断 stream 后显示降级提示，并由轮询在命令结束前收到首段输出、最终补齐全部内容。

下一项：独立设计 `CH-FB-005C` 多终端标签与浏览器重启恢复规则；code-server 模板继续后置。

## CH-FB-003：多标签编辑状态

- **状态**：已完成
- **开始日期**：2026-07-29
- **完成日期**：2026-07-29
- **目标**：让 Files 页面可以同时保留多个打开文件，每个标签独立维护内容、ETag、dirty、预览和冲突状态，并在切换标签时不丢失编辑内容。
- **非目标**：本轮不接入 Monaco、不持久化标签到浏览器重启后、不实现终端、Git、LSP 或跨标签批量保存。
- **涉及范围**：`web/admin/src/app.js`、新增文件标签状态模块及 Node 单元测试、Admin 样式、生成资源、资源测试和文件浏览器文档。
- **依赖与风险**：现有页面通过 URL 表示当前目录和文件；浏览器前进/后退、离开 Files、关闭 dirty 标签、重命名和删除必须与内存标签状态一致。审批跳转不能静默丢失其他 dirty 标签。
- **预计规模**：约 450–850 行有效 diff。
- **验收条件**：
  - 打开多个文件时显示标签栏，切换标签不会重新读取或丢失其他标签中的未保存内容。
  - 每个文本标签独立维护内容、保存基线、ETag、dirty、Markdown 预览和 ETag 冲突状态。
  - 关闭 dirty 标签和离开 Files 时有明确确认；关闭干净标签和 Files 内部导航不产生无关确认。
  - 浏览器前进/后退可以在 Files 标签和目录上下文间切换，离开 Files 时仍保护全部 dirty 标签。
  - 直接完成的文件重命名和删除会同步更新或关闭受影响标签；等待审批的操作保留标签状态。
  - 前端状态模块有独立 Node 单元测试，生成资源和现有 Go 测试继续通过。
- **验证方式**：`npm run check`、文件标签 Node 单元测试、`go test ./web/admin ./internal/adminapi ./internal/filebrowser`、重复构建产物哈希、`git diff --check`、`go test ./...`。
- **实际实现**：
  - 新增独立 `FileTabStore` 模块，按 workspace、root 和 path 管理标签；每个标签独立保存当前内容、保存基线、ETag、dirty、Markdown 预览模式和冲突状态。
  - Files 内部的文件、目录和浏览器历史导航会保留所有标签；离开 Files、刷新浏览器或关闭 dirty 标签时统一保护未保存内容。
  - 标签切换优先使用内存状态，不重复读取已打开文件；关闭活动标签后自动选择相邻标签。
  - 文件直接重命名或移动时同步更新匹配标签路径，文件或目录直接删除时关闭受影响标签；进入审批页面时保留标签状态，避免在审批前静默丢失编辑内容。
  - 新增水平滚动标签栏、活动和 dirty 标记、暗色模式样式，并将状态模块纳入前端构建产物与 Go embed 资源测试。
- **验证结果**：
  - `npm run check`：通过，6 个文件标签状态测试全部通过，源码和生成 JavaScript 语法检查通过。
  - 连续两次构建的 `app.js`、`styles.css`、`index.html` SHA-256 一致。
  - 无头 Chrome 交互冒烟通过：打开两个标签、修改、切换、dirty 保留、取消关闭和确认关闭均符合预期，未发现运行时异常。
  - `go test ./web/admin ./internal/adminapi ./internal/filebrowser`：通过。
  - `go test ./...`：通过。
  - `BUILD_ADMIN=1 OUTPUT=/tmp/localgate-ch-fb-003 bash scripts/build.sh`：通过。
  - `git diff --check`：通过。

后续：先完成 `CH-SEC-001` 工具与 Session 安全加固，再开始 `CH-FB-004` 终端与任务联动。

## CH-ADMIN-003：前端构建链与 Monaco 决策

- **状态**：已完成
- **开始日期**：2026-07-29
- **完成日期**：2026-07-29
- **目标**：为 Admin Console 建立最小、可重复的前端构建链，把可维护源码与 Go embed 使用的提交产物分离，并形成明确的 Monaco 引入决策。
- **非目标**：本轮不接入 Monaco、不改写为 TypeScript 或组件框架、不实现多标签页，也不改变 Admin API。
- **涉及范围**：`web/admin/src/`、`web/admin/dist/`、`web/admin/package.json`、锁文件、Admin 构建脚本、资源测试和相关文档。
- **依赖与风险**：Go 二进制构建和运行不能强制依赖 Node；`dist/` 必须继续提交并可直接 embed；构建输出必须稳定，不能破坏现有 CSP、路由和无鉴权资源加载方式。
- **预计规模**：约 150–350 行有效 diff，另包含现有前端资源从 `dist/` 到 `src/` 的机械复制。
- **验收条件**：
  - `web/admin/src/` 成为前端源码入口，`dist/` 明确为提交的生成产物。
  - 使用 npm 锁文件和固定版本构建工具，可通过单一命令重新生成 `dist/`。
  - 默认 Go 构建继续只消费已提交的 `dist/`，没有 Node 或网络时仍可编译 LocalGate。
  - 生成的 Admin 页面继续满足现有路由、CSP 和文件管理器资源测试。
  - 文档明确 Monaco 的体积、Worker、CSP、离线构建影响，以及当前是否接入的结论和后续启动条件。
- **验证方式**：`npm ci`、`npm run build`、重复构建后 `git diff --exit-code -- web/admin/dist`、`node --check web/admin/dist/app.js`、`go test ./web/admin`、`git diff --check`、`go test ./...`。
- **实际实现**：
  - 新增 `web/admin/src/` 作为可维护源码目录，`dist/` 继续作为提交并由 Go embed 消费的生成产物。
  - 使用固定版本的 esbuild、npm lockfile 和显式 install-script 许可建立可重复构建；新增根目录 `scripts/build-admin.sh`。
  - `scripts/build.sh` 仅在 `BUILD_ADMIN=1` 时重建前端，默认 Go 构建仍不依赖 Node 或网络。
  - Admin 资源测试增加构建输入检查，并解除对旧压缩 CSS 空格格式的脆弱依赖。
  - Monaco 当前暂缓：必须先完成多标签编辑状态模块化和编辑器 adapter，再验证同源 Worker、现有 CSP、离线打包、体积与启动成本；不接受 CDN、`unsafe-eval` 或默认放宽 CSP。
- **验证结果**：
  - `npm ci`、`npm run check`：通过，无已知 npm 漏洞。
  - 连续两次构建的 `app.js`、`styles.css`、`index.html` SHA-256 一致。
  - `go test ./...`：通过。
  - `BUILD_ADMIN=1 OUTPUT=/tmp/localgate-ch-admin-003 bash scripts/build.sh`：通过。
  - `git diff --check`：通过。

后续编辑器增强：增加编辑器 adapter，再评估 Monaco 的 Worker、CSP、体积和启动成本。

## CH-FB-002：预览与冲突处理

- **状态**：已完成
- **开始日期**：2026-07-29
- **完成日期**：2026-07-29
- **目标**：让 Files 页面可以安全预览 Markdown 和图片，并在 ETag 冲突时提供明确的 Reload、Overwrite 和 Diff 处理路径。
- **非目标**：本轮不做搜索、Monaco、多标签页、Git 集成或终端集成。
- **涉及范围**：`internal/filebrowser`、`internal/adminapi/files.go`、`web/admin/dist/app.js`、`web/admin/dist/styles.css`、相关测试和文件浏览器文档。
- **依赖与风险**：Markdown 预览必须避免注入风险；图片读取继续遵守 workspace、root、隐藏文件、exclude、文件大小和 symlink 安全边界；显式覆盖仍必须经过现有审批与审计策略。
- **预计规模**：约 400–750 行有效 diff。
- **验收条件**：
  - Markdown 文件可在编辑与安全预览之间切换。
  - 受支持的图片文件可在 Files 页面预览，无法预览时降级为下载。
  - ETag 冲突后提供 Reload、显式 Overwrite 和 Diff 入口，不只显示错误文字。
  - Overwrite 请求继续进入配置要求的审批任务，并保留审计记录。
  - 预览切换和冲突操作遵守现有未保存内容保护。
- **验证方式**：`node --check web/admin/dist/app.js`、文件浏览器与 Admin API 定向测试、`git diff --check`、`go test ./...`。
- **实际实现**：
  - 新增受限图片预览 API，只允许内容嗅探确认的 PNG、JPEG、GIF 和 WebP，并继续执行文件大小、workspace sandbox、exclude 和 symlink 校验。
  - Admin 前端使用带 Bearer Token 的 Blob 请求加载图片和下载文件，不依赖无法附加鉴权头的直接 `<img src>` 或地址跳转。
  - Markdown 预览使用内置受控渲染器，支持标题、列表、引用、代码块和基础强调；原始 HTML 全部转义。
  - ETag 冲突时读取服务器最新内容，保留本地编辑值并提供 Reload、显式 Overwrite 和并排逐行 Diff。
  - Overwrite 继续使用原有 `file_browser.write` 审批任务和成功审计，并增加对应审批流测试。
- **验证结果**：
  - `node --check web/admin/dist/app.js`：通过。
  - `go test ./internal/filebrowser ./internal/adminapi ./web/admin`：通过。
  - `git diff --check`：通过。
  - `go test ./...`：通过。
- **实际规模**：532 行有效 diff（490 行新增、42 行删除，包含计划与专项文档）。

后续顺序：`CH-ADMIN-003` 前端构建链与 Monaco 决策 → `CH-FB-003` 多标签状态 → `CH-SEC-001` 工具与 Session 安全加固 → `CH-FB-004` 终端与任务联动 → `CH-FB-005` 高级工作区能力。

## CH-ADMIN-002：可折叠管理侧栏

- **状态**：已完成
- **开始日期**：2026-07-29
- **完成日期**：2026-07-29
- **目标**：让桌面端管理侧栏可折叠为窄栏，释放 Files 等工作页面的横向空间，并记住用户选择。
- **非目标**：本轮不调整管理后台信息架构、不引入图标依赖、不重做移动端导航。
- **涉及范围**：`web/admin/dist/app.js`、`web/admin/dist/styles.css`、`web/admin/adminui_test.go`。
- **依赖与风险**：侧栏切换不得重新渲染页面，否则可能丢失文件编辑器中的未保存内容；移动端应忽略桌面折叠宽度。
- **预计规模**：约 60–140 行有效 diff。
- **验收条件**：
  - 桌面端可在完整侧栏和窄栏之间切换。
  - 折叠状态使用浏览器本地存储保留。
  - 切换侧栏时不触发页面重新渲染，不影响未保存文件内容。
  - 折叠后导航仍可通过缩写、标题和无障碍标签识别。
  - 小屏幕继续显示完整导航标签。
- **验证方式**：`node --check web/admin/dist/app.js`、`go test ./web/admin`、`git diff --check`、`go test ./...`。
- **实际实现**：
  - 桌面端侧栏可在 230px 完整模式和 72px 窄栏模式之间切换。
  - 折叠状态写入 `localStorage`，刷新和页面导航后继续保留。
  - 切换仅修改现有 DOM class、按钮状态和提示，不重新渲染页面，不影响文件编辑内容。
  - 窄栏使用字母缩写，并保留 `title`、`aria-label` 和展开状态信息。
  - 800px 以下忽略桌面折叠布局并显示完整标签，560px 以下自动换成三列导航。
- **验证结果**：
  - `node --check web/admin/dist/app.js`：通过。
  - `go test ./web/admin`：通过。
  - `git diff --check`：通过。
  - `go test ./...`：通过。
- **实际规模**：67 行产品代码与测试有效 diff，另有 29 行计划记录。

## CH-FB-001：文件编辑安全导航与目录操作

- **状态**：已完成
- **开始日期**：2026-07-29
- **完成日期**：2026-07-29
- **目标**：让 `/admin/files` 在存在未保存内容时阻止意外导航，并为目录和未打开文件提供重命名、删除入口。
- **非目标**：本轮不引入 Monaco、Node 构建链、多标签页、Diff 合并、文件搜索或终端面板。
- **涉及范围**：`web/admin/dist/app.js`、`web/admin/dist/styles.css`、`web/admin/adminui_test.go`、`todo.md`。
- **依赖与风险**：前端为无构建步骤的原生 JavaScript；浏览器前进/后退需要在 URL 已变化后恢复原文件页面。
- **预计规模**：约 150–300 行有效 diff。
- **验收条件**：
  - 修改文件后，侧栏导航、文件切换、目录切换、workspace/root 切换、Reload/Refresh 和浏览器关闭均有未保存保护。
  - 文件列表可直接重命名和删除文件或目录；目录删除使用递归确认参数。
  - 保存、审批跳转及已确认的丢弃操作不会重复弹出确认框。
  - 管理前端资源测试和 JavaScript 语法检查通过。
- **验证方式**：`node --check web/admin/dist/app.js`、`go test ./web/admin ./internal/adminapi ./internal/filebrowser`、`go test ./...`。
- **实际实现**：
  - 使用统一 dirty state 拦截侧栏、文件树、workspace/root、Reload/Refresh、浏览器后退和关闭。
  - 保存成功、进入审批或用户已确认丢弃时通过显式强制导航，避免重复确认。
  - 文件列表增加文件和目录的重命名、删除操作；目录删除带递归确认字段，目录改名后同步修正已选子文件路径。
  - 小屏幕始终展示列表操作，桌面端在 hover/focus 时展示。
- **验证结果**：
  - `node --check web/admin/dist/app.js`：通过。
  - `go test ./web/admin ./internal/adminapi ./internal/filebrowser`：通过。
  - `git diff --check`：通过。
  - `go test ./...`：通过。
- **实际规模**：169 行有效 diff。

## 本轮目标

将已经在 `LocalGate` 中实现的后台会话工具迁移到 `LocalGate.mcp`，包括：

- `exec_command`
- `write_stdin`
- `read_thread_terminal`

同时补齐会话内核、配置接线、HTTP/MCP 接口和测试，确保 `LocalGate.mcp` 可以独立工作。

## 已完成

### 1. 会话内核

- 新增：`internal/core/session.go`
- 新增：`internal/core/session_manager.go`
- 新增清理测试：`internal/core/session_manager_test.go`
- 能力包括：
  - session 生命周期管理
  - 输出缓冲
  - stdin 写入
  - 主动关闭
  - 空闲 / 结束态清理

### 2. Shell 会话工具

- 新增：`internal/capabilities/shell/session_runner.go`
- 新增：`internal/capabilities/shell/exec_command_tool.go`
- 新增测试：`internal/capabilities/shell/session_runner_test.go`
- 支持：
  - 普通 shell 会话
  - `tty=true` 的 PTY 会话
  - `write_stdin`
  - terminal 缓冲读取

### 3. App / 配置 / 审批接线

- 更新：`internal/app/app.go`
  - 挂接 `SessionManager`
  - 注册 3 个新工具
  - 支持后台 janitor
  - 新增 `App.Close()`
- 更新：`internal/config/config.go`
  - 新增 `session_cleanup`
- 新增：`internal/config/config_test.go`
- 更新：`internal/approval/service.go`
  - `exec_command` 纳入审批判断
- 更新：`cmd/localgate/main.go`
  - 退出时停止 janitor

### 4. HTTP / MCP 接口

- 更新：`internal/httpapi/api.go`
  - 新增 `/sessions/exec`
  - 新增 `/sessions/{id}`
  - 新增 `/sessions/{id}/stdin`
  - 新增 `/sessions/{id}/terminal`
  - 新增 `/sessions/{id}/close`
- 更新：`internal/httpapi/api_test.go`
  - 增加 session API 测试
- 更新：`internal/mcp/stdio.go`
  - 对 session 工具走直调而不是 task 包装
- 更新：`internal/mcp/stdio_test.go`
  - 增加 MCP session tools 测试

### 5. 示例配置

- 更新：`localgate.yaml`
  - 新增 `session_cleanup` 默认配置

### 6. 接口命令文档

- 新增：`api-command-collection.md`
- 内容覆盖：
  - 全部 HTTP API
  - 全部 MCP JSON-RPC 方法
  - 全部 MCP tools
  - 典型请求参数与示例
  - HTTP MCP `/mcp` 的启动命令
  - HTTP MCP `/mcp` 的 `curl` 示例

### 7. Skill 文档增强

- 更新：`skills/localgate-http-api/SKILL.md`
- 补充内容：
  - 设计思路
  - 交互方式
  - HTTP / HTTP MCP / stdio MCP 的选择规则
  - 工具介绍
  - 启动命令
  - 标准任务流、会话流、审批流、MCP 流

### 8. HTTP Bearer Token 认证

- 更新：`internal/config/config.go`
  - 新增 `http_auth`
  - 支持 `enabled`、`bearer_token`、`allow_paths`
  - 支持环境变量覆盖
  - `SafeSummary()` 不输出明文 token
- 更新：`internal/server/server.go`
  - 在 server 层增加全局 Bearer 中间件
  - 默认放行 `/healthz`
  - 同时保护普通 HTTP API 与 HTTP MCP `/mcp`
  - 使用 `subtle.ConstantTimeCompare` 校验 token
- 更新：`cmd/localgate/main.go`
  - 将 `http_auth` 配置接入 HTTP Server
- 新增：`internal/server/server_test.go`
  - 覆盖放行、缺失 token、错误 token、正确 token
- 更新：`internal/config/config_test.go`
  - 覆盖 `http_auth` 加载、必填校验、摘要脱敏
- 更新：`localgate.yaml`
  - 增加 Bearer Token 配置示例
- 更新：`api-command-collection.md`
  - 补充鉴权头与 `/mcp` 调用示例
- 更新：`skills/localgate-http-api/SKILL.md`
  - 补充启用 HTTP 鉴权时的调用要求

### 9. 编译与启动脚本

- 新增：`scripts/build.sh`
  - 支持编译 `cmd/localgate`
  - 支持 `VERSION`、`OUTPUT`、`GOOS`、`GOARCH`
- 新增：`scripts/start-http.sh`
  - 默认先编译，再启动 HTTP API + HTTP MCP `/mcp`
  - 支持 `CONFIG`、`BIND`、`SKIP_BUILD`、`DAEMON`
- 新增：`scripts/start-mcp.sh`
  - 默认先编译，再启动 stdio MCP
  - 支持 `CONFIG`、`SKIP_BUILD`
- 新增：`scripts/stop-http.sh`
  - 根据 pid 文件停止 HTTP daemon 进程
  - 支持 `STOP_TIMEOUT_SECONDS`
- 更新：`api-command-collection.md`
  - 补充脚本启动方式与参数示例
  - 补充 HTTP daemon 模式的 pid/log 约定
  - 补充 HTTP 停止脚本用法
- 新增：HTTP daemon 二进制自动升级接口
  - `POST /system/upgrades`
  - `GET /system/upgrades/{id}`
  - 独立 `X-LocalGate-Upgrade-Token` 请求头
  - 仅从 `LOCALGATE_UPGRADE_TOKEN` 环境变量加载
  - 上传后异步切换，失败自动回滚
  - daemon 启动脚本向子进程注入 runtime 元信息，供升级 runner 复用

## 结果

- `LocalGate.mcp` 已具备和 `LocalGate` 对齐的 session 工具能力
- HTTP 和 MCP 都能调用这 3 个工具
- janitor 和 session cleanup 已接入配置
- HTTP API 与 HTTP MCP `/mcp` 已支持可配置的 Bearer Token 认证
- 仓库内已提供完整的 HTTP / MCP 命令集合文档
- 文档已明确区分 stdio MCP 与 HTTP MCP，并补充 `/mcp` 的启动与 `curl` 用法
- 仓库内已提供可直接使用的编译、HTTP 启动和 stdio MCP 启动脚本
- HTTP 启动脚本已支持 daemon 模式，并提供基础 pid/log 文件管理
- 已补充 HTTP daemon 的标准停止脚本
- Skill 文档已增强为可直接指导模型选传输、选交互模型、选工具的版本
- 已支持通过上传预编译二进制对 HTTP daemon 做自动升级，并提供跨重启状态查询
- 全量测试已通过：`go test ./...`

## 暂未支持

- session 输出持久化
- 显式 cursor / offset 协议
- HTTP session 流式订阅
