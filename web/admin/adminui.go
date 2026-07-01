package adminui

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"localgate/internal/server"
)

//go:embed dist/*
var assets embed.FS

func Handler(baseURL string) http.Handler {
	dist, _ := fs.Sub(assets, "dist")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !server.RequestHostMatchesBaseURL(r.Host, baseURL) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/admin")
		name = strings.TrimPrefix(name, "/")
		if name == "" || !strings.Contains(path.Base(name), ".") {
			name = "index.html"
		}
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(dist, name)
		if err != nil {
			if path.Ext(name) == "" {
				data, err = fs.ReadFile(dist, "index.html")
			}
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
