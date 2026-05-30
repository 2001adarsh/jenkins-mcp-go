package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// testHistoryFixture seeds one build's response. Report is the testReport
// returned for the build; nil means HTTP 404 (build had no report).
type testHistoryFixture struct {
	Result string
	Report *junitReport
}

func newTestHistoryDeps(t *testing.T, jobPath string, fixtures map[int64]testHistoryFixture) (Deps, *httptest.Server) {
	t.Helper()
	prefix := jenkins.JobAPIPath(jobPath) + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if rest == "api/json" {
			type buildRef struct {
				Number int64  `json:"number"`
				Result string `json:"result"`
			}
			listing := struct {
				Builds []buildRef `json:"builds"`
			}{}
			nums := sortedKeysDesc(fixtures)
			for _, n := range nums {
				listing.Builds = append(listing.Builds, buildRef{Number: n, Result: fixtures[n].Result})
			}
			_ = json.NewEncoder(w).Encode(listing)
			return
		}
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
		f, ok := fixtures[n]
		if !ok || rest[slash:] != "/testReport/api/json" {
			http.NotFound(w, r)
			return
		}
		if f.Report == nil {
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

func histCase(className, name, status string, durationSec float64, errDetails string) junitCase {
	c := junitCase{
		ClassName: className,
		Name:      name,
		Status:    status,
		Duration:  durationSec,
	}
	if errDetails != "" {
		c.ErrorDetails = &errDetails
	}
	return c
}

func TestGetTestHistory_HappyPathRendersTimelineAndSummary(t *testing.T) {
	d, srv := newTestHistoryDeps(t, "svc", map[int64]testHistoryFixture{
		91: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.42, ""),
		}}}}},
		90: {Result: "FAILURE", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "FAILED", 1.10, "AssertionError: expected 1 got 2 (multi-line\ntrace details)"),
		}}}}},
		89: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.38, ""),
		}}}}},
	})
	defer srv.Close()

	res, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{
		JobPath:      "svc",
		TestFullName: "com.example.FooTest.bar",
	})
	if err != nil {
		t.Fatalf("GetTestHistory: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"History of com.example.FooTest.bar in svc (3 builds)",
		"#91",
		"#90",
		"#89",
		"PASS",
		"FAIL",
		"AssertionError: expected 1 got 2",
		"Summary: 2 PASS, 1 FAIL, 0 SKIP. 2 status flips in window.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Error detail should be one-line (no embedded newline in the rendered row).
	if strings.Contains(out, "trace details") {
		t.Errorf("expected error detail to be first-line-only, got:\n%s", out)
	}
}

func TestGetTestHistory_AcceptsSlashSeparator(t *testing.T) {
	d, srv := newTestHistoryDeps(t, "svc", map[int64]testHistoryFixture{
		91: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.42, ""),
		}}}}},
	})
	defer srv.Close()

	res, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{
		JobPath:      "svc",
		TestFullName: "com.example.FooTest/bar",
	})
	if err != nil {
		t.Fatalf("GetTestHistory: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected the slash-separated test name to resolve, got:\n%s", out)
	}
}

func TestGetTestHistory_TestNotFoundReturnsHint(t *testing.T) {
	d, srv := newTestHistoryDeps(t, "svc", map[int64]testHistoryFixture{
		91: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.42, ""),
		}}}}},
		90: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.4, ""),
		}}}}},
	})
	defer srv.Close()

	res, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{
		JobPath:      "svc",
		TestFullName: "com.example.NotPresent.missing",
	})
	if err != nil {
		t.Fatalf("GetTestHistory: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "not seen in") {
		t.Errorf("expected 'not seen in' hint, got:\n%s", out)
	}
}

func TestGetTestHistory_BuildWithNoTestReportRendersNoReport(t *testing.T) {
	d, srv := newTestHistoryDeps(t, "svc", map[int64]testHistoryFixture{
		91: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.42, ""),
		}}}}},
		90: {Result: "SUCCESS", Report: nil}, // 404 — no test report
	})
	defer srv.Close()

	res, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{
		JobPath:      "svc",
		TestFullName: "com.example.FooTest.bar",
	})
	if err != nil {
		t.Fatalf("GetTestHistory: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "(no report)") {
		t.Errorf("expected (no report) row for build 90, got:\n%s", out)
	}
}

func TestGetTestHistory_IncludeSkippedTogglesSkipRows(t *testing.T) {
	fixtures := map[int64]testHistoryFixture{
		91: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.42, ""),
		}}}}},
		90: {Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "SKIPPED", 0.0, ""),
		}}}}},
	}

	// Default: include_skipped=false → SKIP row omitted from timeline.
	d, srv := newTestHistoryDeps(t, "svc", fixtures)
	defer srv.Close()
	res, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{
		JobPath: "svc", TestFullName: "com.example.FooTest.bar",
	})
	if err != nil {
		t.Fatalf("GetTestHistory: %v", err)
	}
	out := resultText(t, res)
	// "SKIP" appears in the summary line ("0 SKIP."); we want zero rows.
	// "0 SKIP" specifically means the count is zero — confirms suppression.
	if !strings.Contains(out, "0 SKIP") {
		t.Errorf("expected summary to report 0 SKIP when suppressed, got:\n%s", out)
	}
	if strings.Count(out, "SKIP") != 1 {
		t.Errorf("expected SKIP to appear only in summary, got %d occurrences:\n%s",
			strings.Count(out, "SKIP"), out)
	}

	// include_skipped=true → SKIP appears in a timeline row AND summary.
	d2, srv2 := newTestHistoryDeps(t, "svc", fixtures)
	defer srv2.Close()
	res2, _, err := d2.GetTestHistory(context.Background(), nil, GetTestHistoryInput{
		JobPath: "svc", TestFullName: "com.example.FooTest.bar", IncludeSkipped: true,
	})
	if err != nil {
		t.Fatalf("GetTestHistory(include_skipped): %v", err)
	}
	out2 := resultText(t, res2)
	if !strings.Contains(out2, "1 SKIP") {
		t.Errorf("expected summary to report 1 SKIP when include_skipped=true, got:\n%s", out2)
	}
}

func TestGetTestHistory_SampleSizeCappedAt50(t *testing.T) {
	fixtures := map[int64]testHistoryFixture{}
	for i := int64(1); i <= 60; i++ {
		fixtures[i] = testHistoryFixture{Result: "SUCCESS", Report: &junitReport{Suites: []junitSuite{{Cases: []junitCase{
			histCase("com.example.FooTest", "bar", "PASSED", 0.1, ""),
		}}}}}
	}
	d, srv := newTestHistoryDeps(t, "svc", fixtures)
	defer srv.Close()

	res, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{
		JobPath: "svc", TestFullName: "com.example.FooTest.bar", SampleSize: 100,
	})
	if err != nil {
		t.Fatalf("GetTestHistory: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "(50 builds)") {
		t.Errorf("expected cap at 50, got:\n%s", out)
	}
}

func TestGetTestHistory_MissingJobPath(t *testing.T) {
	d := Deps{}
	if _, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{TestFullName: "x.y"}); err == nil {
		t.Fatal("expected error for empty job_path")
	}
}

func TestGetTestHistory_MissingTestFullName(t *testing.T) {
	d := Deps{}
	if _, _, err := d.GetTestHistory(context.Background(), nil, GetTestHistoryInput{JobPath: "svc"}); err == nil {
		t.Fatal("expected error for empty test_full_name")
	}
}
