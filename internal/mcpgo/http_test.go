package mcpgo

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"localgate/internal/app"
	"localgate/internal/config"
	"localgate/internal/httpapi"
)

func TestMountHTTPRejectsRequestsWithoutSessionIDByDefault(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		Root:             config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	s, err := New(application, "localgate", "test")
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}

	mux := http.NewServeMux()
	s.MountHTTP(mux, "/mcp", HTTPOptions{})

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Invalid session ID")) {
		t.Fatalf("expected Invalid session ID, got %s", w.Body.String())
	}
}

func TestStatelessMCPFilePreviewAndSignedDownloadOverHTTP(t *testing.T) {
	root := t.TempDir()
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"picture.png": img.Bytes(),
		"report.pdf":  []byte("%PDF-1.7\nexample document\n"),
		"bundle.zip":  []byte("PK\x03\x04example archive"),
	} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	application, err := app.New(config.Config{
		Root: config.Root{Path: root}, LogStore: t.TempDir(),
		HTTP: config.HTTP{BaseURL: "https://gate.example"},
		FileExports: config.FileExports{Enabled: true, BaseURL: "https://gate.example",
			DefaultTTLSeconds: 300, MaxTTLSeconds: 600, MaxFileBytes: 1 << 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	s, err := New(application, "localgate", "test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.MountHTTP(mux, "/mcp", HTTPOptions{Stateless: true})
	(&httpapi.API{App: application}).RegisterControl(mux)
	endpoint := httptest.NewServer(mux)
	defer endpoint.Close()

	for name, original := range map[string][]byte{
		"picture.png": img.Bytes(),
		"report.pdf":  []byte("%PDF-1.7\nexample document\n"),
		"bundle.zip":  []byte("PK\x03\x04example archive"),
	} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "tools/call",
				"params": map[string]any{"name": "export_file", "arguments": map[string]any{"path": filepath.Join(root, name)}},
			})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, endpoint.URL+"/mcp", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			resp, err := endpoint.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			responseBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("MCP HTTP call failed: %s, %v, body=%s", resp.Status, err, responseBytes)
			}
			var envelope struct {
				Result struct {
					IsError bool `json:"isError"`
					Content []struct {
						Type     string `json:"type"`
						URI      string `json:"uri"`
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal(responseBytes, &envelope); err != nil {
				t.Fatalf("parse MCP result: %v: %s", err, responseBytes)
			}
			if envelope.Result.IsError {
				t.Fatalf("tool failed: %s", responseBytes)
			}
			var signedURI string
			var images int
			for _, block := range envelope.Result.Content {
				switch block.Type {
				case "image":
					images++
					if name != "picture.png" || block.MIMEType != "image/png" || block.Data == "" {
						t.Fatalf("unexpected image block for %s", name)
					}
				case "resource_link":
					signedURI = block.URI
				}
			}
			if (name == "picture.png" && images != 1) || (name != "picture.png" && images != 0) || !strings.Contains(signedURI, "signature=") {
				t.Fatalf("incorrect MCP content for %s: %#v", name, envelope.Result.Content)
			}
			parsed, err := url.Parse(signedURI)
			if err != nil {
				t.Fatal(err)
			}
			// Follow the URI through the real HTTP handler, using the httptest
			// host instead of the configured public base URL.
			get, err := endpoint.Client().Get(endpoint.URL + parsed.RequestURI())
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(get.Body)
			get.Body.Close()
			if err != nil || get.StatusCode != http.StatusOK || !bytes.Equal(got, original) {
				t.Fatalf("original HTTP download mismatch: %v %s", err, get.Status)
			}
			if disposition := get.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
				t.Fatalf("expected downloadable file: %s", disposition)
			}
			headReq, err := http.NewRequest(http.MethodHead, endpoint.URL+parsed.RequestURI(), nil)
			if err != nil {
				t.Fatal(err)
			}
			head, err := endpoint.Client().Do(headReq)
			if err != nil {
				t.Fatal(err)
			}
			headBody, _ := io.ReadAll(head.Body)
			head.Body.Close()
			if head.StatusCode != http.StatusOK || len(headBody) != 0 {
				t.Fatalf("signed HEAD mismatch: status=%d body=%q", head.StatusCode, headBody)
			}
			rangeReq, err := http.NewRequest(http.MethodGet, endpoint.URL+parsed.RequestURI(), nil)
			if err != nil {
				t.Fatal(err)
			}
			rangeReq.Header.Set("Range", "bytes=0-3")
			partial, err := endpoint.Client().Do(rangeReq)
			if err != nil {
				t.Fatal(err)
			}
			partialBody, err := io.ReadAll(partial.Body)
			partial.Body.Close()
			if err != nil || partial.StatusCode != http.StatusPartialContent || !bytes.Equal(partialBody, original[:4]) {
				t.Fatalf("signed Range mismatch: status=%s body=%q err=%v", partial.Status, partialBody, err)
			}
		})
	}
}

func TestMountHTTPAllowsRequestsWithoutSessionIDWhenStateless(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		LogStore:         t.TempDir(),
		DefaultTimeoutMs: 10_000,
		Root:             config.Root{Path: root},
	}
	application, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })

	s, err := New(application, "localgate", "test")
	if err != nil {
		t.Fatalf("mcpgo.New: %v", err)
	}

	mux := http.NewServeMux()
	s.MountHTTP(mux, "/mcp", HTTPOptions{Stateless: true})

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["result"] == nil {
		t.Fatalf("expected JSON-RPC result, got %#v", body)
	}
}
