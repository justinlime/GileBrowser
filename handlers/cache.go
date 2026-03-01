package handlers

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
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

// collectDirs sends root and every subdirectory beneath it into dirs.
// It is used by WarmCache to discover paths for size pre-warming without
// retaining a full FileIndex in memory.
func collectDirs(root string, dirs chan<- string) {
	dirs <- root
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if entryIsDir(root, e) {
			collectDirs(filepath.Join(root, e.Name()), dirs)
		}
	}
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

		// Pre-populate size cache for every directory the index knows about,
		// plus each root itself. A worker pool bounds the parallelism.
		const workers = 8
		dirs := make(chan string, 512)

		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for d := range dirs {
					cachedDirSize(d)
				}
			}()
		}

		// Walk each root to collect all directory paths for size warming.
		// This mirrors what buildIndex does but only retains directories,
		// avoiding the need to keep (or decode) a full FileIndex in memory.
		for _, fsRoot := range roots {
			collectDirs(fsRoot, dirs)
		}

		close(dirs)
		wg.Wait()
		log.Println("cache: warming complete")
	}()
}
