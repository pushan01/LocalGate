const API = "/api/admin/v1";
const app = document.querySelector("#app");
const state = { token: sessionStorage.getItem("localgate_admin_token") || "", error: "", notice: "", fileDirty: false, fileURL: "" };

const esc = (value) => String(value ?? "").replace(/[&<>'"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","'":"&#39;",'"':"&quot;"}[c]));
const short = (value, n=18) => value ? (value.length > n ? `${value.slice(0,n)}…` : value) : "—";
const fmtTime = value => value ? new Date(value).toLocaleString() : "—";
const fmtDuration = value => value ? (value < 1000 ? `${value} ms` : `${(value/1000).toFixed(1)} s`) : "—";
const badge = value => `<span class="badge ${esc(value)}">${esc(value || "unknown")}</span>`;
const sessionBadge = item => item.session_id
  ? `<div class="mono" title="${esc(item.session_id)}">${esc(short(item.session_id,22))}</div><div class="muted">${esc(item.session_mode || "")} ${item.session_id_source === "client_supplied" ? "· Client supplied" : ""}</div>`
  : `<span class="muted">No session</span>`;

async function request(path, options={}) {
  const headers = new Headers(options.headers || {});
  if (state.token) headers.set("Authorization", `Bearer ${state.token}`);
  if (options.body && !(options.body instanceof FormData) && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const response = await fetch(`${API}${path}`, {...options, headers});
  if (response.status === 401) throw Object.assign(new Error("需要管理 Token"), {unauthorized:true});
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

function currentURL() {
  return `${location.pathname}${location.search}${location.hash}`;
}

function hasUnsavedFileChanges() {
  return Boolean(state.fileDirty && document.querySelector("[data-file-editor]"));
}

function confirmDiscardFileChanges(message="当前文件有未保存修改。离开后这些修改会丢失，确定继续？") {
  return !hasUnsavedFileChanges() || confirm(message);
}

function navigate(path, options={}) {
  if (!options.force && !confirmDiscardFileChanges()) return false;
  setFileDirty(false);
  history.pushState({}, "", path);
  render();
  return true;
}

function layout(title, body, actions="") {
  const path = location.pathname;
  const notice = state.notice;
  state.notice = "";
  return `<div class="shell"><aside class="sidebar"><div class="brand">LocalGate Admin</div><nav class="nav">
    <a data-nav="/admin" class="${path==="/admin"||path==="/admin/"?"active":""}">Overview</a>
    <a data-nav="/admin/history" class="${path.startsWith("/admin/history")?"active":""}">History</a>
    <a data-nav="/admin/files" class="${path.startsWith("/admin/files")?"active":""}">Files</a>
    <a data-nav="/admin/deployments" class="${path.startsWith("/admin/deployments")?"active":""}">Deployments</a>
    <a data-nav="/admin/approvals" class="${path.startsWith("/admin/approvals")?"active":""}">Approvals</a>
  </nav></aside><main class="content"><header class="topbar"><h1>${esc(title)}</h1><div class="actions">${actions}<button class="button" data-action="token">Token</button></div></header>
  ${state.error ? `<div class="error">${esc(state.error)}</div>` : ""}${notice ? `<div class="notice">${esc(notice)}</div>` : ""}${body}</main></div>`;
}

function loginView(message="") {
  app.innerHTML = `<section class="login"><h1>LocalGate Admin</h1><p class="muted">输入 HTTP Bearer Token。未启用认证的实例可以留空。</p>${message?`<div class="error">${esc(message)}</div>`:""}<input id="token-input" type="password" autocomplete="current-password" placeholder="Bearer token"><button class="button primary" data-action="save-token">进入后台</button></section>`;
}

async function render() {
  state.error = "";
  const path = location.pathname.replace(/\/$/, "") || "/admin";
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
const metric = (label,value) => `<article class="card"><div class="muted">${esc(label)}</div><div class="metric">${esc(value)}</div></article>`;

function taskHeaders(){return "<tr><th>Time</th><th>Tool</th><th>Status</th><th>Session</th><th>Transport</th><th>Duration</th><th>Requester</th></tr>"}
function taskRow(item){return `<tr><td>${fmtTime(item.created_at)}</td><td><button class="link mono" data-nav="/admin/history/${esc(item.id)}">${esc(item.tool||item.id)}</button></td><td>${badge(item.status)}</td><td>${sessionBadge(item)}</td><td>${esc(item.transport||"—")}</td><td>${fmtDuration(item.duration_ms)}</td><td>${esc(item.requester||"—")}</td></tr>`}
function table(headers, rows){return `<div class="table-wrap"><table><thead>${headers}</thead><tbody>${rows || `<tr><td colspan="8" class="empty">暂无数据</td></tr>`}</tbody></table></div>`}

async function renderHistory() {
  const query = new URLSearchParams(location.search);
  const data = await request(`/tasks?${query}`);
  const rows = (data.items || []).map(taskRow).join("");
  app.innerHTML = layout("History", `<form class="filters" id="history-filters">
    <input name="session_id" placeholder="Session ID" value="${esc(query.get("session_id")||"")}"><input name="tool" placeholder="Tool" value="${esc(query.get("tool")||"")}">
    <select name="status"><option value="">All statuses</option>${["awaiting_approval","queued","running","succeeded","failed","canceled","timeout"].map(v=>`<option ${query.get("status")===v?"selected":""}>${v}</option>`).join("")}</select>
    <select name="transport"><option value="">All transports</option>${["mcp_http","mcp_stdio","mcp_runtime","rest","tool_http","admin","system"].map(v=>`<option ${query.get("transport")===v?"selected":""}>${v}</option>`).join("")}</select>
    <button class="button primary">Filter</button><button type="button" class="button" data-action="clear-history">Clear</button>
  </form>${table(taskHeaders(), rows)}${data.next_cursor?`<div class="section"><button class="button" data-action="next-history" data-cursor="${esc(data.next_cursor)}">Next page</button></div>`:""}`);
}

async function renderTask(id) {
  const [detail, events, audits] = await Promise.all([request(`/tasks/${id}`), request(`/tasks/${id}/events`), request(`/tasks/${id}/audits`)]);
  const task = detail.task;
  app.innerHTML = layout(`Task ${short(id,12)}`, `<section class="card detail-grid">${kv("Status",badge(task.status))}${kv("Tool",task.tool)}${kv("Transport",task.transport||"—")}${kv("Session",task.session_id||"No session")}${kv("Session source",task.session_id_source||"none")}${kv("Workspace",task.workspace_id||"—")}${kv("Requester",task.requester||"—")}${kv("Created",fmtTime(task.created_at))}</section>
  ${task.error_msg?`<section class="section card"><h2>Error</h2><p>${esc(task.error_msg)}</p></section>`:""}
  <section class="section card"><div class="section-head"><h2>Result</h2>${!["succeeded","failed","canceled","timeout"].includes(task.status)?`<button class="button danger" data-action="cancel-task" data-id="${esc(id)}">Cancel</button>`:""}</div><pre>${esc(JSON.stringify(task.result??{},null,2))}</pre></section>
  <section class="section card"><h2>Events</h2><pre>${esc(JSON.stringify(events.items||[],null,2))}</pre></section>
  <section class="section card"><h2>Audit</h2><pre>${esc(JSON.stringify(audits.items||[],null,2))}</pre></section>`);
}
const kv=(label,value)=>`<div class="kv"><strong>${esc(label)}</strong><div>${value&&String(value).startsWith("<span")?value:esc(value)}</div></div>`;

async function renderFiles() {
  const workspacesData = await request("/file-browser/workspaces");
  const workspaces = workspacesData.items || [];
  if (!workspaces.length) {
    app.innerHTML = layout("Files", `<div class="empty">没有可浏览的 workspace</div>`);
    return;
  }
  const query = new URLSearchParams(location.search);
  let workspaceID = query.get("workspace_id") || workspaces[0].id;
  let workspace = workspaces.find(item => item.id === workspaceID) || workspaces[0];
  workspaceID = workspace.id;
  const roots = workspace.roots || [];
  if (!roots.length) {
    app.innerHTML = layout("Files", `<div class="empty">当前 workspace 没有可浏览 root</div>`);
    return;
  }
  let rootID = query.get("root_id") || roots[0].id;
  if (!roots.some(root => root.id === rootID)) rootID = roots[0].id;
  const dirPath = query.get("path") || "";
  const filePath = query.get("file") || "";
  const listQuery = fileBrowserQuery({workspaceID, rootID, path: dirPath});
  const list = await request(`/file-browser/files/list?${listQuery}`);
  let fileContent = null;
  let fileError = "";
  if (filePath) {
    try {
      fileContent = await request(`/file-browser/files/content?${fileBrowserQuery({workspaceID, rootID, path: filePath})}`);
    } catch (error) {
      fileError = error.message;
    }
  }
  const workspaceOptions = workspaces.map(item => `<option value="${esc(item.id)}" ${item.id===workspaceID?"selected":""}>${esc(item.id)}</option>`).join("");
  const rootOptions = roots.map(root => `<option value="${esc(root.id)}" ${root.id===rootID?"selected":""}>${esc(root.name || root.id)}</option>`).join("");
  const currentLabel = dirPath || "/";
  const rows = fileRows(list.entries || [], {workspaceID, rootID, dirPath, filePath});
  const preview = filePreview(filePath, fileContent, fileError);
  app.innerHTML = layout("Files", `<section class="file-toolbar card">
    <label>Workspace <select data-file-select="workspace_id" data-current-value="${esc(workspaceID)}">${workspaceOptions}</select></label>
    <label>Root <select data-file-select="root_id" data-current-value="${esc(rootID)}">${rootOptions}</select></label>
    <div class="file-path mono">${esc(currentLabel)}</div>
    <button class="button" data-file-new-file="1">New file</button><button class="button" data-file-new-folder="1">New folder</button><button class="button" data-file-upload="1">Upload</button><input class="hidden" type="file" data-file-upload-input><button class="button" data-file-refresh="1">Refresh</button>
  </section>
  <section class="file-browser section">
    <div class="file-panel card">
      <div class="section-head"><h2>Files</h2>${dirPath?`<button class="link" data-file-dir="${esc(parentPath(dirPath))}">Up</button>`:""}</div>
      <div class="file-list">${rows || `<div class="empty">目录为空</div>`}</div>
    </div>
    <div class="file-preview card">
      ${preview}
    </div>
  </section>`);
  state.fileDirty = false;
  state.fileURL = currentURL();
}

function fileRows(entries, context) {
  return entries.map(entry => {
    const isDir = entry.type === "directory";
    const active = !isDir && context.filePath === entry.path ? " active" : "";
    const targetAttr = isDir ? `data-file-dir="${esc(entry.path)}"` : `data-file-open="${esc(entry.path)}"`;
    const icon = isDir ? "📁" : "📄";
    const actions = entry.type === "file" || entry.type === "directory"
      ? `<div class="file-row-actions"><button class="file-row-action" data-file-entry-rename data-entry-path="${esc(entry.path)}" data-entry-type="${esc(entry.type)}">Rename</button><button class="file-row-action danger" data-file-entry-delete data-entry-path="${esc(entry.path)}" data-entry-type="${esc(entry.type)}">Delete</button></div>`
      : "";
    return `<div class="file-row-wrap"><button class="file-row${active}" ${targetAttr}><span>${icon}</span><span class="file-name">${esc(entry.name)}</span><span class="muted">${isDir?"directory":formatBytes(entry.size)}</span></button>${actions}</div>`;
  }).join("");
}

function filePreview(filePath, content, error) {
  if (!filePath) return `<div class="empty">选择一个文本文件预览</div>`;
  if (error) return `<div class="error">${esc(error)}</div><p class="muted">二进制文件或超过大小限制的文件暂不在浏览器中打开。</p>`;
  if (!content) return `<div class="empty">正在加载文件内容…</div>`;
  return `<div class="section-head"><h2>${esc(filePath.split("/").pop())}</h2><div class="actions"><span class="badge">${esc(content.language || "text")}</span><span class="badge pending hidden" data-file-dirty>Unsaved</span><button class="button primary" data-file-save="1">Save</button><button class="button" data-file-rename="1">Rename</button><button class="button" data-file-download="1">Download</button><button class="button danger" data-file-delete="1">Delete</button><button class="button" data-file-reload="1">Reload</button></div></div>
    <div data-file-status></div>
    <div class="detail-grid file-meta">${kv("Path", content.path)}${kv("Size", formatBytes(content.size))}${kv("Modified", fmtTime(content.mod_time))}${kv("ETag", content.etag)}</div>
    <textarea class="file-editor" data-file-editor data-workspace-id="${esc(content.workspace_id)}" data-root-id="${esc(content.root_id)}" data-path="${esc(content.path)}" data-etag="${esc(content.etag)}" spellcheck="false">${esc(content.content)}</textarea>`;
}

function fileBrowserQuery({workspaceID, rootID, path}) {
  const q = new URLSearchParams();
  if (workspaceID) q.set("workspace_id", workspaceID);
  if (rootID) q.set("root_id", rootID);
  if (path) q.set("path", path);
  return q.toString();
}

function joinFilePath(base, name) {
  base = String(base || "").replace(/^\/+|\/+$/g, "");
  name = String(name || "").replace(/^\/+|\/+$/g, "");
  return base ? `${base}/${name}` : name;
}

function fileDownloadURL({workspaceID, rootID, path}) {
  return `${API}/file-browser/files/download?${fileBrowserQuery({workspaceID, rootID, path})}`;
}

function fileBrowserURL({workspaceID, rootID, path, file}) {
  const q = new URLSearchParams();
  if (workspaceID) q.set("workspace_id", workspaceID);
  if (rootID) q.set("root_id", rootID);
  if (path) q.set("path", path);
  if (file) q.set("file", file);
  const suffix = q.toString();
  return `/admin/files${suffix ? `?${suffix}` : ""}`;
}

function currentFileContext() {
  const q = new URLSearchParams(location.search);
  return {workspaceID: q.get("workspace_id"), rootID: q.get("root_id"), path: q.get("path") || "", file: q.get("file") || ""};
}

function parentPath(value) {
  const parts = String(value || "").split("/").filter(Boolean);
  parts.pop();
  return parts.join("/");
}

function formatBytes(value) {
  const n = Number(value || 0);
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n/1024).toFixed(1)} KiB`;
  return `${(n/1024/1024).toFixed(1)} MiB`;
}

function setFileStatus(kind, message) {
  const target = document.querySelector("[data-file-status]");
  if (!target) return;
  const cls = kind === "error" ? "error" : "notice";
  target.innerHTML = `<div class="${cls}">${esc(message)}</div>`;
}

function setFileDirty(dirty) {
  state.fileDirty = Boolean(dirty);
  const badge = document.querySelector("[data-file-dirty]");
  if (!badge) return;
  badge.classList.toggle("hidden", !dirty);
}

function reloadFiles(message="当前文件有未保存修改。重新加载会丢弃这些修改，确定继续？") {
  if (!confirmDiscardFileChanges(message)) return false;
  setFileDirty(false);
  render();
  return true;
}

async function uploadFileToCurrentDirectory(file) {
  if (!file) return;
  if (!confirmDiscardFileChanges("当前文件有未保存修改。上传完成后页面会重新加载，确定丢弃这些修改？")) return;
  const ctx = currentFileContext();
  const target = joinFilePath(ctx.path, file.name);
  const form = new FormData();
  if (ctx.workspaceID) form.set("workspace_id", ctx.workspaceID);
  if (ctx.rootID) form.set("root_id", ctx.rootID);
  form.set("path", target);
  form.set("create", "true");
  form.set("file", file, file.name);
  try {
    const result = await request("/file-browser/files/upload", {method:"POST", body: form});
    if (result.approval?.id) {
      state.notice = `上传等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(`/admin/approvals/${encodeURIComponent(result.approval.id)}`, {force:true});
    }
    state.notice = "文件已上传";
    return navigate(fileBrowserURL({workspaceID:ctx.workspaceID, rootID:ctx.rootID, path:ctx.path, file:target}), {force:true});
  } catch (error) {
    alert(error.message || "上传失败");
  }
}

async function createFileInCurrentDirectory() {
  if (!confirmDiscardFileChanges("当前文件有未保存修改。新建文件后会离开当前编辑内容，确定继续？")) return;
  const name = prompt("New file name");
  if (name === null) return;
  const clean = name.trim();
  if (!clean || clean.includes("/")) return alert("请输入当前目录下的文件名，不要包含 /");
  const ctx = currentFileContext();
  const newPath = joinFilePath(ctx.path, clean);
  try {
    const result = await request("/file-browser/files/content", {method:"PUT", body: JSON.stringify({workspace_id:ctx.workspaceID, root_id:ctx.rootID, path:newPath, content:"", create:true})});
    if (result.approval?.id) {
      state.notice = `新建文件等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(`/admin/approvals/${encodeURIComponent(result.approval.id)}`, {force:true});
    }
    state.notice = "文件已创建";
    return navigate(fileBrowserURL({workspaceID:ctx.workspaceID, rootID:ctx.rootID, path:ctx.path, file:newPath}), {force:true});
  } catch (error) {
    alert(error.message || "新建文件失败");
  }
}

async function createFolderInCurrentDirectory() {
  if (!confirmDiscardFileChanges("当前文件有未保存修改。创建目录后页面会重新加载，确定丢弃这些修改？")) return;
  const name = prompt("New folder name");
  if (name === null) return;
  const clean = name.trim();
  if (!clean || clean.includes("/")) return alert("请输入当前目录下的目录名，不要包含 /");
  const ctx = currentFileContext();
  const newPath = joinFilePath(ctx.path, clean);
  try {
    const result = await request("/file-browser/files/mkdir", {method:"POST", body: JSON.stringify({workspace_id:ctx.workspaceID, root_id:ctx.rootID, path:newPath})});
    if (result.approval?.id) {
      state.notice = `新建目录等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(`/admin/approvals/${encodeURIComponent(result.approval.id)}`, {force:true});
    }
    state.notice = "目录已创建";
    setFileDirty(false);
    return render();
  } catch (error) {
    alert(error.message || "新建目录失败");
  }
}

async function deleteFileEntry(path, type="file") {
  if (!path) return;
  if (!confirmDiscardFileChanges("当前文件有未保存修改。删除操作完成后页面会重新加载，确定丢弃这些修改？")) return;
  const recursive = type === "directory";
  if (!confirm(recursive ? `确认递归删除目录 ${path} 及其中全部内容？` : `确认删除 ${path}？`)) return;
  const ctx = currentFileContext();
  try {
    const result = await request("/file-browser/files/delete", {method:"POST", body: JSON.stringify({workspace_id:ctx.workspaceID, root_id:ctx.rootID, path, recursive, confirmation:recursive?`delete ${path}`:""})});
    if (result.approval?.id) {
      state.notice = `删除等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(`/admin/approvals/${encodeURIComponent(result.approval.id)}`, {force:true});
    }
    state.notice = recursive ? "目录已删除" : "文件已删除";
    const selectedFile = ctx.file === path || ctx.file.startsWith(`${path}/`) ? "" : ctx.file;
    return navigate(fileBrowserURL({workspaceID:ctx.workspaceID, rootID:ctx.rootID, path:ctx.path, file:selectedFile}), {force:true});
  } catch (error) {
    setFileStatus("error", error.message || "删除失败");
  }
}

async function deleteCurrentFile() {
  const editor = document.querySelector("[data-file-editor]");
  if (editor) return deleteFileEntry(editor.dataset.path, "file");
}

async function renameFileEntry(path, type="file") {
  if (!path) return;
  if (!confirmDiscardFileChanges("当前文件有未保存修改。重命名完成后页面会重新加载，确定丢弃这些修改？")) return;
  const next = prompt("New path", path);
  if (next === null) return;
  const clean = next.trim().replace(/^\/+|\/+$/g, "");
  if (!clean) return alert("请输入目标路径");
  if (clean === path) return;
  const ctx = currentFileContext();
  try {
    const result = await request("/file-browser/files/rename", {method:"POST", body: JSON.stringify({workspace_id:ctx.workspaceID, root_id:ctx.rootID, from_path:path, to_path:clean})});
    if (result.approval?.id) {
      state.notice = `重命名等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(`/admin/approvals/${encodeURIComponent(result.approval.id)}`, {force:true});
    }
    state.notice = type === "directory" ? "目录已重命名" : "文件已重命名";
    let selectedFile = ctx.file;
    if (ctx.file === path) {
      selectedFile = clean;
    } else if (type === "directory" && ctx.file.startsWith(`${path}/`)) {
      selectedFile = `${clean}${ctx.file.slice(path.length)}`;
    }
    return navigate(fileBrowserURL({workspaceID:ctx.workspaceID, rootID:ctx.rootID, path:ctx.path, file:selectedFile}), {force:true});
  } catch (error) {
    setFileStatus("error", error.message || "重命名失败");
  }
}

async function renameCurrentFile() {
  const editor = document.querySelector("[data-file-editor]");
  if (editor) return renameFileEntry(editor.dataset.path, "file");
}

async function saveCurrentFile() {
  const editor = document.querySelector("[data-file-editor]");
  if (!editor) return;
  setFileStatus("notice", "正在保存…");
  try {
    const result = await request("/file-browser/files/content", {
      method: "PUT",
      body: JSON.stringify({
        workspace_id: editor.dataset.workspaceId,
        root_id: editor.dataset.rootId,
        path: editor.dataset.path,
        content: editor.value,
        etag: editor.dataset.etag,
        create: false,
        overwrite: false
      })
    });
    if (result.approval?.id) {
      state.notice = `保存等待审批：${result.approval.summary || result.approval.id}`;
      return navigate(`/admin/approvals/${encodeURIComponent(result.approval.id)}`, {force:true});
    }
    state.notice = "文件已保存";
    const ctx = currentFileContext();
    return navigate(fileBrowserURL(ctx), {force:true});
  } catch (error) {
    if (error.code === "file_changed") {
      setFileStatus("error", "文件已被外部修改。请复制当前改动后点击 Reload，再重新合并保存。");
      return;
    }
    setFileStatus("error", error.message || "保存失败");
  }
}

async function renderDeployments() {
  const data = await request("/deployments");
  const rows = (data.items||[]).map(item=>`<tr><td><strong>${esc(item.Site||item.site)}</strong><div class="mono">${esc(item.Type||item.type)}</div></td><td>${badge(item.Status||item.status)}</td><td>${badge(item.Access||item.access||"public")}</td><td>${esc(item.Health||item.health||"—")}</td><td><a class="link" href="${esc(item.URL||item.url)}" target="_blank" rel="noopener">Open</a><div class="mono">${esc(item.Hostname||item.hostname||"")}</div></td><td>${esc(item.ReleaseID||item.release_id||"—")}</td><td>${deploymentActions(item)}</td></tr>`).join("");
  app.innerHTML = layout("Deployments", table("<tr><th>Name</th><th>Status</th><th>Access</th><th>Health</th><th>URL</th><th>Release</th><th>Actions</th></tr>",rows));
}
function deploymentActions(item){const type=item.Type||item.type,name=item.Site||item.site,status=item.Status||item.status,access=item.Access||item.access||"public";let buttons=`<button class="button" data-deploy-action="${access==="private"?"set-public":"set-private"}" data-type="${esc(type)}" data-name="${esc(name)}">${access==="private"?"Make public":"Make private"}</button>`;if(type==="dynamic"){if(["stopped","failed"].includes(status))buttons+=`<button class="button success" data-deploy-action="start" data-type="${esc(type)}" data-name="${esc(name)}">Start</button>`;else buttons+=`<button class="button" data-deploy-action="stop" data-type="${esc(type)}" data-name="${esc(name)}">Stop</button>`;buttons+=`<button class="button" data-deploy-action="restart" data-type="${esc(type)}" data-name="${esc(name)}">Restart</button>`}return `<div class="actions">${buttons}<button class="button danger" data-deploy-action="delete" data-type="${esc(type)}" data-name="${esc(name)}">Delete</button></div>`}

async function renderApprovals() {
  const query = new URLSearchParams(location.search);
  if(!query.has("status")) query.set("status","pending");
  const data = await request(`/approvals?${query}`);
  const rows=(data.items||[]).map(item=>`<tr><td>${fmtTime(item.created_at)}</td><td><button class="link" data-nav="/admin/approvals/${esc(item.id)}">${esc(item.summary||item.tool)}</button><div class="mono">${esc(item.id)}</div></td><td>${esc(item.tool)}</td><td>${badge(item.risk_level||"unknown")}</td><td>${badge(item.status)}</td><td>${sessionBadge(item)}</td></tr>`).join("");
  app.innerHTML=layout("Approvals",`<form class="filters" id="approval-filters"><select name="status">${["pending","approved","rejected","canceled","expired",""] .map(v=>`<option value="${v}" ${query.get("status")===v?"selected":""}>${v||"all"}</option>`).join("")}</select><input name="session_id" placeholder="Session ID" value="${esc(query.get("session_id")||"")}"><input name="tool" placeholder="Tool" value="${esc(query.get("tool")||"")}"><button class="button primary">Filter</button></form>${table("<tr><th>Created</th><th>Request</th><th>Tool</th><th>Risk</th><th>Status</th><th>Session</th></tr>",rows)}`);
}

async function renderApproval(id) {
  const data=await request(`/approvals/${id}`), item=data.approval;
  const actions=item.status==="pending"?`<button class="button success" data-approval-action="approve" data-id="${esc(id)}">Approve</button><button class="button danger" data-approval-action="reject" data-id="${esc(id)}">Reject</button><button class="button" data-approval-action="cancel" data-id="${esc(id)}">Cancel</button>`:"";
  app.innerHTML=layout(`Approval ${short(id,12)}`,`<section class="card detail-grid">${kv("Status",badge(item.status))}${kv("Summary",item.summary||"—")}${kv("Tool",item.tool)}${kv("Risk",item.risk_level||"—")}${kv("Requester",item.requester||"—")}${kv("Session",item.session_id||"No session")}${kv("Created",fmtTime(item.created_at))}${kv("Expires",fmtTime(item.expires_at))}</section><section class="section"><div class="actions">${actions}${item.task_id?`<button class="button" data-nav="/admin/history/${esc(item.task_id)}">Open task</button>`:""}</div></section><section class="section card"><h2>Arguments</h2><pre>${esc(JSON.stringify(item.args||{},null,2))}</pre></section><section class="section card"><h2>Audit</h2><pre>${esc(JSON.stringify(data.audits||[],null,2))}</pre></section>`);
}

async function mutate(path, options={}) {state.error="";const result=await request(path,options);if(result.approval?.id){state.notice=`操作等待审批：${result.approval.summary||result.approval.id}`;history.pushState({},"",`/admin/approvals/${encodeURIComponent(result.approval.id)}`)}else{state.notice="操作已提交"}await render()}

document.addEventListener("click", async event => {
  const nav=event.target.closest("[data-nav]");if(nav){event.preventDefault();return navigate(nav.dataset.nav)}
  const action=event.target.closest("[data-action]")?.dataset.action;
  if(action==="token"){if(!confirmDiscardFileChanges())return;setFileDirty(false);return loginView()}
  if(action==="save-token"){state.token=document.querySelector("#token-input")?.value.trim()||"";sessionStorage.setItem("localgate_admin_token",state.token);return navigate(location.pathname)}
  if(action==="clear-history"){history.replaceState({},"","/admin/history");return render()}
  if(action==="next-history"){const q=new URLSearchParams(location.search);q.set("cursor",event.target.dataset.cursor);history.replaceState({},"",`${location.pathname}?${q}`);return render()}
  const fileDir=event.target.closest("[data-file-dir]");if(fileDir){const ctx=currentFileContext();return navigate(fileBrowserURL({workspaceID:ctx.workspaceID,rootID:ctx.rootID,path:fileDir.dataset.fileDir,file:""}))}
  const fileOpen=event.target.closest("[data-file-open]");if(fileOpen){const ctx=currentFileContext();return navigate(fileBrowserURL({workspaceID:ctx.workspaceID,rootID:ctx.rootID,path:ctx.path,file:fileOpen.dataset.fileOpen}))}
  const fileEntryRename=event.target.closest("[data-file-entry-rename]");if(fileEntryRename)return renameFileEntry(fileEntryRename.dataset.entryPath,fileEntryRename.dataset.entryType);
  const fileEntryDelete=event.target.closest("[data-file-entry-delete]");if(fileEntryDelete)return deleteFileEntry(fileEntryDelete.dataset.entryPath,fileEntryDelete.dataset.entryType);
  if(event.target.closest("[data-file-new-file]")) return createFileInCurrentDirectory();
  if(event.target.closest("[data-file-new-folder]")) return createFolderInCurrentDirectory();
  if(event.target.closest("[data-file-upload]")){document.querySelector("[data-file-upload-input]")?.click(); return;}
  if(event.target.closest("[data-file-save]")) return saveCurrentFile();
  if(event.target.closest("[data-file-rename]")) return renameCurrentFile();
  if(event.target.closest("[data-file-download]")){const editor=document.querySelector("[data-file-editor]"); if(editor) window.location.href=fileDownloadURL({workspaceID:editor.dataset.workspaceId, rootID:editor.dataset.rootId, path:editor.dataset.path}); return;}
  if(event.target.closest("[data-file-delete]")) return deleteCurrentFile();
  if(event.target.closest("[data-file-reload]")) return reloadFiles();
  if(event.target.closest("[data-file-refresh]")) return reloadFiles("当前文件有未保存修改。刷新目录会丢弃这些修改，确定继续？");
  if(action==="cancel-task"&&confirm("确认取消这个 Task？")) return mutate(`/tasks/${event.target.dataset.id}/cancel`,{method:"POST"});
  const approval=event.target.closest("[data-approval-action]");if(approval&&confirm(`确认 ${approval.dataset.approvalAction}？`))return mutate(`/approvals/${approval.dataset.id}/${approval.dataset.approvalAction}`,{method:"POST"});
  const deploy=event.target.closest("[data-deploy-action]");if(deploy){const act=deploy.dataset.deployAction;if(!confirm(`确认对 ${deploy.dataset.name} 执行 ${act}？`))return;const encoded=`/deployments/${encodeURIComponent(deploy.dataset.type)}/${encodeURIComponent(deploy.dataset.name)}`;return mutate(act==="delete"?encoded:`${encoded}/${act}`,{method:act==="delete"?"DELETE":"POST",body:act==="delete"?JSON.stringify({confirmation:"delete"}):undefined})}
});
document.addEventListener("input", event => {
  if (event.target.closest("[data-file-editor]")) setFileDirty(true);
});
document.addEventListener("change", event => {
  const uploadInput = event.target.closest("[data-file-upload-input]");
  if (uploadInput) {
    const file = uploadInput.files?.[0];
    uploadInput.value = "";
    return uploadFileToCurrentDirectory(file);
  }
  const selector = event.target.closest("[data-file-select]");
  if (!selector) return;
  const ctx = currentFileContext();
  if (selector.dataset.fileSelect === "workspace_id") {
    if (!navigate(fileBrowserURL({workspaceID: selector.value, rootID: "", path: "", file: ""}))) selector.value = selector.dataset.currentValue || "";
    return;
  }
  if (selector.dataset.fileSelect === "root_id") {
    if (!navigate(fileBrowserURL({workspaceID: ctx.workspaceID, rootID: selector.value, path: "", file: ""}))) selector.value = selector.dataset.currentValue || "";
    return;
  }
});
document.addEventListener("submit",event=>{event.preventDefault();const form=new FormData(event.target),q=new URLSearchParams();for(const [k,v] of form)if(v)q.set(k,v);history.replaceState({},"",`${location.pathname}?${q}`);render()});
window.addEventListener("beforeunload", event => {
  if (!hasUnsavedFileChanges()) return;
  event.preventDefault();
  event.returnValue = "";
});
window.addEventListener("popstate", () => {
  if (!confirmDiscardFileChanges()) {
    history.pushState({}, "", state.fileURL || "/admin/files");
    return;
  }
  setFileDirty(false);
  render();
});
render();
