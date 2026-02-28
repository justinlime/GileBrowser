package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"

	"gileserver/models"
)

// FileHandler serves a raw file download (with proper Content-Type and
// Content-Length headers so the browser can show download progress).
func FileHandler(roots map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		urlPath := path.Clean("/" + r.URL.Path)

		fsPath, err := resolvePath(roots, urlPath)
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		info, err := os.Stat(fsPath)
		if err != nil || info.IsDir() {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		ip := clientIP(r)
		log.Printf("file download   ip=%-15s  size=%-10s  file=%s", ip, formatSize(info.Size()), urlPath)
		start := time.Now()

		f, err := os.Open(fsPath)
		if err != nil {
			http.Error(w, "Could not open file", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		mimeType := mimeForName(fsPath)
		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filepath.Base(fsPath)))

		// http.ServeContent sets Content-Length from the ReadSeeker and fully
		// supports range requests, so the browser can track download progress.
		http.ServeContent(w, r, filepath.Base(fsPath), info.ModTime(), f)

		log.Printf("file complete   ip=%-15s  size=%-10s  duration=%s  file=%s",
			ip, formatSize(info.Size()), time.Since(start).Round(time.Millisecond), urlPath)
	}
}

// formatSize formats a byte count as a human-readable string.
func formatSize(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// IndexHandler serves the search index as JSON.
// The index is built once and cached with a background refresh on expiry so
// that repeated search requests never trigger a synchronous full tree walk.
func IndexHandler(roots map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		index := cachedIndex(roots)
		w.Header().Set("Content-Type", "application/json")
		encodeJSON(w, index)
	}
}

func encodeJSON(w http.ResponseWriter, v interface{}) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "JSON encoding error", http.StatusInternalServerError)
	}
}

// buildIndex walks all roots and builds a flat FileIndex.
func buildIndex(roots map[string]string) *models.FileIndex {
	idx := &models.FileIndex{}
	for rootName, fsRoot := range roots {
		walkDir(rootName, fsRoot, fsRoot, idx)
	}
	return idx
}

func walkDir(rootName, fsRoot, dir string, idx *models.FileIndex) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			continue
		}
		rel, err := filepath.Rel(fsRoot, filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		urlPath := "/" + rootName + "/" + filepath.ToSlash(rel)
		idx.Files = append(idx.Files, models.IndexEntry{
			Name: e.Name(),
			Path: urlPath,
			Dir:  e.IsDir(),
		})
		if e.IsDir() {
			walkDir(rootName, fsRoot, filepath.Join(dir, e.Name()), idx)
		}
	}
}
