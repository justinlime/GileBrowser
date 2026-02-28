package handlers

import (
	"log"
	"sync"
	"time"

	"gileserver/models"
)

// safetyTTL is a long backstop expiry applied to every cache entry.
// Under normal operation the watcher invalidates entries long before this
// fires; it exists only as a safety net in case a kernel watch event is
// ever missed (e.g. watch-limit exhaustion, network filesystem edge cases).
const safetyTTL = 5 * time.Minute

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

// ---------------------------------------------------------------------------
// Search-index cache
// ---------------------------------------------------------------------------

// indexCache holds one lazily-refreshed search index for all roots.
var indexCache struct {
	mu         sync.Mutex
	index      *models.FileIndex
	expires    time.Time
	refreshing bool
}

// cachedIndex returns the search index, rebuilding it if stale.
//
//   - First request: builds synchronously (guaranteed non-nil return).
//   - Subsequent stale hits: returns the old index immediately and triggers a
//     single background goroutine to refresh it. The refreshing flag prevents
//     duplicate concurrent refreshes. A deferred reset ensures the flag is
//     always cleared, even if buildIndex panics.
func cachedIndex(roots map[string]string) *models.FileIndex {
	indexCache.mu.Lock()
	idx := indexCache.index
	expired := time.Now().After(indexCache.expires)
	refreshing := indexCache.refreshing
	indexCache.mu.Unlock()

	if idx == nil {
		// First request ever: build synchronously so we never return nil.
		fresh := buildIndex(roots)
		indexCache.mu.Lock()
		indexCache.index = fresh
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

			fresh := buildIndex(roots)
			indexCache.mu.Lock()
			indexCache.index = fresh
			indexCache.expires = time.Now().Add(safetyTTL)
			indexCache.mu.Unlock()
		}()
	}

	return idx
}

// invalidateIndex marks the index as expired so the next call to cachedIndex
// triggers a background rebuild.  Any in-flight refresh is left to complete.
func invalidateIndex() {
	indexCache.mu.Lock()
	indexCache.expires = time.Time{} // zero time is always in the past
	indexCache.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Startup cache warming
// ---------------------------------------------------------------------------

// WarmCache pre-populates the directory-size cache and the search index in
// the background so that the very first page load is served from cache.
//
// It must be called after the roots map is finalized. All work runs in a
// goroutine so server startup is never delayed.
func WarmCache(roots map[string]string) {
	go func() {
		log.Println("cache: warming started")

		// Build the search index — the single most expensive walk.
		idx := buildIndex(roots)
		indexCache.mu.Lock()
		indexCache.index = idx
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

		for _, fsRoot := range roots {
			dirs <- fsRoot
		}

		for _, entry := range idx.Files {
			if !entry.Dir {
				continue
			}
			// Reconstruct the filesystem path from the URL path /<rootName>/…
			urlPath := entry.Path
			if len(urlPath) <= 1 {
				continue
			}
			rel := urlPath[1:] // strip leading "/"
			rootName, rest := rel, ""
			for i, c := range rel {
				if c == '/' {
					rootName = rel[:i]
					rest = rel[i+1:]
					break
				}
			}
			if fsRoot, ok := roots[rootName]; ok {
				var fsDir string
				if rest == "" {
					fsDir = fsRoot
				} else {
					fsDir = joinPath(fsRoot, rest)
				}
				select {
				case dirs <- fsDir:
				default:
					// Channel full — handler will compute on demand.
				}
			}
		}

		close(dirs)
		wg.Wait()
		log.Println("cache: warming complete")
	}()
}
