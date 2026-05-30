package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// findTestFixture lets tests seed responses by path. Missing paths 404.
// delays let one path stall so the per-job-timeout case can run quickly.
type findTestFixture struct {
	bodies   map[string]string
	statuses map[string]int
	delays   map[string]time.Duration
}

func newFindTestDeps(t *testing.T, f findTestFixture) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d, ok := f.delays[r.URL.Path]; ok {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}
		if s, ok := f.statuses[r.URL.Path]; ok {
			w.WriteHeader(s)
			return
		}
		if body, ok := f.bodies[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

// Shorthand for the most common listing shape: two leaf jobs at root.
func twoJobListing(names ...string) string {
	var b strings.Builder
	b.WriteString(`{"jobs":[`)
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"`)
		b.WriteString(n)
		b.WriteString(`","_class":"hudson.model.FreeStyleProject","url":"u","color":"blue"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

func lastCompletedJSON(number int64, result string) string {
	return `{"number":` + intStr(number) + `,"result":"` + result + `"}`
}

func testReportJSON(cases ...[2]string) string {
	var b strings.Builder
	b.WriteString(`{"suites":[{"cases":[`)
	for i, c := range cases {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"className":"`)
		b.WriteString(c[0])
		b.WriteString(`","name":"`)
		b.WriteString(c[1])
		b.WriteString(`"}`)
	}
	b.WriteString(`]}]}`)
	return b.String()
}

func intStr(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestFindTestByName_HappyPath(t *testing.T) {
	d, srv := newFindTestDeps(t, findTestFixture{
		bodies: map[string]string{
			"/api/json":                                         twoJobListing("job-a", "job-b"),
			"/job/job-a/lastCompletedBuild/api/json":            lastCompletedJSON(42, "SUCCESS"),
			"/job/job-a/lastCompletedBuild/testReport/api/json": testReportJSON([2]string{"com.example.FooSpec", "test_one"}, [2]string{"com.example.FooSpec", "must_return_404"}),
			"/job/job-b/lastCompletedBuild/api/json":            lastCompletedJSON(7, "FAILURE"),
			"/job/job-b/lastCompletedBuild/testReport/api/json": testReportJSON([2]string{"com.example.BarSpec", "must_return_404"}),
		},
	})
	defer srv.Close()

	res, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{Substring: "must_return_404"})
	if err != nil {
		t.Fatalf("FindTestByName: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		`Tests matching "must_return_404"`,
		"job-a",
		"job-b",
		"com.example.FooSpec.must_return_404",
		"com.example.BarSpec.must_return_404",
		"#42",
		"SUCCESS",
		"#7",
		"FAILURE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "test_one") {
		t.Errorf("expected non-matching test to be excluded, got:\n%s", out)
	}
}

func TestFindTestByName_CaseInsensitive(t *testing.T) {
	d, srv := newFindTestDeps(t, findTestFixture{
		bodies: map[string]string{
			"/api/json":                                       twoJobListing("svc"),
			"/job/svc/lastCompletedBuild/api/json":            lastCompletedJSON(1, "SUCCESS"),
			"/job/svc/lastCompletedBuild/testReport/api/json": testReportJSON([2]string{"com.example.FooSpec", "must_return_404"}),
		},
	})
	defer srv.Close()

	res, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{Substring: "MUST_RETURN"})
	if err != nil {
		t.Fatalf("FindTestByName: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "must_return_404") {
		t.Errorf("expected case-insensitive match, got:\n%s", out)
	}
}

func TestFindTestByName_MaxResultsTruncates(t *testing.T) {
	d, srv := newFindTestDeps(t, findTestFixture{
		bodies: map[string]string{
			"/api/json":                                       twoJobListing("svc"),
			"/job/svc/lastCompletedBuild/api/json":            lastCompletedJSON(1, "SUCCESS"),
			"/job/svc/lastCompletedBuild/testReport/api/json": testReportJSON([2]string{"com.example.FooSpec", "must_a"}, [2]string{"com.example.FooSpec", "must_b"}, [2]string{"com.example.FooSpec", "must_c"}),
		},
	})
	defer srv.Close()

	res, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{Substring: "must_", MaxResults: 2})
	if err != nil {
		t.Fatalf("FindTestByName: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "stopped at max_results=2") {
		t.Errorf("expected truncation footer, got:\n%s", out)
	}
	// Should render exactly 2 rows.
	if strings.Count(out, "com.example.FooSpec.must_") != 2 {
		t.Errorf("expected 2 result rows, got %d:\n%s", strings.Count(out, "com.example.FooSpec.must_"), out)
	}
}

func TestFindTestByName_NoTestReportCountedAsSkipped(t *testing.T) {
	d, srv := newFindTestDeps(t, findTestFixture{
		bodies: map[string]string{
			"/api/json": "{\"jobs\":[{\"name\":\"no-report\",\"_class\":\"hudson.model.FreeStyleProject\"}]}",
			"/job/no-report/lastCompletedBuild/api/json": lastCompletedJSON(1, "SUCCESS"),
		},
		statuses: map[string]int{
			"/job/no-report/lastCompletedBuild/testReport/api/json": http.StatusNotFound,
		},
	})
	defer srv.Close()

	res, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{Substring: "x"})
	if err != nil {
		t.Fatalf("FindTestByName: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "no test report") {
		t.Errorf("expected 'no test report' in footer, got:\n%s", out)
	}
}

func TestFindTestByName_NoCompletedBuildCountedAsSkipped(t *testing.T) {
	d, srv := newFindTestDeps(t, findTestFixture{
		bodies: map[string]string{
			"/api/json": "{\"jobs\":[{\"name\":\"never-built\",\"_class\":\"hudson.model.FreeStyleProject\"}]}",
		},
		statuses: map[string]int{
			"/job/never-built/lastCompletedBuild/api/json": http.StatusNotFound,
		},
	})
	defer srv.Close()

	res, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{Substring: "x"})
	if err != nil {
		t.Fatalf("FindTestByName: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "no completed build") {
		t.Errorf("expected 'no completed build' in footer, got:\n%s", out)
	}
}

func TestFindTestByName_FolderPathScopes(t *testing.T) {
	d, srv := newFindTestDeps(t, findTestFixture{
		bodies: map[string]string{
			// root has a job we should NOT scan.
			"/api/json":          "{\"jobs\":[{\"name\":\"root-job\",\"_class\":\"hudson.model.FreeStyleProject\"}]}",
			"/job/team/api/json": twoJobListing("nested"),
			"/job/team/job/nested/lastCompletedBuild/api/json":            lastCompletedJSON(1, "SUCCESS"),
			"/job/team/job/nested/lastCompletedBuild/testReport/api/json": testReportJSON([2]string{"com.example.FooSpec", "must_return_404"}),
		},
	})
	defer srv.Close()

	res, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{
		Substring:  "must_return_404",
		FolderPath: "team",
	})
	if err != nil {
		t.Fatalf("FindTestByName: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "team/nested") {
		t.Errorf("expected team/nested in output, got:\n%s", out)
	}
	if strings.Contains(out, "root-job") {
		t.Errorf("expected root-job to be excluded by folder_path, got:\n%s", out)
	}
}

func TestFindTestByName_PerJobTimeoutCounted(t *testing.T) {
	saved := findTestPerJobTimeout
	findTestPerJobTimeout = 50 * time.Millisecond
	t.Cleanup(func() { findTestPerJobTimeout = saved })

	d, srv := newFindTestDeps(t, findTestFixture{
		bodies: map[string]string{
			"/api/json": twoJobListing("slow"),
		},
		delays: map[string]time.Duration{
			"/job/slow/lastCompletedBuild/api/json": 500 * time.Millisecond,
		},
	})
	defer srv.Close()

	res, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{Substring: "x"})
	if err != nil {
		t.Fatalf("FindTestByName: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "timed out") {
		t.Errorf("expected 'timed out' in footer, got:\n%s", out)
	}
}

func TestFindTestByName_MissingSubstring(t *testing.T) {
	d := Deps{}
	if _, _, err := d.FindTestByName(context.Background(), nil, FindTestByNameInput{}); err == nil {
		t.Fatal("expected error for empty substring")
	}
}
