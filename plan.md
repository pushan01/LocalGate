# Plan

## 下一项：CH-FB-002 文件名搜索与快速打开

- **状态**：待开始
- **目标**：让用户在大型 workspace 中按文件名快速定位并打开文件，不需要逐层展开目录。
- **非目标**：本轮不做全文搜索、Git 搜索、Monaco、多标签页或终端集成。
- **涉及范围**：`internal/filebrowser`、`internal/adminapi/files.go`、`web/admin/dist/app.js`、`web/admin/dist/styles.css`、相关测试和文件浏览器文档。
- **依赖与风险**：搜索必须复用隐藏文件、exclude、workspace sandbox 和 symlink 安全边界；结果数量必须有上限，避免大 workspace 中的无界遍历和响应。
- **预计规模**：约 350–650 行有效 diff。
- **验收条件**：
  - 支持按文件名片段搜索当前 workspace root。
  - 搜索忽略大小写，过滤隐藏和排除目录，并拒绝路径逃逸。
  - API 使用有界 `limit`，返回相对路径、类型、大小和修改时间。
  - Files 页面可输入关键词、查看结果并打开文件或进入目录。
  - 搜索和结果导航遵守现有未保存内容保护。
- **验证方式**：`node --check web/admin/dist/app.js`、文件浏览器与 Admin API 定向测试、`git diff --check`、`go test ./...`。

后续顺序：`CH-FB-003` 预览与冲突处理 → `CH-ADMIN-003` 前端构建链与 Monaco 决策 → `CH-FB-004` 多标签状态 → `CH-FB-005` 终端与任务联动。

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
