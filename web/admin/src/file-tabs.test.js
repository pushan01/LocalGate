import assert from "node:assert/strict";
import test from "node:test";

import { FileTabStore, fileTabKey } from "./file-tabs.js";

test("open deduplicates tabs and preserves order", () => {
  const store = new FileTabStore();
  const first = store.open({ path: "a.txt", content: "a" });
  store.open({ path: "b.txt", content: "b" });
  const reopened = store.open({ path: "a.txt", content: "changed" });
  assert.equal(store.items.length, 2);
  assert.equal(reopened, first);
  assert.equal(reopened.content, "changed");
  assert.equal(store.activeKey, fileTabKey("a.txt"));
});

test("close selects the next neighboring tab", () => {
  const store = new FileTabStore();
  const a = store.open({ path: "a.txt" });
  const b = store.open({ path: "b.txt" });
  const c = store.open({ path: "c.txt" });
  store.activate(b.key);
  const result = store.close(b.key);
  assert.equal(result.removed.path, "b.txt");
  assert.equal(result.active.path, "c.txt");
  store.close(c.key);
  assert.equal(store.active().key, a.key);
});

test("dirtyItems returns only dirty tabs", () => {
  const store = new FileTabStore();
  const clean = store.open({ path: "a.txt" });
  const dirty = store.open({ path: "b.txt", dirty: true });
  assert.deepEqual(store.dirtyItems().map(item => item.key), [dirty.key]);
  store.update(clean.key, { dirty: true });
  assert.equal(store.dirtyItems().length, 2);
});

test("directory rename updates paths, directory context, and active key", () => {
  const store = new FileTabStore();
  const tab = store.open({ path: "docs/a.md", dirPath: "docs" });
  store.renameMatching("docs", "guide", true);
  assert.equal(tab.path, "guide/a.md");
  assert.equal(tab.dirPath, "guide");
  assert.equal(store.activeKey, fileTabKey("guide/a.md"));
});

test("file move updates its directory context", () => {
  const store = new FileTabStore();
  const tab = store.open({ path: "docs/a.md", dirPath: "docs" });
  store.renameMatching("docs/a.md", "guide/a.md", false);
  assert.equal(tab.path, "guide/a.md");
  assert.equal(tab.dirPath, "guide");
});

test("recursive removal closes matching descendants only", () => {
  const store = new FileTabStore();
  store.open({ path: "docs/a.md" });
  store.open({ path: "other.txt" });
  store.open({ path: "docs/b.md" });
  const removed = store.removeMatching("docs", true);
  assert.deepEqual(removed.map(item => item.path), ["docs/a.md", "docs/b.md"]);
  assert.equal(store.items.length, 1);
});
