# Active TODO

## Static deployment hardening

- Make site deletion transactional by renaming to a trash directory before committing the registry update, with rollback on failure.
- Add startup reconciliation for stale `.deploy-*`, `.backup-*`, `.localgate-source-*`, missing registered sites, and orphan site directories.
- Harden source traversal against TOCTOU replacement with descriptor-relative traversal (`openat`/`O_NOFOLLOW`) where supported.
- Add cross-process locking or transactional storage so multiple LocalGate processes cannot lose `sites.json` updates.
- Normalize deployed file permissions instead of preserving executable and world-writable bits from the source tree.
- Add structured deployment error types for conflicts, missing sources, storage exhaustion, and registry corruption.
- Expand the tool schema validator with `maxLength`, `pattern`, and richer composition keywords.
- Add explicit multi-workspace routing for the MCP execution runtime instead of binding deployment to the default workspace.
- Add deployment manifests, content hashes, version history, rollback, and garbage collection.
- Add configurable static response headers, cache policy, SPA fallback policy, and custom index files.
- Add time-range filters and export/retention controls for deployment audit records.

## Existing runtime work

- [x] Define and document the session event cursor contract, including absolute cursor, retained-buffer base offset, truncation and cursor expiry.
- Add optional session persistence and streaming lifecycle events.
- Add descriptor-relative file access hardening for general filesystem operations on supported Unix platforms.

## File browser / online IDE

- [x] **File browser planning.** Keep the design in `docs/file_browser_plan.md` aligned with implementation decisions.
- [x] **Read-only browser backend MVP.** Add `file_browser` config, `internal/filebrowser` service, `/api/admin/v1/file-browser/*` APIs for workspace/root listing, directory listing, and text file reads.
- [x] **Admin Console Files page.** Add `/admin/files` with workspace selector, file tree, and read-only text preview.
- [x] **Path and file safety.** Uses `workspace_id + root_id + relative_path`, workspace sandbox validation, symlink escape rejection, hidden/excluded directory filtering, and file size / binary detection.
- [x] **Safe write support.** Save, create file, mkdir, rename/move, upload, download, and delete APIs use ETag conflict detection and atomic writes where applicable.
- [x] **Approval and audit integration.** Write/delete/move/upload operations enter LocalGate approval tasks when configured and emit file-browser-specific audit actions.
- [x] **Editor UX.** Dirty navigation protection, list-level rename/delete, collapsible sidebar, safe Markdown/image preview, actionable ETag conflict handling, frontend build-chain/Monaco decision, and per-tab content/ETag/dirty/conflict state are implemented through `CH-FB-003`. Search remains intentionally out of scope.
- [x] **Tool and Session hardening.** `CH-SEC-001` completed environment / trusted Shell isolation, Session ownership / absolute cursor reliability, upload SSRF protection, atomic file I/O, and read / traversal limits.
- [x] **Terminal integration.** `CH-FB-004` added current-directory Session creation, absolute-cursor polling, stdin/close, Task / Approval linkage, and approval return to the original Files context.
- [ ] **Real-time and multi-terminal UX.** Replace polling with an Admin SSE / WebSocket stream, add multiple terminal tabs, and define browser-restart restoration after measuring retained Session and output limits.
- [ ] **Optional code-server template.** Provide a private dynamic deployment template for code-server with explicit warnings that code-server internals bypass LocalGate's fine-grained file approvals.

## Approval backlog

- [ ] **Crash-consistent approval consumption (item 2).** Replace the current `approved -> in-memory gate release` two-step window with a recoverable `pending -> consuming -> consumed` state machine or execution coordinator. Define compensation when persistence succeeds but gate release fails.
- [ ] **Exactly-once and concurrent decision semantics (item 4).** Cover repeated approve, approve/reject races, approve/expire races, client retries after response loss, and gate-released-but-response-lost cases. A decision must never execute the task twice.
- [ ] **Half-created and orphan reconciliation (item 6).** Reconcile approval-without-task, awaiting-task-without-approval, mismatched IDs, created-audit failures, bind failures, and task-cancel failures. Add startup repair/audit behavior.
- [ ] **Approval query pagination and filters (item 7).** Add `workspace_id`, `tool`, `requester`, `risk_level`, time-range filters, bounded `limit`, stable cursor/offset semantics, and documented sorting.
- [ ] **Conditional policy rules (item 8).** Extend `approval.rules` beyond tool-name matching with normalized facts, `when`, reason codes, policy versions, explicit priority/conflict behavior, and rule-hit details in approval/audit records.
- [ ] **Idempotency keys and pending deduplication (item 9).** Reuse an existing pending approval/task for equivalent requester/workspace/tool/args-digest submissions within a defined window, and specify terminal-state reuse behavior.
- [ ] **Scoped temporary grants (item 10).** Support narrowly scoped, short-lived, revocable and auditable grants such as one request, one digest/session, or one low-risk tool. Never default to broad session-wide approval.
- [ ] **Recoverable approval tasks across restart (item 11).** Persist a safe reconstructable execution request, tool/policy versions, execution lease, and exactly-once recovery contract before replacing the current safe restart-cancel behavior.
- [ ] **Transactional multi-process storage (item 12).** Move approval/task/audit coordination to SQLite or equivalent cross-process transactional storage with conditional updates, leases, and duplicate-release prevention.
