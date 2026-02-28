package handlers

import (
	"bytes"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// FaviconHandler serves the favicon.
//
// Resolution order:
//  1. If faviconPath is non-empty, serve that file from the real filesystem
//     (read on every request so the file can be swapped without a restart).
//  2. Otherwise, serve the default favicon.svg baked into the embedded FS.
func FaviconHandler(embeddedFS fs.FS, faviconPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if faviconPath != "" {
			data, err := os.ReadFile(faviconPath)
			if err != nil {
				http.Error(w, "favicon not found", http.StatusNotFound)
				return
			}
			info, err := os.Stat(faviconPath)
			if err != nil {
				http.Error(w, "favicon not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", mimeForExtension(faviconPath))
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeContent(w, r, "favicon", info.ModTime(), bytes.NewReader(data))
			return
		}

		// Default: serve the embedded favicon.svg.
		data, err := fs.ReadFile(embeddedFS, "images/favicon.svg")
		if err != nil {
			http.Error(w, "favicon not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "favicon.svg", time.Time{}, bytes.NewReader(data))
	}
}

// mimeForExtension returns a MIME type based on the file extension.
func mimeForExtension(path string) string {
	switch filepath.Ext(path) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "image/x-icon"
	}
}
