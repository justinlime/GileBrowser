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
//     Chroma injects as inline style attributes. The 'frame-src' directive is
//     set to 'self' so the HTML-preview iframe (srcdoc) can render; external
//     iframes are blocked. When previewImages is true, img-src is widened to
//     include https: so that external images embedded in Markdown/Org documents
//     (badges, screenshots, etc.) are allowed to load.  When false, only
//     same-origin and data: URIs are permitted, which matches the policy that
//     the sanitizer enforces in rendered document HTML.
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
		rtc := handlers.GetRuntimeConfig()
		imgSrc := "img-src 'self' data:;"
		if rtc.PreviewImages {
			imgSrc = "img-src 'self' data: https:;"
		}
		csp := "default-src 'self'; " +
			"script-src 'self' 'unsafe-inline'; " +
			"style-src 'self' 'unsafe-inline'; " +
			imgSrc + " " +
			"font-src 'self'; " +
			"frame-src 'self'; " +
			"object-src 'none';"

		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

// registerRoutes attaches all handlers to the given mux and wraps the entire
// mux in the security-headers middleware so every response carries the
// defensive headers regardless of which route matched.
func registerRoutes(mux *http.ServeMux, theme, title, faviconPath, defaultTheme string, bw *handlers.BandwidthManager, tmpl *Templates) {
	dataDir := handlers.GetDataDir()

	// Static assets
	mux.Handle("/static/", http.StripPrefix("/static/", staticHandler()))

	// Favicon — reads custom path from runtime config on each request
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static sub fs for favicon: %v", err)
	}
	mux.HandleFunc("/favicon.ico", handlers.FaviconHandler(staticSub))

	// Favicon upload (POST) and delete (DELETE)
	mux.HandleFunc("/settings/favicon/upload", handlers.FaviconUploadHandler(dataDir))
	mux.HandleFunc("/settings/favicon/delete", handlers.FaviconDeleteHandler())

	// Directory management API
	mux.HandleFunc("/api/dirs", handlers.ListDirsHandler())
	mux.HandleFunc("/api/dirs/add", handlers.AddDirHandler())
	mux.HandleFunc("/api/dirs/remove", handlers.RemoveDirHandler())

	// Search index (JSON) - reads roots dynamically
	mux.HandleFunc("/api/index", handlers.IndexHandler())

	// ZIP download for directories (bandwidth-limited) - reads roots dynamically
	mux.Handle("/zip/", bw.Wrap(handlers.ZipHandler(title)))

	// File downloads (bandwidth-limited, counted in stats) - uses dynamic resolution
	mux.Handle("/download/", bw.Wrap(http.StripPrefix("/download", handlers.FileHandler())))

	// Inline file serving for previews (bandwidth-limited, not counted in stats) - uses dynamic resolution
	mux.Handle("/view/", bw.Wrap(http.StripPrefix("/view", handlers.ViewHandler())))

	// Chroma syntax-highlighting stylesheet (read from runtime config)
	mux.HandleFunc("/highlight.css", handlers.HighlightCSSHandler())

	// File previews - reads roots dynamically
	mux.HandleFunc("/preview/", handlers.PreviewHandler(title, defaultTheme, tmpl))

	// Settings page
	mux.HandleFunc("/settings", handlers.SettingsHandler(tmpl))

	// Directory / root listing (catch-all) - reads roots dynamically
	mux.HandleFunc("/", routeRoot(title, defaultTheme, tmpl))
}

// routeRoot dispatches between the root listing and subdirectory listings.
func routeRoot(title, defaultTheme string, tmpl *Templates) http.HandlerFunc {
	rootHandler := handlers.RootHandler(title, defaultTheme, tmpl)
	dirHandler := handlers.DirHandler(title, defaultTheme, tmpl)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "" {
			rootHandler(w, r)
			return
		}
		dirHandler(w, r)
	}
}
