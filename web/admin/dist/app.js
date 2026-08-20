// src/file-tabs.js
function fileTabKey(path) {
  return String(path || "");
}
function pathMatches(candidate, target, recursive) {
  if (candidate === target) return true;
  return Boolean(recursive && target && candidate.startsWith(`${target}/`));
}
function replacePathPrefix(candidate, fromPath, toPath) {
  if (candidate === fromPath) return toPath;
  if (fromPath && candidate.startsWith(`${fromPath}/`)) return `${toPath}${candidate.slice(fromPath.length)}`;
  return candidate;
}
function parentPath(value) {
  const parts = String(value || "").split("/").filter(Boolean);
  parts.pop();
  return parts.join("/");
}
var FileTabStore = class {
  constructor() {
    this.items = [];
    this.activeKey = "";
  }
  get(key) {
    return this.items.find((item) => item.key === key) || null;
  }
  find(path) {
    return this.get(fileTabKey(path));
  }
  active() {
    return this.get(this.activeKey);
  }
  open(input) {
    const key = fileTabKey(input.path);
    let tab = this.get(key);
    if (!tab) {
      tab = {
        dirty: false,
        conflict: null,
        previewMode: false,
        ...input,
        key
      };
      this.items.push(tab);
    } else {
      Object.assign(tab, input, { key });
    }
    this.activeKey = key;
    return tab;
  }
  activate(key) {
    if (!this.get(key)) return null;
    this.activeKey = key;
    return this.active();
  }
  update(key, patch) {
    const tab = this.get(key);
    if (!tab) return null;
    Object.assign(tab, patch);
    return tab;
  }
  close(key) {
    const index = this.items.findIndex((item) => item.key === key);
    if (index < 0) return { removed: null, active: this.active() };
    const [removed] = this.items.splice(index, 1);
    if (this.activeKey === key) {
      const next = this.items[index] || this.items[index - 1] || null;
      this.activeKey = next?.key || "";
    }
    return { removed, active: this.active() };
  }
  dirtyItems() {
    return this.items.filter((item) => item.dirty);
  }
  matching(path, recursive = false) {
    return this.items.filter((item) => pathMatches(item.path, path, recursive));
  }
  removeMatching(path, recursive = false) {
    const keys = new Set(this.matching(path, recursive).map((item) => item.key));
    if (!keys.size) return [];
    const removed = this.items.filter((item) => keys.has(item.key));
    const activeIndex = this.items.findIndex((item) => item.key === this.activeKey);
    this.items = this.items.filter((item) => !keys.has(item.key));
    if (keys.has(this.activeKey)) {
      const next = this.items[Math.min(activeIndex, this.items.length - 1)] || null;
      this.activeKey = next?.key || "";
    }
    return removed;
  }
  renameMatching(fromPath, toPath, recursive = false) {
    const matches = this.matching(fromPath, recursive);
    if (!matches.length) return [];
    const active = this.active();
    for (const tab of matches) {
      const exactFile = tab.path === fromPath;
      tab.path = replacePathPrefix(tab.path, fromPath, toPath);
      tab.dirPath = exactFile ? parentPath(toPath) : replacePathPrefix(tab.dirPath || "", fromPath, toPath);
      tab.key = fileTabKey(tab.path);
    }
    if (active) this.activeKey = active.key;
    return matches;
  }
  clear() {
    this.items = [];
    this.activeKey = "";
  }
};

// src/file-terminal.js
function fileTerminalContextKey(path) {
  return String(path || "");
}
var FileTerminalState = class {
  constructor() {
    this.reset();
  }
  reset() {
    this.contextKey = "";
    this.path = "";
    this.taskID = "";
    this.task = null;
    this.approval = null;
    this.sessionID = "";
    this.session = null;
    this.cursor = 0;
    this.baseOffset = 0;
    this.items = [];
    this.cursorExpired = false;
    this.truncated = false;
    this.error = "";
    this.transport = "idle";
    this.transportWarning = "";
    return this;
  }
  setContext(path) {
    const key = fileTerminalContextKey(path);
    if (this.contextKey && this.contextKey !== key) this.reset();
    this.contextKey = key;
    this.path = path || "";
    return this;
  }
  matchesPath(path) {
    return Boolean(this.contextKey && this.path === (path || ""));
  }
  attachTask(taskID, approval = null) {
    if (taskID && this.taskID !== taskID) {
      this.taskID = taskID;
      this.task = null;
      this.approval = approval;
      this.sessionID = "";
      this.session = null;
      this.cursor = 0;
      this.baseOffset = 0;
      this.items = [];
      this.cursorExpired = false;
      this.truncated = false;
      this.error = "";
      this.transport = "idle";
      this.transportWarning = "";
    } else if (approval !== void 0) {
      this.approval = approval;
    }
    return this;
  }
  attachSession(sessionID) {
    if (!sessionID || this.sessionID === sessionID) return this;
    this.sessionID = sessionID;
    this.session = null;
    this.cursor = 0;
    this.baseOffset = 0;
    this.items = [];
    this.cursorExpired = false;
    this.truncated = false;
    this.error = "";
    this.transport = "idle";
    this.transportWarning = "";
    return this;
  }
  applyTask(payload) {
    const task = payload?.task || payload || null;
    this.task = task;
    this.approval = payload?.approval || this.approval;
    const sessionID = task?.result?.session_id;
    if (sessionID) this.attachSession(sessionID);
    if (["failed", "canceled", "timeout"].includes(task?.status)) {
      this.error = task.error_msg || task.error_message || `Terminal task ${task.status}`;
    } else if (task?.status === "succeeded" && !sessionID) {
      this.error = "Terminal task succeeded without returning a session_id";
    } else {
      this.error = "";
    }
    return this;
  }
  applyRead(payload) {
    if (!payload) return this;
    this.session = payload.session || this.session;
    this.baseOffset = Number(payload.base_offset || 0);
    this.cursorExpired = Boolean(payload.cursor_expired);
    this.truncated = Boolean(payload.truncated);
    if (this.cursorExpired) this.items = [];
    const byOffset = new Map(this.items.map((item) => [Number(item.offset), item]));
    for (const item of payload.items || []) byOffset.set(Number(item.offset), item);
    this.items = Array.from(byOffset.values()).sort((a, b) => Number(a.offset) - Number(b.offset));
    if (this.items.length > 2e3) this.items = this.items.slice(-2e3);
    this.cursor = Number(payload.next_offset ?? this.cursor);
    this.error = payload.output_error || "";
    return this;
  }
  mode() {
    if (this.sessionID) return "session";
    if (this.taskID) return "task";
    return "start";
  }
  output() {
    return this.items.map((item) => item.data || "").join("");
  }
  shouldPoll() {
    if (this.sessionID) {
      return ["starting", "running"].includes(this.session?.status || "starting");
    }
    return Boolean(this.taskID && !["succeeded", "failed", "canceled", "timeout"].includes(this.task?.status));
  }
};

// src/file-terminal-stream.js
var SSEParser = class {
  constructor(onEvent) {
    this.onEvent = onEvent;
    this.buffer = "";
  }
  push(chunk) {
    this.buffer += chunk || "";
    while (true) {
      const match = this.buffer.match(/(?:\r\n|\r|\n){2}/);
      if (!match || match.index === void 0) return;
      const block = this.buffer.slice(0, match.index);
      this.buffer = this.buffer.slice(match.index + match[0].length);
      const event = parseSSEBlock(block);
      if (event) this.onEvent(event);
    }
  }
};
function parseSSEBlock(block) {
  let event = "message";
  let id = "";
  const data = [];
  for (const line of String(block || "").split(/\r\n|\r|\n/)) {
    if (!line || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? "" : line.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") event = value || "message";
    else if (field === "id") id = value;
    else if (field === "data") data.push(value);
  }
  if (!data.length) return null;
  return { event, id, data: data.join("\n") };
}
async function consumeSSE(response, onEvent) {
  if (!response?.body?.getReader) throw new Error("Streaming response body is unavailable");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  const parser = new SSEParser(onEvent);
  let completed = false;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      parser.push(decoder.decode(value, { stream: true }));
    }
    parser.push(decoder.decode());
    completed = true;
  } finally {
    if (!completed && typeof reader.cancel === "function") {
      try {
        await reader.cancel();
      } catch {
      }
    }
    if (typeof reader.releaseLock === "function") reader.releaseLock();
  }
}

// src/file-git.js
function fileGitContextKey(path) {
  return String(path || "");
}
var FileGitState = class {
  constructor() {
    this.open = false;
    this.contextKey = "";
    this.path = "";
    this.loading = false;
    this.loadingDiff = false;
    this.status = null;
    this.diff = null;
    this.error = "";
  }
  setContext(path) {
    const key = fileGitContextKey(path);
    if (key !== this.contextKey) {
      this.contextKey = key;
      this.status = null;
      this.diff = null;
      this.error = "";
      this.loading = false;
      this.loadingDiff = false;
    }
    this.path = path || "";
    return this;
  }
  applyStatus(status) {
    this.status = status || null;
    this.loading = false;
    this.error = "";
    return this;
  }
  applyDiff(diff) {
    this.diff = diff || null;
    this.loadingDiff = false;
    this.error = "";
    return this;
  }
  fail(error) {
    this.loading = false;
    this.loadingDiff = false;
    this.error = error?.message || String(error || "Git request failed");
    return this;
  }
  clearDiff() {
    this.diff = null;
    this.loadingDiff = false;
    return this;
  }
};
function gitEntryHasStagedChanges(entry) {
  const value = String(entry?.index_status || ".");
  return value !== "." && value !== "?";
}
function gitEntryHasWorktreeChanges(entry) {
  const value = String(entry?.worktree_status || ".");
  return value !== "." && value !== "?";
}

// src/app.js
var API = "/api/admin/v1";
var app = document.querySelector("#app");
var SIDEBAR_STORAGE_KEY = "localgate_admin_sidebar_collapsed";
function loadSidebarCollapsed() {
  try {
    return localStorage.getItem(SIDEBAR_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}
var state = { token: sessionStorage.getItem("localgate_admin_token") || "", error: "", notice: "", fileDirty: false, fileURL: "", fileConflict: null, fileTabs: new FileTabStore(), fileTerminal: new FileTerminalState(), fileGit: new FileGitState(), terminalPollTimer: 0, terminalStreamAbort: null, imageObjectURL: "", sidebarCollapsed: loadSidebarCollapsed() };
var esc = (value) => String(value ?? "").replace(/[&<>'"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[c]);
var short = (value, n = 18) => value ? value.length > n ? `${value.slice(0, n)}…` : value : "—";
var compact = (value, head = 20, tail = 12) => {
  const text = String(value || "");
  return text.length > head + tail + 1 ? `${text.slice(0, head)}…${text.slice(-tail)}` : text || "—";
};
var fmtTime = (value) => value ? new Date(value).toLocaleString() : "—";
var fmtDuration = (value) => value ? value < 1e3 ? `${value} ms` : `${(value / 1e3).toFixed(1)} s` : "—";
var badge = (value) => `<span class="badge ${esc(value)}">${esc(value || "unknown")}</span>`;
var sessionBadge = (item) => item.session_id ? `<div class="mono" title="${esc(item.session_id)}">${esc(short(item.session_id, 22))}</div><div class="muted">${esc(item.session_mode || "")} ${item.session_id_source === "client_supplied" ? "· Client supplied" : ""}</div>` : `<span class="muted">No session</span>`;
async function request(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (state.token) headers.set("Authorization", `Bearer ${state.token}`);
  if (options.body && !(options.body instanceof FormData) && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const response = await fetch(`${API}${path}`, { ...options, headers });
  if (response.status === 401) throw Object.assign(new Error("需要管理 Token"), { unauthorized: true });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(data.message || `HTTP ${response.status}`);
    error.status = response.status;
    error.code = data.code;
    error.data = data;
    throw error;
  }
  return data;
}
async function requestBlob(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (state.token) headers.set("Authorization", `Bearer ${state.token}`);
  const response = await fetch(`${API}${path}`, { ...options, headers });
  if (response.status === 401) throw Object.assign(new Error("需要管理 Token"), { unauthorized: true });
  if (!response.ok) {
    const data = await response.json().catch(() => ({}));
    const error = new Error(data.message || `HTTP ${response.status}`);
    error.status = response.status;
    error.code = data.code;
    throw error;
  }
  return { blob: await response.blob(), headers: response.headers };
}
function releaseImageObjectURL() {
  if (!state.imageObjectURL) return;
  URL.revokeObjectURL(state.imageObjectURL);
  state.imageObjectURL = "";
}
function activeFileTab() {
  return state.fileTabs.active();
}
function syncActiveFileTabFromDOM() {
  const editor = document.querySelector("[data-file-editor]");
  const tab = activeFileTab();
  if (!editor || !tab || editor.dataset.tabKey !== tab.key) return tab;
  tab.content = editor.value;
  tab.etag = editor.dataset.etag;
  tab.dirty = editor.value !== tab.savedContent;
  state.fileDirty = tab.dirty;
  return tab;
}
function resetFileTabs() {
  releaseImageObjectURL();
  clearFileTerminalUpdates();
  state.fileTerminal.reset();
  state.fileTabs.clear();
  state.fileDirty = false;
  state.fileConflict = null;
}
function clearTerminalPoll() {
  if (!state.terminalPollTimer) return;
  clearTimeout(state.terminalPollTimer);
  state.terminalPollTimer = 0;
}
function stopFileTerminalStream() {
  const controller = state.terminalStreamAbort;
  state.terminalStreamAbort = null;
  if (controller) controller.abort();
}
function clearFileTerminalUpdates() {
  clearTerminalPoll();
  stopFileTerminalStream();
}
function fileTabsWithUnsavedChanges() {
  syncActiveFileTabFromDOM();
  return state.fileTabs.dirtyItems();
}
function currentURL() {
  return `${location.pathname}${location.search}${location.hash}`;
}
function hasUnsavedFileChanges() {
  return fileTabsWithUnsavedChanges().length > 0;
}
function confirmDiscardFileChanges(message = "存在未保存的文件标签。离开后这些修改会丢失，确定继续？") {
  const dirty = fileTabsWithUnsavedChanges();
  if (!dirty.length) return true;
  const detail = dirty.length === 1 ? dirty[0].path : `${dirty.length} 个文件`;
  return confirm(`${message}

未保存：${detail}`);
}
function navigate(path, options = {}) {
  syncActiveFileTabFromDOM();
  const target = new URL(path, location.origin);
  const staysInFiles = target.pathname.replace(/\/$/, "") === "/admin/files";
  if (staysInFiles) {
    history.pushState({}, "", path);
    render();
    return true;
  }
  if (!options.force && !confirmDiscardFileChanges()) return false;
  if (!options.preserveFileTabs) resetFileTabs();
  history.pushState({}, "", path);
  render();
  return true;
}
function setSidebarCollapsed(collapsed) {
  state.sidebarCollapsed = Boolean(collapsed);
  try {
    localStorage.setItem(SIDEBAR_STORAGE_KEY, state.sidebarCollapsed ? "1" : "0");
  } catch {
  }
  document.querySelector(".shell")?.classList.toggle("sidebar-collapsed", state.sidebarCollapsed);
  const toggle = document.querySelector('[data-action="toggle-sidebar"]');
  if (!toggle) return;
  const label = state.sidebarCollapsed ? "展开侧边栏" : "折叠侧边栏";
  toggle.setAttribute("aria-label", label);
  toggle.setAttribute("title", label);
  toggle.setAttribute("aria-expanded", String(!state.sidebarCollapsed));
  const icon = toggle.querySelector("[data-sidebar-toggle-icon]");
  if (icon) icon.textContent = state.sidebarCollapsed ? "›" : "‹";
}
function layout(title, body, actions = "") {
  const path = location.pathname;
  const notice = state.notice;
  state.notice = "";
  const collapsed = state.sidebarCollapsed;
  const toggleLabel = collapsed ? "展开侧边栏" : "折叠侧边栏";
  return `<div class="shell${collapsed ? " sidebar-collapsed" : ""}"><aside class="sidebar"><div class="sidebar-head"><div class="brand"><span class="brand-mark" aria-hidden="true">LG</span><span class="sidebar-label">LocalGate Admin</span></div><button class="sidebar-toggle" data-action="toggle-sidebar" type="button" aria-label="${toggleLabel}" title="${toggleLabel}" aria-expanded="${String(!collapsed)}"><span data-sidebar-toggle-icon aria-hidden="true">${collapsed ? "›" : "‹"}</span></button></div><nav class="nav">
    <a data-nav="/admin" title="Overview" aria-label="Overview" class="${path === "/admin" || path === "/admin/" ? "active" : ""}"><span class="nav-icon" aria-hidden="true">O</span><span class="nav-label">Overview</span></a>
    <a data-nav="/admin/history" title="History" aria-label="History" class="${path.startsWith("/admin/history") ? "active" : ""}"><span class="nav-icon" aria-hidden="true">H</span><span class="nav-label">History</span></a>
    <a data-nav="/admin/files" title="Files" aria-label="Files" class="${path.startsWith("/admin/files") ? "active" : ""}"><span class="nav-icon" aria-hidden="true">F</span><span class="nav-label">Files</span></a>
    <a data-nav="/admin/deployments" title="Deployments" aria-label="Deployments" class="${path.startsWith("/admin/deployments") ? "active" : ""}"><span class="nav-icon" aria-hidden="true">D</span><span class="nav-label">Deployments</span></a>
    <a data-nav="/admin/approvals" title="Approvals" aria-label="Approvals" class="${path.startsWith("/admin/approvals") ? "active" : ""}"><span class="nav-icon" aria-hidden="true">A</span><span class="nav-label">Approvals</span></a>
  </nav></aside><main class="content"><header class="topbar"><h1>${esc(title)}</h1><div class="actions">${actions}<button class="button" data-action="token">Token</button></div></header>
  ${state.error ? `<div class="error">${esc(state.error)}</div>` : ""}${notice ? `<div class="notice">${esc(notice)}</div>` : ""}${body}</main></div>`;
}
function loginView(message = "") {
  app.innerHTML = `<section class="login"><h1>LocalGate Admin</h1><p class="muted">输入 HTTP Bearer Token。未启用认证的实例可以留空。</p>${message ? `<div class="error">${esc(message)}</div>` : ""}<input id="token-input" type="password" autocomplete="current-password" placeholder="Bearer token"><button class="button primary" data-action="save-token">进入后台</button></section>`;
}
async function render() {
  state.error = "";
  const path = location.pathname.replace(/\/$/, "") || "/admin";
  if (path !== "/admin/files") {
    releaseImageObjectURL();
    clearFileTerminalUpdates();
  }
  try {
    if (path === "/admin") return renderOverview();
    if (path === "/admin/history") return renderHistory();
    if (path.startsWith("/admin/history/")) return renderTask(path.split("/").pop());
    if (path === "/admin/files") return renderFiles();
    if (path === "/admin/deployments") return renderDeployments();
    if (path === "/admin/approvals") return renderApprovals();
    if (path.startsWith("/admin/approvals/")) return renderApproval(path.split("/").pop());
    navigate("/admin");
  } catch (error) {
    if (error.unauthorized) return loginView(error.message);
    state.error = error.message;
    app.innerHTML = layout("Error", `<div class="empty">请求失败</div>`);
  }
}
async function renderOverview() {
  const data = await request("/overview");
  const tasks = data.tasks || {}, approvals = data.approvals || {}, deployments = data.deployments || {};
  const rows = (data.recent_tasks || []).map(taskRow).join("");
  app.innerHTML = layout("Overview", `<section class="grid">
    ${metric("Calls / 24h", tasks.total_24h || 0)}${metric("Running", tasks.running || 0)}${metric("Failed / 24h", tasks.failed_24h || 0)}${metric("Pending approvals", approvals.pending || 0)}${metric("Deployments", deployments.total || 0)}${metric("Deployment failures", deployments.failed || 0)}
  </section><section class="section"><div class="section-head"><h2>Recent calls</h2><button class="link" data-nav="/admin/history">View all</button></div>${table(taskHeaders(), rows)}</section>`);
}
var metric = (label, value) => `<article class="card"><div class="muted">${esc(label)}</div><div class="metric">${esc(value)}</div></article>`;
function taskHeaders() {
  return "<tr><th>Time</th><th>Tool</th><th>Status</th><th>Session</th><th>Transport</th><th>Duration</th><th>Requester</th></tr>";
}
function taskRow(item) {
  return `<tr><td>${fmtTime(item.created_at)}</td><td><button class="link mono" data-nav="/admin/history/${esc(item.id)}">${esc(item.tool || item.id)}</button></td><td>${badge(item.status)}</td><td>${sessionBadge(item)}</td><td>${esc(item.transport || "—")}</td><td>${fmtDuration(item.duration_ms)}</td><td>${esc(item.requester || "—")}</td></tr>`;
}
function table(headers, rows) {
  return `<div class="table-wrap"><table><thead>${headers}</thead><tbody>${rows || `<tr><td colspan="8" class="empty">暂无数据</td></tr>`}</tbody></table></div>`;
}
async function renderHistory() {
  const query = new URLSearchParams(location.search);
  const data = await request(`/tasks?${query}`);
  const rows = (data.items || []).map(taskRow).join("");
  app.innerHTML = layout("History", `<form class="filters" id="history-filters">
    <input name="session_id" placeholder="Session ID" value="${esc(query.get("session_id") || "")}"><input name="tool" placeholder="Tool" value="${esc(query.get("tool") || "")}">
    <select name="status"><option value="">All statuses</option>${["awaiting_approval", "queued", "running", "succeeded", "failed", "canceled", "timeout"].map((v) => `<option ${query.get("status") === v ? "selected" : ""}>${v}</option>`).join("")}</select>
    <select name="transport"><option value="">All transports</option>${["mcp_http", "mcp_stdio", "mcp_runtime", "rest", "tool_http", "admin", "system"].map((v) => `<option ${query.get("transport") === v ? "selected" : ""}>${v}</option>`).join("")}</select>
    <button class="button primary">Filter</button><button type="button" class="button" data-action="clear-history">Clear</button>
  </form>${table(taskHeaders(), rows)}${data.next_cursor ? `<div class="section"><button class="button" data-action="next-history" data-cursor="${esc(data.next_cursor)}">Next page</button></div>` : ""}`);
}
async function renderTask(id) {
  const [detail, events, audits] = await Promise.all([request(`/tasks/${id}`), request(`/tasks/${id}/events`), request(`/tasks/${id}/audits`)]);
  const task = detail.task;
  app.innerHTML = layout(`Task ${short(id, 12)}`, `<section class="card detail-grid">${kv("Status", badge(task.status))}${kv("Tool", task.tool)}${kv("Transport", task.transport || "—")}${kv("Session", task.session_id || "No session")}${kv("Session source", task.session_id_source || "none")}${kv("Requester", task.requester || "—")}${kv("Created", fmtTime(task.created_at))}</section>
  ${task.error_msg ? `<section class="section card"><h2>Error</h2><p>${esc(task.error_msg)}</p></section>` : ""}
  <section class="section card"><div class="section-head"><h2>Result</h2>${!["succeeded", "failed", "canceled", "timeout"].includes(task.status) ? `<button class="button danger" data-action="cancel-task" data-id="${esc(id)}">Cancel</button>` : ""}</div><pre>${esc(JSON.stringify(task.result ?? {}, null, 2))}</pre></section>
  <section class="section card"><h2>Events</h2><pre>${esc(JSON.stringify(events.items || [], null, 2))}</pre></section>
  <section class="section card"><h2>Audit</h2><pre>${esc(JSON.stringify(audits.items || [], null, 2))}</pre></section>`);
}
var kv = (label, value) => `<div class="kv"><strong>${esc(label)}</strong><div>${value && String(value).startsWith("<span") ? value : esc(value)}</div></div>`;
async function renderFiles() {
  syncActiveFileTabFromDOM();
  releaseImageObjectURL();
  const query = new URLSearchParams(location.search);
  const dirPath = query.get("path") || "";
  const filePath = query.get("file") || "";
  state.fileGit.setContext(dirPath);
  await hydrateFileTerminal({
    path: query.get("terminal_path") || dirPath,
    taskID: query.get("terminal_task") || "",
    sessionID: query.get("terminal_session") || ""
  });
  const list = await request(`/file-browser/files/list?${fileBrowserQuery({ path: dirPath })}`);
  const currentLabel = dirPath || "/";
  const entries = list.entries || [];
  const selectedEntry = entries.find((entry) => entry.path === filePath) || null;
  let tab = null;
  if (filePath) {
    tab = await openFileTab({ dirPath, filePath, entry: selectedEntry });
  } else {
    state.fileTabs.activeKey = "";
  }
  const rows = fileRows(entries, { dirPath, filePath });
  const preview = filePreview(filePath, fileContentFromTab(tab), tab?.error || "", { entry: selectedEntry || tab?.entry, imageFile: tab?.imageFile, tab });
  state.fileConflict = tab?.conflict || null;
  app.innerHTML = layout("Files", `<section class="file-toolbar card">
    <div class="file-path mono">${esc(currentLabel)}</div>
    <button class="button" data-file-new-file="1">New file</button><button class="button" data-file-new-folder="1">New folder</button><button class="button" data-file-upload="1">Upload</button><input class="hidden" type="file" data-file-upload-input><button class="button${state.fileGit.open ? " primary" : ""}" data-file-git-toggle="1">Git</button><button class="button" data-file-terminal-open="1">Terminal</button><button class="button" data-file-refresh="1">Refresh</button>
  </section>
  ${fileGitPanel()}
  <section class="file-browser section">
    <div class="file-panel card">
      <div class="section-head"><h2>Files</h2>${dirPath ? `<button class="link" data-file-dir="${esc(parentPath2(dirPath))}">Up</button>` : ""}</div>
      <div class="file-list">${rows || `<div class="empty">目录为空</div>`}</div>
    </div>
    <div class="file-preview card">
      ${fileTabsView()}
      <div class="file-tab-content">${preview}</div>
    </div>
  </section>
  ${fileTerminalPanel()}`);
  setFileDirty(Boolean(tab?.dirty));
  state.fileURL = currentURL();
  if (tab?.imageFile) loadImagePreview({ path: filePath });
  startFileTerminalUpdates();
  if (state.fileGit.open && !state.fileGit.status && !state.fileGit.loading) loadFileGitStatus();
}
async function openFileTab({ dirPath, filePath, entry }) {
  const existing = state.fileTabs.find(filePath);
  if (existing) {
    existing.dirPath = dirPath;
    if (entry) existing.entry = entry;
    state.fileTabs.activate(existing.key);
    return existing;
  }
  const imageFile = isPreviewableImage(filePath);
  let content = null;
  let error = "";
  if (!imageFile) {
    try {
      content = await request(`/file-browser/files/content?${fileBrowserQuery({ path: filePath })}`);
    } catch (loadError) {
      error = loadError.message;
    }
  }
  return state.fileTabs.open({
    dirPath,
    path: filePath,
    entry,
    imageFile,
    error,
    content: content?.content || "",
    savedContent: content?.content || "",
    etag: content?.etag || "",
    language: content?.language || (imageFile ? "image" : "text"),
    size: content?.size ?? entry?.size ?? 0,
    modTime: content?.mod_time || entry?.mod_time || "",
    dirty: false,
    conflict: null,
    previewMode: false
  });
}
function fileContentFromTab(tab) {
  if (!tab || tab.imageFile || tab.error) return null;
  return {
    path: tab.path,
    content: tab.content,
    etag: tab.etag,
    language: tab.language,
    size: tab.size,
    mod_time: tab.modTime
  };
}
function fileTabsView() {
  if (!state.fileTabs.items.length) return "";
  return `<div class="file-tabs" role="tablist" aria-label="Open files">${state.fileTabs.items.map((tab) => {
    const active = tab.key === state.fileTabs.activeKey;
    const name = tab.path.split("/").pop() || tab.path;
    return `<div class="file-tab${active ? " active" : ""}${tab.dirty ? " dirty" : ""}" role="presentation"><button class="file-tab-open" type="button" role="tab" aria-selected="${String(active)}" title="${esc(tab.path)}" data-file-tab="${esc(tab.key)}"><span class="file-tab-label">${esc(name)}</span>${tab.dirty ? `<span class="file-tab-dirty" aria-label="Unsaved">●</span>` : ""}</button><button class="file-tab-close" type="button" aria-label="Close ${esc(name)}" title="Close" data-file-tab-close="${esc(tab.key)}">×</button></div>`;
  }).join("")}</div>`;
}
function fileTabURL(tab) {
  return fileBrowserURL({ path: tab.dirPath || parentPath2(tab.path), file: tab.path });
}
function closeFileTab(key) {
  syncActiveFileTabFromDOM();
  const tab = state.fileTabs.get(key);
  if (!tab) return;
  if (tab.dirty && !confirm(`关闭 ${tab.path} 会丢弃未保存修改，确定继续？`)) return;
  const wasActive = state.fileTabs.activeKey === key;
  const result = state.fileTabs.close(key);
  if (!wasActive) return render();
  if (result.active) return navigate(fileTabURL(result.active), { force: true });
  const ctx = currentFileContext();
  return navigate(fileBrowserURL({ path: ctx.path, file: "" }), { force: true });
}
function fileRows(entries, context) {
  return entries.map((entry) => {
    const isDir = entry.type === "directory";
    const active = !isDir && context.filePath === entry.path ? " active" : "";
    const targetAttr = isDir ? `data-file-dir="${esc(entry.path)}"` : `data-file-open="${esc(entry.path)}"`;
    const icon = isDir ? "📁" : "📄";
    const actions = entry.type === "file" || entry.type === "directory" ? `<div class="file-row-actions"><button class="file-row-action" data-file-entry-rename data-entry-path="${esc(entry.path)}" data-entry-type="${esc(entry.type)}">Rename</button><button class="file-row-action danger" data-file-entry-delete data-entry-path="${esc(entry.path)}" data-entry-type="${esc(entry.type)}">Delete</button></div>` : "";
    return `<div class="file-row-wrap"><button class="file-row${active}" ${targetAttr}><span>${icon}</span><span class="file-name">${esc(entry.name)}</span><span class="muted">${isDir ? "directory" : formatBytes(entry.size)}</span></button>${actions}</div>`;
  }).join("");
}
function filePreview(filePath, content, error, context = {}) {
  if (!filePath) return `<div class="empty">选择一个文本文件预览</div>`;
  if (context.imageFile) return imageFilePreview(filePath, context);
  if (error) return unavailableFilePreview(filePath, error, context);
  if (!content) return `<div class="empty">正在加载文件内容…</div>`;
  const markdown = content.language === "markdown";
  const tab = context.tab || {};
  const previewMode = Boolean(markdown && tab.previewMode);
  const etag = `<span class="mono file-etag" title="${esc(content.etag)}">${esc(compact(content.etag))}</span>`;
  return `<div class="section-head"><h2>${esc(filePath.split("/").pop())}</h2><div class="actions"><span class="badge">${esc(content.language || "text")}</span><span class="badge pending${tab.dirty ? "" : " hidden"}" data-file-dirty>Unsaved</span>${markdown ? `<button class="button" data-file-markdown-preview="1">${previewMode ? "Edit" : "Preview"}</button>` : ""}<button class="button primary" data-file-save="1">Save</button><button class="button" data-file-rename="1">Rename</button><button class="button" data-file-download="1">Download</button><button class="button danger" data-file-delete="1">Delete</button><button class="button" data-file-reload="1">Reload</button></div></div>
    <div data-file-status>${tab.conflict ? fileConflictPanel() : ""}</div>
    <div class="detail-grid file-meta">${kv("Path", content.path)}${kv("Size", formatBytes(content.size))}${kv("Modified", fmtTime(content.mod_time))}${kv("ETag", etag)}</div>
    <textarea class="file-editor${previewMode ? " hidden" : ""}" data-file-editor data-tab-key="${esc(tab.key || "")}" data-path="${esc(content.path)}" data-etag="${esc(content.etag)}" data-language="${esc(content.language || "text")}" spellcheck="false">${esc(content.content)}</textarea>
    ${markdown ? `<article class="markdown-preview${previewMode ? "" : " hidden"}" data-markdown-preview>${previewMode ? renderMarkdown(content.content) : ""}</article>` : ""}`;
}
function fileConflictPanel() {
  return `<div class="conflict-panel"><div class="error">文件已被外部修改。请选择重新加载、覆盖服务器版本或查看差异。</div><div class="actions"><button class="button" data-file-conflict-reload>Reload server</button><button class="button danger" data-file-conflict-overwrite>Overwrite</button><button class="button" data-file-conflict-diff>Show diff</button></div><div class="hidden" data-file-conflict-diff-view></div></div>`;
}
function imageFilePreview(filePath, context) {
  const entry = context.entry || {};
  return `<div class="section-head"><h2>${esc(filePath.split("/").pop())}</h2><div class="actions"><span class="badge">image</span><button class="button" data-file-download-path="${esc(filePath)}">Download</button><button class="button" data-file-entry-rename data-entry-path="${esc(filePath)}" data-entry-type="file">Rename</button><button class="button danger" data-file-entry-delete data-entry-path="${esc(filePath)}" data-entry-type="file">Delete</button><button class="button" data-file-reload="1">Reload</button></div></div>
    <div data-file-status><div class="notice">正在加载图片预览…</div></div>
    <div class="detail-grid file-meta">${kv("Path", filePath)}${kv("Size", formatBytes(entry.size))}${kv("Modified", fmtTime(entry.mod_time))}</div>
    <div class="image-preview-stage"><img class="file-preview-image hidden" data-file-image-preview alt="${esc(filePath.split("/").pop())}"></div>`;
}
function unavailableFilePreview(filePath, error, context) {
  return `<div class="section-head"><h2>${esc(filePath.split("/").pop())}</h2><div class="actions"><button class="button" data-file-download-path="${esc(filePath)}">Download</button><button class="button" data-file-entry-rename data-entry-path="${esc(filePath)}" data-entry-type="file">Rename</button><button class="button danger" data-file-entry-delete data-entry-path="${esc(filePath)}" data-entry-type="file">Delete</button></div></div>
    <div class="error">${esc(error)}</div><p class="muted">该文件不能作为文本或受支持图片预览，可以直接下载。</p>`;
}
function isPreviewableImage(path) {
  return /\.(png|jpe?g|gif|webp)$/i.test(String(path || ""));
}
function fileBrowserQuery({ path }) {
  const q = new URLSearchParams();
  if (path) q.set("path", path);
  return q.toString();
}
function fileGitPanel() {
  const git = state.fileGit;
  if (!git.open) return "";
  let body = "";
  if (git.loading) {
    body = `<div class="notice">正在读取 Git 状态…</div>`;
  } else if (git.error && !git.status) {
    body = `<div class="error">${esc(git.error)}</div>`;
  } else if (!git.status) {
    body = `<div class="empty">尚未读取 Git 状态</div>`;
  } else if (!git.status.repository) {
    body = `<div class="empty">当前目录不在 Git 仓库中</div>`;
  } else {
    const status = git.status;
    const entries = status.entries || [];
    const branch = status.detached ? `detached · ${short(status.oid, 12)}` : status.branch || "unknown branch";
    const tracking = status.upstream ? `${status.upstream} · ahead ${status.ahead || 0} · behind ${status.behind || 0}` : "No upstream";
    body = `<div class="git-summary"><div><strong>${esc(branch)}</strong><div class="muted mono">${esc(status.repository_path || "/")} · ${esc(tracking)}</div></div><div class="actions"><button class="button" data-file-git-diff data-git-path="${esc(git.path)}" data-git-staged="false">Working diff</button><button class="button" data-file-git-diff data-git-path="${esc(git.path)}" data-git-staged="true">Staged diff</button></div></div>
      ${status.truncated ? `<div class="notice">状态项超过显示上限，仅展示前 ${entries.length} 项。</div>` : ""}
      <div class="git-status-list">${entries.length ? entries.map(fileGitStatusEntry).join("") : `<div class="empty">工作区干净</div>`}</div>`;
  }
  const diff = git.diff;
  const diffBody = git.loadingDiff ? `<div class="notice">正在读取 Diff…</div>` : diff ? `<section class="git-diff"><div class="section-head"><div><h3>${diff.staged ? "Staged" : "Working tree"} diff</h3><div class="muted mono">${esc(diff.path || "/")} · ${formatBytes(diff.bytes || 0)}</div></div><button class="button" data-file-git-diff-close>Close diff</button></div>${diff.diff ? `<pre data-file-git-diff-output>${esc(diff.diff)}</pre>` : `<div class="empty">没有匹配的差异</div>`}</section>` : "";
  return `<section class="section card file-git-card" data-file-git-panel><div class="section-head"><div><h2>Git</h2><div class="muted mono">${esc(git.path || "/")}</div></div><div class="actions"><button class="button" data-file-git-refresh>Refresh</button><button class="button" data-file-git-toggle>Close</button></div></div>${body}${git.error && git.status ? `<div class="error">${esc(git.error)}</div>` : ""}${diffBody}</section>`;
}
function fileGitStatusEntry(entry) {
  const staged = gitEntryHasStagedChanges(entry);
  const worktree = gitEntryHasWorktreeChanges(entry);
  const canOpen = entry.kind !== "deleted";
  const statusLabel = `${entry.index_status || "."}${entry.worktree_status || "."}`;
  return `<div class="git-status-row"><div class="git-status-code mono" title="index / worktree">${esc(statusLabel)}</div><div class="git-status-path"><strong>${esc(entry.path)}</strong>${entry.original_path ? `<div class="muted mono">from ${esc(entry.original_path)}</div>` : ""}<div class="muted">${esc(entry.kind || "changed")}</div></div><div class="actions">${staged ? `<button class="button" data-file-git-diff data-git-path="${esc(entry.path)}" data-git-staged="true">Staged</button>` : ""}${worktree ? `<button class="button" data-file-git-diff data-git-path="${esc(entry.path)}" data-git-staged="false">Working</button>` : ""}${canOpen ? `<button class="button" data-file-git-open="${esc(entry.path)}">Open</button>` : ""}</div></div>`;
}
function refreshFileGitPanel() {
  const panel = document.querySelector("[data-file-git-panel]");
  if (!panel) return;
  panel.outerHTML = fileGitPanel();
}
async function toggleFileGitPanel() {
  syncActiveFileTabFromDOM();
  const ctx = currentFileContext();
  state.fileGit.setContext(ctx.path);
  state.fileGit.open = !state.fileGit.open;
  await render();
}
async function loadFileGitStatus() {
  const git = state.fileGit;
  if (!git.open || git.loading) return;
  git.loading = true;
  git.error = "";
  refreshFileGitPanel();
  try {
    const result = await request(`/file-browser/git/status?${fileBrowserQuery({ path: git.path })}`);
    git.applyStatus(result);
  } catch (error) {
    git.fail(error);
  }
  refreshFileGitPanel();
}
async function loadFileGitDiff(path, staged) {
  const git = state.fileGit;
  git.loadingDiff = true;
  git.diff = null;
  git.error = "";
  refreshFileGitPanel();
  try {
    const query = new URLSearchParams(fileBrowserQuery({ path: path || git.path }));
    query.set("staged", String(Boolean(staged)));
    git.applyDiff(await request(`/file-browser/git/diff?${query}`));
  } catch (error) {
    git.fail(error);
  }
  refreshFileGitPanel();
  document.querySelector("[data-file-git-diff-output]")?.scrollIntoView({ behavior: "smooth", block: "nearest" });
}
function openFileFromGit(path) {
  return navigate(fileBrowserURL({ path: parentPath2(path), file: path }));
}
function joinFilePath(base, name) {
  base = String(base || "").replace(/^\/+|\/+$/g, "");
  name = String(name || "").replace(/^\/+|\/+$/g, "");
  return base ? `${base}/${name}` : name;
}
function filePreviewPath({ path }) {
  return `/file-browser/files/preview?${fileBrowserQuery({ path })}`;
}
function fileBrowserURL({ path, file, terminalTask, terminalSession, terminalPath, preserveTerminal = true }) {
  const q = new URLSearchParams();
  if (path) q.set("path", path);
  if (file) q.set("file", file);
  const current = new URLSearchParams(location.search);
  const hasTerminalContext = Boolean(current.get("terminal_task") || current.get("terminal_session") || state.fileTerminal.taskID || state.fileTerminal.sessionID);
  const sameTerminalScope = preserveTerminal && hasTerminalContext && state.fileTerminal.matchesPath(path);
  const taskValue = terminalTask !== void 0 ? terminalTask : sameTerminalScope ? current.get("terminal_task") || state.fileTerminal.taskID : "";
  const sessionValue = terminalSession !== void 0 ? terminalSession : sameTerminalScope ? current.get("terminal_session") || state.fileTerminal.sessionID : "";
  const terminalPathValue = terminalPath !== void 0 ? terminalPath : sameTerminalScope ? current.get("terminal_path") || state.fileTerminal.path : "";
  if (taskValue) q.set("terminal_task", taskValue);
  if (sessionValue) q.set("terminal_session", sessionValue);
  if (terminalPathValue) q.set("terminal_path", terminalPathValue);
  const suffix = q.toString();
  return `/admin/files${suffix ? `?${suffix}` : ""}`;
}
function currentFileContext() {
  const q = new URLSearchParams(location.search);
  return {
    path: q.get("path") || "",
    file: q.get("file") || ""
  };
}
async function hydrateFileTerminal({ path, taskID, sessionID }) {
  clearFileTerminalUpdates();
  if (!taskID && !sessionID) {
    state.fileTerminal.reset().setContext(path);
    return;
  }
  state.fileTerminal.setContext(path);
  if (taskID) state.fileTerminal.attachTask(taskID, state.fileTerminal.approval);
  if (sessionID) state.fileTerminal.attachSession(sessionID);
  try {
    if (state.fileTerminal.taskID) {
      const detail = await request(`/tasks/${encodeURIComponent(state.fileTerminal.taskID)}`);
      state.fileTerminal.applyTask(detail);
    }
    if (state.fileTerminal.sessionID) {
      await readFileTerminal();
    }
    syncFileTerminalURL();
  } catch (error) {
    state.fileTerminal.error = error.message || "终端状态读取失败";
  }
}
function fileTerminalPanel() {
  const terminal = state.fileTerminal;
  const mode = terminal.mode();
  const terminalPath = terminal.path || "/";
  let body = "";
  if (mode === "start") {
    body = `<div class="file-terminal-start"><label>Command <input class="mono" data-file-terminal-command placeholder="sh, npm test, go test ./..." value="sh"></label><label>Shell <input class="mono" data-file-terminal-shell placeholder="/bin/sh (optional)"></label><label class="terminal-check"><input type="checkbox" data-file-terminal-tty checked> TTY</label><button class="button primary" data-file-terminal-start>Start terminal</button></div><p class="muted">命令会在当前目录启动，并继续遵守实例级命令策略、环境变量 allowlist 和审批规则。</p>`;
  } else if (mode === "task") {
    const task = terminal.task || {};
    const approval = terminal.approval;
    body = `<div class="terminal-task-state"><div data-terminal-status>${badge(task.status || "queued")}</div><p class="muted">正在等待终端 Task 创建 Session。</p><div class="actions">${terminal.taskID ? `<button class="button" data-nav="/admin/history/${esc(terminal.taskID)}">Open task</button>` : ""}${approval?.id ? `<button class="button primary" data-nav="${esc(approvalReturnURL(approval.id, currentURL()))}">Open approval</button>` : ""}<button class="button" data-file-terminal-reset>New terminal</button></div>${terminal.error ? `<div class="error" data-terminal-error>${esc(terminal.error)}</div>` : `<div data-terminal-error></div>`}</div>`;
  } else {
    const session = terminal.session || {};
    body = `<div class="terminal-session-meta"><div data-terminal-status>${badge(session.status || "starting")}</div><div class="mono" title="${esc(terminal.sessionID)}">Session ${esc(short(terminal.sessionID, 24))}</div><div class="actions">${terminal.taskID ? `<button class="button" data-nav="/admin/history/${esc(terminal.taskID)}">Open task</button>` : ""}<button class="button" data-file-terminal-reset>New terminal</button><button class="button danger" data-file-terminal-close ${["closed", "exited", "failed"].includes(session.status) ? "disabled" : ""}>Close</button></div></div><div data-terminal-warning>${fileTerminalWarning()}</div><pre class="file-terminal-output" data-terminal-output tabindex="0">${esc(terminal.output())}</pre><div class="file-terminal-input"><input class="mono" data-file-terminal-stdin placeholder="Send input and press Enter" ${["closed", "exited", "failed"].includes(session.status) ? "disabled" : ""}><button class="button primary" data-file-terminal-send ${["closed", "exited", "failed"].includes(session.status) ? "disabled" : ""}>Send</button><button class="button" data-file-terminal-interrupt ${["closed", "exited", "failed"].includes(session.status) ? "disabled" : ""}>Ctrl-C</button></div>${terminal.error ? `<div class="error" data-terminal-error>${esc(terminal.error)}</div>` : `<div data-terminal-error></div>`}`;
  }
  return `<section class="section card file-terminal-card" data-terminal-mode="${mode}"><div class="section-head"><div><h2>Terminal</h2><div class="muted mono">${esc(terminalPath)}</div></div></div>${body}</section>`;
}
function fileTerminalWarning() {
  const terminal = state.fileTerminal;
  const messages = [];
  if (terminal.transport === "poll" && terminal.transportWarning) messages.push(terminal.transportWarning);
  if (terminal.cursorExpired) messages.push(`较早输出已被终端缓冲裁剪，当前从 cursor ${terminal.baseOffset} 继续。`);
  else if (terminal.truncated) messages.push("终端缓冲已达到保留上限，较早输出可能被裁剪。");
  return messages.length ? `<div class="notice">${esc(messages.join(" "))}</div>` : "";
}
function refreshFileTerminalPanel(force = false) {
  const current = document.querySelector("[data-terminal-mode]");
  const nextMode = state.fileTerminal.mode();
  if (!current) return;
  if (force || current.dataset.terminalMode !== nextMode) {
    current.outerHTML = fileTerminalPanel();
    scrollFileTerminalOutput();
    return;
  }
  const status = document.querySelector("[data-terminal-status]");
  const statusValue = nextMode === "session" ? state.fileTerminal.session?.status : state.fileTerminal.task?.status;
  if (status) status.innerHTML = badge(statusValue || (nextMode === "task" ? "queued" : "unknown"));
  const output = document.querySelector("[data-terminal-output]");
  if (output) {
    const nearBottom = output.scrollHeight - output.scrollTop - output.clientHeight < 48;
    output.textContent = state.fileTerminal.output();
    if (nearBottom) output.scrollTop = output.scrollHeight;
  }
  const warning = document.querySelector("[data-terminal-warning]");
  if (warning) warning.innerHTML = fileTerminalWarning();
  const error = document.querySelector("[data-terminal-error]");
  if (error) {
    error.className = state.fileTerminal.error ? "error" : "";
    error.textContent = state.fileTerminal.error || "";
  }
  const ended = ["closed", "exited", "failed"].includes(state.fileTerminal.session?.status);
  document.querySelectorAll("[data-file-terminal-stdin],[data-file-terminal-send],[data-file-terminal-interrupt],[data-file-terminal-close]").forEach((item) => {
    item.disabled = ended;
  });
}
function scrollFileTerminalOutput() {
  const output = document.querySelector("[data-terminal-output]");
  if (output) output.scrollTop = output.scrollHeight;
}
function scheduleFileTerminalPoll(delay = 800) {
  clearTerminalPoll();
  if (!state.fileTerminal.shouldPoll()) return;
  state.terminalPollTimer = setTimeout(pollFileTerminal, delay);
}
function startFileTerminalUpdates(delay = 100) {
  clearTerminalPoll();
  const terminal = state.fileTerminal;
  if (!terminal.shouldPoll()) return;
  if (!terminal.sessionID || terminal.transport === "poll") {
    scheduleFileTerminalPoll(delay);
    return;
  }
  startFileTerminalStream();
}
async function startFileTerminalStream() {
  const terminal = state.fileTerminal;
  if (!terminal.sessionID || !terminal.shouldPoll()) return;
  stopFileTerminalStream();
  clearTerminalPoll();
  const sessionID = terminal.sessionID;
  const controller = new AbortController();
  state.terminalStreamAbort = controller;
  terminal.transport = "stream";
  terminal.transportWarning = "";
  refreshFileTerminalPanel();
  const query = new URLSearchParams({ cursor: String(terminal.cursor), max_output_items: "200" });
  const headers = new Headers({ Accept: "text/event-stream" });
  if (state.token) headers.set("Authorization", `Bearer ${state.token}`);
  try {
    const response = await fetch(`${API}/file-browser/terminals/${encodeURIComponent(sessionID)}/stream?${query}`, { headers, signal: controller.signal });
    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      throw new Error(data.message || `Terminal stream HTTP ${response.status}`);
    }
    if (!String(response.headers.get("Content-Type") || "").toLowerCase().includes("text/event-stream")) {
      throw new Error("Terminal stream returned an unsupported content type");
    }
    await consumeSSE(response, (event) => {
      if (state.fileTerminal.sessionID !== sessionID) return;
      if (event.event === "error") {
        let message = event.data || "Terminal stream failed";
        try {
          message = JSON.parse(event.data)?.message || message;
        } catch {
        }
        throw new Error(message);
      }
      if (event.event !== "terminal") return;
      const payload = JSON.parse(event.data);
      state.fileTerminal.applyRead(payload);
      state.fileTerminal.transport = "stream";
      state.fileTerminal.transportWarning = "";
      refreshFileTerminalPanel();
      syncFileTerminalURL();
    });
    if (state.terminalStreamAbort === controller) state.terminalStreamAbort = null;
    if (state.fileTerminal.sessionID === sessionID && state.fileTerminal.shouldPoll()) {
      fallbackFileTerminalPolling("实时终端流意外结束，已回退到轮询。");
    }
  } catch (error) {
    if (state.terminalStreamAbort === controller) state.terminalStreamAbort = null;
    if (controller.signal.aborted || state.fileTerminal.sessionID !== sessionID) return;
    fallbackFileTerminalPolling(`实时终端流不可用，已回退到轮询：${error.message || "连接失败"}`);
  }
}
function fallbackFileTerminalPolling(message) {
  stopFileTerminalStream();
  const terminal = state.fileTerminal;
  if (!terminal.shouldPoll()) return;
  terminal.transport = "poll";
  terminal.transportWarning = message;
  refreshFileTerminalPanel();
  scheduleFileTerminalPoll(300);
}
async function pollFileTerminal() {
  state.terminalPollTimer = 0;
  if (location.pathname.replace(/\/$/, "") !== "/admin/files") return;
  const beforeMode = state.fileTerminal.mode();
  try {
    if (state.fileTerminal.taskID && !state.fileTerminal.sessionID) {
      const detail = await request(`/tasks/${encodeURIComponent(state.fileTerminal.taskID)}`);
      state.fileTerminal.applyTask(detail);
      syncFileTerminalURL();
    }
    if (state.fileTerminal.sessionID) {
      if (state.fileTerminal.transport !== "poll") {
        refreshFileTerminalPanel(beforeMode !== state.fileTerminal.mode());
        startFileTerminalStream();
        return;
      }
      await readFileTerminal();
    }
  } catch (error) {
    state.fileTerminal.error = error.message || "终端轮询失败";
  }
  refreshFileTerminalPanel(beforeMode !== state.fileTerminal.mode());
  scheduleFileTerminalPoll();
}
async function readFileTerminal() {
  const terminal = state.fileTerminal;
  if (!terminal.sessionID) return;
  const query = new URLSearchParams({ cursor: String(terminal.cursor), max_output_items: "200" });
  const result = await request(`/file-browser/terminals/${encodeURIComponent(terminal.sessionID)}?${query}`);
  terminal.applyRead(result);
}
function syncFileTerminalURL() {
  if (location.pathname.replace(/\/$/, "") !== "/admin/files") return;
  const ctx = currentFileContext();
  const next = fileBrowserURL({
    path: ctx.path,
    file: ctx.file,
    terminalTask: state.fileTerminal.taskID,
    terminalSession: state.fileTerminal.sessionID,
    terminalPath: state.fileTerminal.path,
    preserveTerminal: false
  });
  if (next !== currentURL()) history.replaceState({}, "", next);
}
function approvalReturnURL(approvalID, returnTo) {
  const query = new URLSearchParams();
  const safe = safeAdminReturnTo(returnTo);
  if (safe) query.set("return_to", safe);
  return `/admin/approvals/${encodeURIComponent(approvalID)}${query.toString() ? `?${query}` : ""}`;
}
function safeAdminReturnTo(value) {
  value = String(value || "").trim();
  if (!value.startsWith("/admin") || value.startsWith("//")) return "";
  try {
    const parsed = new URL(value, location.origin);
    if (parsed.origin !== location.origin || !parsed.pathname.startsWith("/admin")) return "";
    return `${parsed.pathname}${parsed.search}${parsed.hash}`;
  } catch {
    return "";
  }
}
async function startFileTerminal() {
  syncActiveFileTabFromDOM();
  const command = document.querySelector("[data-file-terminal-command]")?.value.trim() || "";
  const shell = document.querySelector("[data-file-terminal-shell]")?.value.trim() || "";
  const tty = Boolean(document.querySelector("[data-file-terminal-tty]")?.checked);
  if (!command) return alert("请输入要运行的命令");
  const ctx = currentFileContext();
  try {
    const result = await request("/file-browser/terminals", { method: "POST", body: JSON.stringify({ path: ctx.path, command, shell, tty, yield_time_ms: 100, max_output_items: 200 }) });
    state.fileTerminal.reset().setContext(ctx.path).attachTask(result.task_id, result.approval || null);
    const returnTo = fileBrowserURL({ path: ctx.path, file: ctx.file, terminalTask: result.task_id, terminalSession: "", terminalPath: ctx.path, preserveTerminal: false });
    history.replaceState({}, "", returnTo);
    if (result.approval?.id) {
      state.notice = `终端启动等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(approvalReturnURL(result.approval.id, returnTo), { force: true, preserveFileTabs: true });
    }
    refreshFileTerminalPanel(true);
    startFileTerminalUpdates(100);
  } catch (error) {
    state.fileTerminal.error = error.message || "终端启动失败";
    refreshFileTerminalPanel(true);
  }
}
async function sendFileTerminalInput(chars) {
  const terminal = state.fileTerminal;
  if (!terminal.sessionID) return;
  try {
    const result = await request(`/file-browser/terminals/${encodeURIComponent(terminal.sessionID)}/stdin`, { method: "POST", body: JSON.stringify({ chars, cursor: terminal.cursor, wait_ms: 100, max_output_items: 200 }) });
    terminal.applyRead(result);
    refreshFileTerminalPanel();
    if (terminal.transport === "poll") scheduleFileTerminalPoll();
  } catch (error) {
    terminal.error = error.message || "发送终端输入失败";
    refreshFileTerminalPanel();
  }
}
async function sendFileTerminalLine() {
  const input = document.querySelector("[data-file-terminal-stdin]");
  if (!input || input.disabled) return;
  const value = input.value;
  input.value = "";
  await sendFileTerminalInput(value.endsWith("\n") ? value : `${value}
`);
}
async function closeFileTerminal() {
  const terminal = state.fileTerminal;
  if (!terminal.sessionID || !confirm("确认关闭这个终端 Session？")) return;
  try {
    const result = await request(`/file-browser/terminals/${encodeURIComponent(terminal.sessionID)}/close`, { method: "POST", body: JSON.stringify({}) });
    terminal.session = result.session || terminal.session;
    if (terminal.transport === "poll") {
      try {
        await readFileTerminal();
      } catch {
      }
      clearTerminalPoll();
    }
    refreshFileTerminalPanel();
  } catch (error) {
    terminal.error = error.message || "关闭终端失败";
    refreshFileTerminalPanel();
  }
}
function resetFileTerminalPanel() {
  clearFileTerminalUpdates();
  const ctx = currentFileContext();
  state.fileTerminal.reset().setContext(ctx.path);
  const next = fileBrowserURL({ path: ctx.path, file: ctx.file, terminalTask: "", terminalSession: "", terminalPath: "", preserveTerminal: false });
  history.replaceState({}, "", next);
  refreshFileTerminalPanel(true);
}
function parentPath2(value) {
  const parts = String(value || "").split("/").filter(Boolean);
  parts.pop();
  return parts.join("/");
}
function formatBytes(value) {
  const n = Number(value || 0);
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}
async function loadImagePreview(context) {
  const image = document.querySelector("[data-file-image-preview]");
  if (!image) return;
  try {
    const result = await requestBlob(filePreviewPath(context));
    releaseImageObjectURL();
    state.imageObjectURL = URL.createObjectURL(result.blob);
    image.src = state.imageObjectURL;
    image.classList.remove("hidden");
    setFileStatus("notice", "图片预览已加载");
  } catch (error) {
    setFileStatus("error", `${error.message || "图片预览失败"}。可以尝试下载文件。`);
  }
}
async function downloadFile(context) {
  try {
    const result = await requestBlob(`/file-browser/files/download?${fileBrowserQuery(context)}`);
    const objectURL = URL.createObjectURL(result.blob);
    const link = document.createElement("a");
    link.href = objectURL;
    link.download = String(context.path || "download").split("/").pop() || "download";
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(objectURL), 0);
  } catch (error) {
    setFileStatus("error", error.message || "下载失败");
  }
}
function markdownInline(value) {
  let text = esc(value);
  text = text.replace(/`([^`]+)`/g, "<code>$1</code>");
  text = text.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  text = text.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  text = text.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  text = text.replace(/_([^_]+)_/g, "<em>$1</em>");
  return text;
}
function renderMarkdown(value) {
  const lines = String(value || "").replace(/\r\n?/g, "\n").split("\n");
  const out = [];
  let inCode = false;
  let code = [];
  let inList = false;
  const closeList = () => {
    if (!inList) return;
    out.push("</ul>");
    inList = false;
  };
  for (const line of lines) {
    if (/^```/.test(line)) {
      closeList();
      if (inCode) {
        out.push(`<pre><code>${esc(code.join("\n"))}</code></pre>`);
        code = [];
      }
      inCode = !inCode;
      continue;
    }
    if (inCode) {
      code.push(line);
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      closeList();
      const level = heading[1].length;
      out.push(`<h${level}>${markdownInline(heading[2])}</h${level}>`);
      continue;
    }
    const item = line.match(/^\s*[-*+]\s+(.+)$/);
    if (item) {
      if (!inList) {
        out.push("<ul>");
        inList = true;
      }
      out.push(`<li>${markdownInline(item[1])}</li>`);
      continue;
    }
    closeList();
    if (!line.trim()) {
      out.push("");
    } else if (/^>\s?/.test(line)) {
      out.push(`<blockquote>${markdownInline(line.replace(/^>\s?/, ""))}</blockquote>`);
    } else {
      out.push(`<p>${markdownInline(line)}</p>`);
    }
  }
  closeList();
  if (inCode) out.push(`<pre><code>${esc(code.join("\n"))}</code></pre>`);
  return out.join("\n");
}
function toggleMarkdownPreview() {
  const editor = document.querySelector("[data-file-editor]");
  const preview = document.querySelector("[data-markdown-preview]");
  const button = document.querySelector("[data-file-markdown-preview]");
  if (!editor || !preview || !button) return;
  const opening = preview.classList.contains("hidden");
  const tab = activeFileTab();
  if (opening) {
    preview.innerHTML = renderMarkdown(editor.value);
    preview.classList.remove("hidden");
    editor.classList.add("hidden");
    button.textContent = "Edit";
    if (tab) tab.previewMode = true;
  } else {
    preview.classList.add("hidden");
    editor.classList.remove("hidden");
    button.textContent = "Preview";
    if (tab) tab.previewMode = false;
  }
}
function lineDiff(serverText, localText) {
  const server = String(serverText || "").split("\n");
  const local = String(localText || "").split("\n");
  const max = Math.max(server.length, local.length);
  const serverLines = [];
  const localLines = [];
  for (let i = 0; i < max; i++) {
    const changed = server[i] !== local[i];
    const cls = changed ? " diff-line-changed" : "";
    serverLines.push(`<span class="diff-line${cls}">${esc(server[i] ?? "")}</span>`);
    localLines.push(`<span class="diff-line${cls}">${esc(local[i] ?? "")}</span>`);
  }
  return `<div class="diff-grid"><section class="diff-pane"><h3>Server</h3><pre>${serverLines.join("\n")}</pre></section><section class="diff-pane"><h3>Your changes</h3><pre>${localLines.join("\n")}</pre></section></div>`;
}
function showFileConflict(serverContent) {
  const editor = document.querySelector("[data-file-editor]");
  const target = document.querySelector("[data-file-status]");
  if (!editor || !target) return;
  const tab = activeFileTab();
  state.fileConflict = serverContent;
  if (tab) tab.conflict = serverContent;
  target.innerHTML = fileConflictPanel();
}
function reloadConflictFile() {
  const editor = document.querySelector("[data-file-editor]");
  const server = state.fileConflict;
  if (!editor || !server) return;
  if (!confirm("重新加载会丢弃当前未保存修改，确定继续？")) return;
  editor.value = server.content;
  editor.dataset.etag = server.etag;
  const tab = activeFileTab();
  if (tab) {
    tab.content = server.content;
    tab.savedContent = server.content;
    tab.etag = server.etag;
    tab.language = server.language;
    tab.size = server.size;
    tab.modTime = server.mod_time;
    tab.conflict = null;
  }
  setFileDirty(false);
  state.fileConflict = null;
  setFileStatus("notice", "已加载服务器最新版本");
  const preview = document.querySelector("[data-markdown-preview]");
  if (preview && !preview.classList.contains("hidden")) preview.innerHTML = renderMarkdown(editor.value);
}
function showConflictDiff() {
  const editor = document.querySelector("[data-file-editor]");
  const server = state.fileConflict;
  const target = document.querySelector("[data-file-conflict-diff-view]");
  if (!editor || !server || !target) return;
  target.innerHTML = lineDiff(server.content, editor.value);
  target.classList.remove("hidden");
}
function setFileStatus(kind, message) {
  const target = document.querySelector("[data-file-status]");
  if (!target) return;
  const cls = kind === "error" ? "error" : "notice";
  target.innerHTML = `<div class="${cls}">${esc(message)}</div>`;
}
function setFileDirty(dirty) {
  state.fileDirty = Boolean(dirty);
  const tab = activeFileTab();
  if (tab) tab.dirty = state.fileDirty;
  const badge2 = document.querySelector("[data-file-dirty]");
  if (badge2) badge2.classList.toggle("hidden", !dirty);
  if (!tab) return;
  const tabElement = Array.from(document.querySelectorAll("[data-file-tab]")).find((item) => item.dataset.fileTab === tab.key)?.closest(".file-tab");
  if (!tabElement) return;
  tabElement.classList.toggle("dirty", state.fileDirty);
  let marker = tabElement.querySelector(".file-tab-dirty");
  if (state.fileDirty && !marker) {
    marker = document.createElement("span");
    marker.className = "file-tab-dirty";
    marker.setAttribute("aria-label", "Unsaved");
    marker.textContent = "●";
    tabElement.querySelector(".file-tab-open")?.appendChild(marker);
  } else if (!state.fileDirty) {
    marker?.remove();
  }
}
async function reloadActiveFile() {
  syncActiveFileTabFromDOM();
  const tab = activeFileTab();
  if (!tab) return render();
  if (tab.dirty && !confirm(`重新加载会丢弃 ${tab.path} 的未保存修改，确定继续？`)) return false;
  if (tab.imageFile) {
    tab.conflict = null;
    state.fileConflict = null;
    return render();
  }
  try {
    const content = await request(`/file-browser/files/content?${fileBrowserQuery({ path: tab.path })}`);
    Object.assign(tab, {
      error: "",
      content: content.content,
      savedContent: content.content,
      etag: content.etag,
      language: content.language,
      size: content.size,
      modTime: content.mod_time,
      dirty: false,
      conflict: null
    });
    state.fileConflict = null;
    state.notice = "文件已重新加载";
    return render();
  } catch (error) {
    setFileStatus("error", error.message || "重新加载失败");
    return false;
  }
}
async function uploadFileToCurrentDirectory(file) {
  if (!file) return;
  syncActiveFileTabFromDOM();
  const ctx = currentFileContext();
  const target = joinFilePath(ctx.path, file.name);
  const form = new FormData();
  form.set("path", target);
  form.set("create", "true");
  form.set("file", file, file.name);
  try {
    const result = await request("/file-browser/files/upload", { method: "POST", body: form });
    if (result.approval?.id) {
      state.notice = `上传等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(approvalReturnURL(result.approval.id, currentURL()), { force: true, preserveFileTabs: true });
    }
    state.notice = "文件已上传";
    return navigate(fileBrowserURL({ path: ctx.path, file: target }), { force: true });
  } catch (error) {
    alert(error.message || "上传失败");
  }
}
async function createFileInCurrentDirectory() {
  syncActiveFileTabFromDOM();
  const name = prompt("New file name");
  if (name === null) return;
  const clean = name.trim();
  if (!clean || clean.includes("/")) return alert("请输入当前目录下的文件名，不要包含 /");
  const ctx = currentFileContext();
  const newPath = joinFilePath(ctx.path, clean);
  try {
    const result = await request("/file-browser/files/content", { method: "PUT", body: JSON.stringify({ path: newPath, content: "", create: true }) });
    if (result.approval?.id) {
      state.notice = `新建文件等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(approvalReturnURL(result.approval.id, currentURL()), { force: true, preserveFileTabs: true });
    }
    state.notice = "文件已创建";
    return navigate(fileBrowserURL({ path: ctx.path, file: newPath }), { force: true });
  } catch (error) {
    alert(error.message || "新建文件失败");
  }
}
async function createFolderInCurrentDirectory() {
  syncActiveFileTabFromDOM();
  const name = prompt("New folder name");
  if (name === null) return;
  const clean = name.trim();
  if (!clean || clean.includes("/")) return alert("请输入当前目录下的目录名，不要包含 /");
  const ctx = currentFileContext();
  const newPath = joinFilePath(ctx.path, clean);
  try {
    const result = await request("/file-browser/files/mkdir", { method: "POST", body: JSON.stringify({ path: newPath }) });
    if (result.approval?.id) {
      state.notice = `新建目录等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(approvalReturnURL(result.approval.id, currentURL()), { force: true, preserveFileTabs: true });
    }
    state.notice = "目录已创建";
    return render();
  } catch (error) {
    alert(error.message || "新建目录失败");
  }
}
async function deleteFileEntry(path, type = "file") {
  if (!path) return;
  const recursive = type === "directory";
  syncActiveFileTabFromDOM();
  const ctx = currentFileContext();
  const affectedDirty = state.fileTabs.matching(path, recursive).filter((tab) => tab.dirty);
  if (affectedDirty.length && !confirm(`删除会同时关闭 ${affectedDirty.length} 个有未保存修改的标签，确定继续？`)) return;
  if (!confirm(recursive ? `确认递归删除目录 ${path} 及其中全部内容？` : `确认删除 ${path}？`)) return;
  try {
    const result = await request("/file-browser/files/delete", { method: "POST", body: JSON.stringify({ path, recursive, confirmation: recursive ? `delete ${path}` : "" }) });
    if (result.approval?.id) {
      state.notice = `删除等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(approvalReturnURL(result.approval.id, currentURL()), { force: true, preserveFileTabs: true });
    }
    state.notice = recursive ? "目录已删除" : "文件已删除";
    state.fileTabs.removeMatching(path, recursive);
    const active = activeFileTab();
    return navigate(active ? fileTabURL(active) : fileBrowserURL({ path: ctx.path, file: "" }), { force: true });
  } catch (error) {
    setFileStatus("error", error.message || "删除失败");
  }
}
async function deleteCurrentFile() {
  const editor = document.querySelector("[data-file-editor]");
  if (editor) return deleteFileEntry(editor.dataset.path, "file");
}
async function renameFileEntry(path, type = "file") {
  if (!path) return;
  syncActiveFileTabFromDOM();
  const next = prompt("New path", path);
  if (next === null) return;
  const clean = next.trim().replace(/^\/+|\/+$/g, "");
  if (!clean) return alert("请输入目标路径");
  if (clean === path) return;
  const ctx = currentFileContext();
  try {
    const result = await request("/file-browser/files/rename", { method: "POST", body: JSON.stringify({ from_path: path, to_path: clean }) });
    if (result.approval?.id) {
      state.notice = `重命名等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(approvalReturnURL(result.approval.id, currentURL()), { force: true, preserveFileTabs: true });
    }
    state.notice = type === "directory" ? "目录已重命名" : "文件已重命名";
    state.fileTabs.renameMatching(path, clean, type === "directory");
    const active = activeFileTab();
    return navigate(active ? fileTabURL(active) : fileBrowserURL({ path: ctx.path, file: "" }), { force: true });
  } catch (error) {
    setFileStatus("error", error.message || "重命名失败");
  }
}
async function renameCurrentFile() {
  const editor = document.querySelector("[data-file-editor]");
  if (editor) return renameFileEntry(editor.dataset.path, "file");
}
async function saveCurrentFile(options = {}) {
  const editor = document.querySelector("[data-file-editor]");
  if (!editor) return;
  syncActiveFileTabFromDOM();
  const tab = activeFileTab();
  if (!tab) return;
  const overwrite = Boolean(options.overwrite);
  setFileStatus("notice", overwrite ? "正在覆盖服务器版本…" : "正在保存…");
  try {
    const result = await request("/file-browser/files/content", {
      method: "PUT",
      body: JSON.stringify({
        path: editor.dataset.path,
        content: editor.value,
        etag: editor.dataset.etag,
        create: false,
        overwrite
      })
    });
    if (result.approval?.id) {
      state.notice = `${overwrite ? "覆盖保存" : "保存"}等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(approvalReturnURL(result.approval.id, currentURL()), { force: true, preserveFileTabs: true });
    }
    state.notice = overwrite ? "文件已覆盖保存" : "文件已保存";
    state.fileConflict = null;
    Object.assign(tab, {
      content: editor.value,
      savedContent: editor.value,
      etag: result.etag,
      size: result.size,
      modTime: result.mod_time,
      dirty: false,
      conflict: null
    });
    return render();
  } catch (error) {
    if (error.code === "file_changed" && !overwrite) {
      try {
        const serverContent = await request(`/file-browser/files/content?${fileBrowserQuery({ path: editor.dataset.path })}`);
        showFileConflict(serverContent);
      } catch (reloadError) {
        setFileStatus("error", `文件已被外部修改，且无法读取服务器最新版本：${reloadError.message || "读取失败"}`);
      }
      return;
    }
    setFileStatus("error", error.message || "保存失败");
  }
}
function overwriteCurrentFile() {
  if (!activeFileTab()?.conflict) return;
  if (!confirm("覆盖会用当前编辑内容替换服务器最新版本，并可能触发审批。确定继续？")) return;
  return saveCurrentFile({ overwrite: true });
}
async function renderDeployments() {
  const data = await request("/deployments");
  const rows = (data.items || []).map((item) => `<tr><td><strong>${esc(item.Site || item.site)}</strong><div class="mono">${esc(item.Type || item.type)}</div></td><td>${badge(item.Status || item.status)}</td><td>${badge(item.Access || item.access || "public")}</td><td>${esc(item.Health || item.health || "—")}</td><td><a class="link" href="${esc(item.URL || item.url)}" target="_blank" rel="noopener">Open</a><div class="mono">${esc(item.Hostname || item.hostname || "")}</div></td><td>${esc(item.ReleaseID || item.release_id || "—")}</td><td>${deploymentActions(item)}</td></tr>`).join("");
  app.innerHTML = layout("Deployments", table("<tr><th>Name</th><th>Status</th><th>Access</th><th>Health</th><th>URL</th><th>Release</th><th>Actions</th></tr>", rows));
}
function deploymentActions(item) {
  const type = item.Type || item.type, name = item.Site || item.site, status = item.Status || item.status, access = item.Access || item.access || "public";
  let buttons = `<button class="button" data-deploy-action="${access === "private" ? "set-public" : "set-private"}" data-type="${esc(type)}" data-name="${esc(name)}">${access === "private" ? "Make public" : "Make private"}</button>`;
  if (type === "dynamic") {
    if (["stopped", "failed"].includes(status)) buttons += `<button class="button success" data-deploy-action="start" data-type="${esc(type)}" data-name="${esc(name)}">Start</button>`;
    else buttons += `<button class="button" data-deploy-action="stop" data-type="${esc(type)}" data-name="${esc(name)}">Stop</button>`;
    buttons += `<button class="button" data-deploy-action="restart" data-type="${esc(type)}" data-name="${esc(name)}">Restart</button>`;
  }
  return `<div class="actions">${buttons}<button class="button danger" data-deploy-action="delete" data-type="${esc(type)}" data-name="${esc(name)}">Delete</button></div>`;
}
async function renderApprovals() {
  const query = new URLSearchParams(location.search);
  if (!query.has("status")) query.set("status", "pending");
  const data = await request(`/approvals?${query}`);
  const rows = (data.items || []).map((item) => `<tr><td>${fmtTime(item.created_at)}</td><td><button class="link" data-nav="/admin/approvals/${esc(item.id)}">${esc(item.summary || item.tool)}</button><div class="mono">${esc(item.id)}</div></td><td>${esc(item.tool)}</td><td>${badge(item.risk_level || "unknown")}</td><td>${badge(item.status)}</td><td>${sessionBadge(item)}</td></tr>`).join("");
  app.innerHTML = layout("Approvals", `<form class="filters" id="approval-filters"><select name="status">${["pending", "approved", "rejected", "canceled", "expired", ""].map((v) => `<option value="${v}" ${query.get("status") === v ? "selected" : ""}>${v || "all"}</option>`).join("")}</select><input name="session_id" placeholder="Session ID" value="${esc(query.get("session_id") || "")}"><input name="tool" placeholder="Tool" value="${esc(query.get("tool") || "")}"><button class="button primary">Filter</button></form>${table("<tr><th>Created</th><th>Request</th><th>Tool</th><th>Risk</th><th>Status</th><th>Session</th></tr>", rows)}`);
}
async function renderApproval(id) {
  const data = await request(`/approvals/${id}`), item = data.approval;
  const returnTo = safeAdminReturnTo(new URLSearchParams(location.search).get("return_to"));
  const actions = item.status === "pending" ? `<button class="button success" data-approval-action="approve" data-id="${esc(id)}">Approve</button><button class="button danger" data-approval-action="reject" data-id="${esc(id)}">Reject</button><button class="button" data-approval-action="cancel" data-id="${esc(id)}">Cancel</button>` : "";
  app.innerHTML = layout(`Approval ${short(id, 12)}`, `<section class="card detail-grid">${kv("Status", badge(item.status))}${kv("Summary", item.summary || "—")}${kv("Tool", item.tool)}${kv("Risk", item.risk_level || "—")}${kv("Requester", item.requester || "—")}${kv("Session", item.session_id || "No session")}${kv("Created", fmtTime(item.created_at))}${kv("Expires", fmtTime(item.expires_at))}</section><section class="section"><div class="actions">${actions}${item.task_id ? `<button class="button" data-nav="/admin/history/${esc(item.task_id)}">Open task</button>` : ""}${returnTo ? `<button class="button" data-nav="${esc(returnTo)}">Return to Files</button>` : ""}</div></section><section class="section card"><h2>Arguments</h2><pre>${esc(JSON.stringify(item.args || {}, null, 2))}</pre></section><section class="section card"><h2>Audit</h2><pre>${esc(JSON.stringify(data.audits || [], null, 2))}</pre></section>`);
}
async function mutate(path, options = {}) {
  state.error = "";
  const { returnTo = "", ...requestOptions } = options;
  const result = await request(path, requestOptions);
  if (result.approval?.id && result.status === "awaiting_approval") {
    state.notice = `操作等待审批：${result.approval.summary || result.approval.id}`;
    history.pushState({}, "", approvalReturnURL(result.approval.id, returnTo));
    await render();
    return;
  }
  state.notice = "操作已提交";
  const safeReturn = safeAdminReturnTo(returnTo);
  if (safeReturn) {
    navigate(safeReturn, { force: true, preserveFileTabs: true });
    return;
  }
  await render();
}
document.addEventListener("click", async (event) => {
  const nav = event.target.closest("[data-nav]");
  if (nav) {
    event.preventDefault();
    return navigate(nav.dataset.nav);
  }
  const action = event.target.closest("[data-action]")?.dataset.action;
  if (action === "toggle-sidebar") return setSidebarCollapsed(!state.sidebarCollapsed);
  if (action === "token") {
    if (!confirmDiscardFileChanges()) return;
    resetFileTabs();
    return loginView();
  }
  if (action === "save-token") {
    state.token = document.querySelector("#token-input")?.value.trim() || "";
    sessionStorage.setItem("localgate_admin_token", state.token);
    return navigate(location.pathname);
  }
  if (action === "clear-history") {
    history.replaceState({}, "", "/admin/history");
    return render();
  }
  if (action === "next-history") {
    const q = new URLSearchParams(location.search);
    q.set("cursor", event.target.dataset.cursor);
    history.replaceState({}, "", `${location.pathname}?${q}`);
    return render();
  }
  const fileDir = event.target.closest("[data-file-dir]");
  if (fileDir) return navigate(fileBrowserURL({ path: fileDir.dataset.fileDir, file: "" }));
  const fileOpen = event.target.closest("[data-file-open]");
  if (fileOpen) {
    const ctx = currentFileContext();
    return navigate(fileBrowserURL({ path: ctx.path, file: fileOpen.dataset.fileOpen }));
  }
  const fileTab = event.target.closest("[data-file-tab]");
  if (fileTab) {
    syncActiveFileTabFromDOM();
    const tab = state.fileTabs.activate(fileTab.dataset.fileTab);
    if (tab) return navigate(fileTabURL(tab), { force: true });
    return;
  }
  const fileTabClose = event.target.closest("[data-file-tab-close]");
  if (fileTabClose) return closeFileTab(fileTabClose.dataset.fileTabClose);
  const fileEntryRename = event.target.closest("[data-file-entry-rename]");
  if (fileEntryRename) return renameFileEntry(fileEntryRename.dataset.entryPath, fileEntryRename.dataset.entryType);
  const fileEntryDelete = event.target.closest("[data-file-entry-delete]");
  if (fileEntryDelete) return deleteFileEntry(fileEntryDelete.dataset.entryPath, fileEntryDelete.dataset.entryType);
  if (event.target.closest("[data-file-new-file]")) return createFileInCurrentDirectory();
  if (event.target.closest("[data-file-new-folder]")) return createFolderInCurrentDirectory();
  if (event.target.closest("[data-file-upload]")) {
    document.querySelector("[data-file-upload-input]")?.click();
    return;
  }
  if (event.target.closest("[data-file-git-toggle]")) return toggleFileGitPanel();
  if (event.target.closest("[data-file-git-refresh]")) {
    state.fileGit.status = null;
    state.fileGit.diff = null;
    return loadFileGitStatus();
  }
  if (event.target.closest("[data-file-git-diff-close]")) {
    state.fileGit.clearDiff();
    refreshFileGitPanel();
    return;
  }
  const gitDiff = event.target.closest("[data-file-git-diff]");
  if (gitDiff) return loadFileGitDiff(gitDiff.dataset.gitPath, gitDiff.dataset.gitStaged === "true");
  const gitOpen = event.target.closest("[data-file-git-open]");
  if (gitOpen) return openFileFromGit(gitOpen.dataset.fileGitOpen);
  if (event.target.closest("[data-file-terminal-open]")) {
    const panel = document.querySelector("[data-terminal-mode]");
    panel?.scrollIntoView({ behavior: "smooth", block: "start" });
    if (panel?.dataset.terminalMode === "start") setTimeout(() => document.querySelector("[data-file-terminal-command]")?.focus(), 200);
    return;
  }
  if (event.target.closest("[data-file-terminal-start]")) return startFileTerminal();
  if (event.target.closest("[data-file-terminal-send]")) return sendFileTerminalLine();
  if (event.target.closest("[data-file-terminal-interrupt]")) return sendFileTerminalInput("");
  if (event.target.closest("[data-file-terminal-close]")) return closeFileTerminal();
  if (event.target.closest("[data-file-terminal-reset]")) return resetFileTerminalPanel();
  if (event.target.closest("[data-file-markdown-preview]")) return toggleMarkdownPreview();
  if (event.target.closest("[data-file-save]")) return saveCurrentFile();
  if (event.target.closest("[data-file-rename]")) return renameCurrentFile();
  if (event.target.closest("[data-file-download]")) {
    const editor = document.querySelector("[data-file-editor]");
    if (editor) return downloadFile({ path: editor.dataset.path });
    return;
  }
  const fileDownload = event.target.closest("[data-file-download-path]");
  if (fileDownload) return downloadFile({ path: fileDownload.dataset.fileDownloadPath });
  if (event.target.closest("[data-file-delete]")) return deleteCurrentFile();
  if (event.target.closest("[data-file-conflict-reload]")) return reloadConflictFile();
  if (event.target.closest("[data-file-conflict-overwrite]")) return overwriteCurrentFile();
  if (event.target.closest("[data-file-conflict-diff]")) return showConflictDiff();
  if (event.target.closest("[data-file-reload]")) return reloadActiveFile();
  if (event.target.closest("[data-file-refresh]")) {
    syncActiveFileTabFromDOM();
    if (state.fileGit.open) {
      state.fileGit.status = null;
      state.fileGit.diff = null;
    }
    return render();
  }
  if (action === "cancel-task" && confirm("确认取消这个 Task？")) return mutate(`/tasks/${event.target.dataset.id}/cancel`, { method: "POST" });
  const approval = event.target.closest("[data-approval-action]");
  if (approval && confirm(`确认 ${approval.dataset.approvalAction}？`)) return mutate(`/approvals/${approval.dataset.id}/${approval.dataset.approvalAction}`, { method: "POST", returnTo: safeAdminReturnTo(new URLSearchParams(location.search).get("return_to")) });
  const deploy = event.target.closest("[data-deploy-action]");
  if (deploy) {
    const act = deploy.dataset.deployAction;
    if (!confirm(`确认对 ${deploy.dataset.name} 执行 ${act}？`)) return;
    const encoded = `/deployments/${encodeURIComponent(deploy.dataset.type)}/${encodeURIComponent(deploy.dataset.name)}`;
    return mutate(act === "delete" ? encoded : `${encoded}/${act}`, { method: act === "delete" ? "DELETE" : "POST", body: act === "delete" ? JSON.stringify({ confirmation: "delete" }) : void 0 });
  }
});
document.addEventListener("input", (event) => {
  if (event.target.closest("[data-file-editor]")) {
    const tab = state.fileTabs.get(event.target.dataset.tabKey);
    if (tab) tab.content = event.target.value;
    setFileDirty(tab ? event.target.value !== tab.savedContent : true);
    const preview = document.querySelector("[data-markdown-preview]");
    if (preview && !preview.classList.contains("hidden")) preview.innerHTML = renderMarkdown(event.target.value);
  }
});
document.addEventListener("change", (event) => {
  const uploadInput = event.target.closest("[data-file-upload-input]");
  if (uploadInput) {
    const file = uploadInput.files?.[0];
    uploadInput.value = "";
    return uploadFileToCurrentDirectory(file);
  }
});
document.addEventListener("keydown", (event) => {
  const terminalInput = event.target.closest("[data-file-terminal-stdin]");
  if (terminalInput && event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    sendFileTerminalLine();
  }
});
document.addEventListener("submit", (event) => {
  event.preventDefault();
  const form = new FormData(event.target), q = new URLSearchParams();
  for (const [k, v] of form) if (v) q.set(k, v);
  history.replaceState({}, "", `${location.pathname}?${q}`);
  render();
});
window.addEventListener("beforeunload", (event) => {
  if (!hasUnsavedFileChanges()) return;
  event.preventDefault();
  event.returnValue = "";
});
window.addEventListener("popstate", () => {
  syncActiveFileTabFromDOM();
  if (location.pathname.replace(/\/$/, "") === "/admin/files") {
    render();
    return;
  }
  if (!confirmDiscardFileChanges()) {
    history.pushState({}, "", state.fileURL || "/admin/files");
    return;
  }
  resetFileTabs();
  render();
});
render();
