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
	// ---------------------------------------------------------------------------
	// ZIP format constants — sourced directly from archive/zip/struct.go in the
	// Go standard library. All values assume zip.Store (no compression) and that
	// FileHeader.Modified is NOT set (zero value).
	//
	// Setting Modified causes Go to emit an extended-timestamp extra field
	// (9 bytes per local header, 5 bytes per central-directory entry) which
	// would silently invalidate the pre-calculated Content-Length.
	// ---------------------------------------------------------------------------

	// Local file header: 30 fixed bytes + filename (no extra when using Store +
	// GP bit 3, because sizes are written as zero at header time — zip64 is
	// never triggered in the local header with this write strategy).
	localHeaderSize = 30 // archive/zip: fileHeaderLen

	// Data descriptor written after each file's data (GP bit 3 is always set
	// by Go's zip writer). The size depends on whether the file needs zip64:
	//   normal : 4 (sig) + 4 (crc32) + 4 (comp) + 4 (uncomp)       = 16 bytes
	//   zip64  : 4 (sig) + 4 (crc32) + 8 (comp) + 8 (uncomp)       = 24 bytes
	dataDescriptorSize   = 16 // archive/zip: dataDescriptorLen
	dataDescriptor64Size = 24 // archive/zip: dataDescriptor64Len

	// Central directory entry: 46 fixed bytes + filename [+ zip64 extra].
	// The 28-byte zip64 extra block is appended by Go when either:
	//   (a) the file's uncompressed/compressed size > uint32max, OR
	//   (b) the file's local-header offset within the archive >= uint32max
	// Both conditions must be checked when computing central-directory sizes.
	centralDirEntrySize  = 46 // archive/zip: directoryHeaderLen
	zip64CentralExtraSize = 28 // 2×uint16 (ID+len) + 3×uint64 (sizes+offset)

	// End of central directory record (always present).
	endRecordSize = 22 // archive/zip: directoryEndLen

	// ZIP64 end-of-central-directory structures, written when any of:
	//   · number of entries  >= 0xFFFF     (uint16max)
	//   · central dir size   >= 0xFFFFFFFF (uint32max)
	//   · central dir offset >= 0xFFFFFFFF (uint32max)
	// The locator immediately precedes the regular EOCD; the zip64 EOCD
	// immediately precedes the locator.
	zip64EndRecordSize  = 56 // archive/zip: directory64EndLen
	zip64LocatorSize    = 20 // archive/zip: directory64LocLen
	zip64EndTotalSize   = zip64EndRecordSize + zip64LocatorSize // 76 bytes

	// Thresholds matching Go's internal uint32max / uint16max.
	zip32Max   = (1 << 32) - 1 // 0xFFFFFFFF
	zip16Max   = (1 << 16) - 1 // 0x0000FFFF

	copyBufferSize = 128 * 1024 // 128 KB I/O buffer for faster copies
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
// rooted at prefix. It follows symlinks (including symlinks to directories)
// and prevents infinite recursion by tracking every resolved real path that
// has been visited.
func collectEntries(fsPath, prefix string) ([]zipEntry, error) {
	// Resolve the root itself so it is in the visited set from the start,
	// preventing a symlink inside the tree from looping back to the root.
	realRoot, err := filepath.EvalSymlinks(fsPath)
	if err != nil {
		return nil, fmt.Errorf("resolving root %s: %w", fsPath, err)
	}

	visited := make(map[string]struct{})
	visited[realRoot] = struct{}{}

	var entries []zipEntry
	err = walkEntries(realRoot, prefix, visited, &entries)
	return entries, err
}

// walkEntries recursively collects zip entries under fsPath, using zipPrefix
// as the archive path for the contents of this directory. visited is shared
// across all recursive calls to detect cycles from symlinks.
//
// filepath.Walk is intentionally avoided here because it does not follow
// symlinks into directories — it calls the walk function with the symlink's
// own FileInfo and never descends. We use os.ReadDir + os.Lstat instead so
// we can detect symlinks ourselves and recurse into their targets explicitly.
func walkEntries(fsPath, zipPrefix string, visited map[string]struct{}, entries *[]zipEntry) error {
	dirEntries, err := os.ReadDir(fsPath)
	if err != nil {
		log.Printf("zip  warning    cannot-read-dir=%s  err=%v", fsPath, err)
		return nil // skip unreadable directory but don't abort the whole walk
	}

	for _, de := range dirEntries {
		filePath := filepath.Join(fsPath, de.Name())
		zipName := zipPrefix + "/" + de.Name()

		// Lstat so we see the symlink itself, not its target.
		fi, err := os.Lstat(filePath)
		if err != nil {
			log.Printf("zip  warning    lstat=%s  err=%v", filePath, err)
			continue
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			// Resolve the symlink and check for cycles before deciding what to do.
			realPath, err := filepath.EvalSymlinks(filePath)
			if err != nil {
				log.Printf("zip  warning    broken-symlink=%s  err=%v", filePath, err)
				continue
			}

			if _, exists := visited[realPath]; exists {
				log.Printf("zip  warning    cycle-detected=%s  real=%s", filePath, realPath)
				continue
			}
			visited[realPath] = struct{}{}

			// Stat the resolved target to find out if it's a file or directory.
			targetInfo, err := os.Stat(realPath)
			if err != nil {
				log.Printf("zip  warning    cannot-stat-resolved=%s  real=%s  err=%v", filePath, realPath, err)
				continue
			}

			if targetInfo.IsDir() {
				// Recurse into the symlinked directory using the resolved path
				// so that further os.ReadDir calls work correctly, but keep
				// zipName derived from the original (logical) path so the
				// archive structure mirrors what the user sees on disk.
				if err := walkEntries(realPath, zipName, visited, entries); err != nil {
					log.Printf("zip  warning    walk-symdir=%s  err=%v", realPath, err)
				}
			} else {
				// Symlink to a regular file — add it directly.
				*entries = append(*entries, zipEntry{
					fsPath:  realPath,
					zipName: zipName,
					size:    targetInfo.Size(),
				})
			}
			continue
		}

		if fi.IsDir() {
			// Regular directory: track its real path and recurse.
			realPath, err := filepath.EvalSymlinks(filePath)
			if err != nil {
				log.Printf("zip  warning    evalsymlinks=%s  err=%v", filePath, err)
				realPath = filePath // best-effort fallback
			}
			if _, exists := visited[realPath]; exists {
				log.Printf("zip  warning    cycle-detected=%s  real=%s", filePath, realPath)
				continue
			}
			visited[realPath] = struct{}{}

			if err := walkEntries(filePath, zipName, visited, entries); err != nil {
				log.Printf("zip  warning    walk-dir=%s  err=%v", filePath, err)
			}
			continue
		}

		// Regular file.
		*entries = append(*entries, zipEntry{
			fsPath:  filePath,
			zipName: zipName,
			size:    fi.Size(),
		})
	}

	return nil
}

// calculateZipSize pre-calculates the exact ZIP archive size without reading
// file data. It mirrors every byte that buildZip will emit, including all
// ZIP64 structures that Go's archive/zip package emits automatically.
//
// # Assumptions that must be maintained in buildZip
//
//  1. Method = zip.Store — data size equals file size exactly (no compression).
//  2. Modified is NOT set on FileHeader (zero value). Setting Modified causes
//     Go to emit extended-timestamp extra fields (9 bytes per local header,
//     5 bytes per central-directory entry) which would silently break the
//     pre-calculated Content-Length.
//
// # Structure sizes (from archive/zip/struct.go)
//
// Per file — local section:
//
//	local file header : 30 + len(filename)
//	  (no zip64 extra: with Store + GP bit 3, sizes are 0 when the local
//	   header is written, so isZip64() is false and no extra is appended)
//	file data         : file size (Store = no compression)
//	data descriptor   : 16 bytes normally, 24 bytes when file size > uint32max
//
// Per file — central directory section:
//
//	central dir entry : 46 + len(filename) [+ 28 zip64 extra]
//	  zip64 extra is appended by Go when EITHER:
//	    · file size > uint32max (individual file is large), OR
//	    · the file's local-header offset within the archive >= uint32max
//	      (cumulative archive bytes so far crossed the 4 GiB boundary)
//
// Footer:
//
//	zip64 EOCD record   : 56 bytes  ┐ only written when ANY of:
//	zip64 EOCD locator  : 20 bytes  ┘   entries >= 65535, or central dir
//	                                     size or offset >= uint32max
//	end of central dir  : 22 bytes  (always present)
func calculateZipSize(entries []zipEntry) int64 {
	var totalSize int64

	// -------------------------------------------------------------------------
	// Pass 1 — local sections (header + data + data-descriptor) for every file.
	// Track the running byte offset so Pass 2 can decide per-entry whether the
	// offset triggers a zip64 central-directory extra field.
	// -------------------------------------------------------------------------
	offsets := make([]int64, len(entries)) // local-header byte offset for each entry

	var localOffset int64
	for i, e := range entries {
		offsets[i] = localOffset

		nameLen := int64(len(e.zipName))

		// Local file header: fixed 30 bytes + filename.
		// No zip64 extra because Go writes zero sizes here (GP bit 3 / Store).
		localHeader := int64(localHeaderSize) + nameLen

		// File data: unchanged by Store.
		fileData := e.size

		// Data descriptor: 16 bytes normally, 24 for zip64 files.
		var dd int64
		if e.size > zip32Max {
			dd = dataDescriptor64Size
		} else {
			dd = dataDescriptorSize
		}

		blockSize := localHeader + fileData + dd
		totalSize += blockSize
		localOffset += blockSize
	}

	// -------------------------------------------------------------------------
	// Pass 2 — central directory entries.
	// A 28-byte zip64 extra is appended by Go when the file's size exceeds
	// uint32max OR when its local-header offset within the archive has reached
	// uint32max. Both conditions are checked independently per entry.
	// -------------------------------------------------------------------------
	var centralDirStart int64 = localOffset // central dir begins right after all local sections
	var centralDirSize int64

	for i, e := range entries {
		nameLen := int64(len(e.zipName))

		needsZip64 := e.size > zip32Max || offsets[i] >= zip32Max

		var extra int64
		if needsZip64 {
			extra = zip64CentralExtraSize // 28 bytes
		}

		entrySize := int64(centralDirEntrySize) + nameLen + extra
		totalSize += entrySize
		centralDirSize += entrySize
	}

	// -------------------------------------------------------------------------
	// Footer: ZIP64 EOCD structures + regular EOCD.
	//
	// Go emits the zip64 EOCD record (56 B) + zip64 locator (20 B) when ANY of:
	//   · number of entries  >= uint16max (65535)
	//   · central dir size   >= uint32max
	//   · central dir offset >= uint32max
	// The regular EOCD (22 bytes) is always present.
	// -------------------------------------------------------------------------
	numEntries := int64(len(entries))
	needsZip64Footer := numEntries >= zip16Max ||
		centralDirSize >= zip32Max ||
		centralDirStart >= zip32Max

	if needsZip64Footer {
		totalSize += zip64EndTotalSize // 56 (record) + 20 (locator) = 76 bytes
	}

	totalSize += int64(endRecordSize) // 22 bytes, always

	return totalSize
}

// buildZip writes all entries into w as a ZIP archive using Store compression.
//
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
