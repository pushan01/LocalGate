---
name: localgate-filesystem
description: Use for LocalGate filesystem operations beyond the direct read_file/apply_patch core, including full-file writes, directory listing, file search, and argv execution when shell parsing is undesirable.
---

# LocalGate Filesystem and Advanced Execution

Use direct `read_file` and `apply_patch` for ordinary code reading and localized edits. This skill covers lower-frequency Registry tools that may not appear as top-level MCP tools.

## Progressive invocation

In normal MCP mode, use `get_tool_schema` for the selected tool and then call it through `invoke_tool`. Do not guess a hidden tool's arguments from memory. In Tool HTTP mode, follow `localgate-tool-http` instead.

## Tool choice

- `write_file`: create or completely replace a text file. Prefer `apply_patch` for partial edits to existing files.
- `list_directory`: list a directory under the configured root when shell listing is unavailable or structured entries are useful.
- `search_files`: search files under the configured root when a structured LocalGate result is preferable to `rg`/shell search.
- `run_argv`: execute an argv vector directly without shell parsing when exact argument boundaries matter.

Keep paths inside the configured root and preserve existing project changes that are unrelated to the current task.
