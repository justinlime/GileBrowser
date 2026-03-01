package handlers

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"gileserver/models"
)

// safetyTTL is a long backstop expiry applied to every cache entry.
// Under normal operation the watcher invalidates entries long before this
// fires; it exists only as a safety net in case a kernel watch event is
// ever missed (e.g. watch-limit exhaustion, network filesystem edge cases).
const safetyTTL = 20 * time.Minute

// ---------------------------------------------------------------------------
// Directory-size cache
// ---------------------------------------------------------------------------

// sizeEntry is one entry in the directory-size cache.
type sizeEntry struct {
	size      int64
	expires   time.Time // safety-net deadline; reset by invalidation
	computing bool      // true while a goroutine is walking this path
	stale     bool      // true when invalidated; size holds the last known value
}

// sizeCache caches recursive directory sizes keyed by absolute filesystem path.
var sizeCache struct {
	mu      sync.Mutex
	cond    *sync.Cond
	entries map[string]*sizeEntry
}

func init() {
	sizeCache.entries = make(map[string]*sizeEntry)
	sizeCache.cond = sync.NewCond(&sizeCache.mu)
	go sizeCacheGC()
}

// sizeCacheGC periodically removes entries for paths that no longer exist on
// disk. This reclaims memory for directories that were deleted or renamed
// between watcher events (e.g. deep trees removed while the inotify watch
// limit was in effect).
func sizeCacheGC() {
	const gcInterval = 10 * time.Minute
	for range time.Tick(gcInterval) {
		// Snapshot keys under the lock, then stat without holding it.
		sizeCache.mu.Lock()
		keys := make([]string, 0, len(sizeCache.entries))
		for k := range sizeCache.entries {
			keys = append(keys, k)
		}
		sizeCache.mu.Unlock()

		var dead []string
		for _, k := range keys {
			if _, err := os.Lstat(k); os.IsNotExist(err) {
				dead = append(dead, k)
			}
		}

		if len(dead) > 0 {
			sizeCache.mu.Lock()
			for _, k := range dead {
				delete(sizeCache.entries, k)
			}
			sizeCache.mu.Unlock()
			log.Printf("cache: size GC removed %d stale entries", len(dead))
		}
	}
}

// cachedDirSize returns the cached recursive byte-count for fsPath.
//
//   - Fresh hit        → returned immediately with no I/O.
//   - Stale hit        → the last known size is returned immediately while one
//     background goroutine recomputes; callers never block on a walk.
//   - Miss (first use) → one goroutine walks the tree; the caller blocks until
//     the result is ready (this only happens before WarmCache has populated
//     the entry, i.e. effectively never for directories the index knows about).
//   - Concurrent miss  → subsequent callers wait on the condvar rather than
//     launching duplicate walks.
func cachedDirSize(fsPath string) int64 {
	sizeCache.mu.Lock()
	e, ok := sizeCache.entries[fsPath]

	// Fresh hit — return immediately.
	if ok && !e.computing && !e.stale && time.Now().Before(e.expires) {
		size := e.size
		sizeCache.mu.Unlock()
		return size
	}

	// Stale hit — return the last known size immediately and recompute in the
	// background (unless a recompute is already in flight).
	if ok && e.stale && !e.computing {
		size := e.size // last known value; good enough for display
		e.computing = true
		e.stale = false
		sizeCache.mu.Unlock()

		go func() {
			fresh := dirSize(fsPath)
			sizeCache.mu.Lock()
			e.size = fresh
			e.expires = time.Now().Add(safetyTTL)
			e.computing = false
			sizeCache.cond.Broadcast()
			sizeCache.mu.Unlock()
		}()

		return size
	}

	// A recompute is already in flight (computing == true); return the last
	// known size if we have one, otherwise wait for the result.
	if ok && e.computing && e.size != 0 {
		size := e.size
		sizeCache.mu.Unlock()
		return size
	}
	if ok && e.computing {
		// No prior value — wait for the in-flight walk to finish.
		for e.computing {
			sizeCache.cond.Wait()
		}
		size := e.size
		sizeCache.mu.Unlock()
		return size
	}

	// True miss — this path has never been computed. Walk synchronously once
	// so the caller gets a real answer (happens only before WarmCache runs).
	if !ok {
		e = &sizeEntry{}
		sizeCache.entries[fsPath] = e
	}
	e.computing = true
	sizeCache.mu.Unlock()

	size := dirSize(fsPath)

	sizeCache.mu.Lock()
	e.size = size
	e.expires = time.Now().Add(safetyTTL)
	e.computing = false
	sizeCache.cond.Broadcast()
	sizeCache.mu.Unlock()

	return size
}

// invalidateSizePath marks a single path as stale in the size cache.
// The last known size remains readable so callers never block; a background
// recompute is triggered the next time that size is requested.
func invalidateSizePath(fsPath string) {
	sizeCache.mu.Lock()
	if e, ok := sizeCache.entries[fsPath]; ok {
		e.stale = true
	}
	// If no entry exists yet the path was never requested; nothing to do.
	sizeCache.mu.Unlock()
}

// evictSizePath removes a path from the size cache entirely. Use this when a
// directory is known to have been deleted or renamed so the entry does not
// linger in memory indefinitely.
func evictSizePath(fsPath string) {
	sizeCache.mu.Lock()
	delete(sizeCache.entries, fsPath)
	sizeCache.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Search-index cache
// ---------------------------------------------------------------------------

// indexCache holds the search index pre-serialised as gzip-compressed JSON bytes.
//
// Two-stage compression strategy:
//  1. buildIndex → *models.FileIndex  ([]IndexEntry, short-lived)
//  2. serializeIndex → gzip(JSON)     (compact []byte, long-lived in cache)
//
// The []IndexEntry slice is released immediately after step 2, so peak RAM
// during a rebuild is: slice + raw JSON (momentary) + gzip output (retained).
// The retained blob is typically 5-10x smaller than the raw JSON it replaces,
// directly reducing the steady-state memory footprint of the cache.
var indexCache struct {
	mu         sync.Mutex
	gzJSON     []byte // gzip-compressed JSON; nil until first build
	expires    time.Time
	refreshing bool
}

// serializeIndex JSON-encodes a FileIndex, gzip-compresses the result, and
// returns the compressed bytes. The FileIndex itself is not retained after
// this call. Using BestSpeed keeps the compression fast at build time while
// still yielding significant size reduction for repetitive JSON text.
func serializeIndex(idx *models.FileIndex) []byte {
	raw, err := json.Marshal(idx)
	if err != nil {
		// Fallback: gzip an empty index.
		raw = []byte(`{"files":[]}`)
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		// Should never happen with a valid level, but return raw JSON as a
		// last resort so the cache is never left nil.
		return raw
	}
	gz.Write(raw)
	gz.Close()
	return buf.Bytes()
}

// cachedIndexGzip returns the pre-serialised, gzip-compressed JSON for the
// search index, rebuilding it in the background when stale.
//
//   - First request: builds synchronously (guaranteed non-nil return).
//   - Subsequent stale hits: returns the old bytes immediately and triggers a
//     single background goroutine to refresh; callers never block on a walk.
func cachedIndexGzip(roots map[string]string) []byte {
	indexCache.mu.Lock()
	data := indexCache.gzJSON
	expired := time.Now().After(indexCache.expires)
	refreshing := indexCache.refreshing
	indexCache.mu.Unlock()

	if data == nil {
		// First request ever: build synchronously so we never return nil.
		fresh := serializeIndex(buildIndex(roots))
		indexCache.mu.Lock()
		indexCache.gzJSON = fresh
		indexCache.expires = time.Now().Add(safetyTTL)
		indexCache.mu.Unlock()
		return fresh
	}

	if expired && !refreshing {
		indexCache.mu.Lock()
		indexCache.refreshing = true
		indexCache.mu.Unlock()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("cache: index refresh panic: %v", r)
				}
				indexCache.mu.Lock()
				indexCache.refreshing = false
				indexCache.mu.Unlock()
			}()

			fresh := serializeIndex(buildIndex(roots))
			indexCache.mu.Lock()
			indexCache.gzJSON = fresh
			indexCache.expires = time.Now().Add(safetyTTL)
			indexCache.mu.Unlock()
		}()
	}

	return data
}

// invalidateIndex marks the index as expired so the next call to cachedIndexJSON
// triggers a background rebuild.  Any in-flight refresh is left to complete.
func invalidateIndex() {
	indexCache.mu.Lock()
	indexCache.expires = time.Time{} // zero time is always in the past
	indexCache.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Startup cache warming
// ---------------------------------------------------------------------------

// buildSizeIndex performs a single depth-first walk of root and returns a map
// from absolute directory path to its total recursive byte count.
//
// Previous approach: collectDirs enumerated all directories into a channel,
// then a worker pool called cachedDirSize (→ dirSize → WalkDir) on each one
// independently. For a tree with N directories the root's walk touched all N
// nodes, its children each touched their subtrees, and so on — O(n²) total
// walk work with up to 8 concurrent WalkDir goroutines pinning memory at once.
//
// This approach: one WalkDir visit per filesystem entry, total.
//  1. Walk pass: for each regular file, add its size to its immediate parent
//     directory's running total in a local map. Symlinks-to-directories are
//     handled by calling the existing dirSize helper (which resolves the target
//     and handles nested symlinks), recording the result under the symlink path,
//     and marking it terminal so step 2 does not double-count it.
//  2. Propagation pass: sort all directory paths by length descending (a child
//     path is always strictly longer than its parent's), then sweep once,
//     adding each directory's subtotal to its parent. Terminal entries are
//     skipped because their contribution was already applied in step 1.
//
// Result: O(n) walk work, one goroutine, peak RAM proportional to the number
// of directories (one int64 per directory in the local map).
func buildSizeIndex(root string) map[string]int64 {
	sizes := make(map[string]int64)
	terminal := make(map[string]bool) // symlink-dirs: already applied to parent in step 1

	// Seed the root so it always appears in the map even if empty.
	sizes[root] = 0

	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Real directory: ensure it has an entry so empty dirs are cached.
		if d.IsDir() {
			if _, ok := sizes[p]; !ok {
				sizes[p] = 0
			}
			return nil
		}

		// Symlink: WalkDir uses Lstat so it does not descend into symlinked
		// directories automatically — we handle them explicitly here.
		if d.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return nil
			}
			fi, err := os.Stat(resolved)
			if err != nil {
				return nil
			}
			parent := filepath.Dir(p)
			if fi.IsDir() {
				// Symlink to a directory: delegate to dirSize (which handles
				// further nested symlinks), record the total under the symlink
				// path so callers using that path get a cache hit, and mark it
				// terminal so the propagation pass does not add it to the
				// parent a second time.
				sz := dirSize(resolved)
				sizes[p] = sz
				terminal[p] = true
				sizes[parent] += sz
			} else {
				sizes[parent] += fi.Size()
			}
			return nil
		}

		// Regular file: accumulate size into the immediate parent directory.
		if fi, err := d.Info(); err == nil {
			sizes[filepath.Dir(p)] += fi.Size()
		}
		return nil
	})

	// Propagation pass: roll each directory's subtotal up to its parent.
	// Sorting by descending path length guarantees children before parents
	// because a child's clean path is always strictly longer than its parent's.
	dirs := make([]string, 0, len(sizes))
	for d := range sizes {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, d := range dirs {
		if terminal[d] {
			continue // contribution already applied to parent during walk pass
		}
		parent := filepath.Dir(d)
		if parent != d { // guard: filepath.Dir of a filesystem root returns itself
			sizes[parent] += sizes[d]
		}
	}

	return sizes
}

// WarmCache pre-populates the directory-size cache and the search index in
// the background so that the very first page load is served from cache.
//
// It must be called after the roots map is finalized. All work runs in a
// goroutine so server startup is never delayed.
func WarmCache(roots map[string]string) {
	go func() {
		log.Println("cache: warming started")

		// Build the search index — the single most expensive walk.
		// Serialise and compress immediately so the []IndexEntry slice can be GC'd.
		fresh := serializeIndex(buildIndex(roots))
		indexCache.mu.Lock()
		indexCache.gzJSON = fresh
		indexCache.expires = time.Now().Add(safetyTTL)
		indexCache.mu.Unlock()

		// Pre-populate the size cache with a single bottom-up walk per root.
		// buildSizeIndex is O(n) in the number of filesystem entries; all results
		// are bulk-inserted into the cache under one lock acquisition per root,
		// bypassing the cachedDirSize hot path entirely.
		expiry := time.Now().Add(safetyTTL)
		for _, fsRoot := range roots {
			sizeIndex := buildSizeIndex(fsRoot)
			sizeCache.mu.Lock()
			for p, sz := range sizeIndex {
				sizeCache.entries[p] = &sizeEntry{
					size:    sz,
					expires: expiry,
				}
			}
			sizeCache.mu.Unlock()
		}

		log.Println("cache: warming complete")
	}()
}
