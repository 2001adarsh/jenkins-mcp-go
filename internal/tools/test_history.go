package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

const (
	defaultTestHistorySampleSize = 20
	maxTestHistorySampleSize     = 50
	testHistoryErrorHeadWidth    = 60
)

// testHistoryTree pulls just enough of each case to render the timeline.
// errorDetails is grabbed so the FAIL row can carry a one-line head.
const testHistoryTree = "suites[cases[className,name,status,duration,errorDetails]]"

// GetTestHistoryInput is the schema for get_test_history.
type GetTestHistoryInput struct {
	JobPath        string `json:"job_path" jsonschema:"Slash-separated job path."`
	TestFullName   string `json:"test_full_name" jsonschema:"Fully-qualified test name. Accepts both className.name and className/name."`
	SampleSize     int    `json:"sample_size,omitempty" jsonschema:"Builds to scan. Default 20, capped at 50."`
	IncludeSkipped bool   `json:"include_skipped,omitempty" jsonschema:"When true, SKIPPED appears in the timeline and counts toward flips. Default false."`
}

// testHistoryResult is one row of the per-build timeline. Found is true
// only when the requested test was present in the build's report; the
// timeline still renders (with a placeholder) when Found is false so the
// agent sees there *was* a build and what its overall result was.
type testHistoryResult struct {
	BuildNumber int64
	BuildResult string
	HasReport   bool
	Found       bool
	Status      JUnitState
	Duration    float64
	ErrorHead   string
	Err         error
}

// GetTestHistory shows the per-build trend of a single test across the
// last N completed builds — the natural follow-up to get_flaky_candidates
// once the agent has a suspect test.
func (d Deps) GetTestHistory(ctx context.Context, _ *mcp.CallToolRequest, in GetTestHistoryInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	if in.TestFullName == "" {
		return nil, nil, fmt.Errorf("test_full_name is required")
	}
	sampleSize := in.SampleSize
	if sampleSize <= 0 {
		sampleSize = defaultTestHistorySampleSize
	}
	if sampleSize > maxTestHistorySampleSize {
		sampleSize = maxTestHistorySampleSize
	}
	normalized := strings.ReplaceAll(in.TestFullName, "/", ".")

	builds, err := d.discoverCompletedBuildsWithResult(ctx, in.JobPath, sampleSize)
	if err != nil {
		return nil, nil, err
	}
	if len(builds) == 0 {
		return textResult(fmt.Sprintf(
			"Need at least 1 completed build; found 0 under %s.",
			in.JobPath,
		)), nil, nil
	}

	results := fetchPerItem(buildNumbers(builds), func(num int64) testHistoryResult {
		return d.fetchOneTestHistory(ctx, in.JobPath, normalized, num, buildResultByNumber(builds, num))
	})
	for _, r := range results {
		if r.Err != nil {
			return nil, nil, r.Err
		}
	}

	if !anyFound(results) {
		return textResult(fmt.Sprintf(
			"Test '%s' not seen in %d builds under %s.",
			in.TestFullName, len(results), in.JobPath,
		)), nil, nil
	}

	return textResult(renderTestHistory(in.TestFullName, in.JobPath, results, in.IncludeSkipped)), nil, nil
}

func buildNumbers(builds []completedBuild) []int64 {
	out := make([]int64, len(builds))
	for i, b := range builds {
		out[i] = b.Number
	}
	return out
}

func buildResultByNumber(builds []completedBuild, num int64) string {
	for _, b := range builds {
		if b.Number == num {
			return b.Result
		}
	}
	return ""
}

func anyFound(results []testHistoryResult) bool {
	for _, r := range results {
		if r.Found {
			return true
		}
	}
	return false
}

func (d Deps) fetchOneTestHistory(ctx context.Context, jobPath, normalized string, num int64, buildResult string) testHistoryResult {
	res := testHistoryResult{BuildNumber: num, BuildResult: buildResult}
	path := jenkins.JobAPIPath(jobPath) + "/" + strconv.FormatInt(num, 10) + "/testReport/api/json"
	body, err := d.Client.Get(ctx, path, map[string]string{"tree": testHistoryTree})
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return res
		}
		res.Err = fmt.Errorf("build %d test report: %w", num, err)
		return res
	}
	res.HasReport = true
	var rep junitReport
	if err := json.Unmarshal(body, &rep); err != nil {
		res.Err = fmt.Errorf("parse build %d test report: %w", num, err)
		return res
	}
	for _, suite := range rep.Suites {
		for _, c := range suite.Cases {
			if c.ClassName+"."+c.Name != normalized {
				continue
			}
			res.Found = true
			res.Status = NormalizeJUnitStatus(c.Status)
			res.Duration = c.Duration
			if c.ErrorDetails != nil {
				res.ErrorHead = firstLine(*c.ErrorDetails)
			}
			return res
		}
	}
	return res
}

func renderTestHistory(testName, jobPath string, results []testHistoryResult, includeSkipped bool) string {
	// Newest build first for the timeline.
	sorted := make([]testHistoryResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].BuildNumber > sorted[j].BuildNumber })

	var out strings.Builder
	fmt.Fprintf(&out, "History of %s in %s (%d builds):\n\n", testName, jobPath, len(results))
	fmt.Fprintf(&out, "  %-5s  %-8s  %-11s  %-8s  %s\n", "build", "result", "status", "duration", "error head")
	fmt.Fprintf(&out, "  %s  %s  %s  %s  %s\n",
		strings.Repeat("-", 5), strings.Repeat("-", 8), strings.Repeat("-", 11),
		strings.Repeat("-", 8), strings.Repeat("-", 26))

	passes, fails, skips := 0, 0, 0
	var ordered []JUnitState // state sequence in build-number order for flip counting
	for _, r := range sorted {
		statusStr := historyStatusString(r)
		if !includeSkipped && r.Status == StateSkip {
			// Skip rendering and skip flip counting.
			continue
		}
		dur := "-"
		if r.Found && r.Duration > 0 {
			dur = fmt.Sprintf("%.2fs", r.Duration)
		}
		errHead := truncate(r.ErrorHead, testHistoryErrorHeadWidth)
		fmt.Fprintf(&out, "  #%-4d  %-8s  %-11s  %-8s  %s\n",
			r.BuildNumber, r.BuildResult, statusStr, dur, errHead)
		switch r.Status {
		case StatePass:
			passes++
		case StateFail:
			fails++
		case StateSkip:
			skips++
		}
	}

	// Flip counting walks oldest → newest so the order reads naturally.
	chrono := make([]testHistoryResult, len(sorted))
	copy(chrono, sorted)
	sort.Slice(chrono, func(i, j int) bool { return chrono[i].BuildNumber < chrono[j].BuildNumber })
	for _, r := range chrono {
		if !r.Found {
			continue
		}
		if r.Status == StateSkip && !includeSkipped {
			continue
		}
		ordered = append(ordered, r.Status)
	}
	flips := 0
	for i := 1; i < len(ordered); i++ {
		if ordered[i] != ordered[i-1] {
			flips++
		}
	}

	fmt.Fprintf(&out, "\nSummary: %d PASS, %d FAIL, %d SKIP. %d status flips in window.\n",
		passes, fails, skips, flips)
	return out.String()
}

func historyStatusString(r testHistoryResult) string {
	if !r.HasReport {
		return "(no report)"
	}
	if !r.Found {
		return "(missing)"
	}
	return r.Status.String()
}
