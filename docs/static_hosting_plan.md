# LocalGate 静态站点托管功能设计规划

## 0. 当前决策：静态部署使用双 Base URL 子域名路由

本节取代本文后续所有以 `/static/{site}/` 为公开访问入口的旧设计。旧章节仅保留为历史实现记录，不再作为目标架构或兼容契约。

### 0.1 目标形态

静态部署与动态部署共享 public/private 两套 base URL 和同一名称空间：

```text
public_base_url=https://apps.example.com
private_base_url=https://private-apps.example.com

static  demo (public)  -> https://demo.apps.example.com/
dynamic api  (private) -> https://api.private-apps.example.com/
```

外部 URL 不再暴露 deployment type。Host 决定 deployment，deployment type 只决定内部由静态文件服务还是动态反向代理处理。

静态与动态公开地址均为运行时派生数据。deployment 注册表不应保存默认 `hostname` 或 `url`，以避免 base URL 变更后需要逐条迁移记录。

deployment 只持久化 `access: public | private`。hostname 和 URL 根据当前 public/private base URL 实时派生。旧记录缺少 access 时按 public 解释，新部署省略 access 时使用 `default_access`，默认 private。

### 0.2 明确移除的能力

- 不再通过 `/static/{site}/` 提供已部署静态站点。
- 不提供旧 path 路由的重定向、兼容别名或长期迁移窗口。
- 静态部署结果不再返回 control-plane base URL 下的 path URL。
- `http_auth.allow_paths` 不承担静态应用公开访问控制。

### 0.3 配置归属

`public_base_url` 和 `private_base_url` 是静态与动态部署共享的应用入口配置，不再被视为动态部署专属能力。当前配置直接使用 `deployments.public_base_url` 和 `deployments.private_base_url`；不再保留独立 scheme/domain 字段。

当前配置结构：

```yaml
deployments:
  public_bind: 127.0.0.1:3738
  private_bind: 127.0.0.1:3740
  public_base_url: https://apps.example.com
  private_base_url: https://private-apps.example.com
  static:
    require_approval: true
  dynamic:
    enabled: false
```

### 0.4 统一名称空间

静态和动态 deployment 名称全局唯一。以下状态不允许同时存在：

```text
static/demo
dynamic/demo
```

因为二者都会占用所选 base URL 下的 `demo` 子域名。跨类型替换不应由普通 `overwrite=true` 隐式完成。

### 0.5 路由流程

```text
request Host
    -> match public/private base URL hostname and validate a single-label subdomain
    -> lookup deployment by hostname/name
    -> static deployment: serve request path from static site root
    -> dynamic deployment: proxy request to managed process
    -> missing/conflicting deployment: 404
```

静态 handler 必须接收已解析的 site name，并从请求根路径提供资源。例如：

```text
Host: demo.apps.example.com
Path: /assets/app.js
File: .localgate/static/demo/assets/app.js
```

### 0.6 本轮实施步骤

- [x] 为静态服务增加按 site name 从根路径提供文件的入口。
- [x] 让 deployment manager 生成静态 hostname 和完整 URL。
- [x] 增加统一 app handler，按 deployment type 分发静态服务或动态代理。
- [x] 调整 host router，使 app domain 请求进入统一 app handler。
- [x] 移除 HTTP API 中 `/static/` 的公开路由注册。
- [x] 增加静态与动态名称冲突检查。
- [x] 更新 deploy/list 输出、管理页面、配置说明与 README。
- [x] 删除或改写依赖 `/static/{site}/` 的测试，并增加 Host 路由测试。
- [x] 运行 `go test ./...` 验证完整回归。

### 0.7 验收标准

- 所选 base URL 下的 `https://{static-name}.<host>/...` 能正确提供静态文件和 SPA fallback。
- 所选 base URL 下的 `https://{dynamic-name}.<host>/...` 保持现有动态代理行为。
- control host 上访问 `/static/{site}/...` 返回 `404`。
- 未注册 app host、嵌套子域名和类型冲突均返回稳定错误或 `404`。
- deploy/list 返回的静态 URL 与动态 URL 具有相同的 hostname 形式。
- 全量测试通过。

## 1. 目标

为 LocalGate 增加一个轻量级静态网页托管能力，用于部署 HTML / JS / CSS 前端资源，并通过 HTTP path 访问。

目标形态：

```
GET /static/{site}/index.html
```

对应本地目录：

```
.localgate/static/{site}/
```

---

## 2. 核心能力

### 2.1 静态站点部署

支持通过 tool 或 API：

- 上传本地目录
- 注册为 site
- 自动复制或链接到 .localgate/static/

接口建议：

```
POST /tools/deploy_static
```

参数：
- name: site 名称
- path: 本地构建目录

---

### 2.2 静态资源访问

HTTP handler：

```
/static/ → http.FileServer
```

支持：
- HTML
- JS
- CSS
- 图片资源

---

### 2.3 站点管理

建议 registry：

```
.localgate/static/sites.json
```

结构：

```json
{
  "sites": {
    "my-site": {
      "path": ".localgate/static/my-site",
      "created_at": "..."
    }
  }
}
```

---

## 3. 系统设计

### 3.1 目录结构

```
.localgate/
  static/
    site-a/
    site-b/
  sites.json
```

---

### 3.2 HTTP 层集成

在 internal/httpapi 增加：

- http.FileServer
- StripPrefix /static/

---

## 4. 安全设计

### 4.1 路径隔离

限制访问范围：

- 只能访问 .localgate/static
- 禁止路径穿越

### 4.2 文件限制

建议：

- 禁止执行文件
- 仅允许静态资源 MIME

---

## 5. 与现有系统关系

### 5.1 upgrade 系统

避免冲突：

- upgrade 不可覆盖 static
- static 独立目录

---

### 5.2 execution runtime

static hosting 不依赖 run_command

---

## 6. 扩展能力（未来）

### 6.1 SPA 支持

```
fallback → index.html
```

### 6.2 headers 支持

自定义 HTTP headers

### 6.3 热更新

watch static 目录变化

---

## 7. 设计原则

- 简单优先（file server 优先）
- registry 可选，不强依赖
- 与 execution runtime 解耦

---

## 8. 总结

该功能本质是：

> 为 LocalGate 增加一个轻量 CDN-like static hosting layer



## 9. Implementation Status (auto-updated)

### DONE
- [x] /static/* FileServer mounted in HTTP API
- [x] deploy_static endpoint added (POST /system/static/deploy)
- [x] directory-based hosting under .localgate/static/

### NOTES
- MVP uses system `cp -R` for deployment
- No registry required for initial version
- Security model: path restricted to .localgate/static only

### NEXT
- replace cp with Go-native file copy
- add SPA fallback support
- add site registry (optional)


## 10. v2 Enhancements (Pending Implementation)

### 10.1 Optional overwrite behavior

Deploy API新增参数 overwrite (default false)

- overwrite=false → existing dir returns 409
- overwrite=true → remove then copy

原则：default safe, explicit destructive

---

### 10.2 SPA fallback support

HTTP static handler behavior:

- if file exists → serve file
- if not exists → fallback /index.html

scope:
- only under /static/{site}/

implementation:
- custom http.Handler wrapper (not plain FileServer)

---

### 10.3 constraints

- FileServer behavior preserved for non-SPA mode
- overwrite must be explicit
- fallback only for HTML navigation routes


## 11. Site Metadata Registry (Design)

### 11.1 Purpose
Introduce registry layer for managing static sites as first-class entities.

---

### 11.2 Data Model

File:

.localgate/static/sites.json

Schema:
{
  "sites": {
    "site-a": {
      "path": ".localgate/static/site-a",
      "spa": true,
      "index": "index.html",
      "headers": {},
      "rewrite": [],
      "created_at": "",
      "updated_at": ""
    }
  }
}

---

### 11.3 API

POST /system/static/sites/register
DELETE /system/static/sites/{name}
GET /system/static/sites

---

### 11.4 Runtime behavior

- registry is source of truth
- SPA fallback depends on site config
- headers applied per site

---

### 11.5 Principle

File system is implementation detail; registry drives behavior.
