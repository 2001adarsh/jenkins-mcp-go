package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

func newSCMDeps(t *testing.T, responses map[string]scmBuildAPI) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

// 2026-05-16 12:04 UTC = 1747396800000 ms (offset by ~4 mins inside the day
// to make rendered values deterministic across timezones).
const sampleTimestampMs int64 = 1747396800000

func TestGetSCMContext_SingleChangeSet(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/team/job/svc/86/api/json": {
			ChangeSets: []scmChangeSet{{
				Kind: "git",
				Items: []scmItem{
					{
						CommitID: "abc1234deadbeef", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "alice"},
						Msg:    "Fix flake in PaymentSpec",
						Paths: []scmPath{
							{File: "internal/payment/processor.go", EditType: "edit"},
							{File: "internal/payment/processor_test.go", EditType: "add"},
						},
					},
					{
						CommitID: "f00ba12f00ba12", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "bob"},
						Msg:    "Remove dead config",
						Paths:  []scmPath{{File: "config/old.yaml", EditType: "delete"}},
					},
				},
			}},
			Culprits: []scmCulprit{{FullName: "alice"}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:     "team/svc",
		BuildNumber: 86,
	})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"SCM context for team/svc build 86",
		"Culprits: alice",
		"abc1234",
		"alice",
		"\"Fix flake in PaymentSpec\"",
		"M  internal/payment/processor.go",
		"A  internal/payment/processor_test.go",
		"f00ba12",
		"bob",
		"D  config/old.yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Single-set should NOT print a per-set header.
	if strings.Contains(out, "change set 1") {
		t.Errorf("single change set should not print per-set header, got:\n%s", out)
	}
}

func TestGetSCMContext_MultiChangeSetPipeline(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{
				{
					Kind: "git",
					Items: []scmItem{{
						CommitID: "aaa1111", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "alice"},
						Msg:    "App change",
						Paths:  []scmPath{{File: "app/main.go", EditType: "edit"}},
					}},
				},
				{
					Kind: "git",
					Items: []scmItem{{
						CommitID: "bbb2222", Timestamp: sampleTimestampMs,
						Author: scmAuthor{FullName: "bob"},
						Msg:    "Lib change",
						Paths:  []scmPath{{File: "vendor/lib.go", EditType: "edit"}},
					}},
				},
			},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"— change set 1 (git) — 1 commit(s)",
		"— change set 2 (git) — 1 commit(s)",
		"aaa1111",
		"bbb2222",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestGetSCMContext_PathFilter(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{{
				Items: []scmItem{
					{
						CommitID: "aaa1111", Author: scmAuthor{FullName: "alice"},
						Msg:   "Payment change",
						Paths: []scmPath{{File: "internal/payment/x.go", EditType: "edit"}},
					},
					{
						CommitID: "bbb2222", Author: scmAuthor{FullName: "bob"},
						Msg:   "Docs change",
						Paths: []scmPath{{File: "docs/README.md", EditType: "edit"}},
					},
					{
						CommitID: "ccc3333", Author: scmAuthor{FullName: "carol"},
						Msg: "Mixed change",
						Paths: []scmPath{
							{File: "docs/api.md", EditType: "edit"},
							{File: "internal/payment/y.go", EditType: "edit"},
						},
					},
				},
			}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:    "svc",
		PathFilter: "^internal/payment/",
	})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "aaa1111") {
		t.Errorf("expected aaa1111 (touches internal/payment) to remain, got:\n%s", out)
	}
	if !strings.Contains(out, "ccc3333") {
		t.Errorf("expected ccc3333 (touches internal/payment among other paths) to remain, got:\n%s", out)
	}
	if strings.Contains(out, "bbb2222") {
		t.Errorf("expected bbb2222 (docs only) to be filtered out, got:\n%s", out)
	}
	if !strings.Contains(out, `2 of 3 commits matched path_filter "^internal/payment/"`) {
		t.Errorf("expected path_filter summary line, got:\n%s", out)
	}
}

func TestGetSCMContext_MaxCommitsTruncates(t *testing.T) {
	items := make([]scmItem, 5)
	for i := range items {
		items[i] = scmItem{
			CommitID: "0000000", Author: scmAuthor{FullName: "alice"}, Msg: "commit",
			Paths: []scmPath{{File: "x.go", EditType: "edit"}},
		}
	}
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {ChangeSets: []scmChangeSet{{Items: items}}},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:    "svc",
		MaxCommits: 2,
	})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "stopped at max_commits=2") {
		t.Errorf("expected truncation footer, got:\n%s", out)
	}
	// Exactly 2 commit header lines should be rendered.
	if got := strings.Count(out, " alice "); got != 2 {
		t.Errorf("expected 2 commit lines rendered, got %d:\n%s", got, out)
	}
}

func TestGetSCMContext_EmptyChangeSet(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {ChangeSets: []scmChangeSet{}},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "(no commits in change set)") {
		t.Errorf("expected no-commits hint, got:\n%s", out)
	}
}

func TestGetSCMContext_GracefulMissingFields(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{{
				Items: []scmItem{{
					// No CommitID, no Timestamp, no Author, empty editType.
					Msg:   "Mystery commit",
					Paths: []scmPath{{File: "weird.txt", EditType: ""}},
				}},
			}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{"(no id)", "(unknown)", "(no timestamp)", "?  weird.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q placeholder in output, got:\n%s", want, out)
		}
	}
}

func TestGetSCMContext_InvalidPathFilter(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{})
	defer srv.Close()

	_, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{
		JobPath:    "svc",
		PathFilter: "[invalid",
	})
	if err == nil {
		t.Fatal("expected error from invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "path_filter") {
		t.Errorf("expected error to mention path_filter, got: %v", err)
	}
}

func TestGetSCMContext_MissingJobPath(t *testing.T) {
	d := Deps{}
	_, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{})
	if err == nil {
		t.Fatal("expected error for empty job_path, got nil")
	}
}

func TestGetSCMContext_MultilineCommitMsg(t *testing.T) {
	d, srv := newSCMDeps(t, map[string]scmBuildAPI{
		"/job/svc/lastBuild/api/json": {
			ChangeSets: []scmChangeSet{{
				Items: []scmItem{{
					CommitID: "abc1234", Author: scmAuthor{FullName: "alice"},
					Msg:   "Subject line\n\nA longer body that the table\nshould not echo verbatim.",
					Paths: []scmPath{{File: "x.go", EditType: "edit"}},
				}},
			}},
		},
	})
	defer srv.Close()

	res, _, err := d.GetSCMContext(context.Background(), nil, GetSCMContextInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetSCMContext: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "\"Subject line\"") {
		t.Errorf("expected just the subject line in quotes, got:\n%s", out)
	}
	if strings.Contains(out, "longer body") {
		t.Errorf("commit body must not appear in the per-commit header line, got:\n%s", out)
	}
}

func TestEditCode(t *testing.T) {
	cases := map[string]string{
		"add":     "A",
		"edit":    "M",
		"modify":  "M",
		"delete":  "D",
		"":        "?",
		"rename":  "R",
		"copy":    "C",
		"unknown": "U",
	}
	for in, want := range cases {
		if got := editCode(in); got != want {
			t.Errorf("editCode(%q) = %q, want %q", in, got, want)
		}
	}
}
