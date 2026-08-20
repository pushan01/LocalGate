---
name: localgate-notification
description: Use only when the user explicitly asks LocalGate to push or send a phone notification through the configured notification provider.
---

# LocalGate Notification

Use `send_notification` only when the user explicitly asks to notify or push to their phone. Do not send notifications merely because a task completed or because a reminder seems useful.

In normal MCP mode, read `send_notification` with `get_tool_schema` and invoke it through `invoke_tool`. In Tool HTTP mode, follow `localgate-tool-http`.

Do not expose configured device keys, encryption keys, or other notification credentials to the caller.
