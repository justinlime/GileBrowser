package server

import (
	"io/fs"
	"log"
	"net/http"

	"gileserver/handlers"
)

// registerRoutes attaches all handlers to the given mux.
func registerRoutes(mux *http.ServeMux, roots map[string]string, theme, title, faviconPath, defaultTheme string, bw *handlers.BandwidthManager, previewOpts handlers.PreviewOptions, tmpl *Templates) {
	// Static assets
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler()))

	// Favicon — pass the static sub-FS so the handler can read the embedded default.
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static sub fs for favicon: %v", err)
	}
	mux.HandleFunc("/favicon.ico", handlers.FaviconHandler(staticSub, faviconPath))

	// Search index (JSON)
	mux.HandleFunc("/api/index", handlers.IndexHandler(roots))

	// ZIP download for directories (bandwidth-limited)
	mux.Handle("/zip/", bw.Wrap(handlers.ZipHandler(roots, title)))

	// File downloads (bandwidth-limited, counted in stats)
	mux.Handle("/download/", bw.Wrap(http.StripPrefix("/download", handlers.FileHandler(roots))))

	// Inline file serving for previews (bandwidth-limited, not counted in stats)
	mux.Handle("/view/", bw.Wrap(http.StripPrefix("/view", handlers.ViewHandler(roots))))

	// Chroma syntax-highlighting stylesheet (generated once at startup)
	mux.HandleFunc("/highlight.css", handlers.HighlightCSSHandler(theme))

	// File previews
	mux.HandleFunc("/preview/", handlers.PreviewHandler(roots, theme, title, defaultTheme, previewOpts, tmpl))

	// Directory / root listing (catch-all)
	mux.HandleFunc("/", routeRoot(roots, title, defaultTheme, tmpl))
}

// routeRoot dispatches between the root listing and subdirectory listings.
func routeRoot(roots map[string]string, title, defaultTheme string, tmpl *Templates) http.HandlerFunc {
	rootHandler := handlers.RootHandler(roots, title, defaultTheme, tmpl)
	dirHandler := handlers.DirHandler(roots, title, defaultTheme, tmpl)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "" {
			rootHandler(w, r)
			return
		}
		dirHandler(w, r)
	}
}
