package fileexport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeContentPreviewAndDownloadFallback(t *testing.T) {
	svc, err := NewService(Options{BaseURL: "https://gate.example", DefaultTTL: time.Minute, MaxTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "render.png")
	issue := func(data []byte, mimeType string) string {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		stat, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		grant, err := svc.Issue(Grant{Path: path, FileName: "render.png", MIMEType: mimeType,
			Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), ModTimeUnixNano: stat.ModTime().UnixNano()}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return grant.ID
	}

	id := issue(pngBytes.Bytes(), "image/png")
	content := svc.NativeContent(id)
	if len(content) != 2 || content[0]["type"] != "text" || content[1]["type"] != "image" || content[1]["mimeType"] != "image/png" {
		t.Fatalf("expected link text and inline PNG, got %#v", content)
	}
	if !strings.Contains(content[0]["text"].(string), "/system/exports/") {
		t.Fatalf("missing fallback download URL: %#v", content[0])
	}
	data, err := base64.StdEncoding.DecodeString(content[1]["data"].(string))
	if err != nil || !bytes.Equal(data, pngBytes.Bytes()) {
		t.Fatal("image data was not preserved")
	}
	if err := os.WriteFile(path, []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := svc.NativeContent(id); len(got) != 1 || got[0]["type"] != "text" {
		t.Fatalf("mutated image should not be inlined: %#v", got)
	}
	if got := svc.NativeContent(issue([]byte("not a PNG"), "image/png")); len(got) != 1 {
		t.Fatalf("fake PNG should only have download link: %#v", got)
	}
	if got := svc.NativeContent(issue([]byte("pdf bytes"), "application/pdf")); len(got) != 1 {
		t.Fatalf("PDF should only have download link: %#v", got)
	}
	if got := svc.NativeContent(issue(bytes.Repeat([]byte("x"), int(MaxInlineImageBytes)+1), "image/png")); len(got) != 1 {
		t.Fatalf("oversized image should only have download link: %#v", got)
	}
	svc.Revoke(id)
	if got := svc.NativeContent(id); len(got) != 0 {
		t.Fatalf("revoked grant must not expose content: %#v", got)
	}
}
