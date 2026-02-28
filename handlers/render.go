package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/niklasfasching/go-org/org"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

// isRenderable reports whether a MIME type has a rich renderer available.
func isRenderable(mimeType string) bool {
	switch baseMIME(mimeType) {
	case "text/markdown", "text/html", "text/x-org":
		return true
	}
	return false
}

// renderContent attempts a rich render for the given content and filename.
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

// renderMarkdown converts Markdown to sanitised HTML using goldmark with
// GitHub-flavoured extensions enabled.
func renderMarkdown(content string) (template.HTML, error) {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,        // tables, strikethrough, linkify, task lists
			extension.Footnote,
			extension.DefinitionList,
			extension.Typographer,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithUnsafe(), // allow raw HTML blocks inside .md files
		),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return "", fmt.Errorf("markdown render: %w", err)
	}
	return template.HTML(buf.String()), nil
}

// renderOrg converts Emacs Org-mode content to HTML using go-org.
func renderOrg(content string) (template.HTML, error) {
	doc := org.New().Parse(strings.NewReader(content), "")
	w := org.NewHTMLWriter()
	out, err := doc.Write(w)
	if err != nil {
		return "", fmt.Errorf("org render: %w", err)
	}
	return template.HTML(out), nil
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
