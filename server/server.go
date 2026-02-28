package server

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"gileserver/config"
	"gileserver/handlers"
)

// staticFS holds the embedded static assets.
var staticFS embed.FS

// SetStaticFS is called from main to inject the embedded FS.
func SetStaticFS(efs embed.FS) {
	staticFS = efs
}

// staticHandler returns an http.Handler that serves files from the embedded static/ subtree.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static sub fs: %v", err)
	}
	return http.FileServer(http.FS(sub))
}

// Run starts the HTTP server with the given configuration.
func Run(cfg *config.Config, templateFS embed.FS) error {
	// Build root map: name -> filesystem path
	roots := make(map[string]string, len(cfg.Dirs))
	for _, d := range cfg.Dirs {
		name := rootName(d)
		roots[name] = d
	}

	tmpl, err := LoadTemplates(templateFS)
	if err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}

	bwManager := handlers.NewBandwidthManager(cfg.BandwidthLimit)

	mux := http.NewServeMux()
	registerRoutes(mux, roots, cfg.Theme, cfg.Title, cfg.FaviconPath, cfg.DefaultTheme, bwManager, tmpl)

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	logStartup(cfg, roots, addr)

	// Warm the directory-size and search-index caches in the background so
	// that the first real page load is never a cold cache miss.
	handlers.WarmCache(roots)

	// Watch all managed directories for filesystem changes and invalidate
	// only the affected cache entries when they occur.
	if _, err := handlers.StartWatcher(roots); err != nil {
		log.Printf("watcher: could not start filesystem watcher: %v", err)
	}

	return http.ListenAndServe(addr, mux)
}

// logStartup prints a structured summary of the active configuration.
func logStartup(cfg *config.Config, roots map[string]string, addr string) {
	sep := "-------------------------------------------"
	log.Println(sep)
	log.Printf("  %s", cfg.Title)
	log.Println(sep)
	log.Printf("  %-18s %s", "Address:", "http://"+addr)
	log.Printf("  %-18s %d", "Port:", cfg.Port)
	log.Printf("  %-18s %s", "Highlight theme:", cfg.Theme)
	log.Printf("  %-18s %s", "Default UI theme:", cfg.DefaultTheme)

	if cfg.FaviconPath != "" {
		log.Printf("  %-18s %s", "Favicon:", cfg.FaviconPath)
	} else {
		log.Printf("  %-18s %s", "Favicon:", "(embedded default)")
	}

	if cfg.BandwidthLimit > 0 {
		log.Printf("  %-18s %s/s", "Bandwidth limit:", formatBandwidth(cfg.BandwidthLimit))
	} else {
		log.Printf("  %-18s %s", "Bandwidth limit:", "unlimited")
	}

	log.Printf("  %-18s %d director%s", "Serving:", len(roots), map[bool]string{true: "y", false: "ies"}[len(roots) == 1])
	for name, fsPath := range roots {
		log.Printf("    /%-16s %s", name, fsPath)
	}
	log.Println(sep)
}

// formatBandwidth converts a bytes/sec value to a human-readable bits/sec string.
func formatBandwidth(bps float64) string {
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
