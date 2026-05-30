package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

type prBuildsFixture struct {
	bodies   map[string]string
	statuses map[string]int
}

func newPRBuildsDeps(t *testing.T, f prBuildsFixture) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func multibranchClassJSON() string {
	return fmt.Sprintf(`{"_class":%q}`, multibranchClass)
}

func TestListPRBuilds_HappyPath_PRBranchFound(t *testing.T) {
	// The branch-name probe and the build-list query both hit the same
	// path; only the tree= query string differs. The handler switches on
	// `tree=builds` to return the right body.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/team/job/svc/api/json":
			_, _ = w.Write([]byte(multibranchClassJSON()))
		case "/job/team/job/svc/job/PR-123/api/json":
			if strings.Contains(r.URL.RawQuery, "tree=builds") {
				_, _ = w.Write([]byte(`{"builds":[
					{"number":5,"result":"SUCCESS","timestamp":1716499200000,"duration":182000,"url":"u5"},
					{"number":4,"result":"FAILURE","timestamp":1716475200000,"duration":165000,"url":"u4"}
				]}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"PR-123"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cli, _ := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	d := Deps{Client: cli}

	res, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{
		JobPath: "team/svc", PRNumber: 123,
	})
	if err != nil {
		t.Fatalf("ListPRBuilds: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"Builds for PR #123 of team/svc (branch: PR-123)",
		"#5",
		"SUCCESS",
		"3m2s",
		"#4",
		"FAILURE",
		"2 builds across the PR's lifetime",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func newPRBuildsHandler(jobPath, branchName, buildsJSON string) http.Handler {
	jobAPI := jenkins.JobAPIPath(jobPath)
	branchPath := jobAPI + "/job/" + branchName + "/api/json"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case jobAPI + "/api/json":
			_, _ = w.Write([]byte(multibranchClassJSON()))
		case branchPath:
			if strings.Contains(r.URL.RawQuery, "tree=builds") {
				_, _ = w.Write([]byte(buildsJSON))
				return
			}
			_, _ = fmt.Fprintf(w, `{"name":%q}`, branchName)
		default:
			http.NotFound(w, r)
		}
	})
}

func TestListPRBuilds_DetectsGitHubPullRequestNaming(t *testing.T) {
	srv := httptest.NewServer(newPRBuildsHandler("svc", "pull/123/head",
		`{"builds":[{"number":1,"result":"SUCCESS","timestamp":1716499200000,"duration":60000,"url":"u1"}]}`))
	defer srv.Close()
	cli, _ := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	d := Deps{Client: cli}

	res, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{
		JobPath: "svc", PRNumber: 123,
	})
	if err != nil {
		t.Fatalf("ListPRBuilds: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "branch: pull/123/head") {
		t.Errorf("expected pull/123/head detected, got:\n%s", out)
	}
}

func TestListPRBuilds_DetectsGerritChangeNaming(t *testing.T) {
	srv := httptest.NewServer(newPRBuildsHandler("svc", "change-456",
		`{"builds":[{"number":1,"result":"SUCCESS","timestamp":1716499200000,"duration":60000,"url":"u1"}]}`))
	defer srv.Close()
	cli, _ := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	d := Deps{Client: cli}

	res, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{
		JobPath: "svc", PRNumber: 456,
	})
	if err != nil {
		t.Fatalf("ListPRBuilds: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "branch: change-456") {
		t.Errorf("expected change-456 detected, got:\n%s", out)
	}
}

func TestListPRBuilds_NoBranchMatchesReturnsHint(t *testing.T) {
	d, srv := newPRBuildsDeps(t, prBuildsFixture{
		bodies: map[string]string{
			"/job/svc/api/json": multibranchClassJSON(),
		},
	})
	defer srv.Close()

	res, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{
		JobPath: "svc", PRNumber: 999,
	})
	if err != nil {
		t.Fatalf("ListPRBuilds: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"no branch found for PR #999",
		"PR-999",
		"pull/999/head",
		"change-999",
		"pr/999",
		"list_branches",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in hint:\n%s", want, out)
		}
	}
}

func TestListPRBuilds_NotMultibranchReturnsHint(t *testing.T) {
	d, srv := newPRBuildsDeps(t, prBuildsFixture{
		bodies: map[string]string{
			"/job/svc/api/json": `{"_class":"hudson.model.FreeStyleProject"}`,
		},
	})
	defer srv.Close()

	res, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{
		JobPath: "svc", PRNumber: 123,
	})
	if err != nil {
		t.Fatalf("ListPRBuilds: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"not a WorkflowMultiBranchProject",
		"list_branches",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in hint:\n%s", want, out)
		}
	}
}

func TestListPRBuilds_MaxBuildsCaps(t *testing.T) {
	// Server returns exactly what the tree query asked for (Jenkins
	// behavior — {0,N} truncates). Verify the request *asked* for the
	// requested cap.
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/svc/api/json":
			_, _ = w.Write([]byte(multibranchClassJSON()))
		case "/job/svc/job/PR-1/api/json":
			if strings.Contains(r.URL.RawQuery, "tree=builds") {
				capturedQuery = r.URL.RawQuery
				_, _ = w.Write([]byte(`{"builds":[{"number":1,"result":"SUCCESS","timestamp":1,"duration":1,"url":"u"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"PR-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cli, _ := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	d := Deps{Client: cli}

	_, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{
		JobPath: "svc", PRNumber: 1, MaxBuilds: 7,
	})
	if err != nil {
		t.Fatalf("ListPRBuilds: %v", err)
	}
	decoded, _ := url.QueryUnescape(capturedQuery)
	if !strings.Contains(decoded, "{0,7}") {
		t.Errorf("expected tree range {0,7} sent in build query, got: %q", decoded)
	}
}

func TestListPRBuilds_MissingJobPath(t *testing.T) {
	d := Deps{}
	if _, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{PRNumber: 1}); err == nil {
		t.Fatal("expected error for empty job_path")
	}
}

func TestListPRBuilds_InvalidPRNumber(t *testing.T) {
	d := Deps{}
	if _, _, err := d.ListPRBuilds(context.Background(), nil, ListPRBuildsInput{JobPath: "svc"}); err == nil {
		t.Fatal("expected error for pr_number=0")
	}
}
