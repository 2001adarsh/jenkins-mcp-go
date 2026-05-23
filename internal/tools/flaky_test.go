package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// flakyFixture is one test report mock keyed by build number.
type flakyFixture struct {
	Result string // "" means in-progress (filtered out of build list)
	Report *junitReport
}

// newFlakyDeps wires a Jenkins-shaped HTTP handler that serves:
//   - /job/<path>/api/json?tree=builds[number,result]{...} → build list
//   - /job/<path>/<n>/testReport/api/json?tree=...        → per-build report
//
// Per-build reports return HTTP 404 when fixtures[n].Report is nil.
func newFlakyDeps(t *testing.T, jobPath string, fixtures map[int64]flakyFixture) (Deps, *httptest.Server) {
	t.Helper()
	prefix := jenkins.JobAPIPath(jobPath) + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if rest == r.URL.Path {
			http.NotFound(w, r)
			return
		}
		if rest == "api/json" {
			listing := flakyJobAPI{}
			// Highest build number first — Jenkins' canonical order.
			nums := sortedKeysDesc(fixtures)
			for _, n := range nums {
				listing.Builds = append(listing.Builds, flakyBuildRef{
					Number: n,
					Result: fixtures[n].Result,
				})
			}
			_ = json.NewEncoder(w).Encode(listing)
			return
		}
		// "<n>/testReport/api/json"
		slash := strings.IndexByte(rest, '/')
		if slash <= 0 {
			http.NotFound(w, r)
			return
		}
		n, err := strconv.ParseInt(rest[:slash], 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if rest[slash+1:] != "testReport/api/json" {
			http.NotFound(w, r)
			return
		}
		f, ok := fixtures[n]
		if !ok || f.Report == nil {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(f.Report)
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

func sortedKeysDesc(m map[int64]flakyFixture) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// simple insertion sort, desc — N is tiny in tests
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] > out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// onlyCases builds a junitReport with one suite containing the provided
// (className, name, status) tuples. Keeps test fixtures short and readable.
func onlyCases(cases ...junitCase) *junitReport {
	return &junitReport{Suites: []junitSuite{{Cases: cases}}}
}

// c is shorthand for a junitCase with class=p (one short class everywhere).
func c(name, status string) junitCase {
	return junitCase{ClassName: "p", Name: name, Status: status}
}

func TestGetFlakyCandidates_RanksByFlipsAndFailures(t *testing.T) {
	// 5 builds; three tests with different flake profiles.
	//   Wobbly:  P F P F P  → flips=4, passes=3, failures=2
	//   Sometimes: P P P F F → flips=1, passes=3, failures=2 (filtered by min_flips=2)
	//   Twitchy: P F F P F  → flips=3, passes=2, failures=3
	fixtures := map[int64]flakyFixture{
		10: {Result: "SUCCESS", Report: onlyCases(c("Wobbly", "PASSED"), c("Sometimes", "PASSED"), c("Twitchy", "PASSED"))},
		11: {Result: "FAILURE", Report: onlyCases(c("Wobbly", "FAILED"), c("Sometimes", "PASSED"), c("Twitchy", "FAILED"))},
		12: {Result: "SUCCESS", Report: onlyCases(c("Wobbly", "PASSED"), c("Sometimes", "PASSED"), c("Twitchy", "FAILED"))},
		13: {Result: "FAILURE", Report: onlyCases(c("Wobbly", "FAILED"), c("Sometimes", "FAILED"), c("Twitchy", "PASSED"))},
		14: {Result: "SUCCESS", Report: onlyCases(c("Wobbly", "PASSED"), c("Sometimes", "FAILED"), c("Twitchy", "FAILED"))},
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	res, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 5,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"Flaky candidates for svc",
		"sample_size=5, builds analyzed=5",
		"min_flips=2",
		"include_skipped=false",
		"p.Wobbly",
		"p.Twitchy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Sometimes has only 1 flip — filtered out by default min_flips=2.
	if strings.Contains(out, "p.Sometimes") {
		t.Errorf("p.Sometimes (flips=1) should not appear when min_flips=2, got:\n%s", out)
	}
	// Wobbly (flips=4) ranks before Twitchy (flips=3).
	wIdx := strings.Index(out, "p.Wobbly")
	tIdx := strings.Index(out, "p.Twitchy")
	if wIdx < 0 || tIdx < 0 || wIdx > tIdx {
		t.Errorf("expected p.Wobbly to rank above p.Twitchy, got:\n%s", out)
	}
}

func TestGetFlakyCandidates_MinFlipsFilter(t *testing.T) {
	// Two tests, both flip exactly twice.
	fixtures := map[int64]flakyFixture{
		1: {Result: "SUCCESS", Report: onlyCases(c("A", "PASSED"), c("B", "FAILED"))},
		2: {Result: "FAILURE", Report: onlyCases(c("A", "FAILED"), c("B", "PASSED"))},
		3: {Result: "SUCCESS", Report: onlyCases(c("A", "PASSED"), c("B", "FAILED"))},
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	// min_flips=3 should filter both out.
	res, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 3, MinFlips: 3,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "No tests with >= 3 flips") {
		t.Errorf("expected no-flaky message, got:\n%s", out)
	}
}

func TestGetFlakyCandidates_IncludeSkippedAffectsFlips(t *testing.T) {
	// Sequence: PASS SKIP FAIL.
	//   include_skipped=false → PASS FAIL → 1 flip
	//   include_skipped=true  → PASS SKIP FAIL → 2 flips
	fixtures := map[int64]flakyFixture{
		1: {Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))},
		2: {Result: "UNSTABLE", Report: onlyCases(c("T", "SKIPPED"))},
		3: {Result: "FAILURE", Report: onlyCases(c("T", "FAILED"))},
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	withoutSkip, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 3, MinFlips: 1,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	outNo := resultText(t, withoutSkip)
	if !strings.Contains(outNo, "p.T") {
		t.Fatalf("expected p.T row in output, got:\n%s", outNo)
	}

	withSkip, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 3, MinFlips: 1, IncludeSkipped: true,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	outYes := resultText(t, withSkip)
	if !strings.Contains(outYes, "p.T") {
		t.Fatalf("expected p.T row in output, got:\n%s", outYes)
	}

	// Spot the flip column for p.T in each output. Column order is
	// "test ... flips ... passes ... failures ... last_seen_build".
	if flipsForTest(outNo, "p.T") != 1 {
		t.Errorf("expected flips=1 without include_skipped, got line:\n%s", findRow(outNo, "p.T"))
	}
	if flipsForTest(outYes, "p.T") != 2 {
		t.Errorf("expected flips=2 with include_skipped, got line:\n%s", findRow(outYes, "p.T"))
	}
}

// findRow returns the rendered line containing the test name. Test helper
// only — assumes the test name is uniquely identifying within the output.
func findRow(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, name) {
			return line
		}
	}
	return ""
}

// flipsForTest parses the "flips" column out of the rendered row for the
// named test. Returns -1 if the row can't be parsed.
func flipsForTest(out, name string) int {
	line := findRow(out, name)
	if line == "" {
		return -1
	}
	// Strip the padded name (we just sliced its trailing portion off).
	rest := strings.TrimSpace(line[strings.Index(line, name)+len(name):])
	fields := strings.Fields(rest)
	if len(fields) < 1 {
		return -1
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return -1
	}
	return n
}

func TestGetFlakyCandidates_InProgressBuildsFilteredOut(t *testing.T) {
	// Build 100 is in progress (Result==""); the discovery loop should
	// skip it and walk back to 99..96 to reach the requested sample_size.
	fixtures := map[int64]flakyFixture{
		100: {Result: "", Report: nil},
		99:  {Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))},
		98:  {Result: "FAILURE", Report: onlyCases(c("T", "FAILED"))},
		97:  {Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))},
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	res, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 3, MinFlips: 1,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "builds analyzed=3") {
		t.Errorf("expected exactly 3 builds analyzed (in-progress excluded), got:\n%s", out)
	}
}

func TestGetFlakyCandidates_MissingTestReports(t *testing.T) {
	// One build has a report, the others don't (404). Result: no flips
	// possible, and the missing-builds note must surface.
	fixtures := map[int64]flakyFixture{
		1: {Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))},
		2: {Result: "FAILURE", Report: nil},
		3: {Result: "SUCCESS", Report: nil},
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	res, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 3, MinFlips: 1,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "2 build(s) had no test report") {
		t.Errorf("expected missing-reports hint, got:\n%s", out)
	}
}

func TestGetFlakyCandidates_SampleSizeCapped(t *testing.T) {
	// Provide 60 builds; the tool should cap analysis at maxFlakySampleSize=50.
	fixtures := map[int64]flakyFixture{}
	for n := int64(1); n <= 60; n++ {
		fixtures[n] = flakyFixture{Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))}
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	res, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 999,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	out := resultText(t, res)
	want := fmt.Sprintf("sample_size=%d", maxFlakySampleSize)
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in output, got:\n%s", want, out)
	}
}

func TestGetFlakyCandidates_TooFewBuilds(t *testing.T) {
	fixtures := map[int64]flakyFixture{
		1: {Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))},
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	res, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 3,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Need at least 2 completed builds") {
		t.Errorf("expected too-few-builds hint, got:\n%s", out)
	}
}

func TestGetFlakyCandidates_MissingJobPath(t *testing.T) {
	d := Deps{}
	_, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{})
	if err == nil {
		t.Fatal("expected error for empty job_path")
	}
	if !strings.Contains(err.Error(), "job_path") {
		t.Errorf("expected error to mention job_path, got: %v", err)
	}
}

func TestGetFlakyCandidates_LastSeenIsLatestBuild(t *testing.T) {
	// Test T appears in builds 1, 2, 4 — not in 3. last_seen should be 4.
	fixtures := map[int64]flakyFixture{
		1: {Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))},
		2: {Result: "FAILURE", Report: onlyCases(c("T", "FAILED"))},
		3: {Result: "SUCCESS", Report: onlyCases(c("Other", "PASSED"))},
		4: {Result: "SUCCESS", Report: onlyCases(c("T", "PASSED"))},
	}
	d, srv := newFlakyDeps(t, "svc", fixtures)
	defer srv.Close()

	res, _, err := d.GetFlakyCandidates(context.Background(), nil, GetFlakyCandidatesInput{
		JobPath: "svc", SampleSize: 4, MinFlips: 1,
	})
	if err != nil {
		t.Fatalf("GetFlakyCandidates: %v", err)
	}
	out := resultText(t, res)
	row := findRow(out, "p.T ")
	if row == "" {
		// padded; try without trailing space
		row = findRow(out, "p.T")
	}
	if !strings.HasSuffix(strings.TrimSpace(row), "4") {
		t.Errorf("expected last_seen_build=4 in row, got: %q\nfull output:\n%s", row, out)
	}
}

func TestNormalizeJUnitStatus(t *testing.T) {
	cases := map[string]JUnitState{
		"PASSED":     StatePass,
		"FIXED":      StatePass,
		"FAILED":     StateFail,
		"REGRESSION": StateFail,
		"SKIPPED":    StateSkip,
		"":           StateUnknown,
		"OTHER":      StateUnknown,
	}
	for in, want := range cases {
		if got := NormalizeJUnitStatus(in); got != want {
			t.Errorf("NormalizeJUnitStatus(%q) = %v, want %v", in, got, want)
		}
	}
}
