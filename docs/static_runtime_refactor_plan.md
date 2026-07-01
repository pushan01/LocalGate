# LocalGate Static Runtime Refactor Plan (v1)

## 1. Goal

Refactor current static hosting implementation into a **clean runtime subsystem** with clear separation of concerns:

- registry (data layer)
- service (business logic)
- handler (HTTP layer)
- middleware (SPA + routing behavior)

---

## 2. Current Problems

- SPA logic embedded in api.go
- FileServer used directly in HTTP layer
- deploy uses cp -R (non-atomic)
- registry exists but not used as source of truth
- no service abstraction

---

## 3. Target Architecture

```
HTTP Layer
   ↓
Static Handler Layer
   ↓
Static Service Layer
   ↓
Registry Layer
   ↓
Filesystem
```

---

## 4. Components

### 4.1 Registry

File:

```
.localgate/static/sites.json
```

Responsibility:
- store site metadata
- single source of truth

---

### 4.2 Service Layer (NEW)

File:

```
internal/static/service.go
```

Responsibilities:

- deploy site
- delete site
- load site config
- resolve SPA behavior
- manage atomic updates

---

### 4.3 HTTP Handler Layer

File:

```
internal/static/handler.go
```

Responsibilities:

- map HTTP → service calls
- no business logic

---

### 4.4 Middleware Layer

File:

```
internal/static/middleware.go
```

Responsibilities:

- SPA fallback
- rewrite rules
- per-site routing behavior

---

## 5. Refactor Steps

### Step 1
Create static package structure

### Step 2
Move registry usage into service layer

### Step 3
Replace direct FileServer usage with middleware

### Step 4
Move deploy logic out of api.go

### Step 5
Gradually remove static logic from api.go

---

## 6. Migration Strategy

- keep current endpoints working during transition
- introduce service in parallel
- switch handlers incrementally

---

## 7. Non-goals (v1)

- no distributed storage
- no S3 integration
- no multi-node sync

---

## 8. Success Criteria

- api.go no longer contains static logic
- SPA logic fully isolated
- registry becomes authoritative source
- deploy becomes atomic service operation


## 12. Unified Deploy Tool (MCP Layer)

### 12.1 Purpose
Introduce a single MCP-level entrypoint for all deployment actions.

Instead of multiple APIs:
- static deploy API
- upgrade API
- script execution

We unify them under:

```
deploy_tool
```

---

### 12.2 Design

```
DeployRequest
- type: static | binary | script
- name
- source
- options
```

---

### 12.3 Current Implementation (v1)

Supported types:

- static → maps to static.Service.DeploySite

---

### 12.4 Execution Model

```
MCP Tool
  → DeployTool
    → Static Service
    → (future: upgrade service, script runner)
```

---

### 12.5 Evolution Plan

v1: static only
v2: add binary deployment (upgrade integration)
v3: add script pipeline execution

---

### 12.6 Design Principle

> Deployment is a unified abstraction layer, not a set of APIs.
