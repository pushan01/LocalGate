---
name: localgate-file-transfer
description: Use for moving files into or out of LocalGate, including host-provided uploads, conversation exports, and cross-sandbox or cross-machine upload/download token flows.
---

# LocalGate File Transfer

Use the currently exposed tools and their schemas as authoritative. File transfer can cross the LocalGate sandbox boundary, so distinguish ordinary workspace file I/O from transfer operations.

## Host-provided file into the workspace

Use `upload_file` when the host supplies a file object or downloadable file reference rather than a path LocalGate can read directly.

- Set `path` to the destination absolute path inside the workspace, including the filename.
- Preserve the host file object under the tool's `file` argument; do not rewrite it into a guessed local path.
- Parent directories may be created by the tool. The source object is not modified.

For ordinary files already inside the workspace, prefer filesystem tools instead of transfer tools.

## Workspace file back to the host

Use `export_file` when the user needs a workspace file returned to the current conversation or host.

- The source must be an existing regular workspace file.
- The result is a short-lived external resource/download reference.
- Export is data egress and may require approval.

## Cross-sandbox or cross-machine transfer

If `create_upload_token` or `create_download_token` is exposed, use those flows when the caller cannot directly pass a host file object or read the workspace filesystem.

For upload tokens, use the returned upload URL and temporary credential exactly as returned. If the instance also requires global HTTP Bearer authentication, include both credentials.

For download tokens, prefer the returned download URL. If the response provides an auth query key/value for manual URL construction, use those values rather than inventing query parameters. Global HTTP Bearer authentication can still be required in addition to the temporary download credential.

## Safety and completion

- Keep destination paths inside allowed workspace roots.
- Do not treat approval waiting as failure and do not duplicate the original transfer operation after approval.
- After transfer, verify the destination or exported resource that matters to the user's task.