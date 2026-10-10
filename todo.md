# Active TODO

## ChatGPT Artifact UI follow-ups

- 评估是否引入受维护的 WebP 解码库，使大于 1 MiB 的 WebP 也可生成有界缩略图；当前大 WebP 仅提供原件下载。
- 评估 `subagent_artifact_get` 返回结构里的原有签名 `url` 字段是否可通过按需签发/专用下载操作替代；目前它会跟随 Task 结果持久化，迁移需要兼顾 HTTP 客户端兼容性。
- 在有可用的 ChatGPT MCP Apps 运行环境后，增加文件/图片画廊 UI resource（带 MIME/CSP/权限边界）并做 Web/Desktop/Mobile 实机测试；原生 MCP `image` 与 `resource_link` 的客户端渲染不保证一致。
- 为 Subagent 的 ChatGPT Provider 增加可靠的图片 Artifact 采集；当前采集只在 Gemini Provider 实现。
- 为 Artifact 建立独立的历史索引、所有权验证及 GC/保留策略，避免最新一轮 Subagent 覆盖可查询的旧索引后留下孤儿文件。
- [x] `CH-ARTIFACT-UI-002` 已为 1–16 MiB 的 PNG/JPEG 增加有界 JPEG 缩略图；其他大图格式/上限以原件下载作为安全回退。

## Subagent runtime follow-ups

- Keep `subagent_list` local-only unless a future bounded bulk-refresh API is designed; default remote refresh belongs to single-agent `subagent_get` so listing many agents cannot accidentally fan out browser I/O.
- Add an official API-backed Provider (for example OpenAI Conversations/Responses) behind the same `internal/subagent.Provider` interface; keep browser-backed ChatGPT as one provider rather than the architecture itself.
- Add Admin Console Subagents page for state, provider, conversation link, last activity, error, close/recover actions, and Task navigation.
- Decide whether LocalGate should optionally create/manage a dedicated ChatGPT Project automatically; MVP only accepts a configured existing Project ID/URL and does not mutate the user's Project list.
- Design an idempotent remote marker/reconciliation strategy for `subagent_spawn` outcome-unknown cases before attempting to auto-adopt a ChatGPT conversation from history; do not match conversations by title/text heuristics alone.
- Add bounded message history / delivery records with stable message IDs if callers need asynchronous per-message result lookup beyond the existing Task result model.
- Add optional subagent completion events/notifications (or a future callback mechanism) so parents can react to finished turns without polling; keep this separate from the core Provider contract until event delivery semantics are defined.
- Evaluate SQLite or another transactional store before supporting multiple LocalGate processes mutating subagents concurrently.
- Add provider-aware conditional approval policies for external data disclosure, account selection, high-cost modes, deep research, file upload, and other provider-specific capabilities.
- Add multiple browser profiles / ChatGPT account pools only after single-profile lifecycle and recovery semantics are stable.
- Add streaming partial replies only after the basic request/reply and cancellation semantics are stable; do not couple the core Provider interface to ChatGPT DOM streaming.
- Define bounded delegation trees, parent/child ownership and recursion limits before allowing a subagent to spawn further subagents.

## Static deployment hardening

- Make site deletion transactional by renaming to a trash directory before committing the registry update, with rollback on failure.
- Add startup reconciliation for stale `.deploy-*`, `.backup-*`, `.localgate-source-*`, missing registered sites, and orphan site directories.
- Harden source traversal against TOCTOU replacement with descriptor-relative traversal (`openat`/`O_NOFOLLOW`) where supported.
- Add cross-process locking or transactional storage so multiple LocalGate processes cannot lose `sites.json` updates.
- Normalize deployed file permissions instead of preserving executable and world-writable bits from the source tree.
- Add structured deployment error types for conflicts, missing sources, storage exhaustion, and registry corruption.
- Expand the tool schema validator with `maxLength`, `pattern`, and richer composition keywords.
- [x] Remove multi-workspace routing entirely; one LocalGate instance now owns one configured root.
- Add deployment manifests, content hashes, version history, rollback, and garbage collection.
- Add configurable static response headers, cache policy, SPA fallback policy, and custom index files.
- Add time-range filters and export/retention controls for deployment audit records.

## Existing runtime work

- [x] Define and document the session event cursor contract, including absolute cursor, retained-buffer base offset, truncation and cursor expiry.
- Add optional session persistence and streaming lifecycle events.
- Add descriptor-relative file access hardening for general filesystem operations on supported Unix platforms.

## Progressive tool exposure follow-ups

- Add a CLI adapter over the same capability path, with commands such as `localgate tool list`, `localgate tool schema <name>`, and `localgate tool invoke <name> --json ...`; it must reuse the App / Task / Approval / Registry path rather than implementing separate execution semantics.
- If future transports need finer policy than the current stable MCP allowlist plus Registry hidden state, introduce explicit capability metadata for `discoverable`, `mcp_exposed`, and `progressive_callable` instead of overloading one visibility bit.
- Add bounded relay capability refresh / reconciliation if downstream MCP servers need runtime `tools/list` changes without a LocalGate restart; preserve progressive exposure rather than re-expanding the top-level MCP surface.

## File browser / online IDE

- [x] **File browser planning.** Keep the design in `docs/file_browser_plan.md` aligned with implementation decisions.
- [x] **Read-only browser backend MVP.** Add `file_browser` config, `internal/filebrowser` service, `/api/admin/v1/file-browser/*` APIs for root-scoped directory listing and text file reads.
- [x] **Admin Console Files page.** Add `/admin/files` with a root-scoped file tree and read-only text preview.
- [x] **Path and file safety.** Uses `relative_path` under the configured root, root sandbox validation, symlink escape rejection, hidden/excluded directory filtering, and file size / binary detection.
- [x] **Safe write support.** Save, create file, mkdir, rename/move, upload, download, and delete APIs use ETag conflict detection and atomic writes where applicable.
- [x] **Approval and audit integration.** Write/delete/move/upload operations enter LocalGate approval tasks when configured and emit file-browser-specific audit actions.
- [x] **Editor UX.** Dirty navigation protection, list-level rename/delete, collapsible sidebar, safe Markdown/image preview, actionable ETag conflict handling, frontend build-chain/Monaco decision, and per-tab content/ETag/dirty/conflict state are implemented through `CH-FB-003`. Search remains intentionally out of scope.
- [x] **Tool and Session hardening.** `CH-SEC-001` completed environment / trusted Shell isolation, Session ownership / absolute cursor reliability, upload SSRF protection, atomic file I/O, and read / traversal limits.
- [x] **Terminal integration.** `CH-FB-004` added current-directory Session creation, absolute-cursor polling, stdin/close, Task / Approval linkage, and approval return to the original Files context.
- [x] **Read-only Git root view.** `CH-FB-005A` added sandbox-bound Git status plus staged/unstaged diff, trusted Git execution and worktree/Git-metadata root checks without repository mutation operations.
- [x] **Real-time terminal stream.** `CH-FB-005B` added Admin SSE with current-instance session access, absolute cursor semantics, lifecycle cancellation, and automatic polling fallback.
- [ ] **Multi-terminal UX and restoration.** Design multiple terminal tabs with independent Session/cursor/stream state, bounded retained output, and explicit browser-restart restoration rules.
- [ ] **Optional code-server template.** Provide a private dynamic deployment template for code-server with explicit warnings that code-server internals bypass LocalGate's fine-grained file approvals.

## Approval backlog

- [ ] **Crash-consistent approval consumption (item 2).** Replace the current `approved -> in-memory gate release` two-step window with a recoverable `pending -> consuming -> consumed` state machine or execution coordinator. Define compensation when persistence succeeds but gate release fails.
- [ ] **Exactly-once and concurrent decision semantics (item 4).** Cover repeated approve, approve/reject races, approve/expire races, client retries after response loss, and gate-released-but-response-lost cases. A decision must never execute the task twice.
- [ ] **Half-created and orphan reconciliation (item 6).** Reconcile approval-without-task, awaiting-task-without-approval, mismatched IDs, created-audit failures, bind failures, and task-cancel failures. Add startup repair/audit behavior.
- [ ] **Approval query pagination and filters (item 7).** Add `tool`, `requester`, `risk_level`, time-range filters, bounded `limit`, stable cursor/offset semantics, and documented sorting.
- [ ] **Conditional policy rules (item 8).** Extend `approval.rules` beyond tool-name matching with normalized facts, `when`, reason codes, policy versions, explicit priority/conflict behavior, and rule-hit details in approval/audit records.
- [ ] **Idempotency keys and pending deduplication (item 9).** Reuse an existing pending approval/task for equivalent requester/tool/args-digest submissions within a defined window, and specify terminal-state reuse behavior.
- [ ] **Scoped temporary grants (item 10).** Support narrowly scoped, short-lived, revocable and auditable grants such as one request, one digest/session, or one low-risk tool. Never default to broad session-wide approval.
- [ ] **Recoverable approval tasks across restart (item 11).** Persist a safe reconstructable execution request, tool/policy versions, execution lease, and exactly-once recovery contract before replacing the current safe restart-cancel behavior.
- [ ] **Transactional multi-process storage (item 12).** Move approval/task/audit coordination to SQLite or equivalent cross-process transactional storage with conditional updates, leases, and duplicate-release prevention.
