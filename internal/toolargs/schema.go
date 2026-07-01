package toolargs

func MCPInputSchema(schema any, base64Enabled bool) any {
	if !base64Enabled {
		return schema
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"base64data": map[string]any{
				"type": "string",
				"description": "Base64-encoded JSON object for this tool's original arguments. " +
					"Decode the string first; the decoded value must match the tool's original input schema.",
			},
		},
		"required": []string{"base64data"},
	}
}
