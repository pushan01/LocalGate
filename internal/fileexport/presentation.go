package fileexport

import "fmt"

// NativeContent is returned in the MCP tool result, not persisted in the task
// result. Its short-lived signed link is therefore regenerated from the grant
// when the user asks for an async task result.
func (s *Service) NativeContent(exportID string) []map[string]any {
	link, err := s.Link(exportID)
	if err != nil {
		return nil
	}
	content := []map[string]any{{
		"type": "text",
		"text": fmt.Sprintf("Download %q: %s", link.Name, link.URI),
	}}
	if mimeType, data, ok := s.ImagePreview(exportID); ok {
		content = append(content, map[string]any{
			"type":     "image",
			"mimeType": mimeType,
			"data":     data,
		})
	}
	return content
}
