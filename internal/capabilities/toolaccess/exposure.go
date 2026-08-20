package toolaccess

const InvokeToolName = "invoke_tool"

// IsDirectMCPTool defines the stable Registry-backed portion of the normal MCP
// surface. invoke_tool itself is transport-provided and therefore is not a
// Registry tool in this list.
func IsDirectMCPTool(name string) bool {
	switch name {
	case "get_startup_instructions", "get_skill", "get_tool_schema",
		"read_file", "apply_patch", "run_command", "upload_file", "export_file":
		return true
	default:
		return false
	}
}

func IsBootstrapTool(name string) bool {
	switch name {
	case "get_startup_instructions", "get_skill", "get_temporary_access_token", "get_tool_schema", InvokeToolName:
		return true
	default:
		return false
	}
}

