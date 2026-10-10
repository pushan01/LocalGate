package fileexport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLargeImagesUseBoundedThumbnailsAndKeepOriginalDownload(t *testing.T) {
	// Deterministic noise yields a source significantly larger than 1 MiB.
	img := image.NewRGBA(image.Rect(0, 0, 1200, 1000))
	noise := rand.New(rand.NewSource(28))
	if _, err := noise.Read(img.Pix); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		mime string
		make func() []byte
	}{
		{"png", "image/png", func() []byte {
			var b bytes.Buffer
			if err := png.Encode(&b, img); err != nil {
				t.Fatal(err)
			}
			return b.Bytes()
		}},
		{"jpeg", "image/jpeg", func() []byte {
			var b bytes.Buffer
			if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 99}); err != nil {
				t.Fatal(err)
			}
			return b.Bytes()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.make()
			if len(src) <= int(MaxInlineImageBytes) || len(src) > int(maxPreviewSourceBytes) {
				t.Fatalf("bad fixture size: %d", len(src))
			}
			path := filepath.Join(t.TempDir(), "original")
			if err := os.WriteFile(path, src, 0o600); err != nil {
				t.Fatal(err)
			}
			stat, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(src)
			svc, err := NewService(Options{BaseURL: "https://gate.example", DefaultTTL: time.Minute, MaxTTL: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			grant, err := svc.Issue(Grant{Path: path, FileName: "original.png", MIMEType: tc.mime,
				Size: int64(len(src)), SHA256: hex.EncodeToString(sum[:]), ModTimeUnixNano: stat.ModTime().UnixNano()}, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := svc.NativeContent(grant.ID)
			if len(got) != 2 || got[1]["type"] != "image" || got[1]["mimeType"] != "image/jpeg" {
				t.Fatalf("expected JPEG thumbnail and original download link, got types: %#v", got)
			}
			encoded := got[1]["data"].(string)
			thumb, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(thumb) > int(MaxInlineImageBytes) {
				t.Fatalf("bad thumbnail size: %d, %v", len(thumb), err)
			}
			decoded, err := jpeg.Decode(bytes.NewReader(thumb))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Bounds().Dx() != 960 || decoded.Bounds().Dy() != 800 {
				t.Fatalf("incorrect aspect ratio: %v", decoded.Bounds())
			}
			link, err := svc.Link(grant.ID)
			if err != nil || link.MIMEType != tc.mime || link.Size != int64(len(src)) {
				t.Fatalf("original download changed: %#v, %v", link, err)
			}
			// A same-size rewrite must not expose a stale preview.
			src[120] ^= 0xff
			if err := os.WriteFile(path, src, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := svc.NativeContent(grant.ID); len(got) != 1 {
				t.Fatalf("changed source still inlined: %d blocks", len(got))
			}
		})
	}
}

func TestPreviewImageGuardsDecompressionBombsAndUnsupportedData(t *testing.T) {
	// PNG headers encode huge dimensions without allocating the pixels.
	tooLarge := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var small bytes.Buffer
	if err := png.Encode(&small, tooLarge); err != nil {
		t.Fatal(err)
	}
	header := append([]byte(nil), small.Bytes()...)
	// Replace only IHDR dimensions to exceed the pixel budget. The config
	// decoder may reject the CRC; either way it must refuse the preview.
	copy(header[16:24], []byte{0, 0, 0, 200, 0, 0, 0, 200})
	if mime, _, ok := previewBytesForTest(t, header, "image/png"); ok {
		t.Fatalf("oversized/malformed PNG preview allowed: %s", mime)
	}
	webp := append([]byte("RIFF"), []byte{0, 0, 0, 0, 'W', 'E', 'B', 'P'}...)
	webp = append(webp, bytes.Repeat([]byte{0}, int(MaxInlineImageBytes)+1)...)
	if _, _, ok := previewBytesForTest(t, webp, "image/webp"); ok {
		t.Fatal("large WebP should fall back to original download")
	}
}

func previewBytesForTest(t *testing.T, b []byte, mimeType string) (string, string, bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return previewImageGrant(Grant{Path: path, FileName: "source", MIMEType: mimeType,
		Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:]), ModTimeUnixNano: stat.ModTime().UnixNano()})
}

func TestTransparentImageThumbnailCompositesOnWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 0})
	src.SetNRGBA(1, 0, color.NRGBA{B: 255, A: 255})
	got := downscaleOnWhite(src, 4)
	if got.RGBAAt(0, 0) != (color.RGBA{255, 255, 255, 255}) || got.RGBAAt(1, 0) != (color.RGBA{0, 0, 255, 255}) {
		t.Fatalf("unexpected alpha composition: %v, %v", got.RGBAAt(0, 0), got.RGBAAt(1, 0))
	}
}
