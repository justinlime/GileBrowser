package handlers

import (
	"bytes"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gileserver/settings"
)

// FaviconHandler serves the favicon.
//
// Resolution order:
//   1. If a custom favicon exists in settings, serve it from disk.
//   2. Otherwise, serve the default favicon.svg baked into the embedded FS.
func FaviconHandler(embeddedFS fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rtc := GetRuntimeConfig()
		faviconPath := rtc.FaviconPath

		if faviconPath != "" {
			data, err := os.ReadFile(faviconPath)
			if err != nil {
				http.Error(w, "favicon not found", http.StatusNotFound)
				return
			}
			info, _ := os.Stat(faviconPath)
			w.Header().Set("Content-Type", mimeForExtension(faviconPath))
			http.ServeContent(w, r, "favicon", info.ModTime(), bytes.NewReader(data))
			return
		}

		// Default: serve the embedded favicon.svg.
		data, err := fs.ReadFile(embeddedFS, "static/images/favicon.svg")
		if err != nil {
			http.Error(w, "favicon not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		http.ServeContent(w, r, "favicon.svg", time.Time{}, bytes.NewReader(data))
	}
}

// FaviconUploadHandler handles POST requests to upload a custom favicon.
// Expects multipart/form-data with a field named "favicon".
func FaviconUploadHandler(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Limit upload size to 1 MB.
		r.ParseMultipartForm(1024 * 1024)

		file, header, err := r.FormFile("favicon")
		if err != nil {
			log.Printf("favicon: failed to read uploaded file: %v", err)
			http.Error(w, "Failed to read upload", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Sanitize filename.
		filename := sanitizeFaviconFilename(header.Filename)
		if filename == "" {
			http.Error(w, "Invalid filename", http.StatusBadRequest)
			return
		}

		faviconPath := filepath.Join(dataDir, "favicons", filename)

		// Ensure the favicons directory exists.
		if err := os.MkdirAll(filepath.Dir(faviconPath), 0755); err != nil {
			log.Printf("favicon: failed to create directory: %v", err)
			http.Error(w, "Failed to create upload directory", http.StatusInternalServerError)
			return
		}

		// Save the uploaded file.
		out, err := os.Create(faviconPath)
		if err != nil {
			log.Printf("favicon: failed to create output file: %v", err)
			http.Error(w, "Failed to save upload", http.StatusInternalServerError)
			return
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			log.Printf("favicon: failed to write uploaded file: %v", err)
			http.Error(w, "Failed to save upload", http.StatusInternalServerError)
			return
		}

		// Update the favicon path setting.
		if err := settings.SaveSetting("favicon_path", faviconPath); err != nil {
			log.Printf("favicon: failed to update setting: %v", err)
			http.Error(w, "Failed to save settings", http.StatusInternalServerError)
			return
		}

		// Reload runtime config.
		LoadRuntimeConfig()

		log.Printf("favicon: uploaded as %s (%.0f bytes)", filename, float64(header.Size))
		http.Redirect(w, r, "/settings?success=1", http.StatusSeeOther)
	}
}

// FaviconDeleteHandler handles DELETE requests to remove the custom favicon.
func FaviconDeleteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rtc := GetRuntimeConfig()
		if rtc.FaviconPath == "" {
			http.Error(w, "No custom favicon to delete", http.StatusNotFound)
			return
		}

		// Delete the file.
		if err := os.Remove(rtc.FaviconPath); err != nil {
			log.Printf("favicon: failed to delete %s: %v", rtc.FaviconPath, err)
			http.Error(w, "Failed to delete favicon", http.StatusInternalServerError)
			return
		}

		// Clear the setting.
		if err := settings.SaveSetting("favicon_path", ""); err != nil {
			log.Printf("favicon: failed to clear setting: %v", err)
			http.Error(w, "Failed to clear settings", http.StatusInternalServerError)
			return
		}

		// Reload runtime config.
		LoadRuntimeConfig()

		log.Printf("favicon: deleted custom favicon")
		http.Redirect(w, r, "/settings?success=1", http.StatusSeeOther)
	}
}

// sanitizeFaviconFilename returns a safe filename for the uploaded favicon.
func sanitizeFaviconFilename(filename string) string {
	// Remove any path components.
	filename = filepath.Base(filename)

	// Keep only alphanumeric, dots, underscores, and hyphens.
	var cleaned strings.Builder
	for _, r := range filename {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			cleaned.WriteRune(r)
		}
	}

	result := cleaned.String()
	if result == "" {
		return "favicon.png"
	}
	return result
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
