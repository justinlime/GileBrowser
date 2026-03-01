// Package handlers contains all HTTP handler functions.
package handlers

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gileserver/models"
)

// dirSize returns the total size in bytes of all files under root.
// It is best-effort: unreadable entries are silently skipped.
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// DirHandler handles directory listing requests.
// roots maps the URL top-level name to the real filesystem path.
func DirHandler(roots map[string]string, siteName, defaultTheme string, tmpl interface{ ExecuteDir(http.ResponseWriter, *models.DirListing) error }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		urlPath := path.Clean("/" + r.URL.Path)

		fsPath, err := resolvePath(roots, urlPath)
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		info, err := os.Stat(fsPath)
		if err != nil || !info.IsDir() {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		entries, err := buildEntries(roots, urlPath, fsPath)
		if err != nil {
			http.Error(w, "Error reading directory", http.StatusInternalServerError)
			return
		}

		listing := &models.DirListing{
			Title:        filepath.Base(urlPath),
			SiteName:     siteName,
			CurrentPath:  urlPath,
			Breadcrumbs:  buildBreadcrumbs(siteName, urlPath),
			Entries:      entries,
			DownloadURL:  "/zip" + urlPath,
			TotalSize:    cachedDirSize(fsPath),
			DefaultTheme: defaultTheme,
		}

		if err := tmpl.ExecuteDir(w, listing); err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
		}
	}
}

// RootHandler returns a handler for the "/" path that lists all configured roots.
func RootHandler(roots map[string]string, siteName, defaultTheme string, tmpl interface{ ExecuteDir(http.ResponseWriter, *models.DirListing) error }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "" {
			http.NotFound(w, r)
			return
		}

		var entries []models.FileEntry
		names := make([]string, 0, len(roots))
		for name := range roots {
			names = append(names, name)
		}
		sort.Strings(names)

		var totalSize int64
		for _, name := range names {
			fsDir := roots[name]
			sz := cachedDirSize(fsDir)
			totalSize += sz
			fe := models.FileEntry{
				Name:  name,
				Path:  "/" + name,
				IsDir: true,
				Size:  sz,
			}
			if fi, err := os.Stat(fsDir); err == nil {
				fe.ModTime = fi.ModTime()
			}
			entries = append(entries, fe)
		}

		listing := &models.DirListing{
			Title:        siteName,
			SiteName:     siteName,
			CurrentPath:  "/",
			Breadcrumbs:  buildBreadcrumbs(siteName, "/"),
			Entries:      entries,
			DownloadURL:  "/zip/",
			TotalSize:    totalSize,
			IsRoot:       true,
			DefaultTheme: defaultTheme,
		}

		if err := tmpl.ExecuteDir(w, listing); err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
		}
	}
}

// buildEntries reads a directory and returns sorted FileEntry values.
// Directory sizes are computed concurrently and served from a short-lived cache
// so that listings with many subdirectories don't block on serial tree walks.
func buildEntries(roots map[string]string, urlPath, fsPath string) ([]models.FileEntry, error) {
	rawEntries, err := os.ReadDir(fsPath)
	if err != nil {
		return nil, err
	}

	// Pre-allocate and populate the slice without sizes yet.
	entries := make([]models.FileEntry, 0, len(rawEntries))
	for _, e := range rawEntries {
		// Skip hidden files.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}

		fi, err := e.Info()
		if err != nil {
			continue
		}

		fe := models.FileEntry{
			Name:    e.Name(),
			Path:    path.Join(urlPath, e.Name()),
			IsDir:   e.IsDir(),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		}

		if !e.IsDir() {
			fullPath := filepath.Join(fsPath, e.Name())
			mime := mimeForFile(fullPath)
			fe.MIMEType = mime
			fe.IsImage = isImage(mime)
			fe.IsText = isText(mime)
			fe.IsPreview = fe.IsImage || fe.IsText
		}

		entries = append(entries, fe)
	}

	// Compute directory sizes concurrently; each goroutine updates its own
	// slot so no mutex is needed for the slice writes.
	var wg sync.WaitGroup
	for i := range entries {
		if !entries[i].IsDir {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fsDir := filepath.Join(fsPath, entries[i].Name)
			entries[i].Size = cachedDirSize(fsDir)
		}(i)
	}
	wg.Wait()

	// Directories first, then files; each group sorted alphabetically.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	return entries, nil
}

// buildBreadcrumbs creates a slice of breadcrumbs from a URL path.
func buildBreadcrumbs(siteName, urlPath string) []models.Breadcrumb {
	crumbs := []models.Breadcrumb{{Name: "root", Path: "/"}}
	if urlPath == "/" {
		return crumbs
	}

	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	current := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		current += "/" + p
		crumbs = append(crumbs, models.Breadcrumb{Name: p, Path: current})
	}
	return crumbs
}
