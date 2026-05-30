package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

const (
	defaultFindTestMaxResults = 50
	maxFindTestMaxResults     = 200
	findTestJobPathWidth      = 50
	findTestTestNameWidth     = 60
)

// findTestPerJobTimeout caps how long a single job's two-call probe
// (lastCompletedBuild metadata + testReport) can take before the job is
// skipped and counted as timed-out. Var (not const) so tests can shrink
// it to keep wall-clock under control.
var findTestPerJobTimeout = 5 * time.Second

// findTestReportTree limits each testReport payload to identity only.
// Stack traces, durations, counts not needed for the locator.
const findTestReportTree = "suites[cases[className,name]]"

// FindTestByNameInput is the schema for find_test_by_name.
type FindTestByNameInput struct {
	Substring  string `json:"substring" jsonschema:"Case-insensitive substring matched against fully-qualified test names (className.name)."`
	FolderPath string `json:"folder_path,omitempty" jsonschema:"Scope the search to a folder. Empty = Jenkins root."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"Cap on the number of hits. Default 50, max 200."`
}

// findTestMatch is one hit row in the rendered table.
type findTestMatch struct {
	JobPath     string
	TestName    string
	BuildNumber int64
	BuildResult string
}

// findTestJobResult is the per-job outcome of the probe.
type findTestJobResult struct {
	JobPath          string
	Matches          []findTestMatch
	NoCompletedBuild bool
	NoTestReport     bool
	TimedOut         bool
	Err              error
}

// FindTestByName locates which job runs a test whose name matches a
// substring. Walks the job tree under folder_path, then fans out per-job
// probes (lastCompletedBuild + testReport) against each leaf with a
// per-job timeout so one stuck job doesn't block the whole search.
func (d Deps) FindTestByName(ctx context.Context, _ *mcp.CallToolRequest, in FindTestByNameInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Substring) == "" {
		return nil, nil, fmt.Errorf("substring is required")
	}
	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = defaultFindTestMaxResults
	}
	if maxResults > maxFindTestMaxResults {
		maxResults = maxFindTestMaxResults
	}
	needle := strings.ToLower(in.Substring)

	var entries []listingEntry
	hitCap := false
	if err := d.walkFolder(ctx, in.FolderPath, true, nil, &entries, &hitCap); err != nil {
		return nil, nil, err
	}
	leaves := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsFolder {
			leaves = append(leaves, e.JobPath)
		}
	}

	perJobTimeout := findTestPerJobTimeout
	results := fetchPerItem(leaves, func(job string) findTestJobResult {
		jobCtx, cancel := context.WithTimeout(ctx, perJobTimeout)
		defer cancel()
		return d.probeOneJobForTest(jobCtx, job, needle)
	})

	matches, noBuild, noReport, timeouts, hardErr := summarizeFindTest(results)
	if hardErr != nil {
		return nil, nil, hardErr
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].JobPath != matches[j].JobPath {
			return matches[i].JobPath < matches[j].JobPath
		}
		return matches[i].TestName < matches[j].TestName
	})
	truncated := false
	if len(matches) > maxResults {
		matches = matches[:maxResults]
		truncated = true
	}

	return textResult(renderFindTest(in.Substring, in.FolderPath, matches, len(leaves), noBuild, noReport, timeouts, truncated, maxResults)), nil, nil
}

func (d Deps) probeOneJobForTest(ctx context.Context, jobPath, needle string) findTestJobResult {
	res := findTestJobResult{JobPath: jobPath}
	apiPath := jenkins.JobAPIPath(jobPath) + "/lastCompletedBuild/api/json"
	body, err := d.Client.Get(ctx, apiPath, map[string]string{"tree": "number,result"})
	if err != nil {
		switch {
		case jenkins.IsHTTPStatus(err, http.StatusNotFound):
			res.NoCompletedBuild = true
			return res
		case errors.Is(err, context.DeadlineExceeded):
			res.TimedOut = true
			return res
		default:
			res.Err = fmt.Errorf("last completed for %s: %w", jobPath, err)
			return res
		}
	}
	var meta struct {
		Number int64  `json:"number"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		res.Err = fmt.Errorf("parse last completed for %s: %w", jobPath, err)
		return res
	}

	reportPath := jenkins.JobAPIPath(jobPath) + "/lastCompletedBuild/testReport/api/json"
	rbody, err := d.Client.Get(ctx, reportPath, map[string]string{"tree": findTestReportTree})
	if err != nil {
		switch {
		case jenkins.IsHTTPStatus(err, http.StatusNotFound):
			res.NoTestReport = true
			return res
		case errors.Is(err, context.DeadlineExceeded):
			res.TimedOut = true
			return res
		default:
			res.Err = fmt.Errorf("test report for %s: %w", jobPath, err)
			return res
		}
	}
	var rep junitReport
	if err := json.Unmarshal(rbody, &rep); err != nil {
		res.Err = fmt.Errorf("parse test report for %s: %w", jobPath, err)
		return res
	}
	for _, suite := range rep.Suites {
		for _, c := range suite.Cases {
			full := c.ClassName + "." + c.Name
			if strings.Contains(strings.ToLower(full), needle) {
				res.Matches = append(res.Matches, findTestMatch{
					JobPath: jobPath, TestName: full, BuildNumber: meta.Number, BuildResult: meta.Result,
				})
			}
		}
	}
	return res
}

func summarizeFindTest(results []findTestJobResult) ([]findTestMatch, int, int, int, error) {
	var matches []findTestMatch
	var noBuild, noReport, timeouts int32
	for _, r := range results {
		if r.Err != nil {
			return nil, 0, 0, 0, r.Err
		}
		matches = append(matches, r.Matches...)
		if r.NoCompletedBuild {
			atomic.AddInt32(&noBuild, 1)
		}
		if r.NoTestReport {
			atomic.AddInt32(&noReport, 1)
		}
		if r.TimedOut {
			atomic.AddInt32(&timeouts, 1)
		}
	}
	return matches, int(noBuild), int(noReport), int(timeouts), nil
}

func renderFindTest(substr, folderPath string, matches []findTestMatch, jobsInspected, noBuild, noReport, timeouts int, truncated bool, maxResults int) string {
	var out strings.Builder
	scope := "(root, recursive)"
	if folderPath != "" {
		scope = fmt.Sprintf("(%s, recursive)", folderPath)
	}
	fmt.Fprintf(&out, "Tests matching %q under %q %s:\n\n", substr, folderPath, scope)

	if len(matches) == 0 {
		out.WriteString("  (no matches)\n")
	} else {
		fmt.Fprintf(&out, "  %s  %s  %-15s  %s\n",
			padRight("job_path", findTestJobPathWidth),
			padRight("test full name", findTestTestNameWidth),
			"last_seen_build",
			"result")
		fmt.Fprintf(&out, "  %s  %s  %-15s  %s\n",
			strings.Repeat("-", findTestJobPathWidth),
			strings.Repeat("-", findTestTestNameWidth),
			strings.Repeat("-", 15),
			strings.Repeat("-", 7))
		for _, m := range matches {
			fmt.Fprintf(&out, "  %s  %s  %-15s  %s\n",
				padRight(truncate(m.JobPath, findTestJobPathWidth), findTestJobPathWidth),
				padRight(truncate(m.TestName, findTestTestNameWidth), findTestTestNameWidth),
				"#"+fmt.Sprintf("%d", m.BuildNumber),
				m.BuildResult)
		}
	}

	if truncated {
		fmt.Fprintf(&out, "\n(stopped at max_results=%d — narrow the substring or pass a deeper folder_path)\n", maxResults)
	}

	// Footer: inspection counts.
	var skips []string
	if noReport > 0 {
		skips = append(skips, fmt.Sprintf("%d skipped: no test report", noReport))
	}
	if noBuild > 0 {
		skips = append(skips, fmt.Sprintf("%d skipped: no completed build", noBuild))
	}
	if timeouts > 0 {
		skips = append(skips, fmt.Sprintf("%d timed out", timeouts))
	}
	out.WriteString("\n")
	if len(skips) == 0 {
		fmt.Fprintf(&out, "Inspected %d jobs.\n", jobsInspected)
	} else {
		fmt.Fprintf(&out, "Inspected %d jobs (%s).\n", jobsInspected, strings.Join(skips, ", "))
	}
	return out.String()
}
