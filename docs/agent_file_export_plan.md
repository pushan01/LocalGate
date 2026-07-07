# LocalGate -> Agent 文件导出方案

## 1. 目标

新增从 LocalGate workspace 向远程 Agent 宿主传输文件的能力，并以 ChatGPT Apps 作为首批支持对象。

首版目标不是假设 Agent 沙箱存在一个可写的绝对路径，而是让 LocalGate 通过 MCP 返回宿主可识别的文件资源。ChatGPT 接收后负责展示下载、取得用户授权，并将文件纳入宿主自己的文件处理流程。

方向定义：

```text
Agent / ChatGPT -> LocalGate
upload_file

LocalGate -> Agent / ChatGPT
export_file
```

## 2. 当前基础

LocalGate 已经具备大部分底层能力：

- `upload_file` 接受 ChatGPT 文件对象，将远程附件保存到 workspace。
- `create_download_token` 为 workspace 文件生成短期 HTTP 下载 URL。
- `/system/downloads` 校验临时令牌并使用 `Content-Disposition` 返回文件。
- MCP stdio 和 mcp-go 两套传输均支持 `structuredContent`。
- 工具元数据已经支持 `_meta["openai/fileParams"]`。
- workspace 路径校验、任务、审批、审计和 HTTP 鉴权已经存在。

现有 `create_download_token` 不能直接作为 ChatGPT 文件导出工具：

- 工具默认隐藏，语义偏向底层跨机器凭证，而不是用户可见文件结果。
- MCP 结果只有文本和 `structuredContent`，没有 `resource_link` 内容块。
- 下载 URL 在开启全局 HTTP Bearer 时仍要求额外的 `Authorization` 请求头。
- 当前授权在内存中保存，可重复下载直到过期，没有独立的导出策略和审计事件。
- 返回结果没有 `outputSchema`、文件大小、MIME、摘要等宿主友好元数据。

## 3. ChatGPT 首版协议选择

### 3.1 首选：MCP `resource_link`

`export_file` 返回标准 MCP 内容块：

```json
{
  "type": "resource_link",
  "uri": "https://files.example.com/system/exports/lgexp_xxx?signature=...",
  "name": "report.pdf",
  "description": "Exported from LocalGate workspace",
  "mimeType": "application/pdf",
  "size": 182736,
  "annotations": {
    "audience": ["user"],
    "priority": 1
  }
}
```

原因：

- MCP 标准原生支持。
- ChatGPT 明确支持把工具返回的 `resource_link` 作为可下载文件。
- LocalGate 可以签发 HTTPS 能力 URL，不需要调用 ChatGPT 私有上传 API。
- 其他 MCP 宿主也可以逐步兼容。

### 3.2 后续增强：ChatGPT tool file reference

在完成兼容性验证后，可同时在 `structuredContent.file` 返回：

```json
{
  "download_url": "https://files.example.com/system/exports/lgexp_xxx?signature=...",
  "file_id": "lgexp_xxx",
  "mime_type": "application/pdf",
  "file_name": "report.pdf"
}
```

ChatGPT 文件引用要求 `download_url` 与 `file_id`。需要先验证 ChatGPT 是否接受服务端自定义的 `file_id`，以及 `window.openai.getFileDownloadUrl` 对这种 ID 的刷新语义。未验证前，`resource_link` 是首版基线，不依赖该行为。

## 4. 新工具设计

工具名：`export_file`

描述：将 workspace 内一个现有文件导出给当前 Agent 宿主。该操作会把本地数据暴露到 LocalGate 之外，默认需要审批。

### 4.1 输入

```json
{
  "path": "/absolute/workspace/output/report.pdf",
  "file_name": "project-report.pdf",
  "ttl_seconds": 300
}
```

字段：

- `path`：必填，workspace 内绝对文件路径。
- `file_name`：可选，宿主显示的文件名，不改变源文件。
- `ttl_seconds`：可选，建议限制为 60 到 600 秒，默认 300 秒。

首版只支持普通文件。目录、FIFO、socket、设备文件和符号链接拒绝。目录打包为 ZIP 放到后续版本。

### 4.2 结构化输出

```json
{
  "status": "ready",
  "export_id": "lgexp_xxx",
  "file_name": "project-report.pdf",
  "mime_type": "application/pdf",
  "size": 182736,
  "sha256": "...",
  "expires_at": "2026-07-07T12:05:00Z"
}
```

能力 URL只放在 MCP `resource_link.uri` 中。HTTP API 调用方如确实需要 URL，可以额外返回 `download_url`，但任务日志和审计展示必须隐藏 query 中的签名。

### 4.3 工具声明

建议增加以下可选工具接口：

```go
type ToolOutputSchemaProvider interface {
    OutputSchema() any
}

type ToolAnnotationsProvider interface {
    ToolAnnotations() map[string]any
}

type ToolResultContentProvider interface {
    ResultContent(result map[string]any) []map[string]any
}
```

`export_file` 使用：

```json
{
  "readOnlyHint": true,
  "destructiveHint": false,
  "openWorldHint": true
}
```

`readOnlyHint` 表示不修改源文件；`openWorldHint` 表示结果会被外部宿主获取。安全决策仍由 LocalGate 审批策略完成，不能只依赖 MCP annotation。

## 5. MCP 结果层改造

当前两套 MCP 传输都把结果转换为一个 JSON 文本块，并设置 `structuredContent`。需要在保持兼容的前提下追加内容块。

建议不修改所有现有工具的 `Execute` 签名，而是增加 `ToolResultContentProvider`：

1. 工具执行结果仍保存为 `map[string]any`。
2. MCP transport 根据 `Task.Tool` 找到工具实例。
3. 如果工具实现 `ToolResultContentProvider`，调用 `ResultContent(result)`。
4. 返回内容由原 JSON 文本块加上 `resource_link` 组成。
5. HTTP 工具接口继续返回原结构化 JSON，不受影响。

需要覆盖：

- `internal/mcp/stdio.go`
- `internal/mcpgo/server.go`
- 直接工具调用结果
- `task_result`
- `wait_task_finish`
- 审批后继续执行的任务结果

同时在 `tools/list` 中增加可选 `outputSchema` 和 `annotations`。

## 6. Export Service

新增包：`internal/export`

建议模型：

```go
type Grant struct {
    ID          string
    SecretHash  [32]byte
    WorkspaceID string
    Path        string
    FileName    string
    MIMEType    string
    Size        int64
    SHA256      string
    FileID      FileIdentity
    ExpiresAt   time.Time
}
```

能力 URL使用至少 256 bit 随机密钥。服务端只保存密钥摘要，不保存明文签名。

### 6.1 文件一致性

首版不复制文件到导出缓存，避免大文件双倍占用。签发时记录：

- canonical path
- 文件大小
- modification time
- 平台可用时记录 device/inode
- SHA-256

下载时：

1. 再次执行 workspace 路径校验。
2. 拒绝符号链接和非普通文件。
3. 使用 `os.Open` 得到文件描述符。
4. 对打开后的文件执行 `Stat` 并核对 identity、大小与修改时间。
5. 使用该文件描述符配合 `http.ServeContent` 输出，避免检查后再次按路径打开。

如果文件已经变化，返回 `409 export_source_changed`，要求重新调用 `export_file`。

### 6.2 生命周期

首版 grant 可保存在内存：

- 默认 TTL 5 分钟。
- 服务重启后全部失效。
- 不提供列表接口。
- 支持显式 revoke，供后续管理后台使用。

不要设计成严格单次 GET。ChatGPT、代理和 CDN 可能发送 HEAD、Range 或重试请求。首版依赖短 TTL 和高强度能力 URL，可限制总请求数，但 HEAD 不计数，Range 请求需要允许合理重试。

## 7. HTTP 接口

新增：

```text
GET /system/exports/{export_id}?signature=...
HEAD /system/exports/{export_id}?signature=...
```

响应头：

```text
Content-Type: application/pdf
Content-Disposition: attachment; filename="report.pdf"; filename*=UTF-8''...
Content-Length: ...
Cache-Control: private, no-store
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
```

支持 HTTP Range，便于大文件和宿主重试。

### 7.1 与 Bearer/OAuth 的关系

导出 URL本身是短期 capability，不能再要求 ChatGPT 添加 LocalGate 固定 Bearer 请求头。

推荐部署方式：

```yaml
file_exports:
  enabled: true
  base_url: "https://files.example.com"
  default_ttl_seconds: 300
  max_ttl_seconds: 600
  max_file_bytes: 104857600
  require_approval: true
```

`files.example.com` 通过 Tunnel 指向 LocalGate，但只允许 `/system/exports/*`。该路由不经过交互式 Cloudflare Access；LocalGate 自己验证高强度短期签名。MCP、管理后台和普通 HTTP API 继续由 Cloudflare Access 或 LocalGate Bearer 保护。

如果使用同一域名，服务器需要在全局 Bearer middleware 之前挂载 export handler，并且只允许签名有效的请求通过。不要把整个 `/system` 加入匿名 allowlist。

## 8. 安全策略

文件导出属于数据外传，默认按高风险操作处理。

必须具备：

- workspace 路径校验在签发和下载两个阶段都执行。
- 拒绝目录、符号链接和非普通文件。
- 文件大小上限，默认建议 100 MiB。
- TTL 上限，调用方不能无限延长。
- MIME 只作为展示提示，响应必须设置 `nosniff`。
- capability 不出现在事件日志、错误消息、审计详情和管理页面 URL 中。
- 审计只记录 `export_id`、workspace、源路径、大小、摘要、调用者、过期时间和下载状态。
- 下载失败不能返回真实本机路径。
- 工具默认需要审批，并在审批摘要中明确显示文件名、路径、大小和目标宿主。
- `path_denylist` 继续生效。
- 默认不允许导出 LocalGate store、凭证目录和 SSH/keychain 等敏感位置，除非 workspace 明确允许且审批通过。

建议审计事件：

- `file_export_issued`
- `file_export_downloaded`
- `file_export_expired`
- `file_export_revoked`
- `file_export_failed`

## 9. ChatGPT 验证流程

首版不需要 Widget。通过 ChatGPT Developer Mode 连接 Remote MCP 后验证：

1. 用户要求“把项目生成的 report.pdf 发给我”。
2. ChatGPT 调用 `export_file`。
3. LocalGate 创建审批，摘要显示源文件与外传风险。
4. 用户批准后，任务继续并返回 `resource_link`。
5. ChatGPT 显示文件下载入口，并在抓取文件前取得用户确认。
6. 下载内容 SHA-256 与 LocalGate 输出一致。
7. TTL 到期后 URL 返回 `410 Gone`。

兼容性验证项：

- ChatGPT Web、macOS 和移动端是否都显示文件。
- 是否先发 HEAD，再发 GET。
- 是否使用 Range。
- 重试次数和 User-Agent 变化。
- 非 ASCII 文件名。
- PDF、ZIP、JSON、图片和纯文本。
- `resource_link` 是否可被后续模型回合继续引用。
- app-defined `file_id` 是否可用于 `getFileDownloadUrl`。

## 10. 测试计划

### 单元测试

- grant 签发、过期、撤销、错误签名。
- TTL 和大小限制。
- MIME 与文件名处理。
- 路径越界、denylist、symlink、目录和特殊文件拒绝。
- 文件签发后变化时拒绝下载。
- HEAD、GET、Range 和重复请求。
- 日志与错误中不出现签名。

### MCP 测试

- stdio 返回 JSON 文本、`structuredContent` 和 `resource_link`。
- mcp-go 返回同样的内容结构。
- `task_result` 和 `wait_task_finish` 保留文件内容块。
- `tools/list` 包含 `outputSchema` 和 annotations。
- 旧客户端忽略 `resource_link` 后仍可读取文本结果。

### 集成测试

- 全局 Bearer 开启时，签名 export URL仍可访问，其他匿名路由仍为 401。
- Cloudflare Tunnel 下 HTTPS 地址可被 ChatGPT 获取。
- 审批拒绝时不签发 grant。
- LocalGate 重启后旧 grant 失效。

## 11. 分阶段实施

### Phase 1：标准 MCP 文件导出

- `internal/export` grant service。
- `/system/exports/{id}`。
- `export_file` 工具。
- `resource_link` MCP 内容输出。
- 配置、审批、审计和测试。
- ChatGPT Developer Mode 实测。

### Phase 2：ChatGPT 文件引用增强

- 在 `structuredContent` 中增加 tool file reference。
- 验证 `file_id` 与 `getFileDownloadUrl` 行为。
- 可选 ChatGPT Widget，展示文件信息、过期时间和重新签发按钮。

### Phase 3：通用 Agent 适配

- 抽象 `AgentFileExportAdapter`。
- 支持只识别普通 URL 的 MCP 客户端。
- 支持能够主动拉取 URL 到 sandbox 的 Agent。
- 根据宿主能力协商 `resource_link`、file reference 或普通结构化 URL。

### Phase 4：目录和批量导出

- 显式 ZIP 打包。
- 多文件 manifest。
- 导出缓存、断点续传和更大文件限制。
- 管理后台导出记录与手动撤销。

## 12. 首版验收标准

- ChatGPT 能从 `export_file` 结果中展示并下载 LocalGate 文件。
- 文件内容、大小和 SHA-256 与源文件一致。
- 不需要把 LocalGate 固定 Bearer 或 Cloudflare Access Token 放入 URL。
- 未审批、过期、篡改签名、路径变化和越界访问都无法下载。
- MCP stdio、Streamable HTTP MCP 和现有 HTTP API 测试全部通过。
- 旧工具与现有 Agent -> LocalGate 上传链路不受影响。
