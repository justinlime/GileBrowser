package handlers

import (
	"bytes"
	"html/template"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	"gileserver/models"
)

// PreviewHandler serves an inline preview page for any path — directory,
// image, text, or binary/unknown.  All cases are handled here; nothing
// redirects to a download anymore.
func PreviewHandler(roots map[string]string, siteName, defaultTheme string, tmpl interface{ ExecutePreview(http.ResponseWriter, *models.PreviewData) error }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rtc := GetRuntimeConfig()

		urlPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/preview"))

		fsPath, err := resolvePath(roots, urlPath)
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		info, err := os.Stat(fsPath)
		if err != nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		pd := &models.PreviewData{
			Title:        filepath.Base(fsPath),
			SiteName:     siteName,
			DefaultTheme: defaultTheme,
			FilePath:     urlPath,
			FileName:     filepath.Base(fsPath),
			Breadcrumbs:  buildBreadcrumbs(siteName, path.Dir(urlPath)),
			ModTime:      info.ModTime(),
			FileSize:     info.Size(),
		}

		if info.IsDir() {
			pd.IsDir = true
			pd.DownloadURL = "/zip" + urlPath
			// Count direct children.
			if entries, err := os.ReadDir(fsPath); err == nil {
				pd.EntryCount = len(entries)
			}
			// Directory size — served from cache to avoid blocking on a full walk.
			pd.FileSize = cachedDirSize(fsPath)
		} else {
			mime := mimeForFile(fsPath)
			pd.MIMEType = mime
			pd.DownloadURL = "/download" + urlPath
			pd.ViewURL = "/view" + urlPath

			switch {
			case isImage(mime) && rtc.PreviewImages:
				// Inline image preview enabled.
				pd.IsImage = true

			case isText(mime) && rtc.PreviewText:
				// Syntax-highlighted (and optionally rendered) text preview enabled.
				pd.IsText = true
				content, err := readTextFile(fsPath)
				if err != nil {
					http.Error(w, "Could not read file", http.StatusInternalServerError)
					return
				}
				// Always populate the highlighted fallback first.
				highlighted, err := highlightContent(content, filepath.Base(fsPath), rtc.HighlightTheme)
				if err != nil {
					highlighted = template.HTML("<pre class=\"chroma\"><code>" +
						template.HTMLEscapeString(content) + "</code></pre>")
				}
				pd.HighlightedContent = highlighted
				// Attempt a rich render only when document previews are also enabled.
				if rtc.PreviewDocs && isRenderable(mime) {
					docURLDir := path.Dir(urlPath)
					if rendered, err := renderContent(content, mime, docURLDir, rtc.PreviewImages); err == nil {
						pd.RenderedContent = rendered
						pd.IsRendered = true
					}
				}

			default:
				// Either the file type has no preview, or the relevant preview
				// type has been disabled by the admin — show the binary info-card.
				pd.IsBinary = true
			}
		}

		if err := tmpl.ExecutePreview(w, pd); err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
		}
	}
}

// HighlightCSSHandler serves the Chroma CSS stylesheet for the configured theme.
// The theme is read from runtime config on each request, so changes apply immediately.
func HighlightCSSHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rtc := GetRuntimeConfig()
		style := styles.Get(rtc.HighlightTheme)
		if style == nil {
			style = styles.Fallback
		}
		formatter := chromahtml.New(chromahtml.WithClasses(true))
		var buf bytes.Buffer
		if err := formatter.WriteCSS(&buf, style); err != nil {
			buf.Reset()
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(buf.Bytes())
	}
}

// highlightContent runs Chroma over content, using filename for language detection.
func highlightContent(content, filename, theme string) (template.HTML, error) {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}

	style := styles.Get(theme)
	if style == nil {
		style = styles.Fallback
	}

	formatter := chromahtml.New(
		chromahtml.WithClasses(true),
		chromahtml.WithLineNumbers(true),
		chromahtml.LineNumbersInTable(true),
		chromahtml.TabWidth(4),
	)

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "", err
	}

	return template.HTML(buf.String()), nil
}

// readTextFile reads a file and returns its content as a string.
// Reading is limited to 2 MB to avoid memory issues with large files.
func readTextFile(fsPath string) (string, error) {
	const maxBytes = 2 * 1024 * 1024
	f, err := os.Open(fsPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
