# Use Case：Cloudflare Tunnel + Zero Trust + Managed OAuth + GPT 应用
> 经测试，ChatGPT网页版 + LocalGate可以取得很不错的效果，而且还会有意外的惊喜。

[返回 LocalGate README](./README.md)

这个场景适合把家中或办公室电脑上的 LocalGate 安全地接入 ChatGPT，同时不开放公网端口，也不把固定密钥直接交给 GPT 应用。

最终效果是：

> 用户在 ChatGPT 中调用 GPT 应用，Cloudflare 完成 OAuth 登录与访问策略校验，LocalGate 在本地机器上修改代码、运行测试、启动服务或部署应用；高风险操作仍然需要 LocalGate 审批，所有行为都可以在后台追踪。

## 架构

```text
ChatGPT / GPT 应用
        │
        │ Remote MCP + OAuth
        ▼
https://gate.example.com/mcp
        │
┌──────────────────────────────────────┐
│ Cloudflare                           │
│ Tunnel · Zero Trust Access           │
│ Self-hosted Application              │
│ Managed OAuth · Access Policy        │
└──────────────────────────────────────┘
        │ outbound-only tunnel
        ▼
LocalGate @ 127.0.0.1:3737
        │
        ├── Filesystem / Shell / Session
        ├── Approval / Audit / Admin UI
        └── Static / Dynamic Deployment
```

## 各组件负责什么

| 组件 | 职责 |
| --- | --- |
| Cloudflare Tunnel | 将本机 LocalGate 接到公网域名，不需要开放入站端口或暴露源站 IP |
| Zero Trust Access | 根据邮箱、组织、用户组、设备状态或 MFA 策略决定谁能访问 |
| Access Application | 保护 `gate.example.com` 下的 MCP、管理后台和 HTTP API |
| Cloudflare Managed OAuth | 为 ChatGPT 等非浏览器客户端提供标准 OAuth 流程 |
| GPT 应用 | 通过远程 MCP 发现和调用 LocalGate 工具 |
| LocalGate | 执行本地开发任务，并提供审批、部署和审计闭环 |

## 1. 本机运行 LocalGate

让 LocalGate 只监听 loopback，避免源站被局域网或公网直接访问：

```yaml
bind: "127.0.0.1:3737"

http:
  base_url: "https://gate.example.com"

admin:
  enabled: true
  base_url: "https://gate.example.com"
```

启动服务：

```bash
DAEMON=1 ./scripts/start-http.sh
```

在这个架构中，用户身份验证由 Cloudflare Access 负责。不要让 LocalGate 直接监听公网地址，也不要创建一条绕过 Access 的公开入口。

## 2. 创建 Cloudflare Tunnel 与动态域名解析

在本机运行 `cloudflared`，把公开域名映射到 LocalGate：

```yaml
tunnel: localgate
credentials-file: /absolute/path/to/tunnel-credentials.json

ingress:
  - hostname: gate.example.com
    service: http://127.0.0.1:3737
  - hostname: "*.apps.example.com"
    service: http://127.0.0.1:3737
  - service: http_status:404
```

Cloudflare Tunnel 由本机主动建立出站连接，因此路由器和防火墙不需要开放入站端口。

这里同时把控制面域名和动态应用通配符域名转发到 LocalGate。LocalGate 会根据请求的 `Host` 判断流量进入控制面，还是进入某个动态部署。

### 推荐方式：通配符 DNS，一次配置覆盖所有动态部署

Cloudflare Tunnel 创建后会获得一个目标地址：

```text
<TUNNEL_UUID>.cfargotunnel.com
```

在 Cloudflare DNS 中创建两条已代理的 CNAME：

| 类型 | 名称 | 目标 | Proxy |
| --- | --- | --- | --- |
| CNAME | `gate` | `<TUNNEL_UUID>.cfargotunnel.com` | Proxied |
| CNAME | `*.apps` | `<TUNNEL_UUID>.cfargotunnel.com` | Proxied |

控制面记录也可以通过命令创建：

```bash
cloudflared tunnel route dns localgate gate.example.com
```

通配符记录需要在 Cloudflare DNS 中创建为 `*.apps`。完成后，以下尚未单独创建的域名都会进入同一个 Tunnel：

```text
demo.apps.example.com
preview-184.apps.example.com
docs-test.apps.example.com
```

这就是本场景中的动态解析：部署新应用时不需要为每个应用单独调用 DNS API。应用名会成为域名左侧标签，通配符 DNS 和 Tunnel 的通配符 ingress 会自动接住请求。

由于 Tunnel 不依赖家庭或办公室的公网 IP，传统的 DDNS 也不再需要。即使宽带公网 IP 发生变化，`cloudflared` 仍会通过主动出站连接维持路由。

### 可选方式：为每个部署创建精确 DNS 记录

如果希望每个应用都拥有独立的 DNS 生命周期，可以在部署成功后通过外部自动化创建精确 CNAME：

```text
preview-184.apps.example.com
  CNAME -> <TUNNEL_UUID>.cfargotunnel.com
```

精确 DNS 记录会优先于通配符记录，适合以下场景：

- 为单个应用设置独立 WAF、缓存或访问策略
- 仅让已经部署的应用域名可以解析
- 对 DNS 记录的创建和删除做额外审计
- 将不同应用指向不同 Tunnel 或源站

LocalGate 当前负责应用运行和 Host 路由，不直接管理 Cloudflare DNS。逐应用记录可以由部署流水线、Cloudflare Worker 或受限的 Cloudflare API 工具管理。相关凭证应只拥有目标 Zone 所需的最小 DNS 权限，不能注入普通应用进程。

## 3. 创建 Zero Trust Access Application

在 Cloudflare Zero Trust 中创建一个 Self-hosted Application：

```text
Application domain: gate.example.com
Path: *
```

然后配置允许策略，例如：

- 只允许指定邮箱
- 只允许公司域名
- 只允许某个 IdP 用户组
- 强制 MFA
- 限制设备状态或网络位置

Access Application 应先于公开 Tunnel 路由创建，避免域名在策略完成前短暂暴露。

建议同时在 Tunnel 设置中启用 `Protect with Access`，或者由前置代理验证 `Cf-Access-Jwt-Assertion`，防止网络配置错误造成 Access 绕过。

## 4. 启用 Cloudflare Managed OAuth

在该 Access Application 的 Advanced settings 中开启 Managed OAuth。

开启后，未认证的非浏览器客户端不会只收到普通登录重定向，而会收到带 OAuth 发现信息的 `401 Unauthorized`。ChatGPT 可以据此完成授权码与 PKCE 登录流程。

建议配置：

- 启用 Dynamic Client Registration，或按 GPT 应用要求预注册客户端
- 将 ChatGPT 应用管理页显示的 OAuth Redirect URI 加入允许列表
- 设置较短的 Access Token 生命周期
- 根据实际需求设置用户会话时长
- 使用邮箱、组织或用户组策略限制访问者

ChatGPT 当前使用的生产 Redirect URI 会在应用管理页面显示，形式类似：

```text
https://chatgpt.com/connector/oauth/<callback_id>
```

不要手工猜测 `<callback_id>`，应复制 GPT 应用配置页面提供的完整地址。

## 5. 在 GPT 应用中连接 LocalGate

在 GPT 应用或 ChatGPT Apps SDK 应用中添加远程 MCP Server：

```text
MCP Server URL: https://gate.example.com/mcp
Authentication: OAuth
```

第一次调用工具时，完整流程如下：

1. ChatGPT 请求 `https://gate.example.com/mcp`。
2. Cloudflare Access 返回 OAuth 认证挑战与发现信息。
3. ChatGPT 打开浏览器登录页。
4. 用户通过 Google、GitHub、企业 OIDC、一次性验证码或其他已配置 IdP 登录。
5. Cloudflare 根据 Access Policy 判断该用户是否允许连接。
6. Cloudflare 向 ChatGPT 签发短期 OAuth Token。
7. ChatGPT 在后续 MCP 请求中携带该 Token。
8. Cloudflare 验证 Token，并把已认证请求转发给 LocalGate。
9. LocalGate 返回工具自描述与可用能力，GPT 应用开始执行任务。

Cloudflare Managed OAuth 使用不透明 Token。GPT 应用不需要，也无法从 Token 中读取用户身份；Cloudflare 会在边缘解析身份，并向源站转发签名后的 Access Assertion。

## 6. 一个完整的使用过程

用户在 ChatGPT 中输入：

```text
检查我电脑上的 web 项目，修复测试失败的问题，测试通过后部署一个预览环境。
```

GPT 应用通过 LocalGate 执行：

1. 调用自描述工具，读取 workspace、工具列表和使用规则。
2. 使用 `read_file`、`run_command` 和 `apply_patch` 检查并修改项目。
3. 执行测试和构建命令。
4. 调用 `deploy_tool` 创建动态预览环境。
5. LocalGate 将部署任务暂停在审批状态。
6. 用户在 LocalGate 管理后台确认部署来源、命令和风险等级。
7. 审批通过后，原 Task 自动继续运行。
8. GPT 应用读取部署结果和日志，并把预览地址返回给用户。

在这个过程中：

- Cloudflare 确认“谁可以连接 LocalGate”。
- GPT 应用决定“需要调用哪些工具完成任务”。
- LocalGate 决定“这些工具能访问什么、哪些操作需要审批”。
- Cloudflare Access Logs 记录登录和访问事件。
- LocalGate Audit 记录工具、Task、审批、部署和执行结果。

## 7. 动态部署如何自动获得公网域名

在 LocalGate 中启用动态托管：

```yaml
dynamic_hosting:
  enabled: true
  control_hosts:
    - "gate.example.com"
  app_domain: "apps.example.com"
  public_scheme: "https"
  require_approval: true

  allowed_executables:
    - "/usr/bin/python3"
    - "/usr/bin/node"
```

GPT 应用完成代码修改和测试后，可以调用动态部署工具：

```json
{
  "type": "dynamic",
  "name": "preview-184",
  "source": "/absolute/path/to/web-app",
  "runtime": {
    "argv": ["/usr/bin/python3", "app.py"],
    "cwd": "/absolute/path/to/web-app"
  },
  "options": {
    "health_path": "/healthz",
    "restart_policy": "on-failure"
  }
}
```

动态应用应监听 LocalGate 注入的 `HOST` 和 `PORT`，不要写死端口。LocalGate 会：

1. 创建 Release 快照。
2. 分配本机监听端口。
3. 注入运行环境和公开 URL。
4. 启动应用并执行健康检查。
5. 将部署名映射为 `preview-184.apps.example.com`。
6. 返回公开地址 `https://preview-184.apps.example.com/`。
7. 监督进程，并按策略处理崩溃重启、日志轮转和回滚。

外部请求的路由链路是：

```text
preview-184.apps.example.com
        │
        │ wildcard CNAME
        ▼
<TUNNEL_UUID>.cfargotunnel.com
        │
        │ cloudflared wildcard ingress
        ▼
LocalGate 127.0.0.1:3737
        │
        │ Host = preview-184.apps.example.com
        ▼
preview-184 对应的本地动态进程
```

动态部署不需要重启 `cloudflared`，也不需要修改 Tunnel 配置。只要部署名称符合 LocalGate 的命名规则，新应用就能立即复用现有通配符路由。

如果预览环境不应公开访问，应为应用域名配置独立的 Cloudflare Access Application 和访问策略。控制面与预览应用可以使用不同策略，例如控制面只允许管理员，预览应用允许项目成员。

### 静态与动态预览可以并存

同一个 GPT 应用可以按项目类型选择部署方式：

- 文档、构建后的 SPA、报告页面：使用静态部署。
- API、SSR、带后端逻辑的 Web 应用：使用动态部署。

例如，GPT 可以把前端构建产物发布为静态站点，同时把当前分支的 API 发布到：

```text
https://preview-184.apps.example.com/
```

测试结束后，用户可以在后台停止、删除或回滚动态部署。使用通配符 DNS 时，删除应用后不必删除 DNS 记录；LocalGate 会对不存在的部署 Host 返回未找到。使用逐应用精确 DNS 时，应在删除部署后同步删除对应 CNAME。

## 8. 管理后台与双层审计

管理员可以通过下面的地址进入 LocalGate 后台：

```text
https://gate.example.com/admin/
```

浏览器首先经过 Cloudflare Access 登录，然后才能访问 LocalGate 管理页面。

这套架构提供两层可观察性：

| 审计层 | 可以回答的问题 |
| --- | --- |
| Cloudflare Access | 哪个用户在什么时间登录、是否通过策略、从哪里访问 |
| LocalGate Audit | 用户或 Agent 调用了什么工具、修改了什么、是否经过审批、最终结果是什么 |

## 这个方案的价值

- 本地机器不需要公网 IP，也不需要开放端口。
- GPT 应用不保存共享的 Cloudflare Service Token。
- 每位用户通过自己的身份登录，Access Policy 可以按人控制权限。
- Agent 获得真实本地开发能力，但仍受 workspace 和命令策略约束。
- 即使用户通过了 Cloudflare 身份认证，高风险操作仍需经过 LocalGate 审批。
- 开发、部署、身份验证和审计形成一条完整闭环。

对于无人值守的 CI、定时任务或服务器间调用，应使用 Cloudflare Service Token；对于由真人在 ChatGPT 中发起的交互式操作，更适合使用 Managed OAuth。
