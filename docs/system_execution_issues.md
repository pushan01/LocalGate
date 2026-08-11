# LocalGate 系统执行与环境模型问题记录

## 1. PATH 行为不一致问题（已修复）

### 问题描述
run_command 使用 login shell：

```
/bin/sh -lc
```

导致 macOS path_helper 介入并重排 PATH。

### 修复
改为：

```
/bin/sh -c
```

### 影响
- Conda / system Python 顺序不一致
- run_command 与 exec_command 行为不同

---

## 2. run_command vs run_argv 语义分裂

| 模式 | PATH解析 | shell | 行为 |
|------|---------|------|------|
| run_command | yes | yes | 非确定 |
| run_argv | yes | no | 更确定 |

问题：行为不一致导致 MCP tool 不可预测。

---

## 3. env_allowlist 模型问题（PATH 部分已修复）

### 当前实现
业务环境变量仍基于 os.Environ snapshot + whitelist filter；`PATH` 作为执行基础环境始终继承 LocalGate 服务进程值。

### 已修复
- `run_command` / `run_argv` / session shell 使用同一服务进程 PATH 基线
- startup 的可执行文件探测与 shell 执行使用一致的 PATH 来源
- request env 只有在 `PATH` 显式进入 `env_allowlist` 时才能覆盖 PATH

### 剩余问题
- 非 inheritance model
- 除 PATH 外的 request env 覆盖语义仍依赖 allowlist

---

## 4. execution 模型未统一

当前存在：
- shell execution
- argv execution
- session execution

缺少 ExecutionSpec 统一模型

---

## 5. upgrade 系统风险

- binary replace 非 atomic
- control plane 与 runtime 耦合
- upgrade = capability injection point

---

## 6. MCP tool 体系膨胀

已接近 mini control plane API，但缺少：
- versioned tool schema
- resource model
- lifecycle model

---

## 7. 安全模型问题

缺少 capability boundary 和 execution isolation level

---

## 8. 总体结论

系统已进入 runtime 阶段，但抽象层未收敛。


## 11. Static Hosting Implementation Issues (Latest)

### 11.1 API layer pollution

Static hosting logic was partially injected into `internal/httpapi/api.go`, including:

- SPA handler implementation inside API file
- statusResponseWriter embedded in HTTP layer
- direct mux.Handle injection for /static

Impact:
- API layer becomes mixed control plane + content plane
- violates separation of concerns

---

### 11.2 Missing service abstraction

Registry and static hosting logic are not encapsulated in a service layer.

Current issues:
- registry is not actively used in runtime routing
- HTTP handler directly manages filesystem behavior
- no static service boundary exists

---

### 11.3 Deploy implementation coupling

Current deploy mechanism uses:

- system `cp -R`
- direct filesystem overwrite

Issues:
- non-atomic operations
- platform dependency
- no diff / versioning support

---

### 11.4 SPA middleware coupling

SPA fallback implemented inside HTTP handler wrapper:

- tightly coupled to FileServer response codes
- no routing-level awareness
- cannot support per-site routing rules

---

### 11.5 Architectural risk

System is drifting toward monolithic API file:

- multiple domains mixed in api.go
- increasing difficulty of reasoning about execution flow

---

### 11.6 Recommended refactor direction

- introduce internal/static/service.go
- move SPA logic into middleware layer
- decouple registry from HTTP API
- replace cp -R with Go-native copy or diff sync
