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
	// defaultFlakySampleSize matches the issue spec — 20 builds is roughly
	// "the last day" of CI on a typical repo.
	defaultFlakySampleSize = 20
	// maxFlakySampleSize caps the sample. Each build is one testReport API
	// call; 50 is the limit before payload/latency become unfriendly to the
	// agent loop.
	maxFlakySampleSize = 50
	// defaultMinFlips is the smallest meaningful flip count: a test that
	// passed then failed (once) once may just be a regression, not flake.
	defaultMinFlips = 2
	// flakyTestNameWidth is the rendered width of the test column; longer
	// names are rune-truncated.
	flakyTestNameWidth = 60
)

// flakyTestsTree limits the testReport payload to identity + status. We
// don't need stack traces, durations, or counts to compute flips.
const flakyTestsTree = "suites[cases[className,name,status]]"

// GetFlakyCandidatesInput is the schema for get_flaky_candidates.
type GetFlakyCandidatesInput struct {
	JobPath        string `json:"job_path" jsonschema:"Slash-separated job path."`
	SampleSize     int    `json:"sample_size,omitempty" jsonschema:"How many of the most recent completed builds to inspect. Default 20, capped at 50."`
	MinFlips       int    `json:"min_flips,omitempty" jsonschema:"Minimum pass↔fail transitions for a test to be reported. Default 2."`
	IncludeSkipped bool   `json:"include_skipped,omitempty" jsonschema:"When true, SKIPPED counts as a state that contributes to flip counting. Default false (SKIPPED is ignored)."`
}

// flakyBuildResult is the per-build slice of the aggregation. Err is set
// for hard failures only — a missing test report (HTTP 404) leaves Tests
// empty and Err nil.
type flakyBuildResult struct {
	BuildNumber int64
	Tests       map[string]JUnitState // key=className.name
	Err         error
}

// flakyTestStats is one row in the rendered output.
type flakyTestStats struct {
	Name     string
	Flips    int
	Passes   int
	Failures int
	LastSeen int64
}

// GetFlakyCandidates ranks tests by pass↔fail flip count across the latest
// sample_size completed builds of one job.
func (d Deps) GetFlakyCandidates(ctx context.Context, _ *mcp.CallToolRequest, in GetFlakyCandidatesInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	sampleSize := in.SampleSize
	if sampleSize <= 0 {
		sampleSize = defaultFlakySampleSize
	}
	if sampleSize > maxFlakySampleSize {
		sampleSize = maxFlakySampleSize
	}
	minFlips := in.MinFlips
	if minFlips <= 0 {
		minFlips = defaultMinFlips
	}

	builds, err := d.discoverCompletedBuilds(ctx, in.JobPath, sampleSize)
	if err != nil {
		return nil, nil, err
	}
	if len(builds) < 2 {
		return textResult(fmt.Sprintf(
			"Need at least 2 completed builds to compute flips; found %d under %s.",
			len(builds), in.JobPath,
		)), nil, nil
	}

	results := fetchPerBuild(builds, func(num int64) flakyBuildResult {
		return d.fetchOneFlakyResult(ctx, in.JobPath, num)
	})
	// Surface the first hard error if any (e.g. auth failure). Missing
	// testReport (404) is not an error — empty Tests map is allowed.
	for _, r := range results {
		if r.Err != nil {
			return nil, nil, r.Err
		}
	}

	agg := aggregateFlaky(results, in.IncludeSkipped)
	return textResult(renderFlaky(in.JobPath, sampleSize, minFlips, in.IncludeSkipped, results, agg)), nil, nil
}

func (d Deps) fetchOneFlakyResult(ctx context.Context, jobPath string, num int64) flakyBuildResult {
	res := flakyBuildResult{BuildNumber: num, Tests: map[string]JUnitState{}}
	path := jenkins.JobAPIPath(jobPath) + "/" + strconv.FormatInt(num, 10) + "/testReport/api/json"
	body, err := d.Client.Get(ctx, path, map[string]string{"tree": flakyTestsTree})
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return res
		}
		res.Err = fmt.Errorf("build %d test report: %w", num, err)
		return res
	}
	var rep junitReport
	if err := json.Unmarshal(body, &rep); err != nil {
		res.Err = fmt.Errorf("parse build %d test report: %w", num, err)
		return res
	}
	for _, suite := range rep.Suites {
		for _, c := range suite.Cases {
			if state := NormalizeJUnitStatus(c.Status); state != StateUnknown {
				res.Tests[c.ClassName+"."+c.Name] = state
			}
		}
	}
	return res
}

// aggregateFlaky walks results in build-number order and builds per-test
// stats: ordered state sequence (drives flip count), pass/fail tallies,
// and the most recent build the test was observed in.
func aggregateFlaky(results []flakyBuildResult, includeSkipped bool) []flakyTestStats {
	sorted := make([]flakyBuildResult, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].BuildNumber < sorted[j].BuildNumber
	})

	type seq struct {
		states   []JUnitState
		lastSeen int64
	}
	perTest := map[string]*seq{}
	for _, r := range sorted {
		for name, state := range r.Tests {
			if state == StateSkip && !includeSkipped {
				continue
			}
			s := perTest[name]
			if s == nil {
				s = &seq{}
				perTest[name] = s
			}
			s.states = append(s.states, state)
			if r.BuildNumber > s.lastSeen {
				s.lastSeen = r.BuildNumber
			}
		}
	}

	out := make([]flakyTestStats, 0, len(perTest))
	for name, s := range perTest {
		var passes, failures, flips int
		for i, st := range s.states {
			switch st {
			case StatePass:
				passes++
			case StateFail:
				failures++
			}
			if i > 0 && s.states[i] != s.states[i-1] {
				flips++
			}
		}
		out = append(out, flakyTestStats{
			Name: name, Flips: flips, Passes: passes, Failures: failures, LastSeen: s.lastSeen,
		})
	}
	return out
}

func renderFlaky(jobPath string, sampleSize, minFlips int, includeSkipped bool, results []flakyBuildResult, agg []flakyTestStats) string {
	var kept []flakyTestStats
	for _, t := range agg {
		if t.Flips >= minFlips {
			kept = append(kept, t)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Flips != kept[j].Flips {
			return kept[i].Flips > kept[j].Flips
		}
		if kept[i].Failures != kept[j].Failures {
			return kept[i].Failures > kept[j].Failures
		}
		return kept[i].Name < kept[j].Name
	})

	var out strings.Builder
	fmt.Fprintf(&out, "Flaky candidates for %s\n", jobPath)
	fmt.Fprintf(&out, "(sample_size=%d, builds analyzed=%d, min_flips=%d, include_skipped=%v)\n",
		sampleSize, len(results), minFlips, includeSkipped)

	missing := 0
	for _, r := range results {
		if r.Err == nil && len(r.Tests) == 0 {
			missing++
		}
	}
	if missing > 0 {
		fmt.Fprintf(&out, "(%d build(s) had no test report and contributed no data)\n", missing)
	}
	out.WriteByte('\n')

	if len(kept) == 0 {
		fmt.Fprintf(&out, "No tests with >= %d flips across this window.\n", minFlips)
		return out.String()
	}

	fmt.Fprintf(&out, "%s  %5s  %6s  %8s  %s\n",
		padRight("test", flakyTestNameWidth), "flips", "passes", "failures", "last_seen_build")
	fmt.Fprintf(&out, "%s  %5s  %6s  %8s  %s\n",
		strings.Repeat("-", flakyTestNameWidth), "-----", "------", "--------", "---------------")
	for _, t := range kept {
		fmt.Fprintf(&out, "%s  %5d  %6d  %8d  %d\n",
			padRight(truncate(t.Name, flakyTestNameWidth), flakyTestNameWidth),
			t.Flips, t.Passes, t.Failures, t.LastSeen)
	}
	return out.String()
}
