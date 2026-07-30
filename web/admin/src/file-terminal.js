export function fileTerminalContextKey(workspaceID, rootID, path) {
  return `${workspaceID || ""}\u0000${rootID || ""}\u0000${path || ""}`;
}

export class FileTerminalState {
  constructor() {
    this.reset();
  }

  reset() {
    this.contextKey = "";
    this.workspaceID = "";
    this.rootID = "";
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
    return this;
  }

  setContext(workspaceID, rootID, path) {
    const key = fileTerminalContextKey(workspaceID, rootID, path);
    if (this.contextKey && this.contextKey !== key) this.reset();
    this.contextKey = key;
    this.workspaceID = workspaceID || "";
    this.rootID = rootID || "";
    this.path = path || "";
    return this;
  }

  matchesWorkspaceRoot(workspaceID, rootID) {
    return Boolean(this.contextKey && this.workspaceID === (workspaceID || "") && this.rootID === (rootID || ""));
  }

  attachTask(taskID, approval=null) {
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
    } else if (approval !== undefined) {
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
    const byOffset = new Map(this.items.map(item => [Number(item.offset), item]));
    for (const item of payload.items || []) byOffset.set(Number(item.offset), item);
    this.items = Array.from(byOffset.values()).sort((a, b) => Number(a.offset) - Number(b.offset));
    if (this.items.length > 2000) this.items = this.items.slice(-2000);
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
    return this.items.map(item => item.data || "").join("");
  }

  shouldPoll() {
    if (this.sessionID) {
      return ["starting", "running"].includes(this.session?.status || "starting");
    }
    return Boolean(this.taskID && !["succeeded", "failed", "canceled", "timeout"].includes(this.task?.status));
  }
}
