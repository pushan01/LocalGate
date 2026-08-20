export function fileTabKey(path) {
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

export class FileTabStore {
  constructor() {
    this.items = [];
    this.activeKey = "";
  }

  get(key) {
    return this.items.find(item => item.key === key) || null;
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
        key,
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
    const index = this.items.findIndex(item => item.key === key);
    if (index < 0) return { removed: null, active: this.active() };
    const [removed] = this.items.splice(index, 1);
    if (this.activeKey === key) {
      const next = this.items[index] || this.items[index - 1] || null;
      this.activeKey = next?.key || "";
    }
    return { removed, active: this.active() };
  }

  dirtyItems() {
    return this.items.filter(item => item.dirty);
  }

  matching(path, recursive = false) {
    return this.items.filter(item => pathMatches(item.path, path, recursive));
  }

  removeMatching(path, recursive = false) {
    const keys = new Set(this.matching(path, recursive).map(item => item.key));
    if (!keys.size) return [];
    const removed = this.items.filter(item => keys.has(item.key));
    const activeIndex = this.items.findIndex(item => item.key === this.activeKey);
    this.items = this.items.filter(item => !keys.has(item.key));
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
}
