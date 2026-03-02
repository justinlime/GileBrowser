package handlers

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)



// ZipHandler streams a directory as a ZIP archive.
// When the URL resolves to the server root ("/"), all configured root
// directories are bundled together into a single archive named after siteName.
func ZipHandler(roots map[string]string, siteName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Strip leading /zip to get the directory URL path.
		urlPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/zip"))

		ip := clientIP(r)

		// Special case: zip everything when the server root is requested.
		if urlPath == "/" {
					log.Printf("zip  download   ip=%-15s  dir=/ (all roots)", ip)
					start := time.Now()
					n := zipAll(w, roots, siteName)
					if n > 0 {
						RecordDownload(n)
					}
					log.Printf("zip  complete   ip=%-15s  duration=%s  dir=/ (all roots)",
						ip, time.Since(start).Round(time.Millisecond))
					return
		}

		fsPath, err := resolvePath(roots, urlPath)
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		info, err := os.Stat(fsPath)
		if err != nil || !info.IsDir() {
			http.Error(w, "Not a directory", http.StatusBadRequest)
			return
		}

		dirName := filepath.Base(fsPath)
		log.Printf("zip  download   ip=%-15s  dir=%s", ip, urlPath)
		start := time.Now()

		entries, err := collectEntries(fsPath, dirName)
		if err != nil {
			http.Error(w, "Failed to read directory", http.StatusInternalServerError)
			return
		}

		n, err := streamZip(w, entries, dirName)
		if err != nil {
			log.Printf("zip  error      ip=%-15s  dir=%s  err=%v", ip, urlPath, err)
		} else {
			RecordDownload(n)
		}
		log.Printf("zip  complete   ip=%-15s  duration=%s  dir=%s",
			ip, time.Since(start).Round(time.Millisecond), urlPath)
	}
}

// zipAll bundles every configured root directory into a single archive named
// after siteName. Each root is placed under its own top-level folder inside
// the archive (e.g. rootName/subdir/file.txt).
// It returns the number of bytes written, or 0 on error.
func zipAll(w http.ResponseWriter, roots map[string]string, siteName string) int64 {
	var allEntries []zipEntry
	for name, fsPath := range roots {
		entries, err := collectEntries(fsPath, name)
		if err == nil {
			allEntries = append(allEntries, entries...)
		}
	}
	n, _ := streamZip(w, allEntries, siteName)
	return n
}

// zipEntry describes a single file to be added to a ZIP archive.
type zipEntry struct {
	fsPath  string // absolute path on disk
	zipName string // path inside the archive (e.g. "rootname/subdir/file.txt")
	size    int64  // uncompressed file size
}

// collectEntries walks fsPath and returns all files with their archive names
// rooted at prefix. It follows symlinks and prevents infinite recursion by
// tracking visited inodes (on Unix) or resolved paths (on Windows).
func collectEntries(fsPath, prefix string) ([]zipEntry, error) {
	var entries []zipEntry
	
	// Track visited directories to prevent infinite loops from circular symlinks.
	// We use the real path (with symlinks resolved) as the key.
	visited := make(map[string]struct{})
	
	err := filepath.Walk(fsPath, func(filePath string, fi os.FileInfo, err error) error {
		if err != nil {
			// Skip files/directories we can't access but continue walking.
			log.Printf("zip  warning    skip=%s  err=%v", filePath, err)
			return nil
		}

		isSymlink := (fi.Mode() & os.ModeSymlink) != 0
		
		// For symlinks, we need to resolve them and check for cycles.
		if isSymlink {
			// Get the real path to detect circular references.
			realPath, err := filepath.EvalSymlinks(filePath)
			if err != nil {
				// Broken symlink - skip it but continue.
				log.Printf("zip  warning    broken-symlink=%s  err=%v", filePath, err)
				return nil
			}
			
			// Check if we've already visited this real path (cycle detection).
			if _, exists := visited[realPath]; exists {
				log.Printf("zip  warning    cycle-detected=%s  real=%s", filePath, realPath)
				return nil
			}
			
			// Mark as visited.
			visited[realPath] = struct{}{}
			
			// Re-stat the resolved path to get actual info.
			fi, err = os.Stat(realPath)
			if err != nil {
				log.Printf("zip  warning    cannot-stat-resolved=%s  real=%s  err=%v", filePath, realPath, err)
				return nil
			}
			
			// Update filePath to the resolved path for actual file access.
			filePath = realPath
		} else if fi.IsDir() {
			// For regular directories, also track them to catch symlink->dir cycles.
			realPath, err := filepath.EvalSymlinks(filePath)
			if err == nil {
				if _, exists := visited[realPath]; exists {
					log.Printf("zip  warning    cycle-detected=%s  real=%s", filePath, realPath)
					return filepath.SkipDir
				}
				visited[realPath] = struct{}{}
			}
		}

		if fi.IsDir() {
			return nil // Skip directories themselves.
		}

		rel, err := filepath.Rel(fsPath, filePath)
		if err != nil {
			log.Printf("zip  warning    cannot-calc-rel=%s  from=%s  err=%v", filePath, fsPath, err)
			return nil
		}
		
		entries = append(entries, zipEntry{
			fsPath:  filePath,
			zipName: prefix + "/" + filepath.ToSlash(rel),
			size:    fi.Size(),
		})
		return nil
	})
	return entries, err
}

// countingWriter counts the number of bytes written to it, discarding the
// data. Used for the dry-run pass to determine the exact ZIP size before
// committing to an http.ResponseWriter.
type countingWriter struct{ n int64 }

func (cw *countingWriter) Write(p []byte) (int, error) {
	cw.n += int64(len(p))
	return len(p), nil
}

// buildZip writes all entries into w as a ZIP archive using Store compression.
// It is called twice by streamZip: once with a countingWriter (dry run) and
// once with the real http.ResponseWriter. Because Store is a verbatim copy,
// the byte count from the dry run is guaranteed to match the real write.
func buildZip(w io.Writer, entries []zipEntry) error {
	zw := zip.NewWriter(w)
	for _, e := range entries {
		fw, err := zw.CreateHeader(&zip.FileHeader{
			Name:   e.zipName,
			Method: zip.Store,
		})
		if err != nil {
			return err
		}
		f, err := os.Open(e.fsPath)
		if err != nil {
			continue // skip unreadable files
		}
		_, copyErr := io.Copy(fw, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return zw.Close()
}

// streamZip measures the exact ZIP size via a cheap dry-run pass over a
// counting writer, sets Content-Length, then streams the real archive directly
// to the client. No temp files or memory buffers are needed.
// It returns the number of bytes written and any error.
func streamZip(w http.ResponseWriter, entries []zipEntry, name string) (int64, error) {
	cw := &countingWriter{}
	if err := buildZip(cw, entries); err != nil {
		http.Error(w, "Could not build archive", http.StatusInternalServerError)
		return 0, err
	}

	// mime.FormatMediaType correctly quotes the filename parameter and escapes
	// any characters (including `"` and `\`) that would otherwise break the
	// header or enable injection. This mirrors the approach used by FileHandler.
	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": name + ".zip",
	})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", cw.n))

	return cw.n, buildZip(w, entries)
}
