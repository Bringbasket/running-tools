package webui

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

type Handler struct {
	files fs.FS
	index []byte
}

func NewHandler() (*Handler, error) {
	files, err := fs.Sub(distribution, "dist")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, err
	}
	return &Handler{files: files, index: index}, nil
}

func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "." && name != "" {
		if info, err := fs.Stat(handler.files, name); err == nil && !info.IsDir() {
			content, readErr := fs.ReadFile(handler.files, name)
			if readErr == nil {
				contentType := mime.TypeByExtension(path.Ext(name))
				if contentType != "" {
					w.Header().Set("Content-Type", contentType)
				}
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(content)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(handler.index)
}
