# Notes

## LocalGate 自重启：必须使用脱离当前请求的延迟重启器

当 LocalGate 正通过自己的 MCP / `run_command` 执行维护操作时，**不要在当前同步命令里直接 `kill` LocalGate，再继续执行启动命令**。当前请求本身由这个 LocalGate 进程承载；daemon 一旦退出，后半段 shell 可能随 transport 一起中断，造成服务停在“已杀旧进程、未启动新进程”的中间状态。

错误模式：

```sh
kill "$old_pid"
# 不可靠：承载当前 run_command 的 LocalGate 已经退出
nohup ./bin/localgate -config ./localgate.yaml ... &
```

通过 LocalGate 自己发起重启时，应先创建一个**与当前请求生命周期脱离的后台 shell**，让当前 `run_command` 先返回 `restart-scheduled`；后台 shell 延迟一小段时间后再停止旧 daemon，并启动新 daemon。

示意流程：

```text
当前 LocalGate
  ├─ 启动 detached restart shell
  └─ 当前 run_command 返回 restart-scheduled
       ↓
   detached shell sleep
       ↓
   SIGTERM 旧 LocalGate
       ↓
   等待旧 daemon 退出
       ↓
   清理确认属于旧 daemon 的遗留子进程
       ↓
   启动新 LocalGate
       ↓
   写入新的 .localgate/run/localgate.pid
```

一个可用的基本模式如下；实际执行前仍应先读当前 PID、命令行和进程树，不要盲信陈旧 pid 文件：

```sh
old_pid="$(ps ax -o pid=,command= | awk '/[b]in\/localgate -config .*localgate\.yaml/ {print $1; exit}')"

# 在旧 daemon 还活着时记录它的直接子进程，供后续定向识别 orphan。
old_children="$(pgrep -P "$old_pid" 2>/dev/null || true)"

nohup /bin/sh -c '
  sleep 1

  old_pid="$1"
  old_children="$2"
  root="$3"

  kill -TERM "$old_pid" 2>/dev/null || true

  i=0
  while kill -0 "$old_pid" 2>/dev/null && [ "$i" -lt 50 ]; do
    sleep 0.1
    i=$((i + 1))
  done

  # 旧 daemon 没有在宽限期内退出时，不要再启动第二个 LocalGate。
  if kill -0 "$old_pid" 2>/dev/null; then
    exit 1
  fi

  # 只处理重启前明确记录过的旧子进程；不要按 PPID=1 全局扫杀。
  for pid in $old_children; do
    if kill -0 "$pid" 2>/dev/null; then
      ppid="$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d " ")"
      cmd="$(ps -o command= -p "$pid" 2>/dev/null || true)"
      cwd="$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -1)"
      case "$cmd $cwd" in
        *"$root/.localgate/dynamic/apps/"*|*"$root/bin/localgate"*)
          [ "$ppid" = "1" ] && kill -TERM "$pid" 2>/dev/null || true
          ;;
      esac
    fi
  done

  mkdir -p "$root/.localgate/run"
  nohup "$root/bin/localgate" \
    -config "$root/localgate.yaml" \
    >> "$root/.localgate/run/localgate.log" 2>&1 < /dev/null &
  echo $! > "$root/.localgate/run/localgate.pid"
' sh "$old_pid" "$old_children" "/Users/pushan/Documents/Projects/LocalGate" \
  >/dev/null 2>&1 &

echo restart-scheduled
```

### 为什么还要记录旧子进程

LocalGate 会监督 dynamic deployment。若 daemon 在重启边界退出，而动态应用没有随父进程正常结束，它可能变成 `PPID=1` 的 orphan。曾实际出现过同一个 `fund-research-platform` 同时存在一个健康 orphan Uvicorn 和一个新受管 Uvicorn；两者同时打开生产 SQLite，导致新实例卡在 FastAPI startup，并最终被 LocalGate 标记为 `health check timed out`。

因此自重启必须遵守：

- 重启前记录旧 LocalGate 的**直接子 PID**；
- 优先给旧 daemon `SIGTERM` 并等待正常 shutdown；
- daemon 退出后，只检查先前记录的子 PID；
- 只有确认这些 PID 仍存活、已经脱管，且命令行或 `cwd` 属于当前 LocalGate/dynamic release 时才定向终止；有些 Python 应用的 argv 只有 `server.py`，必须结合 `cwd` 判断；
- **禁止**用 `pkill uvicorn`、`pkill python`、扫描所有 `PPID=1` 等宽泛方式清理；
- 新 daemon 启动后验证 PID、监听端口、启动日志，并检查 dynamic deployment 没有重复进程。

若已经在本机终端之外有独立 supervisor，则应优先让 supervisor 执行重启；这里的 delayed detached 模式主要用于“必须通过 LocalGate 自己维护 LocalGate”的开发场景。

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

