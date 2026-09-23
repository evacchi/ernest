package ui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

const (
	chromaFormatter = "terminal256"
	chromaDark      = "monokai"
	chromaLight     = "github"
)

// highlighter colors code with chroma.
type highlighter struct {
	style *chroma.Style
	fmt   chroma.Formatter
}

func newHighlighter(dark bool) *highlighter {
	name := chromaLight
	if dark {
		name = chromaDark
	}

	// Drop backgrounds: blocks must blend with the terminal.
	style, err := styles.Get(name).Builder().Transform(func(e chroma.StyleEntry) chroma.StyleEntry {
		e.Background = 0
		return e
	}).Build()
	if err != nil {
		style = styles.Fallback
	}
	return &highlighter{style: style, fmt: formatters.Get(chromaFormatter)}
}

// code highlights src, picking the lexer by language name, then file name.
// Unknown languages come back unchanged.
func (h *highlighter) code(src, filename, lang string) string {
	lexer := lexers.Get(lang)
	if lexer == nil && filename != "" {
		lexer = lexers.Match(filename)
	}
	if lexer == nil {
		return src
	}

	it, err := chroma.Coalesce(lexer).Tokenise(nil, src)
	if err != nil {
		return src
	}

	var b strings.Builder
	if err := h.fmt.Format(&b, h.style, it); err != nil {
		return src
	}
	return strings.TrimRight(b.String(), "\n")
}
