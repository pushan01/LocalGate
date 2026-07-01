# Plan

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
