package server

import (
	"path/filepath"
	"strings"
)

// rootName derives a URL-safe root name from a filesystem directory path.
// It uses the base name of the path, lowercased, with spaces replaced by
// hyphens.
func rootName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")
	return base
}
