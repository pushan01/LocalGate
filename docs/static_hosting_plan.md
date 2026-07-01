# LocalGate 静态站点托管功能设计规划

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
