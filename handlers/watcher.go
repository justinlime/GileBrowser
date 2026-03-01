package handlers

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

// StartWatcher sets up recursive filesystem watches on all configured roots.
// On any change it invalidates only the affected cache entries so the next
// request is served fresh without a full re-walk.
//
// It returns immediately; all watch processing runs in a background goroutine.
// The returned stop function closes the watcher and terminates the goroutine.
func StartWatcher(roots map[string]string) (stop func(), err error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Watch every existing directory under every root recursively.
	for _, fsRoot := range roots {
		if err := watchRecursive(w, fsRoot); err != nil {
			log.Printf("watcher: could not watch %s: %v", fsRoot, err)
		}
	}

	go func() {
		defer w.Close()
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				handleEvent(w, roots, event)

			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Printf("watcher: %v", err)
			}
		}
	}()

	return func() { _ = w.Close() }, nil
}

// watchRecursive adds a watch for dir and every subdirectory beneath it.
// If the kernel inotify watch limit is reached, it logs a single actionable
// message and stops — directories beyond that point fall back to the
// safetyTTL for cache invalidation.
func watchRecursive(w *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Log but continue — a single unreadable dir shouldn't abort the walk.
			log.Printf("watcher: skipping %s: %v", path, err)
			return nil
		}
		if !entryIsDir(filepath.Dir(path), d) {
			return nil
		}
		if err := w.Add(path); err != nil {
			if errors.Is(err, syscall.ENOSPC) {
				log.Printf(
					"watcher: inotify watch limit reached (stopped at %s).\n"+
						"  Directories beyond this point will not receive instant cache invalidation;\n"+
						"  the %s safety TTL will still correct any stale entries.\n"+
						"  To enable full coverage, raise the kernel limit:\n"+
						"    echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf\n"+
						"    sudo sysctl -p",
					path, safetyTTL,
				)
				return filepath.SkipAll
			}
			// Any other error: log and keep walking.
			log.Printf("watcher: could not add watch for %s: %v", path, err)
		}
		return nil
	})
}

// handleEvent processes a single fsnotify event.
func handleEvent(w *fsnotify.Watcher, roots map[string]string, event fsnotify.Event) {
	// If a new directory was created, start watching it (and its children)
	// immediately so subsequent changes inside it are also caught.
	if event.Has(fsnotify.Create) {
		if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
			if err := watchRecursive(w, event.Name); err != nil {
				log.Printf("watcher: could not watch new dir %s: %v", event.Name, err)
			}
		}
	}

	// When a directory is removed or renamed, evict its size-cache entry
	// entirely rather than just marking it stale. The path no longer exists
	// so there is nothing to recompute — keeping it would waste memory.
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		evictSizePath(event.Name)
	}

	// Invalidate (mark stale) the size for the parent directory and every
	// ancestor up to the root. A file change at any depth affects the
	// cumulative size of all directories above it.
	invalidateSizeChain(roots, filepath.Dir(event.Name))

	// Any structural change (new file/dir, removal, rename) means the search
	// index is stale.  Write events to existing files don't change the index.
	if event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		invalidateIndex()
	}
}

// invalidateSizeChain removes fsPath and every ancestor up to the root from
// the size cache.  This is needed because dirSize is cumulative — a byte
// added deep in the tree changes the reported size of every directory above.
func invalidateSizeChain(roots map[string]string, fsPath string) {
	// Build a set of all root paths for quick membership tests.
	rootSet := make(map[string]bool, len(roots))
	for _, r := range roots {
		rootSet[filepath.Clean(r)] = true
	}

	cur := filepath.Clean(fsPath)
	for {
		invalidateSizePath(cur)

		if rootSet[cur] {
			// We have invalidated up to and including the root itself.
			break
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the filesystem root without hitting a managed root —
			// shouldn't happen, but guard against an infinite loop.
			break
		}
		cur = parent
	}
}

// joinPath joins a filesystem root with a slash-separated relative URL path.
func joinPath(fsRoot, urlRel string) string {
	return filepath.Join(fsRoot, filepath.FromSlash(urlRel))
}
