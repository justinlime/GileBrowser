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

const (
	// ZIP format constants for Store method (no compression, no Modified timestamp).
	// These values assume zip.FileHeader.Modified is NOT set (zero value), which
	// prevents Go from writing extended-timestamp extra fields that would otherwise
	// add 9 bytes per local header and 5 bytes per central-directory entry and
	// silently break the pre-calculated Content-Length.
	localHeaderSize     = 30           // bytes before filename in local file header
	dataDescriptorSize  = 16           // signature(4) + CRC32(4) + comp_size(4) + uncomp_size(4); always written by Go (GP bit 3)
	centralDirEntrySize = 46           // bytes before filename in central directory entry
	endRecordSize       = 22           // end of central directory record
	copyBufferSize      = 128 * 1024   // 128 KB buffer for faster io.Copy
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

// calculateZipSize pre-calculates the exact ZIP archive size without reading
// file data. This relies on two important assumptions that must be matched in
// buildZip:
//
//  1. Method = zip.Store (no compression — data size == file size exactly).
//  2. Modified is NOT set on the FileHeader (zero value). Setting Modified
//     causes Go to emit an extended-timestamp extra field (9 bytes in the local
//     header, 5 bytes in the central-directory entry) which would silently
//     invalidate the Content-Length we send to the client.
//
// Formula per file:
//
//	local header:      30 + len(filename)
//	file data:         file size (unchanged by Store)
//	data descriptor:   16  (Go always writes this when GP bit 3 is set)
//	central dir entry: 46 + len(filename)
//
// Plus a single 22-byte end-of-central-directory record.
func calculateZipSize(entries []zipEntry) int64 {
	var totalSize int64

	for _, e := range entries {
		filenameLen := int64(len(e.zipName))

		// Local file header + filename
		totalSize += int64(localHeaderSize) + filenameLen

		// File data (Store = no compression)
		totalSize += e.size

		// Data descriptor written by Go (GP flag bit 3 set)
		totalSize += int64(dataDescriptorSize)
	}

	// Central directory entries (written after all file data)
	for _, e := range entries {
		filenameLen := int64(len(e.zipName))
		totalSize += int64(centralDirEntrySize) + filenameLen
	}

	// End of central directory record
	totalSize += int64(endRecordSize)

	return totalSize
}

// buildZip writes all entries into w as a ZIP archive using Store compression.
// IMPORTANT: Modified is intentionally left unset (zero value) so that Go does
// not emit extended-timestamp extra fields, keeping the on-wire size consistent
// with calculateZipSize. If you ever need to preserve timestamps, you must also
// update calculateZipSize to add 9 bytes per local header and 5 bytes per
// central-directory entry.
func buildZip(w io.Writer, entries []zipEntry) error {
	zw := zip.NewWriter(w)

	// Reuse this buffer for all file copies to reduce GC pressure.
	copyBuf := make([]byte, copyBufferSize)

	for _, e := range entries {
		fw, err := zw.CreateHeader(&zip.FileHeader{
			Name:   e.zipName,
			Method: zip.Store,
			// Modified intentionally omitted — see calculateZipSize.
		})
		if err != nil {
			zw.Close()
			return err
		}

		f, err := os.Open(e.fsPath)
		if err != nil {
			log.Printf("zip  warning    cannot-open=%s  err=%v", e.fsPath, err)
			continue // skip unreadable files but continue with others
		}

		_, copyErr := io.CopyBuffer(fw, f, copyBuf)
		f.Close()

		if copyErr != nil {
			zw.Close()
			return fmt.Errorf("copying %s: %w", e.fsPath, copyErr)
		}
	}

	return zw.Close()
}

// streamZip pre-calculates the ZIP size mathematically, sets Content-Length,
// then streams the archive directly to the client in a single pass.
// Returns the number of bytes written and any error.
func streamZip(w http.ResponseWriter, entries []zipEntry, name string) (int64, error) {
	totalSize := calculateZipSize(entries)

	// mime.FormatMediaType correctly quotes the filename parameter and escapes
	// any characters (including `"` and `\`) that would otherwise break the
	// header or enable injection.
	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": name + ".zip",
	})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", totalSize))

	// Single pass — stream directly to client.
	return totalSize, buildZip(w, entries)
}
