import test from "node:test";
import assert from "node:assert/strict";
import { FileTerminalState, fileTerminalContextKey } from "./file-terminal.js";

test("terminal context reset isolates workspace and root", () => {
  const state = new FileTerminalState().setContext("ws", "0", "docs").attachTask("task-1");
  state.attachSession("session-1").applyRead({next_offset: 1, items: [{offset: 0, data: "hello"}]});
  state.setContext("other", "0", "docs");
  assert.equal(state.taskID, "");
  assert.equal(state.sessionID, "");
  assert.equal(state.output(), "");
  assert.equal(state.contextKey, fileTerminalContextKey("other", "0", "docs"));
});

test("task result attaches the created session", () => {
  const state = new FileTerminalState().setContext("ws", "0", "").attachTask("task-1");
  state.applyTask({task: {id: "task-1", status: "succeeded", result: {session_id: "session-1"}}});
  assert.equal(state.mode(), "session");
  assert.equal(state.sessionID, "session-1");
  assert.equal(state.cursor, 0);
});

test("incremental reads deduplicate offsets", () => {
  const state = new FileTerminalState().setContext("ws", "0", "").attachSession("session-1");
  state.applyRead({next_offset: 2, items: [{offset: 0, data: "one"}, {offset: 1, data: "two"}]});
  state.applyRead({next_offset: 3, items: [{offset: 1, data: "two"}, {offset: 2, data: "three"}]});
  assert.equal(state.output(), "onetwothree");
  assert.equal(state.cursor, 3);
});

test("expired cursor replaces stale output with retained buffer", () => {
  const state = new FileTerminalState().setContext("ws", "0", "").attachSession("session-1");
  state.applyRead({next_offset: 2, items: [{offset: 0, data: "old"}, {offset: 1, data: "data"}]});
  state.applyRead({base_offset: 10, next_offset: 12, cursor_expired: true, truncated: true, items: [{offset: 10, data: "new"}, {offset: 11, data: "tail"}]});
  assert.equal(state.output(), "newtail");
  assert.equal(state.baseOffset, 10);
  assert.equal(state.cursor, 12);
  assert.equal(state.cursorExpired, true);
  assert.equal(state.truncated, true);
});
