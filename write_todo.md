# Write TODO

以下事项在 Step 01 暂不实现，放入后续步骤或未来迭代：

- 配置文件多路径搜索策略（XDG 规范：`~/.config/localgate/config.yaml`、`/etc/localgate/config.yaml` 等），含优先级与合并逻辑
- 结构化日志后端（zap/logrus 等）与 JSON 日志输出模式切换
- 健康检查扩展（/readyz，包含依赖探测，如持久化可用性）
- CLI 子命令（如 `localgate config validate`、`localgate run`）与命令行自动补全
- 配置热更新（SIGHUP 或文件监控触发）
- 更细粒度的错误码空间分配与文档化

---

## 下一阶段（Step 16–20）拆解

- Step 16：安全命令执行（argv/模板化）
  - 新增 `run_argv` 工具与 Runner argv 执行路径
  - command policy 模板化白名单（命令 + 参数模式）

- Step 17：事件管道的背压与保留策略
  - 每 task 事件上限与截断；全局上限与驱逐策略
  - SSE 心跳/保活与实时通知（替代轮询）

- Step 18：工具 Schema 导出（MCP/HTTP 同源）
  - Registry 生成 inputSchema/outputSchema
  - MCP tools/list 返回 schema；run_command 结构化结果与错误码规范

- Step 19：持久化接线与最小恢复
  - EventSink Append hook 对接 Store.AppendEvent
  - Task 完结写 Store.SaveTask；保留策略落地；启动最小恢复

- Step 20：审批策略配置化与审计查询
  - config 增加 approval policy；HTTP 审计查询与导出（过滤 + 分页）

以下事项在 Step 02–09 实现中标记为延期/后续：

- Task/状态机
  - 失败/超时路径的细化与可复现单测
  - requester/workspace_id 等上下文字段的来源与绑定（待接入 HTTP/MCP）

- 事件/日志
  - 事件 cursor/offset 的跨进程语义与持久化对齐
  - stdout/stderr 的分片大小上限、节流策略与丢弃策略
  - EventSink 的实时通知机制（避免轮询）与 per-connection 背压/缓冲控制
  - SSE 心跳与连接保活策略（避免中间层超时）

- Tool Registry/Schema
  - 工具 JSON Schema 的定义与导出（供 MCP/HTTP 使用）
  - InvokeContext 与 Runner 的日志/取消信号透传

- MCP
  - Streamable HTTP transport 支持（单一路径 POST/GET + SSE），与现有 /tasks API 的复用与边界

- Demo
  - 更贴近真实项目的 demo：支持多文件修改、lint/test 多步骤、失败诊断报告与可导出 artifacts

- 持久化
  - 与 EventSink/Manager 的实时接线（hook 写入器）
  - 保留策略与清理：按天/按条数/按总大小裁剪

- Path Sandbox
  - 更严格的符号链接策略（跨文件系统、循环链接检测），拒绝原因细分
  - 与 Workspace 配置打通 follow_symlink 的默认值与覆盖

- Filesystem 工具
  - 大文件/二进制读取策略（大小上限、截断、编码）
  - 搜索深度限制与忽略规则（如 .git、node_modules）

- Command Policy
  - 参数模板化白名单/黑名单规则
  - argv 级解析与跨平台 shell 差异处理

- Shell Runner
  - 输出大小上限（max_output_bytes）与超量处理策略（截断/中止/丢弃）

- 跨平台
  - Windows 平台的取消/超时子进程治理（Job Object）与抽象层

- HTTP
  - HTTP 鉴权与 requester 身份注入（requester 的可信来源与签名/令牌）
  - 在任务结果中保存并返回 tool 执行结果（result/artifacts），并规范化错误码

- Approval / Audit
  - approval policy 改为配置化（按 workspace/tool/path/command 规则）
  - audit 查询与导出接口（按 approval_id/task_id/requester 过滤）
