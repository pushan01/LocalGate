# LocalGate Admin

This directory contains the standalone LocalGate management frontend and its Go static-asset handler.

The browser application only communicates with `/api/admin/v1`; it does not depend on Go HTML templates. Editable sources live in `src/`. Production assets are generated into `dist/`, committed to the repository, and embedded into the LocalGate binary.

The Go build intentionally does not require Node or network access. It consumes the committed `dist/` directory. Frontend contributors use the pinned npm toolchain to regenerate those assets:

```bash
cd web/admin
npm ci
npm run build
```

From the repository root, `./scripts/build-admin.sh` runs `npm ci` and rebuilds `dist/`. For a local repeat build with an already synchronized `node_modules`, use `SKIP_NPM_CI=1 ./scripts/build-admin.sh`. `BUILD_ADMIN=1 ./scripts/build.sh` rebuilds the frontend before compiling LocalGate; the default Go build skips that step.

The current source remains dependency-free browser JavaScript and CSS. Esbuild is used only as a deterministic build boundary, not as a framework commitment. This keeps the migration path open for later module splitting or TypeScript without changing the Admin API or the Go embed layer.

Routes:

- `/admin`
- `/admin/history`
- `/admin/deployments`
- `/admin/approvals`
- `/admin/files`

The UI loads without credentials and requests the configured HTTP Bearer token when an admin API call returns `401`. The token is kept in `sessionStorage` and attached only as an `Authorization` header.

## Files editor state

The Files page keeps open tabs in memory for the lifetime of the Admin application. Each tab owns its current content, saved baseline, ETag, dirty flag, Markdown preview mode, and conflict state. Switching files or directories does not discard other tabs. Leaving Files or closing a dirty tab requires confirmation.

Tabs are intentionally not restored after a full page reload or browser restart yet. Pending approval navigation preserves the in-memory tabs while the current Admin application remains loaded; direct rename, move, and delete operations update the matching tabs after success.

## Files terminal

The Files page can start an `exec_command` Session in the currently selected workspace root and directory. The browser only calls same-origin Admin endpoints under `/api/admin/v1/file-browser/terminals`; the backend resolves the relative directory through the file-browser sandbox before passing an absolute workdir to the existing Task, approval, command-policy, and Session layers.

Terminal output uses the Session absolute cursor contract and is polled without re-rendering the file editor. Stdin, Ctrl-C, and close operations preserve workspace ownership checks. When terminal creation requires approval, the approval URL carries a validated `return_to` value so the SPA can return to the original Files URL, keep in-memory dirty tabs, poll the original Task, and attach the resulting `session_id`.

The current terminal state is represented in the Files URL with `terminal_task`, `terminal_session`, and `terminal_path`. A full browser restart can reconnect while the Session remains retained, but multiple terminal tabs, persisted terminal history, and SSE/WebSocket streaming remain future work.

## Monaco decision

Monaco is not included yet. It is substantially larger than the current Admin assets, depends on Web Workers, and requires explicit worker URL and CSP handling for an embedded, offline-capable binary. Adding it before the Files page has a modular editor state would make the current single-file application harder to maintain without improving the underlying multi-tab and conflict model.

The intended sequence is:

1. Keep the new build boundary and committed `dist/` assets.
2. Split Files editor state into modules while implementing multi-tab editing.
3. Add a small editor adapter interface and verify worker loading under the existing same-origin CSP.
4. Introduce Monaco only if the bundle-size and startup-cost measurements are acceptable and the plain-text fallback remains available.

Monaco must remain self-hosted. CDN loading, `unsafe-eval`, and widening the Admin CSP are not acceptable defaults.
