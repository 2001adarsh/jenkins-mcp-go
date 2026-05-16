package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- get_console_log ----

// GetConsoleLogInput is the schema for get_console_log.
type GetConsoleLogInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path, e.g. 'folder/subfolder/job-name'"`
	BuildNumber int64  `json:"build_number" jsonschema:"Build number. Use 0 or omit for the latest build."`
	TailLines   int    `json:"tail_lines,omitempty" jsonschema:"Lines from the end of the log to return. 0 (default) = last 500. Negative = full log (may be very large)."`
}

// GetConsoleLog returns the build's console output, tailed to TailLines.
func (d Deps) GetConsoleLog(ctx context.Context, _ *mcp.CallToolRequest, in GetConsoleLogInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	tail := in.TailLines
	if tail == 0 {
		tail = 500
	}
	body, cachePath, err := d.Cache.Fetch(ctx, in.JobPath, in.BuildNumber)
	if err != nil {
		return nil, nil, err
	}
	text := string(body)
	if tail > 0 {
		lines := strings.Split(text, "\n")
		if len(lines) > tail {
			text = strings.Join(lines[len(lines)-tail:], "\n")
		}
	}
	return textResult(text + pathFooter(cachePath, len(body))), nil, nil
}

// ---- get_console_log_path ----

// GetConsoleLogPathInput is the schema for get_console_log_path.
type GetConsoleLogPathInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number" jsonschema:"Build number. Must be > 0 — only completed, immutable builds get cached to disk."`
}

// GetConsoleLogPath ensures the full log is cached on disk and returns its path.
func (d Deps) GetConsoleLogPath(ctx context.Context, _ *mcp.CallToolRequest, in GetConsoleLogPathInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" || in.BuildNumber <= 0 {
		return nil, nil, fmt.Errorf("job_path and a positive build_number are required (only completed builds can be cached to disk)")
	}
	body, cachePath, err := d.Cache.Fetch(ctx, in.JobPath, in.BuildNumber)
	if err != nil {
		return nil, nil, err
	}
	if cachePath == "" {
		return textResult(fmt.Sprintf(
			"Build is still running (no `Finished:` marker yet); not cached to disk. In-memory log size: %d bytes.",
			len(body),
		)), nil, nil
	}
	lines := 1 + strings.Count(string(body), "\n")
	return textResult(fmt.Sprintf(
		"Cached console log path: %s\nSize: %d bytes (%d lines)\n"+
			"Use Read/Grep/Bash on this path to inspect the full log.",
		cachePath, len(body), lines,
	)), nil, nil
}

// ---- search_console_log ----

// SearchConsoleLogInput is the schema for search_console_log.
type SearchConsoleLogInput struct {
	JobPath      string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber  int64  `json:"build_number" jsonschema:"Build number. Use 0 or omit for the latest build."`
	Pattern      string `json:"pattern" jsonschema:"Go regexp pattern (RE2 syntax)"`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"Lines of context before and after each match (default 3)"`
	MaxMatches   int    `json:"max_matches,omitempty" jsonschema:"Cap on matches returned (default 50)"`
}

// SearchConsoleLog runs an RE2 regex over the log and returns matches with
// surrounding context lines.
func (d Deps) SearchConsoleLog(ctx context.Context, _ *mcp.CallToolRequest, in SearchConsoleLogInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" || in.Pattern == "" {
		return nil, nil, fmt.Errorf("job_path and pattern are required")
	}
	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid pattern: %w", err)
	}
	ctxLines := in.ContextLines
	if ctxLines == 0 {
		ctxLines = 3
	}
	maxMatches := in.MaxMatches
	if maxMatches == 0 {
		maxMatches = 50
	}

	body, cachePath, err := d.Cache.Fetch(ctx, in.JobPath, in.BuildNumber)
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(string(body), "\n")

	var out strings.Builder
	matches := 0
	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		matches++
		start := i - ctxLines
		if start < 0 {
			start = 0
		}
		end := i + ctxLines + 1
		if end > len(lines) {
			end = len(lines)
		}
		fmt.Fprintf(&out, "── match %d (line %d) ──\n", matches, i+1)
		for n := start; n < end; n++ {
			marker := "  "
			if n == i {
				marker = "→ "
			}
			fmt.Fprintf(&out, "%s%d: %s\n", marker, n+1, lines[n])
		}
		out.WriteString("\n")
		if matches >= maxMatches {
			fmt.Fprintf(&out, "(stopped at max_matches=%d)\n", maxMatches)
			break
		}
	}
	if matches == 0 {
		return textResult(fmt.Sprintf(
			"No matches for /%s/ in this build's console log.%s",
			in.Pattern, pathFooter(cachePath, len(body)),
		)), nil, nil
	}
	return textResult(out.String() + pathFooter(cachePath, len(body))), nil, nil
}
