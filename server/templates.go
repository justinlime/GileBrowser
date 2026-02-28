// Package server contains the HTTP server setup and template management.
package server

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

	"gileserver/models"
)

// Templates wraps compiled per-page template sets.
type Templates struct {
	dir     *template.Template
	preview *template.Template
}

var tmplFuncs = template.FuncMap{
	"humanSize": humanSize,
	"add":       func(a, b int) int { return a + b },
}

// LoadTemplates parses all templates from the embedded FS.
// Each page gets its own template.Template cloned from base so that
// {{define "content"}} blocks don't collide.
func LoadTemplates(tfs embed.FS) (*Templates, error) {
	sub, err := fs.Sub(tfs, "templates")
	if err != nil {
		return nil, fmt.Errorf("sub fs: %w", err)
	}

	base, err := template.New("").Funcs(tmplFuncs).ParseFS(sub, "base.html")
	if err != nil {
		return nil, fmt.Errorf("parse base: %w", err)
	}

	dir, err := cloneAndParse(base, sub, "directory.html")
	if err != nil {
		return nil, fmt.Errorf("parse directory template: %w", err)
	}

	prev, err := cloneAndParse(base, sub, "file-preview.html")
	if err != nil {
		return nil, fmt.Errorf("parse preview template: %w", err)
	}

	return &Templates{dir: dir, preview: prev}, nil
}

// loadTemplatesFromDisk loads templates directly from the filesystem.
// Used in tests where the embedded FS is not available.
func loadTemplatesFromDisk(dir string) (*Templates, error) {
	base, err := template.New("").Funcs(tmplFuncs).ParseFiles(dir + "/base.html")
	if err != nil {
		return nil, fmt.Errorf("parse base: %w", err)
	}

	dirTmpl, err := cloneAndParseFiles(base, dir+"/directory.html")
	if err != nil {
		return nil, fmt.Errorf("parse directory template: %w", err)
	}

	prevTmpl, err := cloneAndParseFiles(base, dir+"/file-preview.html")
	if err != nil {
		return nil, fmt.Errorf("parse preview template: %w", err)
	}

	return &Templates{dir: dirTmpl, preview: prevTmpl}, nil
}

// cloneAndParse clones a base template set and adds one more file from an fs.FS.
func cloneAndParse(base *template.Template, fsys fs.FS, name string) (*template.Template, error) {
	t, err := base.Clone()
	if err != nil {
		return nil, err
	}
	return t.ParseFS(fsys, name)
}

// cloneAndParseFiles clones a base template set and adds files from the OS.
func cloneAndParseFiles(base *template.Template, files ...string) (*template.Template, error) {
	t, err := base.Clone()
	if err != nil {
		return nil, err
	}
	return t.ParseFiles(files...)
}

// ExecuteDir renders the directory listing template.
func (t *Templates) ExecuteDir(w http.ResponseWriter, data *models.DirListing) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return t.dir.ExecuteTemplate(w, "base", data)
}

// ExecutePreview renders the file preview template.
func (t *Templates) ExecutePreview(w http.ResponseWriter, data *models.PreviewData) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return t.preview.ExecuteTemplate(w, "base", data)
}

// humanSize formats a byte count into a human-readable string.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n := n / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
