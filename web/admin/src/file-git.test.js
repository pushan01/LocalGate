import assert from "node:assert/strict";
import test from "node:test";

import { FileGitState, fileGitContextKey, gitEntryHasStagedChanges, gitEntryHasWorktreeChanges } from "./file-git.js";

test("git path change clears stale status and diff", () => {
  const state = new FileGitState().setContext("one");
  state.applyStatus({ repository: true }).applyDiff({ diff: "x" });
  state.setContext("two");
  assert.equal(state.status, null);
  assert.equal(state.diff, null);
  assert.equal(state.contextKey, fileGitContextKey("two"));
});

test("same git path preserves loaded state", () => {
  const state = new FileGitState().setContext("one");
  const status = { repository: true, entries: [] };
  state.applyStatus(status).setContext("one");
  assert.equal(state.status, status);
});

test("git entry helpers distinguish staged and worktree changes", () => {
  assert.equal(gitEntryHasStagedChanges({ index_status: "M", worktree_status: "." }), true);
  assert.equal(gitEntryHasStagedChanges({ index_status: "?", worktree_status: "?" }), false);
  assert.equal(gitEntryHasWorktreeChanges({ index_status: ".", worktree_status: "M" }), true);
  assert.equal(gitEntryHasWorktreeChanges({ index_status: "A", worktree_status: "." }), false);
});
