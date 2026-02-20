package main

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlightCode returns syntax-highlighted ANSI text for the given code,
// using the filename to determine the language. Falls back to plain text
// if the language can't be detected or highlighting fails.
func highlightCode(code, filename string) string {
	if code == "" {
		return ""
	}

	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("monokai")
	formatter := formatters.Get("terminal256")

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return code
	}

	// Chroma often appends a trailing newline and reset; trim it so the
	// caller controls line splitting cleanly.
	return strings.TrimRight(buf.String(), "\n\r")
}
