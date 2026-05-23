// Package tools implements the MCP tool handlers exposed by the server.
//
// Each file groups related tools. All handlers share a Deps value carrying
// the Jenkins client and the on-disk console-log cache.
package tools

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// Deps is the shared dependency set passed to every tool handler.
type Deps struct {
	Client   *jenkins.Client
	Cache    *jenkins.ConsoleCache
	Version  string
	ReadOnly bool
}

// textResult wraps a string as an mcp.CallToolResult.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// compileFilter compiles a case-insensitive RE2 regex used by the *_filter
// tool inputs. Returns (nil, nil) when expr is empty so callers can guard
// with a plain nil check. paramName is the JSON field name and is woven
// into the error so the caller sees which filter failed to parse.
func compileFilter(paramName, expr string) (*regexp.Regexp, error) {
	if expr == "" {
		return nil, nil
	}
	re, err := regexp.Compile("(?i)" + expr)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", paramName, err)
	}
	return re, nil
}

// formatBuildDuration renders Jenkins' millisecond duration as a compact
// Go-style string truncated to whole seconds (e.g. "2m15s", "1h2m3s").
func formatBuildDuration(millis int64) string {
	return (time.Duration(millis) * time.Millisecond).Truncate(time.Second).String()
}

// truncate shortens s to at most n runes, replacing the trailing rune with
// "…" when truncation happens. Rune-aware so it never splits a multi-byte
// codepoint and so callers get a predictable display width.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// padRight pads s with spaces on the right so its rune-counted width
// equals width. Use this for tabular columns whose contents may include
// multi-byte runes — Go's %-Ns format pads by byte count, which under-pads
// non-ASCII or the "…" returned by truncate.
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// pathFooter formats the trailing breadcrumb that points callers at the
// on-disk cache. Empty string when the build is not cached.
func pathFooter(cachePath string, totalBytes int) string {
	if cachePath == "" {
		return ""
	}
	return fmt.Sprintf(
		"\n\n— full log cached on disk at: %s (%d bytes)\n"+
			"Use Read/Grep/Bash on this path to inspect the entire log, including content not shown above.",
		cachePath, totalBytes,
	)
}
