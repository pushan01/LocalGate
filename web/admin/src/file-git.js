export function fileGitContextKey(path) {
  return String(path || "");
}

export class FileGitState {
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
}

export function gitEntryHasStagedChanges(entry) {
  const value = String(entry?.index_status || ".");
  return value !== "." && value !== "?";
}

export function gitEntryHasWorktreeChanges(entry) {
  const value = String(entry?.worktree_status || ".");
  return value !== "." && value !== "?";
}
