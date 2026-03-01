// Package models defines data structures used throughout the server.
package models

import (
	"html/template"
	"time"
)

// FileEntry represents a single file or directory in a listing.
type FileEntry struct {
	Name        string
	Path        string    // URL path relative to server root (e.g. /test1/subdir/file.txt)
	IsDir       bool
	Size        int64
	ModTime     time.Time
	MIMEType    string
	IsPreview   bool // true if the file can be previewed (image or text)
	IsImage     bool // true if the file is an image
	IsText      bool // true if the file is a plain-text type
}

// DirListing holds everything a directory template needs.
type DirListing struct {
	Title       string
	SiteName    string // branding name shown in the header and page title
	CurrentPath string // URL path of this directory
	Breadcrumbs []Breadcrumb
	Entries     []FileEntry
	// DownloadURL is the URL that will serve this directory as a ZIP archive.
	DownloadURL string
	// TotalSize is the aggregate size in bytes of all files under this directory,
	// used to annotate the "Download All / Download Folder" button label.
	TotalSize int64
	// IsRoot is true when this listing represents the top-level server root.
	IsRoot bool
	// DefaultTheme is the server-configured theme ("dark" or "light").
	DefaultTheme string
}

// Breadcrumb is one segment of the path shown in the navigation bar.
type Breadcrumb struct {
	Name string
	Path string // URL path for this breadcrumb
}

// FileIndex is a flat list of all files known to the server, used by the
// client-side fuzzy search.
type FileIndex struct {
	Files []IndexEntry `json:"files"`
}

// IndexEntry is a single entry in the search index.
// Only files are indexed; directories are excluded to minimise index size.
type IndexEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// PreviewData holds the information needed to render a file preview page.
// Exactly one of IsImage / IsText / IsDir / IsBinary will be true.
type PreviewData struct {
	Title        string
	SiteName     string // branding name shown in the header and page title
	// DefaultTheme is the server-configured theme ("dark" or "light").
	DefaultTheme string
	FilePath string // URL path of the file or directory
	FileName string

	// IsDir is true when previewing a directory.
	IsDir bool
	// IsImage / IsText / IsBinary describe the file type for non-directories.
	IsImage  bool
	IsText   bool
	IsBinary bool // not image, not text — generic info card

	// DownloadURL is the download (or ZIP) href for explicit user-initiated downloads.
	DownloadURL string
	// ViewURL is the inline-serving href used for image previews.
	// It does not set Content-Disposition: attachment and is not counted in stats.
	ViewURL string

	// FileSize, MIMEType and ModTime are shown on the generic info card.
	FileSize int64
	MIMEType string
	ModTime  time.Time

	// EntryCount is the number of direct children; populated for directories.
	EntryCount int

	// HighlightedContent is the Chroma-highlighted HTML for text files.
	HighlightedContent template.HTML

	// IsRendered is true when RenderedContent should be shown instead of
	// HighlightedContent (e.g. Markdown, Org-mode, HTML).
	IsRendered bool
	// RenderedContent is the fully-rendered HTML for supported formats.
	// HighlightedContent is always populated as a fallback even when this is set.
	RenderedContent template.HTML

	Breadcrumbs []Breadcrumb
}
