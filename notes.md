# Notes

## 执行层的 task_id 从哪里获取

- task_id 不是“从哪里查到”的，而是**执行层在创建任务时生成并返回给调用方**的稳定句柄；后续所有 `status/logs/cancel` 都围绕同一个 task_id 进行。

### HTTP（任务式 API）

- 调用方创建任务：`POST /tasks`
- 服务端返回（示例）：

```json
{ "task_id": "01J....", "status": "queued" }
```

- 调用方后续使用：
  - `GET /tasks/{task_id}`：查询状态与结果
  - `GET /tasks/{task_id}/logs`：拉取日志（分页/offset）
  - `POST /tasks/{task_id}/cancel`：取消任务

### MCP（Tool 调用）

- 如果工具是“异步任务式”语义：MCP 的一次 tool 调用返回里直接携带 `task_id`（作为结构化字段），表示任务已创建但可能仍在执行。
- 后续通过 MCP 暴露的管理工具继续操作同一任务（示例命名）：
  - `task_status(task_id)`
  - `task_logs(task_id, cursor?)`
  - `task_cancel(task_id)`

### 生成方式（实现层面）

- 由执行层 `create_task()` 生成，常见选择：UUID / ULID / 雪花 ID。
- 设计要求：
  - 全局唯一（至少在单机/单实例范围内强唯一）
  - 可安全暴露（不要包含路径、用户名等敏感信息）
  - 可用于存储索引与查询（长度、字符集、排序需求按实现取舍）

### 客户端自带 ID（可选增强）

- 可支持 `client_request_id` 或 `idempotency_key`（客户端传入）用于幂等重试与去重。
- 推荐做法：**服务端仍生成权威 `task_id`**，并将客户端键作为辅助索引/关联字段，避免把外部不受控 ID 当作主键。

