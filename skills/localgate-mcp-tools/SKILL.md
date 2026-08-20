---
name: localgate-mcp-tools
description: 当用户想了解 LocalGate 工具怎么选、怎么用、参数怎么传时使用这个 skill。优先用于工具选型、参数构造、task 与 session 的区别、默认可见工具和隐藏工具的说明。
---

# LocalGate 工具使用指引

这个 skill 用来指导模型如何使用 `LocalGate.mcp` 暴露出来的工具。

它主要覆盖：

- 发现当前可用工具
- 为任务选择合适工具
- 正确构造工具参数
- 理解哪些工具默认可见
- 判断什么时候该用 task 工具，什么时候该用 session 工具

完整的 HTTP 示例和接口参考见 [api-command-collection.md](file:///Users/bytedance/project/LocalGate.mcp/api-command-collection.md)。

## 设计意图

LocalGate 通过具名工具暴露能力，而不是直接暴露内部实现。使用这些工具时，模型应该遵守这个约定：

- 先选对工具，再构造参数
- 区分一次性任务和会话任务
- 不要假设所有内部能力都会默认暴露

不要假设所有内部能力都会以工具形式暴露。LocalGate 支持 hidden tools，默认可见工具集合也刻意小于完整内部能力面。

另外，LocalGate 还有一种单独的**工具 HTTP 化模式**。在该模式下，MCP 只保留 `get_startup_instructions`、`get_skill`、`get_temporary_access_token`、`get_tool_schema` 四个 bootstrap 工具，其余业务工具改为通过 HTTP 直调。`get_skill` 用于按 startup 的 built-in skill 索引加载复杂跨工具工作流。

## 普通 MCP 模式：稳定 9 工具核心面

普通 MCP 不再把完整 Registry 平铺到 `tools/list`。顶层只保留：

- `get_startup_instructions`
- `get_skill`
- `get_tool_schema`
- `invoke_tool`
- `read_file`
- `apply_patch`
- `run_command`
- `upload_file`
- `export_file`

其中：

- `read_file`、`apply_patch`、`run_command` 是高频基础操作，直接调用。
- `upload_file` 依赖 MCP Host 的文件参数注入元数据；不要藏到通用调用后再猜文件对象。
- `export_file` 需要返回 MCP `resource_link` 等原生资源语义，因此也保持直接暴露。
- `get_startup_instructions` / `get_skill` / `get_tool_schema` / `invoke_tool` 共同构成渐进能力协议。

普通领域工具的标准流程是：

1. 调用 `get_startup_instructions`，从 built-in skill 索引判断能力域。
2. 用 `get_skill` 加载匹配的工作流说明。
3. 对目标工具调用 `get_tool_schema`，获取运行实例中的精确参数契约。
4. 调用 `invoke_tool`：

```json
{
  "tool": "rollback_deployment",
  "arguments": {
    "type": "dynamic",
    "name": "demo",
    "release_id": "release_xxx"
  }
}
```

`invoke_tool` 只是 transport dispatch。审批规则、Task 的 `tool` 字段、审计、root 限制和真实执行语义都按 `rollback_deployment` 这样的目标工具处理，而不是按 `invoke_tool` 处理。

### 渐进能力域

- `localgate-filesystem`：`write_file`、`list_directory`、`search_files`、`run_argv`。
- `localgate-task-session`：`exec_command`、`write_stdin`、`read_thread_terminal`、`close_session`，以及 `task_status`、`task_logs`、`task_cancel`、`wait_task_finish`、`task_result`。
- `localgate-file-transfer`：临时 upload/download token 流程；`upload_file` / `export_file` 本身仍是直接 MCP 工具。
- `localgate-deployment`：`deploy_tool`、list/get/logs/start/stop/restart/access/releases/rollback/delete 等部署能力。
- `localgate-subagents`：启用后提供 `subagent_spawn` / send / poll / wait / get / list / archive / close / artifact_get。
- `localgate-notification`：显式用户请求时使用 `send_notification`。
- `localgate-mcp-relay`：以 `server__tool` 命名的下游 MCP capability，通过 `invoke_tool` 调用，不再铺平到顶层 MCP。

`subagent_spawn` 返回稳定的 `sg_*` ID，并在首轮消息成功提交后立即返回 `busy`，不会等待 assistant 完整回复。之后用 `subagent_poll` 非阻塞检查，或在确实需要结果时用 `subagent_wait`；状态到 `ready` 后再使用 `subagent_send`。这些调用在普通 MCP 模式下都先按 `localgate-subagents` 的说明取 schema，再经 `invoke_tool` 执行。

`deploy_tool` 支持 `type=static` 和 `type=dynamic`。动态部署必须提供 `runtime.argv`，不经过 shell；应用应监听 LocalGate 注入的 `HOST` 和 `PORT`。具体生命周期顺序以 `localgate-deployment` Builtin Skill 为准。

## 工具 HTTP 模式下的最小工具面

当 `tools/list` 只出现下面四个工具时，说明实例运行在工具 HTTP 化模式下：

- `get_startup_instructions`
- `get_skill`
- `get_temporary_access_token`
- `get_tool_schema`

这时不要继续按默认 MCP `tools/call` 模式推荐业务工具。

正确流程是：

1. 先读取 `get_startup_instructions`
2. 如果 startup 中的 built-in skill 与任务匹配，调用 `get_skill` 加载完整工作流说明
3. 调用 `get_temporary_access_token`
4. 从结果中读取：
   - `auth_value`
   - `auth_header_name`
   - `base_url`
   - `call_path_pattern`
5. 从 `get_startup_instructions.tools` 里读取工具名称和描述
6. 对目标工具调用 `get_tool_schema`
7. 再通过 `/tools/{tool}/call` 直接调业务工具 HTTP 接口

工具 HTTP 模式下的 HTTP 请求要同时携带：

- `Authorization: Bearer ...`
- `X-LocalGate-Tool-Access: <auth_value>`

注意：

- 结果字段仍然刻意避免使用 `token`
- `get_startup_instructions.tools` 才是当前真正可直调的业务工具清单
- 如果某个工具需要文件入参，例如 `upload_file`，先调用 `get_tool_schema` 查看其 schema；`upload_file.file` 至少要有 `download_url`
- 如果服务启用了 `tool_arguments.enable_base64=true`，工具参数还可以整体传成一个 base64 字符串；解码后的内容必须仍然是该工具 schema 对应的 JSON object

## 默认隐藏工具

这些工具在 Registry 中存在，但默认不会作为顶层 MCP 工具出现：

- `list_directory`
- `search_files`
- `run_argv`
- `create_upload_token`
- `create_download_token`

原因：

- `list_directory` 和 `search_files` 往往可以由 `run_command` 替代
- `run_argv` 在常见模型驱动流程里与 `run_command` 高度重叠
- `create_upload_token` 和 `create_download_token` 属于跨 sandbox / 跨机器传输的专用能力，不应默认干扰常规工具选择

普通 MCP 模式下，不要因为它们没有出现在 `tools/list` 就判断能力不存在：先加载 `localgate-filesystem` 或 `localgate-file-transfer`，再通过 `get_tool_schema` + `invoke_tool` 使用。Tool HTTP 模式仍以 `get_startup_instructions.tools` 返回的 HTTP 可调用清单为准。

## 工具选型规则

选工具时按下面的规则。凡是不属于稳定 9 工具顶层面的目标工具，在真正调用前先加载对应 Builtin Skill、读取 schema，再通过 `invoke_tool` 执行：

1. 直接读文件内容时用 `read_file`。
2. 整个文件替换时用 `write_file`。
3. 已经知道目标修改内容是 patch 时用 `apply_patch`。
4. 一次性 shell 命令用 `run_command`。
5. 进程需要交互、长时间运行或后续还要写 stdin 时用 `exec_command`。
6. `write_stdin` 只能在 `exec_command` 成功之后使用；空 `chars` 可以只等待/读取，不发送输入。
7. 想读 session 缓冲输出但不写 stdin 时用 `read_thread_terminal`；可用 `wait_ms` 等待新输出，避免高频轮询。
8. Session 不再需要时用 `close_session`；不要用已完成的 `exec_command` Task 的 `task_cancel` 代替 Session 关闭。
9. 需要独立、可持续的委派对话时用 `subagent_spawn`；它只负责 submit，不等待模型完成。
10. 用 `subagent_poll` 检查子 Agent 是否完成；只有当前结果必须立刻拿到时才用 `subagent_wait` 阻塞等待。
11. 不要根据 `busy` 持续时间或 `updated_at` 不变化就判断会话卡死；先看 `last_observed_at`、`phase`、`next_action` 并调用 get/poll 对账。仅为清除 stale busy 而 close + respawn 会制造重复 provider conversation，应禁止。
11. 同一个委派线程到 `ready` 后用 `subagent_send` 复用 `subagent_id`；`busy` 时不要继续 send，也不要通过新 spawn 制造重复对话。
12. 多个互不依赖的 delegated tasks 可以使用不同 subagent 并行。
13. 需要删除 LocalGate 子 Agent 但保留 ChatGPT 历史时优先用 `subagent_archive`。若返回 `subagent_archive_busy`，稍后重试；若返回 `subagent_outcome_unknown`，不要先删本地映射或手工重建，先确认远端 Project 归属再显式重试。
14. 若返回 `subagent_conversation_conflict`，说明两个本地身份试图绑定同一 Provider conversation；这是隔离故障，不要继续向冲突 ID 发消息，也不要用 close/re-spawn 掩盖。先保留诊断信息并修复/清理重复映射。

## `run_command` 使用指引

适用场景：

- 命令是短时执行的
- 用 shell 字符串表达最自然
- 用户想要的是单次结果，而不是持续存在的 session

典型参数：

```json
{
  "command": "pwd",
  "cwd": ".",
  "timeout_ms": 10000
}
```

注意：

- 它走 task 系统
- 非 0 退出码会被视为失败
- 仍然受 command allowlist 和 shell policy 约束

## `exec_command` 使用指引

适用场景：

- 命令可能持续运行
- 用户想要 REPL、dev server、watch 进程或交互式 shell
- 后续还要继续通过 `write_stdin` 发送输入

典型参数：

```json
{
  "cmd": "python3",
  "workdir": ".",
  "yield_time_ms": 200,
  "max_output_items": 100,
  "tty": true
}
```

注意：

- 它会返回一个 `session_id`
- 它也能执行一次性命令，但这不是短任务的默认首选
- 建好 session 之后，后续要用 session 工具，不是 task admin tools

## `apply_patch` 使用指引

适用场景：

- 目标修改已经能明确表达成 patch
- 小范围定点修改比整文件替换更合适

约束：

- 支持 `Add File`、`Update File`、`Delete File`
- 多文件 patch 会先完整验证，并在提交失败时做进程内回滚
- 更新会保留原文件权限并检查目标是否被并发替换
- 不承诺进程崩溃或多个 LocalGate 进程之间的事务原子性
- 不支持 unified diff

典型参数：

```json
{
  "patch": "*** Begin Patch\n*** Update File: path/to/file\n@@\n-old line\n+new line\n*** End Patch"
}
```

## 文件跨 sandbox / 跨机器传输

当文件不在同一个 sandbox、同一台机器，或者调用方无法直接读写 LocalGate 配置 root 路径时，优先考虑下面两个工具，而不是强行使用 `read_file` / `write_file`。

### `create_upload_token`

适用场景：

- 当前 Agent 所在 sandbox 或机器能读到源文件
- 但不能直接写入 LocalGate 配置 root 路径
- 需要把文件推送到 LocalGate 服务所在机器

调用顺序：

1. 调用 `create_upload_token`，传目标绝对路径
2. 读取返回里的 `upload_url`、`auth_header_name`、`auth_value`
3. 对 `/system/uploads` 发 `multipart/form-data`
4. 表单里使用 `file` 字段上传文件内容
5. 在请求头里带上 `auth_header_name: auth_value`

典型返回：

```json
{
  "auth_value": "lgup_xxx",
  "expires_at": "2026-06-25T12:34:56Z",
  "path": "/abs/path/to/target.bin",
  "upload_url": "http://127.0.0.1:8000/system/uploads",
  "auth_header_name": "X-LocalGate-Upload-Token"
}
```

### `create_download_token`

适用场景：

- 文件已经位于 LocalGate 所在 sandbox 或机器中
- 外部 sandbox 或另一台机器需要把它取走
- 调用方不能直接读取该绝对路径

调用顺序：

1. 调用 `create_download_token`，传文件绝对路径
2. 读取返回里的 `download_url`
3. 访问该下载 URL 获取文件内容

典型返回：

```json
{
  "auth_value": "lgdl_xxx",
  "expires_at": "2026-06-25T12:34:56Z",
  "path": "/abs/path/to/file.bin",
  "download_url": "http://127.0.0.1:8000/system/downloads?token=lgdl_xxx",
  "auth_query_key": "token"
}
```

注意：

- 这两个工具的核心用途是跨 sandbox、跨机器传文件
- 普通本地文件读写优先用 `read_file` / `write_file`
- 工具输出字段刻意避免使用 `token` 命名，减少自动脱敏对模型读取结果的影响；但底层 HTTP 协议本身仍然保留现有 header/query 名称
- 如果服务启用了全局 HTTP Bearer 鉴权，上传和下载的 HTTP 请求除了临时凭证之外，还要再带 `Authorization: Bearer ...`

## 宿主文件对象落盘

如果用户在 ChatGPT Apps 或兼容宿主里上传了文件，而不是提供本地绝对路径，优先使用 `upload_file`，不要要求用户先手工下载。

典型参数：

```json
{
  "path": "/abs/path/to/report.pdf",
  "file": {
    "download_url": "https://...",
    "file_id": "file_123",
    "mime_type": "application/pdf",
    "file_name": "report.pdf"
  }
}
```

## 异步任务跟进

LocalGate 里的大多数普通工具调用都会包装成 task。拿到 `task_id` 之后，可以用这些管理工具：

- `task_status`：轻量轮询状态
- `task_logs`：增量读取执行日志
- `wait_task_finish`：等待进入终态，但不拉最终结果体
- `task_result`：获取最终结构化结果
- `task_cancel`：取消运行中的任务

如果只关心任务是否结束，不关心最终结果内容，优先用 `wait_task_finish`。

## 审批任务闭环

任意工具都可能因为全局 `approval.rules` 或静态部署兜底策略进入审批流程。

当工具返回 `status=pending_approval` 时：

1. 这不是工具失败。
2. 向用户展示 `approval_url`，并说明 `summary`、`risk_level` 和 `expires_at`。
3. 保存 `task_id`，不要重复调用原工具；原 task 已经创建并停在 approval gate 前。
4. 用户完成审批后，用 `task_status` 查询该 `task_id`。
5. 任务进入终态后，再用 `task_result` 获取结构化结果和输出。

审批被拒绝、取消或过期时，工具代码不会执行，关联 task 会进入可查询的取消终态。服务重启后，无法恢复内存 gate 的历史 pending 审批也会被统一取消；不要尝试使用旧审批链接重新释放它们。

审批参数和审计详情中的 `token`、`password`、`secret`、`authorization`、`cookie`、`private_key` 等敏感字段会显示为 `[REDACTED]`。

## Session 跟进

对 `exec_command`，按下面顺序处理：

1. 调用 `exec_command`
2. 读取返回里的 `session_id`
3. 保存返回里的 `next_offset`
4. 用 `write_stdin` 继续发输入，并把上次 `next_offset` 作为 `cursor`
5. 用 `read_thread_terminal` 只读缓冲输出，同样持续传递 `cursor`；`has_more=true` 时继续翻页
6. 只有 `completed=true` / `output_complete=true` 才表示终态进程的保留输出已经追平；`buffer_truncated`/`cursor_expired` 分别表示旧缓冲被裁剪/客户端 cursor 已过期

不要对 session 工具使用 `task_status` 或 `task_result`。

Session 属于当前 LocalGate 实例。若输出缓冲已裁剪，检查 `base_offset`、`truncated` 和 `cursor_expired`，并从服务端返回的新 cursor 继续读取。

## 常见错误

避免这些误用：

- 误以为 hidden tools 默认可见
- 把 `exec_command` 当作短平快命令的默认首选
- 给 `apply_patch` 传 unified diff
- 对 session 执行去轮询 task admin tools
- 不考虑 hidden tool 策略，就假设所有工具都能直接被发现和调用
- 把 `pending_approval` 当成失败后自动重放原工具
- 审批通过后重新创建一个同参数任务，而不是查询原 `task_id`
```
