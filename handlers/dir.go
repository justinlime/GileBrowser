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

// entryIsDir reports whether a directory entry is a directory, correctly
// following symlinks. os.ReadDir / filepath.WalkDir use os.Lstat semantics,
// so DirEntry.IsDir() returns false for symlinks that point to directories.
// This helper resolves the symlink via os.Stat when necessary.
func entryIsDir(parent string, d os.DirEntry) bool {
	if d.Type()&os.ModeSymlink == 0 {
		return d.IsDir()
	}
	fi, err := os.Stat(filepath.Join(parent, d.Name()))
	return err == nil && fi.IsDir()
}

// dirSize returns the total size in bytes of all files under root.
// It is best-effort: unreadable entries are silently skipped.
// Symlinks to directories are followed so their subtrees are included.
func dirSize(root string) int64 {
	// filepath.WalkDir uses os.Lstat semantics for every path it visits,
	// including the root itself. If root is a symlink to a directory, WalkDir
	// sees it as a symlink (not a dir) and never descends. Resolve the full
	// symlink chain upfront so WalkDir always receives a real directory path.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	var total int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Real directory — descend normally, don't count its inode size.
		if d.IsDir() {
			return nil
		}
		// Symlink encountered mid-walk: resolve and handle based on target type.
		if d.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return nil
			}
			fi, err := os.Stat(resolved)
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				// Symlink to a directory: recurse with the resolved real path
				// so the inner WalkDir can descend into it correctly.
				total += dirSize(resolved)
			} else {
				total += fi.Size()
			}
			return nil
		}
		// Regular file — count it.
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
		fullPath := filepath.Join(fsPath, e.Name())
		isDir := entryIsDir(fsPath, e)

		// Use os.Stat so that symlinks are followed for size and modtime.
		fi, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		fe := models.FileEntry{
			Name:    e.Name(),
			Path:    path.Join(urlPath, e.Name()),
			IsDir:   isDir,
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		}

		if !isDir {
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
