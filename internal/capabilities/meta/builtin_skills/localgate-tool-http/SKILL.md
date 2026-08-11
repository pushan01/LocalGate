---
name: localgate-tool-http
description: Use when LocalGate runs in tool HTTP mode and MCP is the bootstrap plane while business tools are called through authenticated /tools/{tool}/call HTTP requests.
---

# LocalGate Tool HTTP Mode

Use this skill only when startup instructions say tool HTTP mode is enabled.

## Bootstrap plane

In this mode MCP exposes bootstrap/control tools rather than the full business tool surface. The bootstrap set includes:

- `get_startup_instructions` for current capability and skill routing;
- `get_skill` for embedded LocalGate workflow skills;
- `get_temporary_access_token` for temporary HTTP tool credentials and call metadata;
- `get_tool_schema` for the original schema of one HTTP-callable business tool.

Do not assume a business tool is callable unless it appears in the `tools` list returned by `get_startup_instructions`.

## Call flow

1. Read `get_startup_instructions` and choose a listed business tool.
2. Load a matching built-in skill with `get_skill` when the startup skill index recommends one.
3. Call `get_tool_schema` for the business tool before constructing nontrivial arguments.
4. Call `get_temporary_access_token` and use the returned base URL, path pattern, header name, and credential values rather than guessing them.
5. Send the business request to the configured `/tools/{tool}/call` path.

The HTTP request normally needs both LocalGate's normal HTTP Bearer authentication, when configured, and the temporary tool-access credential returned by `get_temporary_access_token` (commonly `X-LocalGate-Tool-Access`).

## Argument encoding

Follow the current startup instructions for `tool_arguments.enable_base64`.

- When base64 mode is disabled, send the tool's normal JSON argument object.
- When base64 mode is enabled, encode the entire original JSON argument object as required by the transport; after decoding it must still match the tool's original schema.
- File parameters that are explicitly exempted by the current tool/transport schema must keep their native file-object shape. Do not base64-wrap them merely because global base64 mode is enabled.

## Task continuation

An HTTP tool call can hand work off to an asynchronous task or approval gate. Preserve the returned `task_id`; do not replay the original business request. Follow the task through the task HTTP endpoints or lifecycle helpers that the current host exposes until terminal.