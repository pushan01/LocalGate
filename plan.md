# Plan

## 文件预览 Widget 精简（CH-ARTIFACT-UI-005）

- **状态**：已实现（2026-10-11）。
- **范围**：移除 v6 文件交接实验：自动上传、回读校验、File ID 与诊断面板、模型可见性测试按钮；保留 `export_file` 的 MCP 原生图片、资源链接、预览图片和手动下载入口。
- **实现**：Widget 简化为无网络读取的只读展示，CSP 不再允许组件发起外部 fetch；UI URI 升为 `ui://localgate/artifact-viewer/v7.html`，避免缓存继续展示旧版。
- **说明**：不改变 Mac 文件导出服务端的授权、文件完整性验证及短期签名 URL 机制。之前的 CH-ARTIFACT-UI-004 代码与文档仅作为 Git 历史，不再作为当前组件功能或验收项。

## ChatGPT 导出文件可验证交接（CH-ARTIFACT-UI-004）

- **状态**：历史方案，已由 CH-ARTIFACT-UI-005 撤销，以下仅为历史实现记录（2026-10-11）。
- **目标**：在现有 MCP Apps `uploadFile` 基础上诊断每个交接阶段；核验 LocalGate 源文件、ChatGPT 重新下载的字节与 SHA-256；恢复已上传文件的 Widget State；允许用户主动发起下一轮模型图片可见性/Sandbox 挂载验证。
- **非目标**：不使用 OpenAI Platform API 创建另一套容器，不宣称 MCP 可以直接挂载 ChatGPT 当前 `/mnt/data`，不自动触发模型消息，不改变 Mac 文件读取权限、导出 grant 和下载协议。
- **范围**：`internal/mcpgo/ui/artifact-viewer.html` 及其测试、组件 URI、README、计划/待办。
- **风险**：`uploadFile`、`getFileDownloadUrl`、`sendFollowUpMessage` 是客户端可选能力；ChatGPT 的下载 URL 可能不允许 iframe 跨域回读；大文件哈希会占用内存。因此需明确区分成功、失败、未验证和跳过（大文件）；不能将本地 Widget 测试当作真实 ChatGPT 挂载测试。
- **验收**：分阶段显示源文件、上传、回读、内容一致性和会话验证状态；对比真实下载字节数及 SHA-256，检测不一致；Widget State 保存/恢复有效文件 ID 和诊断状态并包含 imageIds；显式点击可触发后续验证请求；自动化测试覆盖成功、哈希不符、回读受限、缺少宿主接口、恢复及下一轮操作。
- **验证**：Node 组件测试、定向 Go MCP HTTP 测试、git diff --check；真实客户端交接单独留下逐层验收记录。
- **实际实现**：保留已有 `uploadFile(file, { library: true })`，在 Widget 内实现源文件/回读文件真实字节 SHA-256、大小校验、分阶段诊断、下载回读受限的区分、Widget State 恢复和同一次导出避免重复上传；新增用户显式点击才触发的模型上下文及 Sandbox 分项验证入口；MCP UI URI 升至 v6，README 和 `docs/chatgpt-file-handoff-verification.md` 给出完整验收步骤。不改服务端导出授权、不使用 OpenAI Platform Containers。
- **自动化结果**：Node Widget 12 项测试通过；`go test -p 1 ./internal/mcpgo ./internal/httpapi ./internal/fileexport`、对应 `go vet` 和 `go build ./cmd/localgate` 通过。HTTP 终端 stdin 时序测试首次运行偶发失败，单独重复两次及模块回归均通过。MCP→ChatGPT 真实文件库、模型图片上下文和当前 Sandbox 挂载无法由模拟测试证明，保留真实客户端验收事项。

## ChatGPT 文件/图片预览 Widget（CH-ARTIFACT-UI-003）

- **状态**：已完成（2026-10-11）
- **目标**：修复客户端不能直接看到 `export_file` 返回图片的实际体验：增加标准 MCP Apps UI 资源，把它关联到直接 MCP `export_file`，由 ChatGPT iframe 自己渲染图片预览和原件下载入口；保留现有 MCP `image`/`resource_link` 供不支持 UI 的宿主使用。
- **范围**：`internal/mcpgo` 的工具元数据、只读 HTML widget、HTTP/stdio resources/read 测试和 README。
- **非目标**：不改变文件沙箱、授权/下载逻辑，不在结构化任务结果中写入签名 URL，不改变其他工具的 UI，不升级运行二进制之前重启服务。
- **风险**：宿主可能缓存已安装插件的工具元数据，必须刷新插件连接才能获得新 UI；App iframe 渲染依赖宿主实际实现，Go 协议测试不能证明前端已经显示。
- **验收**：`export_file` 的 tools/list 包含 `_meta.ui.resourceUri`，对应 `resources/read` 返回 `text/html;profile=mcp-app`；UI 有 MCP Apps 初始化、tool-result 消息与下载/图片渲染逻辑；普通 MCP 返回和原有任务执行不变。
- **验证**：widget JS 语法检查、MCP HTTP tools/list/resources/read/tools/call 测试、`go test -p 1 ./...`、`go vet -p 1 ./...`、构建与 `git diff --check`；宿主界面须后续通过客户端观察。
- **实际实现**：`export_file` tools/list 增加标准 `ui.resourceUri` 和兼容 `openai/outputTemplate`；Go embed 注册可读的 MCP Apps HTML UI 资源，通过桥接 `ui/notifications/tool-result` 渲染图片和文件下载链接；不影响原有 `image`、`resource_link`、审批或持久化。
- **验证结果**：Node widget 单元测试 3 个通过（图片、PDF、危险输入）；MCP HTTP tools/list 和 resources/read 验证通过；定向 Go 测试、串行全量 `go test -p 1 ./...`、`go vet -p 1 ./...`、Windows amd64 编译及 `git diff --check` 通过。实际 ChatGPT UI 展示尚待插件连接刷新和客户端观察。


## ChatGPT Artifact 大图预览与 MCP 验证（CH-ARTIFACT-UI-002）

- **状态**：已完成（2026-10-10）
- **目标**：超过 1 MiB 的 PNG/JPEG 图片在限定体积及像素尺寸内生成小型 JPEG 内联预览，同时保留原件短期下载资源；对实际 MCP tool content 与 HTTP GET/HEAD/Range 下载做端到端回归验证。
- **非目标**：不改已有运行 LocalGate 实例、不触碰用户实际文件、不把 MCP `resource_link` 称为 ChatGPT 原生附件、不引入完整 MCP Apps 画廊、不扩展 WebP 解码依赖。
- **安全约束**：原件必须按 grant 校验 size、mtime、SHA-256 和内容 MIME 后才能生成缩略图；每次最多读 16 MiB，限像素数和输出尺寸；无法解码只返回下载链接。缩略图不缓存到持久化记录。
- **涉及范围**：`internal/fileexport`、MCP/HTTP 导出测试、README、`plan.md`、`todo.md`。
- **验收条件**：超过 1 MiB 的有效图片返回压缩缩略图+原件 URL；无效/超限/被篡改图片不内联；PDF/ZIP 仅下载；标准 MCP 内容及 HTTP 下载端点均通过测试。
- **验证方式**：定向测试、`go test -p 1 ./...`、`go vet -p 1 ./...`、`git diff --check`。真正 ChatGPT 页面显示依赖客户端渲染，需安装构建后的服务才能实机测试。
- **实际实现**：`fileexport.ImagePreview` 保留原件 SHA-256/mtime/MIME 校验，新增 PNG/JPEG 受限解码和长边最大 960 像素的 JPEG 白底缩略图；超限图片、无法解码的源文件或大 WebP 仍返回原件资源而不内联。新增真实 Streamable HTTP MCP `tools/call` 到签名 HTTP GET/HEAD/Range 的端到端测试，覆盖 PNG、PDF、ZIP；不修改生产配置和正在运行的实例。
- **验证结果**：定向大图 PNG/JPEG、篡改/伪造图片、透明度合成、MCP HTTPS 资源交付测试通过；`go test -p 1 ./...`、`go vet -p 1 ./...`、Windows amd64 构建和 `git diff --check` 全部通过。ChatGPT 客户端中是否出现原生卡片尚未实机验证；原生文件库和 MCP Apps iframe 均非本轮范围。


## ChatGPT Artifact 展示对齐（CH-ARTIFACT-UI-001）

- **状态**：已完成（2026-10-10）
- **目标**：`export_file` 与 `subagent_artifact_get` 保留原有安全下载资源，同时对小型真实图片返回 MCP `image` 预览；异步 `task_result` 也能呈现原产物的资源链接与预览。
- **非目标**：不自动导入 ChatGPT 文件库、不伪装成原生 ChatGPT 附件、不搭建 MCP Apps iframe、不扩展 Subagent 对 ChatGPT 生成图片的采集、不改变 Artifact 历史保存范围。
- **涉及范围**：`internal/fileexport`、两种导出工具、MCP transport、测试和 README。
- **依赖与风险**：不同 MCP Host 的渲染策略不同；预览必须限制图片格式及响应大小，链接保持短时授权，不能向 Task 持久化结果写入带签名 URL。
- **预计规模**：约 300–750 行有效 diff。
- **验收**：真实 PNG 在 MCP 响应中有 `image` 及 `resource_link`；非图片/超限/伪造图片不做内联；异步 `task_result` 能恢复相同资源；原有鉴权、过期和 hash 校验不回退。
- **验证**：定向 Go 测试、`go test ./...`、`go vet ./...`、`git diff --check`。真实 ChatGPT UI 效果需要客户端手工联调，不用单元测试冒充证明。
- **实际实现**：统一生成临时文件下载文本和可选 MCP `image` 块；图片预览上限 1 MiB，且校验真实 MIME、大小、mtime、SHA-256；`export_file`、`subagent_artifact_get` 复用同一展示逻辑；`task_result` 对成功完成的原任务恢复原工具资源，新增的预览和链接文本不写入持久化结果。`subagent_artifact_get` 原有结构化 `url` 字段暂为兼容保留。
- **验证结果**：图片/Artifact、MCP 同步与异步协议定向测试通过；`go test -p 1 ./...`、`go vet -p 1 ./...`、`git diff --check` 全部通过。并行 `go test ./...` 曾出现 Relay、进程及 Session 的竞争/超时失败，串行复测通过；ChatGPT 客户端的实际卡片 UI 尚待实机验证。


## Single Root：移除 Workspace 多安全域模型

- **状态**：已完成（CH-ROOT-001 ～ CH-ROOT-004）
- **规划日期**：2026-08-20
- **分支**：`work/localgate-next`
- **目标**：一个 LocalGate 实例只对应一个受控根目录，不再支持多 workspace。配置改为顶层 `root` 与实例级 path/command/env/shell/approval policy；App 只保留一个 Registry；MCP、REST、Admin、Task、Session、Subagent、Deployment、Tool Access 不再携带或选择 `workspace_id`。
- **安全不变量**：所有文件路径、Shell cwd/workdir、deployment source 和本地导出源在 symlink 解析后都必须位于配置的单一 `root` 内；移除 workspace 选择不能降低现有路径、命令、环境变量、审批和资源生命周期检查。
- **兼容策略**：这是有意的配置/API breaking change。旧 `workspaces:` 配置启动时明确报错并提示改用顶层 `root`；不保留“第一个 workspace 作为 default”的隐式兼容。现有持久化 Task/Session/Subagent/Deployment 记录中的旧 `workspace_id` 只在需要安全迁移时做向后读取，不再写入新记录或暴露为新 API 字段。

### CH-ROOT-001：单 Root 配置与 App 单 Registry

- **状态**：已完成
- **开始日期**：2026-08-20
- **完成日期**：2026-08-20
- **优先级**：P0
- **目标**：删除外部 `workspaces` 配置与 `DefaultWSID/Workspaces/Registries` App 模型，建立顶层 `root` + 单 `App.Registry`，并让 filesystem/shell/deployment 等工具绑定同一 root policy。
- **非目标**：本 Change 暂不删除 Task/Session/Subagent 持久化结构中的 workspace 字段，也不改所有 HTTP/Admin 请求结构；这些在后续 Change 处理。
- **预计规模**：约 500–900 行有效 diff，配置测试中的批量 fixture 迁移作为机械变化单独看待。
- **验收条件**：`Config` 必须有一个绝对 root；旧 `workspaces` 明确拒绝；root policy 在启动时校验；App 只构建一次 Registry；MCP 使用单 Registry 且不再持有 WSID。
- **验证方式**：`go test ./internal/config ./internal/app ./internal/security ./internal/capabilities/filesystem ./internal/capabilities/shell ./internal/mcpgo`、`git diff --check`。
- **实际实现**：配置改为顶层 `root` 与实例级 path/command/env/shell/approval policy；旧 `workspaces:` 明确拒绝。`App` 只持有一个 `Root` 和一个 `Registry`，filesystem/shell/deployment/MCP 全部绑定该单一 root，不再构建 workspace map 或选择默认 workspace。
- **验证结果**：配置、App、安全、filesystem、shell、MCP 定向测试通过；后续全量验证由 CH-ROOT-004 统一记录。

### CH-ROOT-002：Task / Session / Subagent 去 Workspace ownership 参数

- **状态**：已完成
- **开始日期**：2026-08-20
- **完成日期**：2026-08-20
- **依赖**：CH-ROOT-001
- **优先级**：P0
- **目标**：删除 invocation/task/session/subagent 新模型中的 workspace 传播和 cross-workspace API，资源身份直接限定在当前 LocalGate 实例。
- **非目标**：不改变 Task 状态机、Session cursor 或 Subagent Provider 行为。
- **预计规模**：约 500–900 行有效 diff。
- **验收条件**：Task lifecycle、Session read/write/close、Subagent get/list/send/poll/wait/archive/close 均无需 workspace 参数；新持久化记录不再写 `workspace_id`。
- **验证方式**：相关 core/capability/subagent 测试、`go test ./internal/core ./internal/capabilities/task ./internal/capabilities/shell ./internal/subagent ./internal/capabilities/subagent`。
- **实际实现**：Task、Invocation、Session、Subagent 和相关 Manager/API 删除 workspace ownership 字段与 cross-workspace 方法；资源身份直接属于当前 LocalGate 实例，旧持久化 JSON 中额外的 `workspace_id` 可被 Go JSON 解码安全忽略。
- **验证结果**：Core、Task、Shell、Subagent capability/runtime 定向测试通过，原 cross-workspace 测试改为单实例下仍有效的 not-found、生命周期和隔离不变量。

### CH-ROOT-003：HTTP / Admin / File Browser / Tool Access 去 Workspace 路由

- **状态**：已完成
- **开始日期**：2026-08-20
- **完成日期**：2026-08-20
- **依赖**：CH-ROOT-001、CH-ROOT-002
- **优先级**：P0
- **目标**：REST、Tool HTTP、Admin/File Browser 不再接受或返回 `workspace_id`，File Browser 直接围绕单 root + relative path 工作，Tool Access grant 不再绑定 workspace。
- **预计规模**：约 600–1000 行有效 diff；前端/API fixture 的机械字段删除单独计量。
- **验收条件**：不存在 workspace selector/registry resolver；Tool HTTP token 只授权当前实例；File Browser API 不需要 workspace 参数且仍不能逃逸 root。
- **验证方式**：`go test ./internal/httpapi ./internal/adminapi ./internal/filebrowser ./internal/toolaccess`，前端测试/构建按仓库既有方式执行。
- **实际实现**：REST、Tool HTTP、Admin/File Browser 全部删除 workspace selector、registry resolver 与 `workspace_id/root_id` API 字段；File Browser 只接受 root-relative `path`，Admin Files 的 Tab/Git/Terminal 前端状态也收敛为 path-only；Tool Access grant 只授权当前实例。
- **验证结果**：HTTP/Admin/FileBrowser/ToolAccess 定向测试通过；Admin 前端 18 个 Node 测试、源码语法检查和 dist 重建通过。

### CH-ROOT-004：Deployment / 持久化 / 文档收口

- **状态**：已完成
- **开始日期**：2026-08-20
- **完成日期**：2026-08-20
- **依赖**：CH-ROOT-001 ～ CH-ROOT-003
- **优先级**：P0
- **目标**：删除 Dynamic/Subagent/Task 等剩余 workspace 持久化语义和 Deployment workspace ownership 字段，更新 README、Builtin Skills、示例配置与 API 文档为单 Root 模型。
- **预计规模**：约 500–1000 行有效 diff，加历史设计文档中的说明性机械更新。
- **验收条件**：生产代码不再存在 `WorkspaceID` / `workspace_id` / `DefaultWSID` / `Workspaces` / `Registries` 概念；示例配置只有 `root`；全量测试和静态检查通过。
- **验证方式**：`rg` 残留检查、`go test ./...`、`go vet ./...`、`git diff --check`。
- **实际实现**：Deployment/Dynamic、Task、Session、Subagent、Tool Access 和文件传输持久化的新模型均不再写入 workspace ownership；旧 JSON 中额外的 `workspace_id` 继续由兼容读取测试覆盖。README、HTTP/MCP Skills、Builtin Skills、示例配置、Admin Files 前端和当前 API 命令文档全部改为单 Root 语义；旧设计计划文档在文件顶部明确标记为历史多 Workspace 模型。
- **验证结果**：生产 Go 源码中 `workspace_id` / `WorkspaceID` / `DefaultWSID` / `Workspaces` / `Registries` 残留为 0；现行 README、example、Skills、Builtin Skills、Admin 前端源码/生成产物中 `workspace` 旧模型表述为 0。`go test ./...`、`go vet ./...`、`web/admin npm run check`（18 个 Node 测试 + 源码/生成 JS 检查 + dist 重建）和 `git diff --check` 全部通过。

## MCP 渐进式工具披露：稳定核心面 + Builtin Skill + Registry Invoke

- **状态**：已实施（CH-MCP-001 ～ CH-MCP-003）
- **规划日期**：2026-08-20
- **分支**：`work/localgate-next`
- **目标**：把 MCP 从“Registry visible tools 全量平铺”收敛为稳定的小型协议面；高频基础能力和 MCP Host 原生文件桥继续直接暴露，其余 Registry 能力通过 Builtin Skill 渐进披露，并由统一 `invoke_tool` 入口执行，保持原有 Task / Approval / Audit / Workspace 安全链路。
- **目标 MCP 常驻面（普通模式）**：`get_startup_instructions`、`get_skill`、`get_tool_schema`、`invoke_tool`、`read_file`、`apply_patch`、`run_command`、`upload_file`、`export_file`。
- **核心设计原则**：
  - MCP exposure 与 Registry capability 分离；Registry 中存在且允许渐进调用的工具不必成为顶层 MCP tool。
  - Builtin Skill 提供能力域、调用顺序、状态机和安全语义；`get_tool_schema` 提供运行时精确 schema，Skill 不复制完整 JSON Schema。
  - `invoke_tool` 只做 transport dispatch，必须把目标工具名和原始参数交给 `App.SubmitToolTaskWithMeta`，审批、审计、任务结果和 workspace 语义均以目标工具为准，不能只记录 `invoke_tool`。
  - `upload_file` 保持直接 MCP 暴露以保留 `openai/fileParams` Host 注入语义；`export_file` 保持直接 MCP 暴露以保留 output schema / annotations / `resource_link` 语义。
  - Tool HTTP Mode 继续作为独立 bootstrap transport，不强行改造成 `invoke_tool`；普通 MCP 模式与 Tool HTTP Mode 都共享同一 Registry capability catalog。
  - 下游 MCP relay 不再默认铺平到顶层 MCP；后续经 capability catalog / Skill / `invoke_tool` 渐进访问。

### CH-MCP-001：`invoke_tool` 与稳定 MCP Core 骨架

- **状态**：已完成
- **开始日期**：2026-08-20
- **完成日期**：2026-08-20
- **优先级**：P0
- **目标**：增加通用渐进调用入口，并把普通 MCP 的 Registry 业务工具收敛到目标 9 工具核心面；为避免迁移中断，本 Change 暂时继续直接暴露现有 5 个 Task lifecycle helper，CH-MCP-002 完成后再从 14 个过渡工具收敛到最终 9 个。
- **非目标**：本 Change 不把 Task lifecycle 五个 helper 迁入 Registry，不重写 Builtin Skill 正文，不改变 Tool HTTP Mode 的四工具 bootstrap 行为。
- **涉及范围**：`internal/mcpgo`、`internal/capabilities/toolaccess`、`internal/core`/`internal/app` 的最小 capability 查询支持、相关测试。
- **预计规模**：约 350–700 行有效 diff。
- **验收条件**：
  - 普通 MCP `tools/list` 只包含目标 9 个核心工具加迁移期 5 个 Task lifecycle helper；Deployment / Session / Subagent / Notification / relay 等业务工具不再直接注册。
  - `invoke_tool(tool, arguments, async?)` 能调用一个未直接 MCP 暴露但允许渐进调用的 Registry tool。
  - 审批规则按目标工具名匹配；创建的 Task `tool` 字段和审计 invocation 记录目标工具名，不是 `invoke_tool`。
  - `invoke_tool` 拒绝递归调用 bootstrap/core dispatch 工具以及不存在/不可渐进调用的工具。
  - 同步、异步、审批等待和目标工具 `resource_link` 结果继续沿用现有 MCP task result 语义。
- **验证方式**：`go test ./internal/capabilities/toolaccess ./internal/core ./internal/app ./internal/mcpgo`、相关 MCP HTTP tools/list 场景、`git diff --check`。
- **实际实现**：普通 MCP 不再遍历并桥接全部 `Registry.ListVisible()`；集中定义稳定直接 MCP allowlist，并新增 transport 级 `invoke_tool(tool, arguments, async?)`。`invoke_tool` 最终调用 `App.SubmitToolTaskWithMeta` 的仍是目标 Registry tool，因此 Approval、Task、Audit 和 workspace 语义保留真实工具名。`get_tool_schema` 改为可读取已注册但未顶层暴露的渐进工具 schema，并禁止 bootstrap/dispatch 工具递归调用。
- **验证结果**：定向 toolaccess/MCP/App 测试通过；Deployment 与 Subagent 回归确认未直接 MCP 暴露但可通过 `invoke_tool` 执行，Deployment 审批和审计仍记录 `deploy_tool`。

### CH-MCP-002：Task lifecycle 下沉 Registry

- **状态**：已完成
- **完成日期**：2026-08-20
- **依赖**：CH-MCP-001
- **优先级**：P0
- **目标**：把 `task_status`、`task_logs`、`task_cancel`、`task_result`、`wait_task_finish` 从 `mcpgo.Server` 特制工具迁移为 LocalGate 标准 Registry tools，使它们可由 `localgate-task-session` + `invoke_tool` 渐进调用，并供 HTTP/CLI 复用。
- **非目标**：不改变 Task 状态机、不增加新的恢复语义或持久化模型。
- **预计规模**：约 300–600 行有效 diff。
- **验收条件**：五个 lifecycle 工具从 MCP 顶层消失但可被 `get_tool_schema` / `invoke_tool` 调用；行为、错误和输出兼容现有 MCP helper；普通 MCP 顶层仍保持 9 个。
- **验证方式**：Task capability 单测、App/HTTP/MCP invoke 测试、`go test ./...`、`git diff --check`。
- **实际实现**：新增 `internal/capabilities/task`，把五个 lifecycle helper 注册到每个 workspace Registry；删除 `mcpgo.Server` 中对应的特制注册和 handler。普通 MCP 顶层因此正式收敛到目标 9 工具，Task helper 由 `localgate-task-session` + `get_tool_schema` + `invoke_tool` 渐进访问。
- **验证结果**：新增 Task result/stream、workspace 隔离和 wait completion 单测；MCP 回归覆盖通过 `invoke_tool` 使用 `wait_task_finish`，相关 App/MCP 测试通过。

### CH-MCP-003：Builtin Skill / startup / relay 渐进披露收口

- **状态**：已完成
- **完成日期**：2026-08-20
- **依赖**：CH-MCP-001、CH-MCP-002
- **优先级**：P0
- **目标**：让 startup 按完整 Registry capability 而不是 MCP exposure 决定 Builtin Skill 可见性；补 `localgate-filesystem`、`localgate-notification`，更新 deployment/subagent/task/file-transfer/mcp-relay Skill 使用 `get_tool_schema → invoke_tool`；停止下游 relay 工具顶层平铺。
- **非目标**：不改变下游 MCP 本身的协议和 schema，不引入动态 MCP `tools/list_changed`。
- **预计规模**：约 400–800 行有效 diff。
- **验收条件**：
  - 即使 Deployment/Subagent 等工具不在顶层 MCP，相关 Builtin Skill 仍按当前 Registry capability 正确出现。
  - startup 清楚区分“常驻 MCP tools”和“可渐进调用 capabilities”，不再暗示 Registry visible 等于 MCP surface。
  - relay 工具可以通过 schema + invoke 路径使用而不进入顶层 MCP tools/list。
  - README / example usage 与新暴露模型一致。
- **验证方式**：meta/skill、relay、MCP tools/list、HTTP capability 定向测试；`go test ./...`、`go vet ./...`、`git diff --check`。
- **实际实现**：startup 在普通 MCP 模式下只摘要直接 MCP 工具，同时用完整 Registry capability 决定 Builtin Skill 路由；Tool HTTP 模式仍严格按 HTTP 可调用 visible list 生成 skill，避免推荐 hidden-only capability。新增 `localgate-filesystem`、`localgate-notification`，并更新 deployment/file-transfer/task-session/subagents/mcp-relay Skills 使用渐进调用协议。下游 MCP 工具包装成 `server__tool` Registry capability，不再顶层平铺，并通过 transport-neutral native-content provider 保留下游 MCP text/image/audio/resource content。
- **验证结果**：普通 MCP 回归明确断言启用 file export 时 `tools/list` 恰为 9 个目标工具；Relay 回归确认不直接暴露、schema/invoke 可用且原生 MCP content 保留；README 和 `skills/localgate-mcp-tools` 已同步。`go test ./...` 全部通过，后续模式边界修正后的 meta/MCP/Task 定向测试、`go vet ./...` 与 `git diff --check` 均通过。


## Subagent Runtime：基于 OpenCLI + ChatGPT Web 的会话型子 Agent

- **状态**：已实施（CH-SUB-001 ～ CH-SUB-008）
- **规划日期**：2026-08-17
- **分支**：`work/localgate-next`
- **目标**：在 LocalGate 中增加可持久化、可恢复、可串行投递消息的 Subagent Runtime。首个 Provider 使用 OpenCLI 驱动已登录的 ChatGPT Web；LocalGate 使用自己的 `subagent_id` 作为稳定业务身份，保存其与 ChatGPT `conversationId` 的映射，并向上层 Agent 暴露统一的 spawn / send / get / list / close 工具。
- **已确认的外部能力**：当前 OpenCLI ChatGPT Web adapter 的 `opencli chatgpt ask` 会返回 `conversationId`、`conversationUrl` 和 `response`，并支持 `--conversation <id-or-url>` 继续已有会话；因此 MVP 不需要依赖浏览器 tab ID 或 DOM 猜测 conversation ID。
- **已确认的 Project 能力**：当前 OpenCLI ChatGPT Web adapter 的 `ask`、`send`、`new` 已支持 `--project <project-id-or-url>`，会先进入指定 ChatGPT Project 再创建新会话；conversation URL 解析也兼容 `/g/g-p-.../c/<conversation-id>`。MVP 默认优先把 LocalGate subagent 新会话创建在一个专用 ChatGPT Project 中，减少普通聊天列表噪声；Project ID 只影响创建归属，后续恢复仍以 `conversationId` 为权威句柄。
- **核心设计原则**：
  - `subagent_id` 是 LocalGate 的稳定主键；ChatGPT `conversationId` 只是 Provider 侧句柄。
  - 浏览器 site session / tab target 只属于临时 transport 状态，不作为持久身份。
  - 同一 subagent 的消息默认串行执行；不同 subagent 可以并发。
  - Provider 接口与 ChatGPT Web 解耦，后续可增加 OpenAI API、Claude Web、Gemini Web 或本地模型 Provider。
  - ChatGPT Web Provider 支持可配置的专用 Project；同一个 LocalGate 实例可默认使用一个 `default_project`，单个 spawn 可在允许时覆盖。
  - 调用 OpenCLI 使用 argv 形式的受控子进程，不通过 `/bin/sh -c` 拼接用户输入。
  - Subagent 调用继续进入 LocalGate Task / Event / Audit 体系，不另建一套不可观察的后台执行机制。
- **本阶段非目标**：
  - 不实现 Subagent 之间自主互发消息或递归无限 spawn。
  - 不实现 Admin Console 的 Subagent 可视化页面。
  - 不把浏览器 tab/session 做跨重启持久化；恢复依赖 conversation ID 重新定位。
  - 不在第一版引入 SQLite，也不顺带重构现有 Task / Approval 存储。
  - 不在第一版实现 token/成本统计、复杂负载均衡、多 ChatGPT 账号池或 Provider 自动切换。
- **总体风险**：ChatGPT Web 和 OpenCLI adapter 都属于外部动态依赖，可能出现登录失效、Challenge、UI/API 变化、生成超时和会话不可恢复；所有失败必须转成稳定的 LocalGate 错误状态，不能让 Manager 卡死或丢失消息状态。
- **Project 列表边界**：官方文档明确说明 shared project 中被移入 Project 的聊天不再显示在 Project 外；对个人 Project，MVP 依赖 ChatGPT 当前 Project 归组行为来减少普通列表噪声，但不把“所有全局搜索/历史入口完全不可见”作为程序保证。

### CH-SUB-001：Subagent 核心模型、持久化与 Provider 抽象

- **状态**：已完成
- **开始日期**：2026-08-17
- **完成日期**：2026-08-17
- **优先级**：P0
- **目标**：建立与具体 ChatGPT 实现无关的 Subagent Manager，支持稳定 ID、Provider 状态、生命周期、持久化和单 Agent 串行执行，为后续真实 Provider 接入提供可独立测试的内核。
- **涉及范围**：新增 `internal/subagent`，`internal/app` 组装，配置结构及单元测试。
- **预计规模**：约 500–850 行有效 diff。
- **建议模型**：
  - `Subagent`：`id`、`name`、`provider`、`status`、`workspace_id`、`instruction`、`provider_state`、创建/更新时间、最后错误。
  - `ProviderState`：首版保存 `conversation_id`、`conversation_url`、可选 `project_id`；browser session / tab 如需记录只能作为可丢失的 hint。
  - `Manager`：Create/Get/List/Close/Send，按 `subagent_id` 维护串行 mailbox 或等价互斥执行器。
  - `Provider`：`Create`、`Send`、`Recover/Validate`、`Close`；测试使用 Fake Provider。
  - `Store`：先沿用 `.localgate/subagents/` 下的原子 JSON 持久化模式，明确单进程保证；跨进程事务作为后续事项。
- **验收条件**：
  - 创建 subagent 会生成与 Provider 外部 ID 无关的稳定 `sg_*` ID。
  - Manager 重建后可以从持久化记录恢复 subagent 元数据和 provider state。
  - 同一 subagent 的两个并发 Send 不会同时进入 Provider；不同 subagent 不被全局串行化。
  - Provider 错误会更新 last error，但不会损坏已有 conversation 映射。
  - Close 后拒绝新消息，并具有幂等语义。
- **验证方式**：`go test ./internal/subagent ./internal/app`、并发单元测试、`go test -race ./internal/subagent`、`git diff --check`。
- **实际实现**：新增 `internal/subagent` 内核，提供稳定 `sg_*` ID、Provider 接口、可持久化 ProviderState、workspace 隔离、Create/Send/Get/List/Close 生命周期、同一 subagent 独立串行锁和跨 subagent 全局并发上限；Store 使用 0600 同目录临时文件、`fsync` 和 rename 保存单 agent JSON。配置新增 `subagents.enabled/default_provider/max_agents/max_concurrency`，App 在启用时加载 `.localgate/subagents` 并在进程关闭时只关闭 Manager，不把持久化 subagent 逻辑关闭。
- **验证结果**：`go test ./internal/subagent ./internal/config ./internal/app`、`go test -race ./internal/subagent`、`go test ./...`、`go vet ./...` 和 `git diff --check` 均通过。首次测试因 LocalGate Shell 未注入 `HOME/GOCACHE` 无法启动 Go build cache，改为显式使用仓库 `.localgate/` 下的 Go cache/module 目录后通过；该问题属于执行环境而非代码失败。
- **实际规模**：核心实现与测试约 980 行有效新增，接近但未超过 1000 行护栏；Manager 级主动恢复流程未提前实现，保留到 `CH-SUB-004`。

### CH-SUB-002：OpenCLI ChatGPT Web Provider

- **状态**：已完成
- **开始日期**：2026-08-17
- **完成日期**：2026-08-17
- **依赖**：CH-SUB-001
- **优先级**：P0
- **目标**：实现首个真实 Provider，通过 OpenCLI ChatGPT Web adapter 创建和继续会话，并将 `conversationId` / `conversationUrl` 稳定映射到 Subagent。
- **涉及范围**：`internal/subagent/providers/opencli_chatgpt`（或等价目录）、配置、命令执行辅助代码、Provider 测试。
- **预计规模**：约 450–800 行有效 diff。
- **执行约定**：
  - 配置了 Project 时，Create 使用 `opencli chatgpt ask <bootstrap-or-first-message> --project <project-id> --format json`；未配置 Project 时才使用 `--new`。从结构化输出读取 `conversationId`、`conversationUrl`、`response`。
  - Send 使用 `opencli chatgpt ask <message> --conversation <conversationId> --format json`。
  - `project_id` 只用于确保新会话从目标 Project 内创建；会话建立后以 `conversationId` 继续对话，不依赖浏览器持续停留在 Project URL。
  - 若 OpenCLI 返回的首轮 `conversationUrl` 带 `/g/g-p-.../c/<id>`，保存原始 URL 作为诊断/展示信息，但恢复逻辑不得只依赖该 URL 形态。
  - 不依赖 OpenCLI persistent site session 才能恢复；有 `conversationId` 时应允许在新的浏览器 transport 中继续对话。
  - OpenCLI executable 必须从配置解析为可信可执行文件，使用 `exec.CommandContext(argv...)`，prompt 作为独立 argv 参数传递，禁止 shell 拼接。
  - 对退出码、空结果、JSON schema 漂移、登录失效、超时、Challenge/Quota 类错误做结构化归类。
- **配置草案**：
  - `subagents.enabled`
  - `subagents.default_provider`
  - `subagents.max_agents`
  - `subagents.max_concurrency`
  - `subagents.max_pending_per_agent`
  - `subagents.providers.opencli_chatgpt.executable`
  - `subagents.providers.opencli_chatgpt.timeout_seconds`
  - `subagents.providers.opencli_chatgpt.default_project`：可接受 Project ID 或 `/g/g-p-...` URL；建议为 LocalGate 建一个专用 Project。
  - 可选 `profile` / `site_session` hint，但不得成为恢复必需字段。
- **验收条件**：
  - Provider 能从 OpenCLI JSON 输出读取 conversation ID，而非解析人类可读文本。
  - 创建后持久化 conversation ID；再次 Send 明确携带 `--conversation`。
  - 配置 `default_project` 时，创建命令必须携带 `--project` 且不得再同时携带 `--new`；Provider state 记录规范化后的 project ID。
  - Prompt 中包含空格、换行、引号、Shell 元字符时不会产生命令注入或参数错位。
  - Context cancel / timeout 会终止 OpenCLI 子进程并返回稳定错误。
  - malformed JSON、缺失 conversation ID、非零退出码均有回归测试。
- **验证方式**：Provider fake-executable 测试、参数捕获测试、超时/取消测试、真实 OpenCLI 冒烟（环境可用时）、`go test -race`、`git diff --check`。
- **实际实现**：新增 `internal/subagent/providers/opencli_chatgpt`，启动时解析并固定真实 OpenCLI executable；Create 在配置 Project 时使用 `--project`，否则使用 `--new`，Send 始终使用 `--conversation`；prompt 通过 argv 和 `--` 传递，不经过 shell 拼接。Provider 解析 OpenCLI 单行 JSON 结果并校验 conversation ID/URL，支持 Project ID/URL 规范化，提供 auth/challenge/quota/timeout/execution/invalid-output 错误分类。配置新增 `providers.opencli_chatgpt.executable/timeout_seconds/default_project`，App 在默认 Provider 为 `opencli_chatgpt` 时注册真实 Provider。
- **验证结果**：fake executable 覆盖 Project 路由、conversation 续聊、Shell 元字符/换行参数、malformed JSON、会话切换、登录错误和超时；`go test -race ./internal/subagent ./internal/subagent/providers/opencli_chatgpt`、`go test ./...`、`go vet ./...`、`git diff --check` 均通过。真实 `/opt/homebrew/bin/opencli` 只读探测确认 ChatGPT `Status=Connected`、`Login=Yes`，且 `ask --help` 暴露 `--project`、`--conversation` 和 JSON 输出列；本阶段未擅自创建真实 ChatGPT 会话。

### CH-SUB-003：LocalGate 工具面与 Task 集成

- **状态**：已完成
- **开始日期**：2026-08-17
- **完成日期**：2026-08-17
- **依赖**：CH-SUB-001、CH-SUB-002
- **优先级**：P0
- **目标**：把 Subagent Runtime 暴露为 LocalGate 标准 Tool，并统一经过现有 Registry、Task、MCP/HTTP 传输和审计链路，使父 Agent 可以稳定创建、投递和读取子 Agent。
- **涉及范围**：`internal/capabilities/subagent`、`internal/app` Registry 注册、HTTP/MCP schema/结果适配、startup instructions / Skills、测试和 README。
- **预计规模**：约 550–950 行有效 diff。
- **首版工具**：
  - `subagent_spawn`：创建 subagent，可选同时发送 bootstrap/initial message，返回 `subagent_id` 和首轮回复。
  - `subagent_send`：向指定 subagent 串行发送消息并等待回复。
  - `subagent_get`：读取状态、provider、conversation 映射摘要、last error，不泄露浏览器凭证。
  - `subagent_list`：列出 workspace 下的 subagents。
  - `subagent_close`：关闭逻辑 subagent；不删除 ChatGPT Web 历史会话。
- **Task 语义**：
  - 每次 spawn/send/close 都是普通 LocalGate Task，复用 Task cancel、日志和 transport metadata。
  - `subagent_send` 运行时间可能较长，但 MVP 先采用普通 Task 的同步/async 双模式，不把它伪装成 LocalGate Session。
  - stdout/stderr 不直接暴露 OpenCLI 内部噪声；必要诊断写入受限 Task event，最终结果使用结构化字段。
- **审批策略**：首版不默认强制审批，但工具必须可以被现有 `admin.approval.rules` 按工具名匹配；后续再增加基于 provider/账号/外传内容的条件策略。
- **验收条件**：
  - stdio MCP、HTTP MCP、REST 经统一 Registry 可以调用相同工具实现。
  - parent agent 只需要保存 `subagent_id`，不需要知道 ChatGPT conversation ID。
  - async 提交能返回 Task ID，完成后 Task result 包含稳定 reply schema。
  - workspace 隔离：一个 workspace 不能读取、发送或关闭另一个 workspace 的 subagent。
  - startup instructions 能正确告诉 Agent 何时 spawn、何时复用已有 subagent、同一 subagent 默认串行。
- **验证方式**：Registry/App/HTTP/MCP 定向测试、跨 workspace 测试、审批规则匹配测试、`go test ./...`、`go vet ./...`、相关 race 测试、`git diff --check`。
- **实际实现**：新增 `internal/capabilities/subagent` 五个标准 Tool：`subagent_spawn`、`subagent_send`、`subagent_get`、`subagent_list`、`subagent_close`，统一使用 InvokeContext workspace 隔离和 Registry/Task 执行链；App 在启用 runtime 时对每个 workspace 注册工具。新增内置 `localgate-subagents` skill，只在 subagent 工具可见时由 startup 索引披露，明确 spawn 一次、复用 `sg_*`、同 agent 串行和逻辑 close 语义。README、tracked example config 和 MCP tools skill 同步补充使用规则。
- **验证结果**：能力层、startup/meta、App Task、REST `/tasks` 和 MCP invoke 定向测试通过；HTTP/MCP 均从配置启动 fake OpenCLI Provider 并成功创建 `sg_*`。`go test -race ./internal/subagent ./internal/subagent/providers/opencli_chatgpt ./internal/capabilities/subagent ./internal/app ./internal/httpapi ./internal/mcpgo`、`go test ./...`、`go vet ./...`、`git diff --check` 均通过。

### CH-SUB-004：恢复、并发与故障语义加固

- **状态**：已完成
- **开始日期**：2026-08-17
- **完成日期**：2026-08-17
- **依赖**：CH-SUB-003
- **优先级**：P1
- **目标**：补齐真实运行中最容易产生重复消息或状态漂移的恢复边界，确保 LocalGate 重启、父 Task 取消、OpenCLI 卡死或返回不确定结果时，Subagent 状态可解释、不会无界并发。
- **涉及范围**：`internal/subagent` Manager/Store、OpenCLI Provider、Task 集成与文档。
- **预计规模**：约 450–850 行有效 diff。
- **重点行为**：
  - 服务重启后保留已知 `conversationId`，首次 Send 前执行轻量恢复/验证；transport session 丢失不视为 subagent 丢失。
  - 明确 `ready / busy / closed / degraded`（或等价）状态以及 last error 更新规则。
  - 同一 subagent 串行队列必须有长度上限，避免父 Agent 无限压入请求。
  - 调用在“消息可能已经发出、但回复/结果丢失”时返回 `outcome_unknown` 类错误，禁止自动无脑重试造成重复用户消息。
  - Context cancel 取消当前等待，但要区分“尚未提交到 ChatGPT”与“可能已提交”。
  - 对 Store 损坏、缺失 conversation ID、已关闭 Agent、Provider 不可用提供可操作诊断。
- **验收条件**：
  - 重启后已持久化 subagent 能继续使用 conversation ID 发送消息。
  - 同 agent 高并发压测保持严格串行；不同 agent 可以受全局并发上限控制地并行。
  - 取消/超时不会导致 Manager 永久保持 busy 或泄漏锁/goroutine。
  - 不确定投递结果不会自动重复发送同一消息。
- **验证方式**：故障注入 Fake Provider、restart/store reload 测试、取消/超时/race 测试、真实 OpenCLI 恢复冒烟、`go test ./...`、`go vet ./...`、`git diff --check`。
- **实际实现**：Manager 为每个 subagent 增加独立 serial permit 和有界 pending queue，新增可观察 `busy` 状态与 `max_pending_per_agent` 配置；排队等待支持 context cancellation，超过上限返回带稳定 Task error code `subagent_queue_full` 的错误。从 Store 加载的非 closed agent 标记为待恢复，首次 Send 前调用 Provider `Recover` 并先持久化恢复后的 provider state。OpenCLI Provider 改为显式 Start/Wait，区分“命令尚未启动”与“已启动、远端操作可能已经发生”；Create/Send 在后者发生超时、取消、执行异常、JSON 不可确认或 conversation 漂移时返回 `outcome_unknown`，其 Task error code 为 `subagent_outcome_unknown`，已有 agent 保留 conversation 映射并进入 `degraded`，不自动重发。
- **验证结果**：覆盖同 agent queue 上限、busy 可见性、排队 context 取消、重启后 Recover-before-Send、outcome unknown 保留 conversation、OpenCLI Send 超时与 conversation 漂移，以及 App Task 层 `error_code=subagent_outcome_unknown`。`go test -race` 相关 runtime/provider/capability/App/HTTP/MCP 包、`go test ./...`、`go vet ./...` 和 `git diff --check` 均通过。
- **保留边界**：若 `subagent_spawn` 在远端已经创建 conversation 后才进入 `outcome_unknown`，LocalGate 没有可靠 conversation ID 可写入映射，因此只会明确禁止自动重试，暂不尝试通过 ChatGPT 历史启发式认领未知远端 conversation。

### CH-SUB-005：异步 Submit / Poll / Wait 语义

- **状态**：已完成
- **开始日期**：2026-08-17
- **完成日期**：2026-08-17
- **依赖**：CH-SUB-004
- **优先级**：P0
- **目标**：把 Subagent 从“每次发送都同步等待完整回复”改成真正的异步委派模型：spawn/send 只确认消息已提交，父 Agent 可稍后 poll 或显式 wait 读取结果。
- **非目标**：本轮不做服务端主动推送、callback/webhook、跨消息多级队列，也不允许同一 conversation 在上一轮仍生成时继续注入下一轮消息。
- **涉及范围**：`internal/subagent` Provider/Manager 状态机、`internal/subagent/providers/opencli_chatgpt`、`internal/capabilities/subagent`、App/HTTP/MCP 注册测试、内置 skill 与 README。
- **预计规模**：约 500–900 行有效 diff。
- **设计约定**：
  - `subagent_spawn` 使用 `opencli chatgpt ask --wait=false`；只等 conversation URL/ID 建立，不等 assistant 完整生成，成功后 subagent 保持 `busy`。
  - `subagent_send` 对既有 conversation 使用 `opencli chatgpt send --conversation ...`；只确认注入成功，随后保持 `busy`。
  - 同一 subagent 为 `busy` 时拒绝再次 Send，避免在远端仍生成时重入；父 Agent 先 poll/wait 到 `ready` 再发下一轮。
  - 新增 `subagent_poll`：通过 Provider 只读检查远端生成状态，若已完成则返回最新 assistant reply 并把状态切回 `ready`。
  - 新增 `subagent_wait`：显式等待当前轮稳定完成并返回最新 reply；OpenCLI Provider 使用 `chatgpt detail <conversationId> --wait=true`。
  - `subagent_get/list` 仍只读取 LocalGate 本地元数据，不隐式触发浏览器或远端 I/O。
  - poll/wait 的读取失败不重发消息；已知 conversation 映射继续保留，错误进入 `degraded` 或保持可恢复状态。
- **验收条件**：
  - spawn 不等待模型回复即可返回稳定 `sg_*` 与 `conversationId` 映射。
  - send 不等待 assistant 回复即可返回 accepted；同一 agent busy 时第二次 send 返回稳定 busy 错误且不会调用 Provider。
  - poll 在生成中返回 `busy`，生成完成后返回 `ready + reply`；wait 能阻塞到完成并返回同一结果。
  - 不同 subagent 的 poll/wait/send 仍受全局并发上限控制并可并行；同一 subagent 的 Provider 调用保持串行。
  - 重启后 persisted `busy` subagent 可通过 poll/wait 恢复远端真实状态，而不会自动重发最后一条消息。
  - REST Task、MCP、startup skill 与 README 对异步语义一致。
- **验证方式**：Provider fake executable 参数/JSON 测试、Manager 状态机与重启测试、capability/App/HTTP/MCP 定向测试、相关 `go test -race`、`go test ./...`、`go vet ./...`、`git diff --check`，并在本机真实 OpenCLI 环境做一次只读/低影响 async smoke。
- **实际实现**：Provider 契约拆成异步 `Create` / `Send` 与只读 `Poll` / `Wait`。OpenCLI Create 使用 `chatgpt ask --wait false` 只等 conversation ID，Send 使用 `chatgpt send --conversation` 只确认消息注入，Poll/Wait 使用 `chatgpt detail` 读取生成状态。Manager 在远端提交后持久化 `busy`，busy 时拒绝下一次 Send（`subagent_busy`），degraded 时要求先 poll/wait（`subagent_not_ready`）；重启后 persisted busy 不会自动重发。ProviderState 只保存待处理消息的规范化 SHA-256，用来确认 detail 中对应 User turn 之后确实出现 Assistant turn，完成后清理该 hint。工具面新增 `subagent_poll` / `subagent_wait`，spawn/send 改为 `accepted + busy` 语义；README、MCP skill 和内置 subagent skill 已同步。
- **验证结果**：Manager/Provider/capability/App/REST/MCP 定向测试通过；`go test -race ./internal/subagent ./internal/subagent/providers/opencli_chatgpt ./internal/capabilities/subagent ./internal/app ./internal/httpapi ./internal/mcpgo` 通过；`go test ./...`、`go vet ./...`、`git diff --check` 通过。尝试对已存在的测试 conversation 执行只读 `opencli chatgpt detail` 真实冒烟时，被当前工具宿主的安全层在命令执行前拦截，因此没有产生外部副作用，也不把该真实冒烟记录为已通过。

### CH-SUB-006：Stale Busy 自愈与可解释状态

- **状态**：已完成
- **开始日期**：2026-08-17
- **完成日期**：2026-08-17
- **依赖**：CH-SUB-005
- **优先级**：P0
- **背景**：真实父 Agent 在服务升级后沿用了旧工具认知，只反复调用 `subagent_get/list`，没有调用新增的 `subagent_poll/wait`。由于 get/list 只读本地快照，远端已经完成的 conversation 仍长期显示 `busy`，最终被误判为卡死并 close/re-spawn，产生了不必要的重复 conversation。
- **目标**：让旧客户端只会调用 `subagent_get` 时也能自动收敛 stale busy，同时明确区分“刚提交尚未观察”和“最近真实观察仍在生成”，避免把 `updated_at` 误当作远端心跳。
- **设计约定**：
  - `subagent_get` 默认在本地状态为 `busy` 或 `degraded` 时执行一次非阻塞 Provider Poll；`refresh=false` 可显式要求纯本地快照。
  - refresh 失败时仍返回最新本地诊断状态，并通过 `refresh_error` 暴露错误；不因为一次查询失败自动重发、close 或 spawn。
  - Subagent 持久化 `last_observed_at`，只在成功完成 Poll/Wait 远端观察时更新；`updated_at` 继续表示 LocalGate 本地状态最后变更时间。
  - 工具输出增加派生 `phase`：`submitted` 表示最新远端提交后尚未成功观察，`generating` 表示最近 Poll 确认仍在生成；其余与 `ready/degraded/closed` 对齐。
  - 工具输出增加 `next_action`：busy/degraded 优先 `poll`，ready 为 `send`，closed 为 `none`。任何长期 busy 都不得仅因时长建议 close/re-spawn。
  - `subagent_list` 保持纯本地批量读取，避免列举大量 agent 时产生 N 次远端浏览器 I/O，但同样输出 `phase/last_observed_at/next_action`。
- **验收条件**：
  - busy agent 调用默认 `subagent_get` 时会调用 Provider Poll，并能自动从 stale busy 切到 ready。
  - `refresh=false` 不触发 Provider；原有 get 作为诊断快照的低成本路径仍可用。
  - Poll 后仍 busy 时 `phase=generating` 且 `last_observed_at` 前进；刚 spawn/send 后未 Poll 时 `phase=submitted`。
  - refresh 远端失败时 get 仍返回 subagent 映射、degraded 状态和 `refresh_error`，不创建新 conversation。
  - README / built-in skill 明确禁止“仅因 busy 时间长就 close + respawn”。
- **验证方式**：Manager/tool 回归测试、App/REST/MCP schema 与调用测试、相关 `go test -race`、`go test ./...`、`go vet ./...`、`git diff --check`。
- **实际实现**：`Subagent` 新增持久化 `last_observed_at`，仅在成功 Poll/Wait 后推进。`subagent_get` 新增 `refresh` 参数并默认在 `busy/degraded` 时执行一次非阻塞 Poll；`refresh=false` 保留纯本地快照。默认 refresh 成功会直接返回最新状态/reply，失败则保留 conversation 映射和 degraded 诊断并通过 `refresh_error` 返回，不把一次只读观察失败升级成 close/re-spawn。get/list 状态摘要新增派生 `phase`、`next_action` 与 `last_observed_at`；list 仍保持本地-only，避免批量查询产生 N 次浏览器 I/O。内置 subagent skill、MCP skill 和 README 明确 `updated_at` 不是远端心跳，并禁止仅因长期 busy 就关闭重建 conversation。
- **事故回归**：真实故障序列中，服务升级后的父 Agent 没有重新加载 startup/skill，只会使用旧的 `subagent_get/list`，导致远端已完成的 reviewer 仍长期显示 stale `busy` 并最终被误关闭重建。本 Change 固化了“旧客户端只调用默认 get 也能远端对账”的测试，同时覆盖 `refresh=false` 不触发远端和 refresh 失败仍保留诊断映射。
- **验证结果**：Subagent Manager/capability/App/REST/MCP/meta 定向测试通过；`go test -race ./internal/subagent ./internal/subagent/providers/opencli_chatgpt ./internal/capabilities/subagent ./internal/app ./internal/httpapi ./internal/mcpgo` 通过；`go test ./...`、`go vet ./...`、`git diff --check` 均通过。

### 实施顺序与 MVP 收口

1. **CH-SUB-001** 先只做纯 Go 内核和 Fake Provider，不碰浏览器。
2. **CH-SUB-002** 接 OpenCLI，先证明 `conversationId` 映射和续聊可靠。
3. **CH-SUB-003** 才暴露给 MCP / HTTP Agent 使用；到这里形成可用 MVP。
4. **CH-SUB-004** 在 MVP 可跑后补恢复和不确定结果语义，再考虑扩大使用范围。
5. **CH-SUB-005** 将父 Agent 交互从同步 request/reply 收口为 submit → poll/wait，避免发送工具长时间占用父 Agent。
6. **CH-SUB-006** 让单 agent get 默认自愈 stale busy，并增加可解释的远端观察时间/phase/next action，兼容未刷新工具认知的旧客户端。

### CH-SUB-007：归档删除与 ChatGPT Project 迁移

- **状态**：已完成（2026-08-17）
- **目标**：删除 LocalGate 子 Agent 时保留 ChatGPT conversation，把 conversation 安全迁移到独立归档 Project，并且任何远端失败或不确定结果都不得丢失 `sg_* → conversationId` 映射。
- **实现**：
  - OpenCLI 新增 `chatgpt project-move <conversation> --project <project>`，通过 ChatGPT 可见的 conversation menu 按 Project ID 选择目标，避免依赖本地化菜单文案。
  - Move 前先探测目标 Project conversation route，使重复调用在 conversation 已迁移时直接返回 `Already moved`；点击后再次打开目标 Project 下同一 conversation 并校验 conversation header，未验证成功不报告成功。
  - LocalGate OpenCLI Provider 新增可选 Archive 能力与 `archive_project` 配置；远端命令启动后发生超时、取消、输出损坏等情况统一保持 `outcome_unknown` 语义。
  - `archive_project` 为空时 Archive 退化为纯本地生命周期操作：仍先 Poll 确认当前 turn 已结束，但不调用远端 `project-move`，直接保留 conversation 原位置并删除 `sg_*` 映射。
  - Manager 新增 `Archive`：先 Recover/Poll 确认当前 turn 不再生成，再执行远端归档；成功后先持久化新的 Project 映射，再删除 Store 文件和内存映射。
  - 新增 `subagent_archive` Tool；`subagent_close` 保持原有逻辑关闭语义，不隐式迁移或删除远端历史。
  - 本地配置的归档 Project 为 `g-p-6a82f9e7eb108191898e41d7594cd407`。
- **安全边界**：
  - `subagent_archive_busy` 时保留本地映射且不调用远端 move。
  - Move/验证失败或 `outcome_unknown` 时保留本地映射并标记 degraded，不自动重放或本地删除。
  - 本轮实现与测试不移动任何现有真实 conversation；真实迁移需由后续显式 `subagent_archive` 调用触发。

### CH-SUB-008：Conversation 隔离与 Turn 对账加固

- **状态**：已完成（2026-08-17）
- **目标**：修复两个并发 spawn 绑定同一 ChatGPT conversation，以及 Markdown 渲染差异导致已完成 turn 长期误判 `busy/generating` 的问题。
- **实现**：
  - OpenCLI ChatGPT Provider 对整个 persistent ChatGPT browser surface 做进程内串行化；`ask/send/detail/project-move` 不再并发驱动同一个持久化 tab。Create/Send 仍在提交后快速返回，因此远端 ChatGPT 生成可以并行。
  - Pending turn 从 v1 的“原始文本 SHA-256 完全相等”升级为“`last_observed_index` 基线 + v2 展示文本 fingerprint”。新 turn 必须先出现大于提交前基线的 User row，再出现其后的 Assistant row，才能从 busy 收敛为 ready，避免发送后立即读取旧 DOM 时误判完成。
  - v2 fingerprint 忽略 Markdown 展示符、常见链接包装与标点差异，兼容 ChatGPT DOM 去掉反引号/粗体等格式后的文本；旧 v1 hash 记录保留一次兼容恢复路径。
  - Manager 对 `(provider, conversation_id)` 建立原子 owner claim；第二个不同 `sg_*` 若返回同一 conversation，直接报 `subagent_conversation_conflict`，不创建第二份本地映射，并把已存在 owner 标记为 degraded 以阻止继续静默执行。
  - 从 Store 恢复历史重复映射时，最早记录取得 owner claim；其他重复记录在尝试 send/poll/wait/archive 等远端操作时会被 conversation conflict 防线拦截，保留只读诊断能力。
- **事故回归**：真实故障中 writer/reviewer 两次 spawn 在约 10 秒间隔内重叠执行，最终两个 `sg_*` 都保存了 `6a831944-566c-83ee-9645-fce832cc8cb4`；该 conversation 实际只有 reviewer turn，且 `chatgpt detail` 已显示 `Generating=false`，LocalGate 却因原 instruction 含 Markdown 反引号而无法匹配 User row，持续显示 busy。本 Change 分别固化并发 Create 隔离、conversation owner 唯一性、Markdown→DOM fingerprint 和 legacy pending 恢复测试。

当前最小成功场景：配置一个专用 ChatGPT Project 后，父 Agent 调用 `subagent_spawn`，新会话直接在该 Project 内创建并快速返回 `sg_* + busy`；父 Agent通过 `subagent_poll` 或 `subagent_wait` 取得首轮结果并进入 `ready`，再用同一个 `sg_*` 调用 `subagent_send` 提交下一轮。LocalGate 内部始终路由到同一个 ChatGPT `conversationId`；LocalGate 重启后 persisted busy 会先 poll/wait 对账而不会自动重发；两个不同 `sg_*` 可并发，而同一个 `sg_*` 在当前轮完成前拒绝新的 send。

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
