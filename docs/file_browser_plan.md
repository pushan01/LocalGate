# File Browser / Online IDE Plan

> 历史设计文档：其中 workspace/root selector API 是旧模型。现行 Files 页面和 Admin API 只使用配置的单一 `root` 与 relative `path`，不存在 workspace/root 选择器。

## 背景

LocalGate 已经具备 workspace 沙箱、文件工具、任务系统、审批、审计、Session 终端和静态 / 动态部署能力。文件浏览器不应该绕过这些控制面能力直接暴露本机文件系统，而应该作为 LocalGate Admin Console 的一个受控功能，复用既有安全边界。

目标不是直接复制 code-server，而是在 LocalGate 内提供一个类似在线 VS Code 的轻量工作台：能浏览、编辑、上传、下载文件，并能在同一个界面里查看任务、审批、终端和部署状态。

code-server 可以作为后续可选的 dynamic deployment 模板存在，但不作为核心文件浏览器的默认实现。

## 设计目标

- 通过浏览器查看和编辑 workspace 内文件。
- 所有路径必须经过 workspace roots、denylist 和 symlink 策略校验。
- 默认不向前端暴露本机绝对路径。Admin API 返回 root display name，具体实现可按配置决定是否返回绝对 root 路径；前端主要使用 `workspace_id + root_id + relative_path`。
- 写入、删除、移动、上传等变更操作可进入现有审批系统。
- 所有文件浏览器变更操作写入审计记录。
- 文件浏览器操作默认挂在 Admin Console 下，不暴露到 deployment 应用面。
- 支持文本文件编辑、二进制文件下载、图片 / Markdown 预览的渐进增强。
- 与现有 Session 终端能力集成，支持在当前目录打开终端。
- 保持与 MCP / Agent 工具解耦：文件浏览器是人用 UI API，Agent 工具仍使用 `read_file`、`write_file`、`apply_patch` 等能力。

## 非目标

- 首版不实现完整 VS Code 扩展系统。
- 首版不实现 LSP、调试器、Remote Extension Host。
- 首版不把 code-server 作为默认文件编辑后端。
- 首版不支持跨 workspace 的任意绝对路径访问。

## 当前状态

截至 2026-07-30：

- Phase 1 只读浏览已完成。
- Phase 2 安全写入、审批与审计已完成。
- Files 页面已经具备基础文本编辑、语言元数据、上传、下载、创建、重命名、删除、ETag 冲突提示和未保存导航保护。
- 管理侧栏可折叠，桌面端可为文件工作区释放更多横向空间。
- 前端源码已迁入 `web/admin/src/`，使用固定版本 esbuild 和 npm lockfile 生成提交到仓库的 `dist/`；默认 Go 构建仍直接 embed 产物，不要求 Node。
- Markdown / 图片预览、冲突 Diff、多标签编辑、受控终端以及只读 Git status / staged 与 unstaged diff 已完成。实时终端流、多终端、通用非 Git Diff、code-server 模板和 LSP 仍属于后续增强。

## 用户体验

Admin Console 新增 Files 页面：

```text
/admin/files
```

页面建议采用三栏布局：

```text
┌──────────────┬────────────────────────┬───────────────┐
│ Workspace    │ Monaco Editor          │ Preview/Meta   │
│ File Tree    │ Tabs                   │ Git/Task Info  │
└──────────────┴────────────────────────┴───────────────┘
┌────────────────────────────────────────────────────────┐
│ Terminal / Task Logs / Approval 状态                    │
└────────────────────────────────────────────────────────┘
```

首版 UI 功能：

- workspace / root 选择器。
- 文件树浏览。
- 打开文本文件。
- Monaco 编辑器。
- 保存文件。
- 新建文件和目录。
- 重命名 / 移动。
- 删除。
- 上传和下载。
- 文件元信息展示。

后续增强：

- 多标签页。
- 未保存状态和离开确认。
- ETag 保存冲突提示。
- Diff 预览。
- Markdown / 图片预览。
- 终端面板。
- 任务日志面板。
- Git status / diff。

## 后端模块

新增独立服务包：

```text
internal/filebrowser/
  service.go
  types.go
  service_test.go
```

该服务负责：

- workspace root 解析。
- 相对路径规范化。
- 统一调用 `security.ValidatePath`。
- 过滤隐藏和排除目录。
- 文件类型探测。
- 大文件限制。
- 文本 / 二进制判断。
- ETag 计算。
- 原子保存。
- 变更审计辅助数据生成。

建议核心类型：

```go
type Root struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

type Entry struct {
    Name     string    `json:"name"`
    Path     string    `json:"path"`
    Type     string    `json:"type"` // file | directory | symlink | other
    Size     int64     `json:"size"`
    ModTime  time.Time `json:"mod_time"`
    ETag     string    `json:"etag,omitempty"`
    Hidden   bool      `json:"hidden"`
    Readable bool      `json:"readable"`
    Writable bool      `json:"writable"`
}

type FileContent struct {
    Path     string    `json:"path"`
    Content  string    `json:"content"`
    Encoding string    `json:"encoding"` // utf-8
    Language string    `json:"language"`
    ETag     string    `json:"etag"`
    Size     int64     `json:"size"`
    ModTime  time.Time `json:"mod_time"`
}
```

## Admin API

新增 Admin API 文件：

```text
internal/adminapi/files.go
```

建议统一挂在 `/api/admin/v1/file-browser/*` 下，避免和未来通用文件 API 或 Agent-facing HTTP API 命名冲突。

注册到：

```go
mux.HandleFunc(Prefix+"/file-browser/workspaces", a.wrap(a.handleFileBrowserWorkspaces))
mux.HandleFunc(Prefix+"/file-browser/files/list", a.wrap(a.handleFileList))
mux.HandleFunc(Prefix+"/file-browser/files/content", a.wrap(a.handleFileContent))
mux.HandleFunc(Prefix+"/file-browser/files/mkdir", a.wrap(a.handleFileMkdir))
mux.HandleFunc(Prefix+"/file-browser/files/rename", a.wrap(a.handleFileRename))
mux.HandleFunc(Prefix+"/file-browser/files/upload", a.wrap(a.handleFileUpload))
mux.HandleFunc(Prefix+"/file-browser/files/download", a.wrap(a.handleFileDownload))
mux.HandleFunc(Prefix+"/file-browser/files/preview", a.wrap(a.handleFilePreview))
mux.HandleFunc(Prefix+"/file-browser/git/status", a.wrap(a.handleFileGitStatus))
mux.HandleFunc(Prefix+"/file-browser/git/diff", a.wrap(a.handleFileGitDiff))
mux.HandleFunc(Prefix+"/file-browser/terminals", a.wrap(a.handleFileTerminalCreate))
mux.HandleFunc(Prefix+"/file-browser/terminals/", a.wrap(a.handleFileTerminalRoutes))
```

接口草案：

### `GET /api/admin/v1/file-browser/workspaces`

返回可浏览 workspace 和 roots。默认只返回展示名和 root id，不要求暴露本机绝对路径。

```json
{
  "items": [
    {
      "id": "default",
      "roots": [
        {"id": "0", "name": "Documents"}
      ]
    }
  ]
}
```

### `GET /api/admin/v1/file-browser/files/list?workspace_id=default&root_id=0&path=LocalGate`

列出目录内容。默认不递归。返回按目录优先、名称排序。

### `GET /api/admin/v1/file-browser/files/content?workspace_id=default&root_id=0&path=LocalGate/README.md`

读取文本内容。超过 `file_browser.max_file_bytes` 或检测为二进制时返回明确错误，引导下载。

### `PUT /api/admin/v1/file-browser/files/content`

保存文本文件。

```json
{
  "workspace_id": "default",
  "root_id": "0",
  "path": "LocalGate/README.md",
  "content": "...",
  "etag": "sha256:...",
  "create": false,
  "overwrite": false
}
```

保存语义：

- `etag` 匹配当前文件时保存。
- `etag` 不匹配返回 `409 file_changed`。
- `create=true` 时仅允许创建不存在的文件。
- `overwrite=true` 可显式覆盖冲突，但必须走审批 / 审计。
- 写入时使用临时文件 + rename，避免部分写入。

### `POST /api/admin/v1/file-browser/files/mkdir`

创建目录。

### `POST /api/admin/v1/file-browser/files/rename`

重命名或移动文件 / 目录，源和目标都必须位于同一 workspace root 内。

### `DELETE /api/admin/v1/file-browser/files`

删除文件或空目录。递归删除需要显式 `recursive=true`，且默认要求审批。

### `POST /api/admin/v1/file-browser/files/upload`

使用 multipart 上传文件到指定目录。需要限制大小，拒绝符号链接覆盖，写入前走路径校验。

### `GET /api/admin/v1/file-browser/files/download`

下载文件。只能下载普通文件，拒绝目录、符号链接和特殊文件。

### `GET /api/admin/v1/file-browser/files/preview`

以内联响应预览受支持的图片。当前只接受经内容嗅探确认的 PNG、JPEG、GIF 和 WebP，继续遵守 workspace sandbox、隐藏和排除路径、symlink 以及 `file_browser.max_file_bytes` 限制。SVG 和其他二进制格式不进入内联预览，前端降级为下载。

### `GET /api/admin/v1/file-browser/git/status`

查询当前目录的只读 Git 状态。返回仓库根的 root 相对路径、分支、upstream、ahead / behind 和 porcelain v2 状态项；状态项路径同样相对选中的 file-browser root。当前目录不在仓库中时返回 `repository=false`，仓库根位于所选 root 外时拒绝。

### `GET /api/admin/v1/file-browser/git/diff`

查询当前目录或文件的 staged / unstaged unified diff，使用 `staged=true|false` 选择索引或工作区。已删除文件通过现存父目录进行 sandbox 校验后仍可查看 Diff。接口不提供 stage、commit、checkout、reset、clean、pull 或 push。

Git 子进程只接受可信系统路径；worktree 根、Git dir 和 common dir 都必须位于选中的 file-browser root 内。执行使用最小环境，并显式禁用 pager、external diff、textconv、fsmonitor、optional locks 以及用户 / 系统配置。status 最多读取 4 MiB 并展示 5000 项，diff 最多 2 MiB，命令最长运行 10 秒；超限或取消返回明确错误。

## 配置

新增配置段：

```yaml
file_browser:
  enabled: true
  read_only: false
  max_file_bytes: 1048576
  max_upload_bytes: 104857600
  show_hidden_files: false
  exclude:
    - ".git"
    - "node_modules"
    - ".localgate"
  require_approval_for_write: true
  require_approval_for_delete: true
```

默认值建议：

- `enabled: true`，仅 Admin Console 可访问。
- `read_only: false`，但写 / 删除默认要求审批。
- `max_file_bytes: 1 MiB`。
- `max_upload_bytes: 100 MiB`。
- `show_hidden_files: false`。
- 默认排除 `.git`、`node_modules`、`.localgate`。

## 安全边界

必须遵守以下规则：

- 前端不直接传绝对路径作为主要定位方式。
- 后端必须把 `workspace_id + root_id + relative_path` 解析成绝对路径后再调用 `security.ValidatePath`。
- 不跟随待写入目标上的符号链接。
- 下载、导出、保存时拒绝特殊文件。
- 大文件和二进制文件只允许下载，不进入文本编辑器。
- 写、删除、移动、上传必须记录审计。
- 浏览器发起的变更操作必须使用现有 Admin Origin 校验。
- 对外开放 Admin Console 时必须依赖 Bearer / 反向代理身份验证。

## 审批与审计

首版可以通过现有任务系统执行写操作：

- 保存文件：复用 `write_file` 或新增 `file_browser.write`。
- 删除文件：新增 `file_browser.delete` 或底层 filesystem 工具。
- 重命名：新增 `file_browser.rename`。
- 上传：新增 `file_browser.upload`。

推荐逐步引入专用操作名，便于审批摘要和审计筛选：

```text
file_browser.write
file_browser.delete
file_browser.rename
file_browser.mkdir
file_browser.upload
```

审批摘要示例：

```text
Save file LocalGate/internal/httpapi/api.go
Delete directory LocalGate/tmp recursively
Rename LocalGate/foo.go to LocalGate/bar.go
Upload file screenshot.png to LocalGate/docs/
```

## Terminal 集成

Admin 与控制面使用独立监听端口，因此 Files 页面不直接跨端口调用控制面的 `/sessions/*`，而是使用同源 Admin API 代理并复用同一个 SessionManager：

- `POST /api/admin/v1/file-browser/terminals`
- `GET /api/admin/v1/file-browser/terminals/{id}`
- `POST /api/admin/v1/file-browser/terminals/{id}/stdin`
- `POST /api/admin/v1/file-browser/terminals/{id}/close`

文件浏览器从当前目录打开终端时，后端先用 `workspace_id + root_id + relative path` 经过 file-browser sandbox 解析为绝对 workdir，再提交审批型 `exec_command` Task。首版使用绝对 cursor 轮询 terminal buffer，后续增加 Admin Console 专用 SSE / WebSocket：

```text
GET /api/admin/v1/file-browser/terminals/{id}/stream
```

Files URL 使用 `terminal_task`、`terminal_session` 和 `terminal_path` 保留关联上下文。需要审批时，审批页通过受限的 `return_to` 返回原 workspace/root/path；Task 产生 `session_id` 后，页面自动接入 Session。轮询只更新终端面板，不重新渲染编辑器，因此不会覆盖未保存内容。

## code-server 集成定位

code-server 不作为默认文件浏览器实现，但可以作为高级动态部署模板：

```json
{
  "type": "dynamic",
  "name": "ide",
  "source": "/Users/yatta/Documents",
  "runtime": {
    "argv": [
      "/path/to/code-server",
      "--bind-addr",
      "127.0.0.1:${PORT}",
      "--auth",
      "none",
      "/Users/yatta/Documents"
    ],
    "cwd": "/Users/yatta/Documents"
  },
  "options": {
    "access": "private",
    "restart_policy": "on-failure"
  }
}
```

使用前提：

- 只能放在 private deployment。
- 外层必须有可信身份认证。
- 明确提示 code-server 内部文件操作和终端不经过 LocalGate 细粒度审批。
- 反向代理必须支持 WebSocket Upgrade。

## 分阶段实施

### Phase 1: 只读浏览 MVP

Status: read-only backend and Admin Console UI implemented.

- [x] 新增 `file_browser` 配置和默认值。
- [x] 新增 `internal/filebrowser` 服务。
- [x] 新增 workspaces、list、content 三个 Admin API。
- [x] Admin Console 增加 Files 页面入口。
- [x] 支持目录树和文本只读查看。
- [x] 补齐路径校验、隐藏文件过滤、大文件错误和测试。

### Phase 2: 安全写入

Status: save-file backend, mkdir backend, rename/move backend, delete backend, download backend/UI, upload backend/UI, approval-backed execution, and basic textarea editing UI implemented.

- [x] 支持保存 / 新建文本文件的后端服务和 Admin API。
- [x] 支持 Files 页面基础文本编辑和保存。
- [x] 支持新建目录后端和 UI。
- [x] 支持重命名 / 移动后端和基础文件 UI。
- [x] 支持删除后端和基础文件 UI；目录递归删除需要显式确认。
- [x] 实现 ETag 冲突检测。
- [x] 写入使用临时文件 + rename。
- 删除递归必须显式确认。
- [x] 保存文件接入审批和审计。
- [x] 新建目录接入审批和审计。
- [x] 上传接入审批和审计。
- [x] 待审批保存 / 上传 payload 落盘 staging，审批执行后清理。
- [x] 重命名 / 移动接入审批和审计。
- [x] 删除接入审批和审计。
- [x] 补齐保存、符号链接、越界路径、只读配置、审批要求配置和 ETag 冲突测试。
- [x] 补齐保存审批流测试。

### Phase 3: 编辑体验

Status: basic editing experience partially implemented; advanced editor integration remains.

- [x] 支持基础文本编辑和保存。
- [x] 支持语言识别元数据。
- [x] 支持 dirty 状态、离开确认和浏览器关闭保护。
- [x] 支持上传 / 下载 UI。
- [x] 支持文件和目录的列表级重命名、删除入口。
- [x] 管理侧栏支持折叠，减少对编辑区横向空间的占用。
- [x] 支持安全的 Markdown 子集预览；原始 HTML 始终转义，不执行脚本和内联 HTML。
- [x] 支持 PNG、JPEG、GIF 和 WebP 图片预览；使用带管理鉴权的 Blob 请求，失败时降级下载。
- [x] 支持 ETag 冲突后的 Reload / Overwrite / 并排 Diff 选择界面。
- [ ] 明确前端依赖管理和构建产物策略后接入 Monaco Editor。
- [x] 支持多标签页和每个标签独立内容、ETag、dirty、预览与冲突状态。
- [ ] 支持通用 Diff 视图。

### Phase 3.5: 工具与 Session 安全加固（已完成）

该阶段对应 `CH-SEC-001`，是终端 UI 的强制前置条件。现有实现审查已确认：命令在省略 `env` 时会继承 LocalGate 全部进程环境；自定义 Shell 仅按 basename 判断；Session 读写缺少 workspace 所有权校验；终端 offset 与缓冲裁剪会重复或跳过输出；超长单行可能被 Scanner 静默丢弃；`upload_file` 缺少 SSRF 防护；部分文件写入不是原子操作；`apply_patch` 会丢失已有权限；读取和遍历缺少资源限制与错误传播。

进入 Phase 4 前必须满足：

- 子进程环境严格由 workspace `env_allowlist` 构造。
- Shell 使用配置允许的可信 canonical path，不能由 workspace 文件伪装。
- Session 的 read、stdin、close 和 REST 路由统一校验 workspace 所有权，并保存调用主体元数据。
- 终端使用客户端 cursor、绝对 offset 和明确的截断 / cursor 过期协议。
- 输出以受限字节块或等价分片读取，长行和读取错误不会静默丢失。
- 远程上传拒绝本地和私有网络目标、重定向绕过，并具有完整超时。
- 文件工具共享失败安全的原子写入流程；Patch 保留权限并准确说明事务边界。
- 读取、列目录和搜索具有大小 / 数量限制、context cancellation 和错误传播。
- 每个已确认缺陷都有永久回归测试，相关并发路径执行 race 测试。

### Phase 4: 终端与任务联动（已完成）

- [x] 从当前目录解析受控 workdir 并启动终端。
- [x] 集成绝对 cursor 轮询、stdin、Ctrl-C 和关闭操作。
- [x] 显示相关 Task / Approval 状态，并支持审批后返回原 Files 上下文。
- [x] 轮询期间保留文件标签、编辑内容和 dirty 状态。
- 后续改造为 SSE / WebSocket 实时流。

### Phase 5: 高级 IDE 能力

- [x] 只读 Git status / staged 与 unstaged diff。
- 可选 code-server dynamic deployment 模板。
- 可选 LSP / 语言服务研究。

## 推荐执行顺序

### CH-FB-002：预览与冲突处理（已完成）

- Markdown 安全预览。
- 图片预览和下载降级。
- ETag 冲突时提供 Reload、显式 Overwrite 和 Diff 入口。
- 保持所有覆盖写入继续经过审批和审计策略。

实现结果：Markdown 预览只渲染受控结构并转义原始 HTML；图片通过受限预览 API 和带鉴权 Blob 加载；ETag 冲突保留本地编辑内容并提供 Reload、Overwrite 和并排 Diff。显式覆盖继续走既有审批任务和审计记录。

### CH-ADMIN-003：前端构建链与 Monaco 决策（已完成）

- 明确是否引入 Node、包管理器和 TypeScript。
- 明确构建产物是否提交仓库，以及 Go embed 如何消费产物。
- 评估 Monaco 体积、Web Worker、CSP 和离线部署影响。
- 完成最小构建链后再接入 Monaco，避免直接手工 vendoring 大型依赖。

实现结果：`web/admin/src/` 是源码入口，esbuild 通过 npm lockfile 生成提交的 `dist/`，Go embed 和默认 Go build 不依赖 Node。Monaco 暂不接入；先在 `CH-FB-003` 中完成多标签状态模块化和编辑器 adapter，再验证同源 Worker、现有 CSP、离线产物、体积和启动成本。CDN、`unsafe-eval` 和默认放宽 Admin CSP 不作为可接受方案。

### CH-FB-003：多标签编辑状态（已完成）

- 多标签页。
- 每标签独立内容、ETag、dirty 和冲突状态。
- 关闭标签、切换 workspace 和浏览器导航时统一保护未保存内容。

实现结果：新增独立标签状态模块，已打开文件在标签切换时直接恢复内存内容，不重复读取；每标签独立维护保存基线、ETag、dirty、Markdown 预览和冲突状态。Files 内部导航保留标签，离开页面和关闭 dirty 标签时统一确认；直接重命名、移动和删除会同步标签，等待审批时保留编辑上下文。

### CH-SEC-001：工具与 Session 安全加固（已完成）

- `CH-SEC-001A`：执行环境与可信 Shell。
- `CH-SEC-001B`：Session 所有权、游标与输出可靠性。
- `CH-SEC-001C`：`upload_file` SSRF 防护。
- `CH-SEC-001D`：原子写入、权限保留与 Patch 事务语义。
- `CH-SEC-001E`：读取和目录遍历资源限制。

五个子项均已完成并通过全量、静态与相关竞态测试，Files 终端 UI 已解除阻塞。

### CH-FB-004：终端与任务联动（已完成）

- 前置条件：`CH-SEC-001` 全部验收通过。
- 从当前目录创建 Session。
- 首版轮询终端缓冲，后续再升级 SSE / WebSocket。
- 在 Files 页面展示相关 Task 和 Approval 状态，并支持审批完成后返回原上下文。

实现结果：新增 Admin 同源终端 API，当前目录经 file-browser sandbox 解析后提交 `exec_command` Task；前端通过 `terminal_task` 轮询 Task，取得 `session_id` 后切换到绝对 cursor Session 轮询，并支持 stdin、Ctrl-C 和关闭。待审批操作使用受限 `return_to` 返回原文件上下文，轮询不重渲染文件编辑器。实时流、多终端标签和浏览器重启恢复继续作为后续独立增强。

### CH-FB-005：高级工作区能力

- [x] `CH-FB-005A`：只读 Git status / staged 与 unstaged diff。
- 可选 code-server private dynamic deployment 模板。
- LSP / 语言服务只作为后续研究项，不阻塞轻量工作台交付。

`CH-FB-005A` 实现结果：新增 sandbox-bound Git 服务和 Admin 同源 status / diff API。worktree 根、Git dir 和 common dir 都必须位于选中 root 内；Git 使用可信系统路径、最小环境、超时和输出上限，并禁用 external diff、textconv、pager、fsmonitor、optional locks 与用户 / 系统配置。Files Git 面板可刷新状态、查看当前目录或单文件 staged / working diff，并从状态项打开文件；导航期间保留其他 dirty 标签。

## 测试计划

- workspace root 内路径允许。
- root 外路径拒绝。
- `../` 越界拒绝。
- symlink 指向 root 外拒绝。
- 写入目标为 symlink 时拒绝。
- `.git`、`node_modules`、`.localgate` 默认过滤。
- 大文件读取返回错误。
- 二进制文件读取返回错误。
- ETag 冲突返回 `409 file_changed`。
- create-only 保存目标已存在返回冲突。
- rename 源 / 目标越界拒绝。
- recursive delete 需要显式参数和审批。
- Admin Origin 校验覆盖浏览器变更请求。
- 审批通过后继续执行同一个 Task。
