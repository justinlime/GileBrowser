package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// statsSnapshot is the public view of the current download counters,
// used by the template function.
type StatsSnapshot struct {
	TotalDownloads int64
	TotalBytes     int64
}

// persistedStats is the on-disk JSON structure.
type persistedStats struct {
	TotalDownloads int64 `json:"total_downloads"`
	TotalBytes     int64 `json:"total_bytes"`
}

var downloadStats struct {
	mu   sync.Mutex
	data persistedStats
	path string
}

// InitStats resolves the stats file path from the given directory, loads any
// existing data from disk, and keeps the path for future writes.  If the file
// does not exist it is created immediately with zero counters so that the path
// is visible on disk from the moment the server starts and any permission
// problems surface right away rather than silently at the time of the first
// download.
func InitStats(statsDir string) {
	filePath := filepath.Join(statsDir, "gile.json")

	downloadStats.mu.Lock()
	defer downloadStats.mu.Unlock()

	downloadStats.path = filePath

	f, err := os.Open(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("stats: could not open %s: %v", filePath, err)
			return
		}
		// File doesn't exist yet — write zeros so the file is present on disk.
		if err := persistStatsLocked(filePath, persistedStats{}); err != nil {
			log.Printf("stats: could not create %s: %v", filePath, err)
		}
		return
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&downloadStats.data); err != nil {
		log.Printf("stats: could not parse %s: %v — starting from zero", filePath, err)
		downloadStats.data = persistedStats{}
	}
}

// RecordDownload increments the counters by one download of bytesSent bytes
// and persists the updated totals to disk.
func RecordDownload(bytesSent int64) {
	downloadStats.mu.Lock()
	downloadStats.data.TotalDownloads++
	downloadStats.data.TotalBytes += bytesSent
	snap := downloadStats.data
	path := downloadStats.path
	downloadStats.mu.Unlock()

	// Write asynchronously so the response is never delayed by disk I/O.
	go persistStats(path, snap)
}

// GetStats returns a point-in-time snapshot of the download counters.
func GetStats() StatsSnapshot {
	downloadStats.mu.Lock()
	defer downloadStats.mu.Unlock()
	return StatsSnapshot{
		TotalDownloads: downloadStats.data.TotalDownloads,
		TotalBytes:     downloadStats.data.TotalBytes,
	}
}

// persistStats logs any error and is safe to call from a goroutine.
func persistStats(filePath string, data persistedStats) {
	if err := persistStatsLocked(filePath, data); err != nil {
		log.Printf("stats: %v", err)
	}
}

// persistStatsLocked does the actual atomic write and returns any error.
// It does not acquire any mutex and may be called from InitStats (which
// already holds the lock) or from the async goroutine in RecordDownload.
func persistStatsLocked(filePath string, data persistedStats) error {
	dir := filepath.Dir(filePath)
	tmp, err := os.CreateTemp(dir, ".gilebrowser-stats-*.tmp")
	if err != nil {
		return fmt.Errorf("could not create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if err := json.NewEncoder(tmp).Encode(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("could not write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("could not close temp file: %w", err)
	}
	if err := os.Rename(tmpName, filePath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("could not rename %s to %s: %w", tmpName, filePath, err)
	}
	return nil
}
