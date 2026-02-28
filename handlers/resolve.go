package handlers

import (
	"fmt"
	"path/filepath"
	"strings"
)

// resolvePath translates a URL path into an absolute filesystem path, using
// the roots map.  It also validates against directory traversal attacks.
func resolvePath(roots map[string]string, urlPath string) (string, error) {
	// URL path must start with /
	if !strings.HasPrefix(urlPath, "/") {
		return "", fmt.Errorf("invalid path")
	}

	// Split into root name and remainder.
	parts := strings.SplitN(strings.TrimPrefix(urlPath, "/"), "/", 2)
	rootName := parts[0]

	rootFS, ok := roots[rootName]
	if !ok {
		return "", fmt.Errorf("unknown root %q", rootName)
	}

	var rel string
	if len(parts) > 1 {
		rel = parts[1]
	}

	fsPath := filepath.Join(rootFS, rel)

	// Security: ensure resolved path is still under the declared root.
	cleanRoot := filepath.Clean(rootFS)
	cleanPath := filepath.Clean(fsPath)
	if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal detected")
	}

	return cleanPath, nil
}
