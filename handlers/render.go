package handlers

import (
	"bytes"
	"fmt"
	"html/template"
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
	p.AllowAttrs("href", "title").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.AllowRelativeURLs(true)
	p.RequireParseableURLs(true)

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
	p.AllowAttrs("id", "class", "lang", "title").Globally()

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
func renderContent(content, mimeType string) (template.HTML, error) {
	switch baseMIME(mimeType) {
	case "text/markdown":
		return renderMarkdown(content)
	case "text/x-org":
		return renderOrg(content)
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
func renderMarkdown(content string) (template.HTML, error) {
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
	return template.HTML(sanitizeHTML(buf.String())), nil
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
func renderOrg(content string) (template.HTML, error) {
	doc := org.New().Parse(strings.NewReader(content), "")
	w := org.NewHTMLWriter()
	w.HighlightCodeBlock = func(source, lang string, inline bool, _ map[string]string) string {
		return chromaHighlightBlock(source, lang)
	}
	out, err := doc.Write(w)
	if err != nil {
		return "", fmt.Errorf("org render: %w", err)
	}
	return template.HTML(sanitizeHTML(out)), nil
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
// If InitRenderOptions has not yet been called (e.g. during tests), a
// conservative policy without data: image support is built on the fly.
func sanitizeHTML(input string) string {
	if docPolicy == nil {
		return buildDocPolicy(false).Sanitize(input)
	}
	return docPolicy.Sanitize(input)
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
