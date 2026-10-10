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
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	// Bound expensive image parsing separately from the ordinary file export
	// allowance (100 MiB by default). Large source files remain downloadable.
	maxPreviewSourceBytes int64 = 16 << 20
	maxPreviewPixels      int64 = 16_000_000
	maxPreviewDimension         = 8192
	thumbnailMaxDimension       = 960
)

func previewImageGrant(grant Grant) (mimeType, encoded string, ok bool) {
	if grant.Size <= 0 || grant.Size > maxPreviewSourceBytes {
		return "", "", false
	}
	mimeType = strings.ToLower(strings.TrimSpace(strings.SplitN(grant.MIMEType, ";", 2)[0]))
	switch mimeType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return "", "", false
	}
	info, err := os.Lstat(grant.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != grant.Size || info.ModTime().UnixNano() != grant.ModTimeUnixNano {
		return "", "", false
	}
	f, err := os.Open(grant.Path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != grant.Size || before.ModTime().UnixNano() != grant.ModTimeUnixNano {
		return "", "", false
	}
	payload, err := io.ReadAll(io.LimitReader(f, maxPreviewSourceBytes+1))
	if err != nil || int64(len(payload)) != grant.Size {
		return "", "", false
	}
	after, err := f.Stat()
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", "", false
	}
	if http.DetectContentType(payload) != mimeType {
		return "", "", false
	}
	digest := sha256.Sum256(payload)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), grant.SHA256) {
		return "", "", false
	}

	// Small WebP files are passed through unchanged. Large WebP previews need
	// a dedicated decoder and are intentionally not decoded using untrusted tools.
	if mimeType == "image/webp" {
		if grant.Size <= MaxInlineImageBytes {
			return mimeType, base64.StdEncoding.EncodeToString(payload), true
		}
		return "", "", false
	}
	imageInfo, err := decodeImageConfig(payload, mimeType)
	if err != nil || imageInfo.Width <= 0 || imageInfo.Height <= 0 || imageInfo.Width > maxPreviewDimension || imageInfo.Height > maxPreviewDimension || int64(imageInfo.Width)*int64(imageInfo.Height) > maxPreviewPixels {
		return "", "", false
	}
	img, err := decodeImage(payload, mimeType)
	if err != nil {
		return "", "", false
	}
	if grant.Size <= MaxInlineImageBytes {
		return mimeType, base64.StdEncoding.EncodeToString(payload), true
	}
	thumb := downscaleOnWhite(img, thumbnailMaxDimension)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 77}); err != nil || out.Len() > int(MaxInlineImageBytes) {
		return "", "", false
	}
	return "image/jpeg", base64.StdEncoding.EncodeToString(out.Bytes()), true
}

func decodeImageConfig(data []byte, mimeType string) (image.Config, error) {
	if mimeType == "image/png" {
		return png.DecodeConfig(bytes.NewReader(data))
	}
	return jpeg.DecodeConfig(bytes.NewReader(data))
}

func decodeImage(data []byte, mimeType string) (image.Image, error) {
	if mimeType == "image/png" {
		return png.Decode(bytes.NewReader(data))
	}
	return jpeg.Decode(bytes.NewReader(data))
}

// Convert to a small JPEG on a white background, preserving aspect ratio.
// No filesystem writes or third-party executables are used for previews.
func downscaleOnWhite(src image.Image, maxDim int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxDim || h > maxDim {
		if w >= h {
			h = max(1, int(int64(h)*int64(maxDim)/int64(w)))
			w = maxDim
		} else {
			w = max(1, int(int64(w)*int64(maxDim)/int64(h)))
			h = maxDim
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := b.Min.Y + int(int64(y)*int64(b.Dy())/int64(h))
		for x := 0; x < w; x++ {
			sx := b.Min.X + int(int64(x)*int64(b.Dx())/int64(w))
			r, g, blue, a := src.At(sx, sy).RGBA()
			// RGBA() returns premultiplied channels; compose against white.
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((r + 65535 - a) >> 8),
				G: uint8((g + 65535 - a) >> 8),
				B: uint8((blue + 65535 - a) >> 8),
				A: 255,
			})
		}
	}
	return dst
}
