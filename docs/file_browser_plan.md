# File Browser / Online IDE Plan

## 背景

LocalGate 已经具备 workspace 沙箱、文件工具、任务系统、审批、审计、Session 终端和静态 / 动态部署能力。文件浏览器不应该绕过这些控制面能力直接暴露本机文件系统，而应该作为 LocalGate Admin Console 的一个受控功能，复用既有安全边界。

目标不是直接复制 code-server，而是在 LocalGate 内提供一个类似在线 VS Code 的轻量工作台：能浏览、编辑、上传、下载、搜索文件，并能在同一个界面里查看任务、审批、终端和部署状态。

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

截至 2026-07-29：

- Phase 1 只读浏览已完成。
- Phase 2 安全写入、审批与审计已完成。
- Files 页面已经具备基础文本编辑、语言元数据、上传、下载、创建、重命名、删除、ETag 冲突提示和未保存导航保护。
- 管理侧栏可折叠，桌面端可为文件工作区释放更多横向空间。
- 当前前端仍是无 Node 构建链的原生 JavaScript 和嵌入式静态资源；引入 Monaco 前需要先明确依赖管理、构建和产物提交方式。
- 文件名搜索、Markdown / 图片预览、多标签页、Diff 合并、终端面板和 Git 联动尚未完成。

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
- 基础文件名搜索。

后续增强：

- 多标签页。
- 未保存状态和离开确认。
- ETag 保存冲突提示。
- Diff 预览。
- Markdown / 图片预览。
- 终端面板。
- 任务日志面板。
- Git status / diff。
- 全文搜索。

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
mux.HandleFunc(Prefix+"/file-browser/files/search", a.wrap(a.handleFileSearch))
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

首版终端可复用现有 Session API：

- `POST /sessions/exec`
- `POST /sessions/{id}/stdin`
- `GET /sessions/{id}/terminal`
- `POST /sessions/{id}/close`

文件浏览器从当前目录打开终端时，传入对应 workdir。首版可轮询 terminal buffer，后续增加 Admin Console 专用 SSE / WebSocket：

```text
GET /api/admin/v1/terminals/{id}/stream
POST /api/admin/v1/terminals/{id}/stdin
```

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
- [ ] 支持基础文件名搜索和快速打开。
- [ ] 支持 Markdown / 图片预览。
- [ ] 支持 ETag 冲突后的 Reload / Overwrite / Diff 选择界面。
- [ ] 明确前端依赖管理和构建产物策略后接入 Monaco Editor。
- [ ] 支持多标签页和每个标签独立 dirty 状态。
- [ ] 支持通用 Diff 视图。

### Phase 4: 终端与任务联动

- 从目录打开终端。
- 集成 Session 输出面板。
- 显示相关 Task / Approval 状态。
- 后续改造为 SSE / WebSocket 实时流。

### Phase 5: 高级 IDE 能力

- Git status / diff。
- 全文搜索。
- 可选 code-server dynamic deployment 模板。
- 可选 LSP / 语言服务研究。

## 推荐执行顺序

### CH-FB-002：文件名搜索与快速打开

- 后端增加受 workspace、root、隐藏文件和 exclude 策略约束的文件名搜索。
- Admin API 增加有界 `limit` 的搜索接口。
- Files 页面增加搜索框、结果列表和键盘快速打开。
- 搜索结果只返回相对路径和必要元数据，不暴露绝对路径。

这是下一项，原因是改动独立、风险低、无需引入前端构建链，同时能明显改善大目录下的使用效率。

### CH-FB-003：预览与冲突处理

- Markdown 安全预览。
- 图片预览和下载降级。
- ETag 冲突时提供 Reload、显式 Overwrite 和 Diff 入口。
- 保持所有覆盖写入继续经过审批和审计策略。

### CH-ADMIN-003：前端构建链与 Monaco 决策

- 明确是否引入 Node、包管理器和 TypeScript。
- 明确构建产物是否提交仓库，以及 Go embed 如何消费产物。
- 评估 Monaco 体积、Web Worker、CSP 和离线部署影响。
- 完成最小构建链后再接入 Monaco，避免直接手工 vendoring 大型依赖。

### CH-FB-004：多标签编辑状态

- 多标签页。
- 每标签独立内容、ETag、dirty 和冲突状态。
- 关闭标签、切换 workspace 和浏览器导航时统一保护未保存内容。

### CH-FB-005：终端与任务联动

- 从当前目录创建 Session。
- 首版轮询终端缓冲，后续再升级 SSE / WebSocket。
- 在 Files 页面展示相关 Task 和 Approval 状态，并支持审批完成后返回原上下文。

### CH-FB-006：高级工作区能力

- Git status / diff。
- 全文搜索。
- 可选 code-server private dynamic deployment 模板。
- LSP / 语言服务只作为后续研究项，不阻塞轻量工作台交付。

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
