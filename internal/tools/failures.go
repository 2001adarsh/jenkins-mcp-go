package tools

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ansiRe        = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)
	ginkgoSummary = regexp.MustCompile(`Summarizing (\d+) Failure`)
	// Failure header decoration. Ginkgo emits the failing node type in
	// brackets; most common is `[It]` but setup/teardown nodes also appear:
	// `[JustBeforeEach]`, `[BeforeEach]`, `[AfterEach]`, `[BeforeAll]`,
	// `[AfterAll]`, `[BeforeSuite]`, etc.
	ginkgoFailLine = regexp.MustCompile(`\[FAIL\]\s*\{([^}]+)\}\s*\[([A-Za-z]+)\]\s*(.+?)\s*\[([^\]]+)\]\s*$`)
	ginkgoFileRef  = regexp.MustCompile(`(/[^\s:]+:\d+)`)
)

// StripANSI removes ECMA-48 SGR/erase-line escape sequences from s.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// GinkgoFailure is one entry parsed out of a Ginkgo `Summarizing N Failure`
// block.
type GinkgoFailure struct {
	Spec        string
	NodeType    string // "It", "JustBeforeEach", "BeforeAll", etc.
	Desc        string
	Tags        string
	FileRef     string
	SummaryLine int // 1-indexed
}

// ParseGinkgoFailures walks the Summarizing block forward from its start and
// returns the structured failure list. summaryLine is 1-indexed; it is -1 when
// no summary was found.
func ParseGinkgoFailures(lines []string) (total int, failures []GinkgoFailure, summaryLine int) {
	summaryLine = -1
	for i := len(lines) - 1; i >= 0; i-- {
		if m := ginkgoSummary.FindStringSubmatch(lines[i]); m != nil {
			total, _ = strconv.Atoi(m[1])
			summaryLine = i
			break
		}
	}
	if summaryLine == -1 {
		return 0, nil, -1
	}
	var cur *GinkgoFailure
	flush := func() {
		if cur != nil {
			failures = append(failures, *cur)
			cur = nil
		}
	}
	for i := summaryLine + 1; i < len(lines); i++ {
		ln := lines[i]
		if strings.Contains(ln, "Ran ") && strings.Contains(ln, " Specs") {
			break
		}
		if m := ginkgoFailLine.FindStringSubmatch(ln); m != nil {
			flush()
			cur = &GinkgoFailure{
				Spec:        m[1],
				NodeType:    m[2],
				Desc:        m[3],
				Tags:        m[4],
				SummaryLine: i + 1,
			}
		} else if cur != nil && cur.FileRef == "" {
			if fm := ginkgoFileRef.FindStringSubmatch(ln); fm != nil {
				cur.FileRef = fm[1]
			}
		}
	}
	flush()
	return total, failures, summaryLine + 1
}

// FindFirstErrorForSpec returns the 1-indexed line number and the snippet
// (±ctxLines) of the first line containing both `[ERROR]` and `{specName}`.
// Returns (0, "") if no such line exists.
func FindFirstErrorForSpec(cleanLines []string, specName string, ctxLines int) (int, string) {
	needle := "{" + specName + "}"
	for i, ln := range cleanLines {
		if !strings.Contains(ln, "[ERROR]") || !strings.Contains(ln, needle) {
			continue
		}
		start := i - ctxLines
		if start < 0 {
			start = 0
		}
		end := i + ctxLines + 1
		if end > len(cleanLines) {
			end = len(cleanLines)
		}
		var b strings.Builder
		for n := start; n < end; n++ {
			marker := "  "
			if n == i {
				marker = "→ "
			}
			fmt.Fprintf(&b, "%s%d: %s\n", marker, n+1, cleanLines[n])
		}
		return i + 1, b.String()
	}
	return 0, ""
}

// GetFailureSummaryInput is the schema for get_ginkgo_failure_summary.
type GetFailureSummaryInput struct {
	JobPath      string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber  int64  `json:"build_number" jsonschema:"Build number. Use 0 or omit for the latest build."`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"Lines of context around each spec's first [ERROR] line (default 20)"`
}

// GetFailureSummary parses a Ginkgo `Summarizing N Failure` block and for each
// failing spec returns the first [ERROR] line tagged with that spec name plus
// surrounding context.
func (d Deps) GetFailureSummary(ctx context.Context, _ *mcp.CallToolRequest, in GetFailureSummaryInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	ctxLines := in.ContextLines
	if ctxLines == 0 {
		ctxLines = 20
	}
	body, cachePath, err := d.Cache.Fetch(ctx, in.JobPath, in.BuildNumber)
	if err != nil {
		return nil, nil, err
	}
	rawLines := strings.Split(string(body), "\n")
	cleanLines := make([]string, len(rawLines))
	for i, ln := range rawLines {
		cleanLines[i] = StripANSI(ln)
	}
	total, failures, summaryLine := ParseGinkgoFailures(cleanLines)
	if summaryLine == -1 {
		return textResult(
			"No Ginkgo `Summarizing N Failure` block found. " +
				"This may not be a Ginkgo build, or the build was aborted before reporting." +
				pathFooter(cachePath, len(body)),
		), nil, nil
	}
	if len(failures) == 0 {
		return textResult(fmt.Sprintf(
			"Ginkgo summary found at line %d but no [FAIL] entries parsed. The log may use a different format.%s",
			summaryLine, pathFooter(cachePath, len(body)),
		)), nil, nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Ginkgo summary: %d failure(s). Summary block at line %d.\n\n", total, summaryLine)
	for i, f := range failures {
		fmt.Fprintf(&out, "=== Failure %d of %d ===\n", i+1, len(failures))
		fmt.Fprintf(&out, "Spec:        {%s}\n", f.Spec)
		fmt.Fprintf(&out,
			"Node type:   [%s]  (where the failure surfaced — "+
				"[It] = test body, [JustBeforeEach]/[BeforeEach]/[BeforeAll] = setup, "+
				"[AfterEach]/[AfterAll] = teardown)\n",
			f.NodeType)
		fmt.Fprintf(&out, "Description: %s\n", f.Desc)
		fmt.Fprintf(&out, "Tags:        %s\n", f.Tags)
		if f.FileRef != "" {
			fmt.Fprintf(&out, "Ginkgo ref:  %s (often a log helper — see error context below for the real source)\n", f.FileRef)
		}
		fmt.Fprintf(&out, "[FAIL] line: %d\n\n", f.SummaryLine)
		if errLine, snippet := FindFirstErrorForSpec(cleanLines, f.Spec, ctxLines); errLine > 0 {
			fmt.Fprintf(&out, "First [ERROR] in this spec (line %d):\n%s\n", errLine, snippet)
		} else {
			fmt.Fprintf(&out,
				"(no `[ERROR]` line found tagged with {%s} — "+
					"the failure may be a panic / non-error log line; inspect the cached file directly)\n\n",
				f.Spec)
		}
	}
	out.WriteString(pathFooter(cachePath, len(body)))
	return textResult(out.String()), nil, nil
}
