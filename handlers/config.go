// Package handlers provides HTTP request handlers for the GileBrowser server.
// This file implements unified configuration and statistics tracking.
package handlers

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"gileserver/db"
	"gileserver/settings"
)

// StatsSnapshot is the public view of the current download counters,
// used by the template function.
type StatsSnapshot struct {
	TotalDownloads int64
	TotalBytes     int64
}

// RuntimeConfig holds the active configuration used by the server.
// Values are loaded from the database at startup and can be updated via the web interface.
type RuntimeConfig struct {
	Title          string
	DefaultTheme   string
	HighlightTheme string  // Derived from DefaultTheme (catppuccin-mocha/latte)
	PreviewImages  bool
	PreviewText    bool
	PreviewDocs    bool
	BandwidthBps   float64
	FaviconPath    string
	RootDirs       []settings.RootDir  // List of configured root directories
}

var (
	runtimeConfig RuntimeConfig
	dbDir         string  // Directory where database and other data is stored
)

// InitConfig initializes both the settings database and stats database.
// The dataDir parameter specifies where gile.db will be stored.
// Call this once during server startup before any other operations.
func InitConfig(dataDir string) {
	// Store data directory for later use by handlers
	dbDir = dataDir
	dbPath := filepath.Join(dbDir, "gile.db")

	// Initialize settings storage.
	if err := settings.InitSettings(dbPath); err != nil {
        log.Printf("config: failed to initialize settings database: %v", err)
    }

	// Initialize roots table for directory management.
	if err := settings.InitRootsTable(); err != nil {
        log.Printf("config: failed to initialize roots table: %v", err)
    }

	// Initialize stats storage (uses same database file).
	if err := db.InitDB(dbPath); err != nil {
        log.Printf("config: failed to initialize stats database: %v", err)
    }

	// Load current settings into runtime config.
	LoadRuntimeConfig()
}

// LoadRuntimeConfig reads settings from the database and populates runtimeConfig.
// This can be called after settings are updated via the web interface.
func LoadRuntimeConfig() {
	s, err := settings.GetAllSettings()
	if err != nil {
        log.Printf("config: failed to load settings: %v", err)
        return
    }

	// Load root directories from database.
	roots, err := settings.GetAllRoots()
	if err != nil {
        log.Printf("config: failed to load roots: %v", err)
        roots = []settings.RootDir{}
    }

	runtimeConfig = RuntimeConfig{
        Title:          s.Title,
        DefaultTheme:   s.DefaultTheme,
        HighlightTheme: highlightThemeForUI(s.DefaultTheme),
        PreviewImages:  s.PreviewImages,
        PreviewText:    s.PreviewText,
        PreviewDocs:    s.PreviewDocs,
        BandwidthBps:   s.BandwidthBps,
        FaviconPath:    s.FaviconPath,
        RootDirs:       roots,
    }

	log.Printf("config: loaded - title=%q theme=%q previews=img=%v txt=%v doc=%v bw=%v favicon=%q roots=%d",
        runtimeConfig.Title, runtimeConfig.DefaultTheme,
        runtimeConfig.PreviewImages, runtimeConfig.PreviewText, runtimeConfig.PreviewDocs,
        formatBandwidth(runtimeConfig.BandwidthBps), runtimeConfig.FaviconPath, len(roots))
}

// GetRuntimeConfig returns the current active configuration.
func GetRuntimeConfig() RuntimeConfig {
	return runtimeConfig
}

// GetDataDir returns the directory where persistent data is stored.
func GetDataDir() string {
	return dbDir
}

// SaveSettings persists new settings to the database and updates runtime config.
func SaveSettings(s settings.Settings) error {
	if err := settings.SaveAllSettings(s); err != nil {
		return err
    }
	LoadRuntimeConfig()
	UpdateRenderOptions() // Update renderer with new theme/image policy
	return nil
}

// rootName derives a URL-safe root name from a filesystem directory path.
// It uses the base name of the path, lowercased, with spaces replaced by hyphens.
func rootName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")
	return base
}

// RecordDownload increments the counters by one download of bytesSent bytes
// and persists the updated totals to SQLite.
func RecordDownload(bytesSent int64) {
	db.RecordDownload(bytesSent)
}

// GetStats returns a point-in-time snapshot of the download counters.
func GetStats() StatsSnapshot {
	totalDownloads, totalBytes, err := db.GetStats()
	if err != nil {
        log.Printf("stats: failed to retrieve stats: %v", err)
        return StatsSnapshot{TotalDownloads: 0, TotalBytes: 0}
    }
	return StatsSnapshot{
        TotalDownloads: totalDownloads,
        TotalBytes:     totalBytes,
    }
}

// highlightThemeForUI returns the Chroma theme name for a given UI theme.
func highlightThemeForUI(theme string) string {
	if theme == "light" {
		return "catppuccin-latte"
    }
	return "catppuccin-mocha"
}

// formatBandwidth converts bytes/sec to human-readable string for logging.
func formatBandwidth(bps float64) string {
	if bps == 0 {
		return "unlimited"
    }
	bits := bps * 8
	switch {
    case bits >= 1_000_000_000:
        return fmt.Sprintf("%.2f Gbps", bits/1_000_000_000)
    case bits >= 1_000_000:
        return fmt.Sprintf("%.2f Mbps", bits/1_000_000)
    case bits >= 1_000:
        return fmt.Sprintf("%.2f Kbps", bits/1_000)
    default:
        return fmt.Sprintf("%.0f bps", bits)
    }
}

// GetRootsMap returns a map of root name -> filesystem path.
// This combines CLI-provided directories with database-stored ones.
// It reads from the runtime config on every call, so directory changes
// take effect immediately without server restart.
func GetRootsMap(cliDirs []string) map[string]string {
	roots := make(map[string]string)
	
	// First, add all database-stored roots.
	rtc := GetRuntimeConfig()
	for _, rd := range rtc.RootDirs {
		roots[rd.Name] = rd.Path
    }

	// CLI directories take precedence and are added/overwritten.
	// This maintains backward compatibility for users who still use -dir flag.
	for _, d := range cliDirs {
        name := rootName(d)
        roots[name] = d
    }

	return roots
}

// GetCurrentRootsMap returns the current roots map by reading CLI dirs from global state.
// Use this in handlers that need dynamic roots without passing cliDirs parameter.
func GetCurrentRootsMap() map[string]string {
	return GetRootsMap(cliDirs)
}

var cliDirs []string  // CLI-provided directories, set at startup

// SetCLIDirs stores the CLI-provided directories for later use in GetRootsMap.
func SetCLIDirs(dirs []string) {
	cliDirs = dirs
}

// RefreshRootsState invalidates caches and restarts the watcher when root directories change.
// Call this after adding/removing directories via the web interface to make changes take effect immediately.
func RefreshRootsState() {
	log.Println("handlers: refreshing roots state - invalidating caches")
	
	// Invalidate search index cache.
	invalidateIndex()
	
	// Clear size cache for all paths (they may have changed).
	sizeCache.mu.Lock()
	for path := range sizeCache.entries {
		delete(sizeCache.entries, path)
    }
	sizeCache.mu.Unlock()
	
	log.Println("handlers: roots state refreshed - caches cleared")
}

// AddRootDir adds a new root directory to the database.
func AddRootDir(name, path string) error {
	if err := settings.AddRoot(name, path); err != nil {
		return err
    }
	LoadRuntimeConfig()
	log.Printf("handlers: added root directory %q -> %q", name, path)
	return nil
}

// RemoveRootDir removes a root directory from the database.
func RemoveRootDir(name string) error {
	if err := settings.RemoveRoot(name); err != nil {
		return err
    }
	LoadRuntimeConfig()
	log.Printf("handlers: removed root directory %q", name)
	return nil
}
