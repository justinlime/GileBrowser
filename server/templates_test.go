package server

import (
	"net/http/httptest"
	"os"
	"testing"

	"gileserver/models"
)

// loadTestTemplates parses templates from the filesystem (not embedded) so that
// the server/package tests don't need the embed FS from main.go.
func loadTestTemplates(t *testing.T) *Templates {
	t.Helper()
	// Restore working dir awareness: templates live at ../templates relative to
	// the server package.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	_ = orig

	tmpl, err := loadTemplatesFromDisk("../templates")
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	return tmpl
}

func TestTemplatesParse(t *testing.T) {
	loadTestTemplates(t)
}

func TestExecuteDir(t *testing.T) {
	tmpl := loadTestTemplates(t)

	data := &models.DirListing{
		Title:       "test",
		CurrentPath: "/test",
		Breadcrumbs: []models.Breadcrumb{{Name: "root", Path: "/"}, {Name: "test", Path: "/test"}},
		Entries: []models.FileEntry{
			{Name: "file.txt", Path: "/test/file.txt", IsDir: false, Size: 1024},
			{Name: "subdir", Path: "/test/subdir", IsDir: true},
		},
		DownloadURL: "/zip/test",
	}

	w := httptest.NewRecorder()
	if err := tmpl.ExecuteDir(w, data); err != nil {
		t.Fatalf("ExecuteDir: %v", err)
	}
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.Len() == 0 {
		t.Error("empty body")
	}
}

func TestExecutePreview(t *testing.T) {
	tmpl := loadTestTemplates(t)

	data := &models.PreviewData{
		Title:              "hello.go",
		FilePath:           "/test/hello.go",
		FileName:           "hello.go",
		DownloadURL:        "/download/test/hello.go",
		IsText:             true,
		HighlightedContent: "<pre class=\"chroma\"><code>package main</code></pre>",
		Breadcrumbs:        []models.Breadcrumb{{Name: "root", Path: "/"}, {Name: "test", Path: "/test"}},
	}

	// Also exercise the binary/dir info-card paths.
	for _, pd := range []*models.PreviewData{
		{Title: "archive.bin", FileName: "archive.bin", DownloadURL: "/download/test/archive.bin",
			IsBinary: true, MIMEType: "application/octet-stream", FileSize: 4096,
			Breadcrumbs: []models.Breadcrumb{{Name: "root", Path: "/"}}},
		{Title: "mydir", FileName: "mydir", FilePath: "/test/mydir",
			DownloadURL: "/zip/test/mydir", IsDir: true, EntryCount: 5, FileSize: 8192,
			Breadcrumbs: []models.Breadcrumb{{Name: "root", Path: "/"}}},
	} {
		wr := httptest.NewRecorder()
		if err := tmpl.ExecutePreview(wr, pd); err != nil {
			t.Fatalf("ExecutePreview(%s): %v", pd.FileName, err)
		}
	}

	w := httptest.NewRecorder()
	if err := tmpl.ExecutePreview(w, data); err != nil {
		t.Fatalf("ExecutePreview: %v", err)
	}
	if w.Body.Len() == 0 {
		t.Error("empty body")
	}
}
