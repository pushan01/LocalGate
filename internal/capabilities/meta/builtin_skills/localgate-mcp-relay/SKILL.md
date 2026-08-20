---
name: localgate-mcp-relay
description: Use for tools relayed from configured downstream MCP servers, including server__tool naming, downstream schemas, transport boundaries, and safe handling of relay configuration.
---

# LocalGate MCP Relay

Use this skill when startup instructions indicate downstream MCP servers are configured. Relayed tools are progressive capabilities and should not need to occupy LocalGate's top-level MCP tool list.

## Identify relay tools

LocalGate can expose downstream MCP tools with names shaped like `server__tool`. Treat these as downstream capabilities, not as LocalGate filesystem, shell, deployment, or sandbox primitives.

- Use `get_tool_schema` for the `server__tool` name and treat the relayed tool's own description and schema as authoritative.
- In normal MCP mode, call the relayed capability through `invoke_tool`; LocalGate dispatches it to the configured downstream server.
- Do not infer LocalGate root path restrictions for downstream tools unless that downstream tool explicitly documents them.
- Do not reuse LocalGate-specific argument conventions such as `cwd`, patch syntax, task/session semantics, or deployment fields unless the downstream schema actually defines them.

## Server context

Startup instructions can include configured downstream server names and environment variable keys. Environment values are intentionally omitted and must not be reconstructed, echoed, or guessed.

A relay server can expose different tools over time or fail independently from LocalGate. Re-check startup capability routing and the current schema when a relay capability is missing or has changed instead of relying on stale names.

## Calls and failures

- Construct arguments from the downstream tool schema.
- Preserve structured downstream errors when reporting failures.
- Distinguish a downstream MCP error from a LocalGate sandbox or shell error.
- If a relay tool returns its own resource identifiers, cursors, or continuation tokens, follow that tool's semantics rather than LocalGate task/session conventions unless the host explicitly wraps it as a LocalGate task.