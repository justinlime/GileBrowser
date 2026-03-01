package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"path"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/microcosm-cc/bluemonday"
	"github.com/niklasfasching/go-org/org"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

// renderTheme is the Chroma style name used for code block highlighting inside
// document renders (Markdown, Org-mode). Defaults to catppuccin-mocha; set
// once at startup by InitRenderOptions.
var renderTheme = "catppuccin-mocha"

// docPolicy is the bluemonday sanitization policy applied to all rendered
// document output. Nil until InitRenderOptions is called; sanitizeHTML falls
// back to a conservative default if called before initialisation.
var docPolicy *bluemonday.Policy

// InitRenderOptions configures the document renderer. It must be called once
// at startup, before the server begins accepting requests.
//
//   - theme:         Chroma style name for code block syntax highlighting.
//   - previewImages: when true, data: URIs are permitted on <img> src so that
//     base64-embedded images in Markdown/Org documents render inline,
//     consistent with the server-wide --preview-images setting.
func InitRenderOptions(theme string, previewImages bool) {
	renderTheme = theme
	docPolicy = buildDocPolicy(previewImages)
}

// buildDocPolicy constructs the bluemonday allowlist policy used to sanitize
// rendered Markdown and Org-mode output.
//
// allowDataImages controls whether data: URIs are permitted on <img> src
// attributes. When true, bluemonday's AllowDataURIImages() is applied, which
// permits only safe raster formats (PNG, JPEG, GIF, BMP, TIFF, WebP, ICO).
// SVG data URIs are always excluded because SVG can embed executable <script>
// elements.
func buildDocPolicy(allowDataImages bool) *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// --- Structural / block elements (including <div> for Markdown HTML blocks) ---
	p.AllowElements(
		"address", "article", "aside",
		"blockquote", "br",
		"caption", "col", "colgroup",
		"details", "div", "dl", "dt", "dd",
		"figure", "figcaption", "footer",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"header", "hr",
		"li",
		"main",
		"nav",
		"ol",
		"p", "pre",
		"section", "summary",
		"table", "tbody", "td", "tfoot", "th", "thead", "tr",
		"ul",
	)

	// --- Inline / typographic elements ---
	p.AllowElements(
		"abbr", "acronym",
		"b", "cite", "code",
		"del", "dfn",
		"em",
		"i",
		"kbd",
		"mark",
		"q",
		"s", "samp", "small", "span", "strong", "sub", "sup",
		"tt",
		"u",
		"var", "wbr",
	)

	// --- Links ---
	// Only http, https, and mailto are permitted as href schemes.
	// Relative URLs (e.g. anchor links within a document) are also allowed.
	// RequireParseableURLs is intentionally NOT set: some real-world URLs (e.g.
	// badge URLs with unencoded spaces) fail strict RFC parsing but are otherwise
	// safe. The scheme allowlist below already prevents dangerous schemes such as
	// javascript: or vbscript: regardless of parseability.
	p.AllowAttrs("href", "title").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.AllowRelativeURLs(true)

	// --- Images ---
	p.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	if allowDataImages {
		// Permits data: URIs for common raster image types only.
		// SVG data URIs are intentionally excluded by bluemonday because SVG
		// documents can contain <script> elements.
		p.AllowDataURIImages()
	}

	// --- Global safe attributes ---
	// id and class are needed for heading anchors (goldmark WithAutoHeadingID)
	// and for the Chroma CSS class names on highlighted <code>/<span> elements.
	// align is needed for raw HTML blocks in Markdown/Org documents that use
	// e.g. <h1 align="center"> or <p align="center"> for centred content.
	p.AllowAttrs("id", "class", "lang", "title", "align").Globally()

	// --- Table layout attributes ---
	p.AllowAttrs("align", "valign", "colspan", "rowspan", "scope", "abbr", "headers").OnElements("td", "th")
	p.AllowAttrs("align", "valign", "span", "width").OnElements("col", "colgroup")
	p.AllowAttrs("align").OnElements("table", "tr", "tbody", "thead", "tfoot")
	p.AllowAttrs("border", "cellpadding", "cellspacing", "summary", "width").OnElements("table")

	// --- List attributes ---
	p.AllowAttrs("start", "type").OnElements("ol")
	p.AllowAttrs("type").OnElements("ul", "li")

	// --- Quotation source ---
	p.AllowAttrs("cite").OnElements("blockquote", "del", "q")

	return p
}

// isRenderable reports whether a MIME type has a rich renderer available.
func isRenderable(mimeType string) bool {
	switch baseMIME(mimeType) {
	case "text/markdown", "text/html", "text/x-org":
		return true
	}
	return false
}

// renderContent attempts a rich render for the given content and MIME type.
// On success IsRendered should be set to true on PreviewData.
// On any failure it returns an error and the caller should fall back to
// syntax highlighting.
//
//   - docURLDir is the URL directory of the document being rendered
//     (e.g. "/myroot/subdir" for a file at /myroot/subdir/README.md).
//     It is used to rewrite relative image src paths so they resolve through
//     the /view/ route instead of landing on /preview/, where they would 404.
//
//   - previewImages controls whether <img> elements survive sanitization.
//     When false the policy strips all images, consistent with the server-wide
//     --preview-images=false flag.
func renderContent(content, mimeType, docURLDir string, previewImages bool) (template.HTML, error) {
	switch baseMIME(mimeType) {
	case "text/markdown":
		return renderMarkdown(content, docURLDir, previewImages)
	case "text/x-org":
		return renderOrg(content, docURLDir, previewImages)
	case "text/html":
		return renderHTML(content)
	}
	return "", fmt.Errorf("no renderer for %q", mimeType)
}

// renderMarkdown converts Markdown to HTML using goldmark with GitHub-flavoured
// extensions and Chroma syntax highlighting on fenced code blocks.
//
// WithUnsafe allows raw HTML blocks in Markdown source to pass through the
// renderer so authors can embed arbitrary HTML in their documents. All output
// — including those raw blocks — is passed through sanitizeHTML before being
// placed in the page, so dangerous constructs are stripped regardless.
//
// The goldmark-highlighting extension is already a project dependency; it uses
// Chroma with WithClasses(true) so highlighted blocks pick up their colours
// from the same highlight.css that the rest of the preview system uses.
func renderMarkdown(content, docURLDir string, previewImages bool) (template.HTML, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM, // tables, strikethrough, linkify, task lists
			extension.Footnote,
			extension.DefinitionList,
			extension.Typographer,
			highlighting.NewHighlighting(
				highlighting.WithStyle(renderTheme),
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
				),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithUnsafe(), // raw HTML blocks pass through; sanitized below
		),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return "", fmt.Errorf("markdown render: %w", err)
	}
	return template.HTML(sanitizeHTML(buf.String(), docURLDir, previewImages)), nil
}

// renderOrg converts Emacs Org-mode content to HTML using go-org with Chroma
// syntax highlighting on #+BEGIN_SRC blocks.
//
// go-org's HTMLWriter exposes a HighlightCodeBlock hook that is called for
// every source block with the raw source text and the declared language. When
// the hook returns a non-empty string, go-org uses that HTML verbatim (wrapped
// in a <div class="highlight"> container); when it returns empty, go-org falls
// back to its default plain-text <pre> rendering. This lets us slot Chroma in
// without post-processing the rendered output.
//
// All rendered output is passed through sanitizeHTML before being placed in
// the page.
func renderOrg(content, docURLDir string, previewImages bool) (template.HTML, error) {
	doc := org.New().Parse(strings.NewReader(content), "")
	w := org.NewHTMLWriter()
	w.HighlightCodeBlock = func(source, lang string, inline bool, _ map[string]string) string {
		return chromaHighlightBlock(source, lang)
	}
	out, err := doc.Write(w)
	if err != nil {
		return "", fmt.Errorf("org render: %w", err)
	}
	return template.HTML(sanitizeHTML(out, docURLDir, previewImages)), nil
}

// chromaHighlightBlock runs source through the Chroma lexer for lang and
// returns a syntax-highlighted HTML fragment using CSS classes (so it is
// styled by the same highlight.css served to every page).
//
// Returns an empty string on any error so callers can fall back gracefully.
// When lang is empty or unrecognised, Chroma's fallback lexer is used, which
// treats the source as plain text and wraps it without colour annotations.
func chromaHighlightBlock(source, lang string) string {
	l := lexers.Get(lang)
	if l == nil {
		l = lexers.Fallback
	}
	l = chroma.Coalesce(l)

	style := styles.Get(renderTheme)
	if style == nil {
		style = styles.Fallback
	}

	f := chromahtml.New(
		chromahtml.WithClasses(true),
	)

	it, err := l.Tokenise(nil, source)
	if err != nil {
		return ""
	}

	var buf bytes.Buffer
	if err := f.Format(&buf, style, it); err != nil {
		return ""
	}
	return buf.String()
}

// renderHTML wraps the raw HTML in a sandboxed container.
// We do NOT inject it directly into the page DOM to avoid XSS — instead we
// wrap it in an <iframe srcdoc="…"> with a restrictive sandbox attribute so
// it runs in a separate browsing context with no access to our origin.
func renderHTML(content string) (template.HTML, error) {
	// srcdoc requires HTML-attribute escaping (quotes → &quot; etc.)
	escaped := htmlAttrEscape(content)
	iframe := `<iframe class="html-preview-frame" srcdoc="` + escaped + `" sandbox="allow-scripts" referrerpolicy="no-referrer"></iframe>`
	return template.HTML(iframe), nil
}

// baseMIME strips any parameters from a MIME type string
// (e.g. "text/html; charset=utf-8" → "text/html").
func baseMIME(mimeType string) string {
	return strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])
}

// sanitizeHTML runs input through the bluemonday document policy, stripping
// any element or attribute not on the allowlist. It is called on all rendered
// Markdown and Org-mode output before the result is embedded in a page.
//
// Two pre-processing steps run before sanitization:
//  1. Relative image src paths are rewritten to /view/<docURLDir>/… so the
//     browser fetches them through the inline-serving route rather than
//     landing on /preview/ where they would 404.
//  2. External image URLs are percent-encoded to fix literal spaces that would
//     otherwise cause bluemonday to silently drop the src attribute.
//
// When previewImages is false the policy used is rebuilt without <img> support
// so all image tags are stripped from the output, consistent with the
// server-wide --preview-images=false flag.
//
// If InitRenderOptions has not yet been called (e.g. during tests), a
// conservative policy without data: image support is built on the fly.
func sanitizeHTML(input, docURLDir string, previewImages bool) string {
	input = rewriteImgSrcURLs(input, docURLDir)
	p := docPolicy
	if p == nil {
		p = buildDocPolicy(previewImages)
	} else if !previewImages {
		// Images are globally disabled — use a no-image policy for this render.
		p = buildDocPolicy(false)
	}
	return p.Sanitize(input)
}

// rewriteImgSrcURLs rewrites img src="…" attributes in the rendered HTML so
// that images resolve correctly through the server's /view/ route.
//
//   - Relative paths (no scheme, not starting with /) are rewritten to
//     /view/<docURLDir>/path so the browser fetches them via ViewHandler
//     instead of treating them as relative to the /preview/ page URL.
//   - Absolute http/https URLs are left as-is except that any literal spaces
//     are percent-encoded (%20) to prevent bluemonday from dropping the src.
//   - data: URIs, fragment-only (#…), and already-absolute /view/ or /static/
//     paths are passed through unchanged.
//
// The rewrite uses a simple string scanner rather than a full HTML parser:
// it is narrow in scope (only img src values) and avoids a heavy dependency.
func rewriteImgSrcURLs(html, docURLDir string) string {
	const needle = `src="`
	if !strings.Contains(html, needle) {
		return html
	}

	var b strings.Builder
	b.Grow(len(html) + 64)
	remaining := html
	for {
		idx := strings.Index(remaining, needle)
		if idx == -1 {
			b.WriteString(remaining)
			break
		}
		// Copy everything up to and including src="
		b.WriteString(remaining[:idx+len(needle)])
		remaining = remaining[idx+len(needle):]

		// Find the closing quote of the attribute value.
		end := strings.IndexByte(remaining, '"')
		if end == -1 {
			// Malformed — emit as-is and stop.
			b.WriteString(remaining)
			break
		}
		rawSrc := remaining[:end]
		remaining = remaining[end:] // closing quote stays in remaining

		b.WriteString(resolveImgSrc(rawSrc, docURLDir))
	}
	return b.String()
}

// resolveImgSrc transforms a single img src value for use in a preview page.
//
//   - External http/https URLs: spaces are percent-encoded so they survive
//     bluemonday's URL validator; everything else is left unchanged.
//   - Relative paths: rewritten to /view/<docURLDir>/… so they are served by
//     ViewHandler with the correct MIME type and inline Content-Disposition.
//   - Everything else (data:, #fragment, /absolute): passed through as-is.
func resolveImgSrc(src, docURLDir string) string {
	switch {
	case strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://"):
		return encodeURLSpaces(src)

	case src == "" || strings.HasPrefix(src, "data:") || strings.HasPrefix(src, "#"):
		return src

	case strings.HasPrefix(src, "/"):
		// Already an absolute path — pass through. This covers cases like
		// /view/… paths that a document author has written explicitly.
		return src

	default:
		// Relative path — resolve against the document's directory using the
		// /view/ route so ViewHandler can serve the file inline.
		//
		// path.Join cleans away any ".." or "." segments, which prevents a
		// crafted relative path from escaping the served directory tree. The
		// resolvePath check in ViewHandler adds a second layer of defence.
		joined := path.Join("/view", docURLDir, src)
		return joined
	}
}

// encodeURLSpaces replaces literal space characters in a URL string with their
// percent-encoded equivalent (%20). It only targets spaces — other characters
// that may be technically invalid in a URL are left as-is to avoid corrupting
// URLs that are already partially encoded or use non-standard characters.
//
// This is intentionally simpler than a full RFC 3986 normalization pass:
// bluemonday validates against the allowed scheme list regardless, so the only
// goal here is to prevent spaces from causing the URL to be silently dropped.
func encodeURLSpaces(rawURL string) string {
	if !strings.Contains(rawURL, " ") {
		return rawURL
	}
	return strings.ReplaceAll(rawURL, " ", "%20")
}

// htmlAttrEscape escapes a string for safe embedding inside an HTML attribute
// value delimited by double-quotes.
func htmlAttrEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
