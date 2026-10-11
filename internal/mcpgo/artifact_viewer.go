package mcpgo

import (
	"context"
	"embed"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// artifactViewerURI changes when the component's shape or protocol changes,
// so hosts do not retain a stale version of the HTML resource.
const artifactViewerURI = "ui://localgate/artifact-viewer/v2.html"
const artifactViewerMIME = "text/html;profile=mcp-app"

//go:embed ui/artifact-viewer.html
var artifactViewerFS embed.FS

// Direct export_file is associated with a small, portable MCP Apps widget.
// It does not replace the existing image + resource_link tool content.
func attachArtifactViewer(tool *mcp.Tool) {
	if tool.Meta == nil {
		tool.Meta = mcp.NewMetaFromMap(map[string]any{})
	}
	if tool.Meta.AdditionalFields == nil {
		tool.Meta.AdditionalFields = make(map[string]any)
	}
	tool.Meta.AdditionalFields["ui"] = map[string]any{"resourceUri": artifactViewerURI}
	// Compatibility for older ChatGPT UI hosts.
	tool.Meta.AdditionalFields["openai/outputTemplate"] = artifactViewerURI
}

func (s *Server) registerArtifactViewer() {
	s.impl.AddResource(mcp.NewResource(artifactViewerURI, "LocalGate file viewer",
		mcp.WithMIMEType(artifactViewerMIME),
		mcp.WithResourceDescription("Preview exported images and download files in a ChatGPT conversation"),
	), func(_ context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		if req.Params.URI != artifactViewerURI {
			return nil, fmt.Errorf("unknown UI resource")
		}
		data, err := artifactViewerFS.ReadFile("ui/artifact-viewer.html")
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{mcp.TextResourceContents{
			URI: artifactViewerURI, MIMEType: artifactViewerMIME, Text: string(data),
			Meta: map[string]any{
				"ui": map[string]any{
					"prefersBorder": true,
					"csp": map[string]any{
						"connectDomains":  []string{},
						"resourceDomains": []string{},
					},
				},
				"openai/ui": map[string]any{"availableDisplayModes": []string{"inline", "fullscreen"}},
			},
		}}, nil
	})
}
