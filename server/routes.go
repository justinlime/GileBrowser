package server

import (
	"io/fs"
	"log"
	"net/http"

	"gileserver/handlers"
)

// securityHeaders wraps h and sets defensive HTTP response headers on every
// reply, regardless of which handler produced it.
//
//   - Content-Security-Policy: restricts resource loading to the server's own
//     origin. 'unsafe-inline' is required for the syntax-highlight CSS that
//     Chroma injects as inline style attributes, and for the theme-toggle
//     script that reads/writes localStorage on first paint to avoid flash.
//     The 'frame-src' directive is set to 'self' so the HTML-preview iframe
//     (srcdoc) can render; external iframes are blocked.
//
//   - X-Content-Type-Options: tells browsers not to MIME-sniff response bodies.
//     Without this a browser might execute a file whose declared Content-Type
//     is benign (e.g. text/plain) if its content looks like JavaScript.
//
//   - X-Frame-Options: prevents the UI from being embedded in a third-party
//     frame, blocking clickjacking attacks that overlay invisible buttons over
//     the file browser to trick users into unintended downloads.
//
//   - Referrer-Policy: suppresses the Referer header on outbound navigations
//     so internal file paths are not leaked to external sites linked from
//     previewed documents.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self'; "+
				"frame-src 'self'; "+
				"object-src 'none';")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

// registerRoutes attaches all handlers to the given mux and wraps the entire
// mux in the security-headers middleware so every response carries the
// defensive headers regardless of which route matched.
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
