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

// newBranchesDeps wires Deps against an httptest server that serves the
// given multibranch response keyed by request path.
func newBranchesDeps(t *testing.T, responses map[string]multibranchAPI) (Deps, *httptest.Server) {
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

func TestListBranches_HappyPath(t *testing.T) {
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{
		"/job/team/job/svc-x/api/json": {
			Class: multibranchClass,
			Jobs: []branchAPIJob{
				{
					Name: "main", URL: "https://j/job/team/job/svc-x/job/main/",
					LastBuild: &branchAPILastBuild{
						Number: 42, Result: "SUCCESS",
						Duration: 135000, Timestamp: 1716387120000,
					},
				},
				{
					Name: "feature/login", URL: "https://j/job/team/job/svc-x/job/feature%2Flogin/",
					LastBuild: &branchAPILastBuild{
						Number: 17, Result: "FAILURE",
						Duration: 62000, Timestamp: 1716380000000,
					},
				},
				{
					Name: "PR-89", URL: "https://j/job/team/job/svc-x/job/PR-89/",
					// No LastBuild — newly indexed branch.
				},
			},
		},
	})
	defer srv.Close()

	res, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{JobPath: "team/svc-x"})
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	out := resultText(t, res)

	for _, want := range []string{
		"Branches in svc-x (multibranch) — 3 total",
		"main",
		"feature/login",
		"PR-89",
		"42",
		"SUCCESS",
		"2m15s",
		"17",
		"FAILURE",
		"1m2s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Never-built branch must render dashes, not zeros.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "PR-89") {
			// Count "-" tokens — the row has 4 of them (last#, result, duration, last_built_at).
			if strings.Count(line, " -") < 4 {
				t.Errorf("expected PR-89 row to dash-fill missing lastBuild columns, got: %q", line)
			}
			return
		}
	}
	t.Errorf("PR-89 row missing in:\n%s", out)
}

func TestListBranches_NameFilter(t *testing.T) {
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{
		"/job/team/job/svc-x/api/json": {
			Class: multibranchClass,
			Jobs: []branchAPIJob{
				{Name: "main", LastBuild: &branchAPILastBuild{Number: 1, Result: "SUCCESS"}},
				{Name: "feature/login", LastBuild: &branchAPILastBuild{Number: 2, Result: "SUCCESS"}},
				{Name: "feature/payment", LastBuild: &branchAPILastBuild{Number: 3, Result: "FAILURE"}},
				{Name: "PR-42", LastBuild: &branchAPILastBuild{Number: 4, Result: "SUCCESS"}},
			},
		},
	})
	defer srv.Close()

	res, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{
		JobPath:    "team/svc-x",
		NameFilter: "^feature/",
	})
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "4 total, 2 shown") {
		t.Errorf("expected '4 total, 2 shown' summary, got:\n%s", out)
	}
	if !strings.Contains(out, "feature/login") || !strings.Contains(out, "feature/payment") {
		t.Errorf("expected both feature branches, got:\n%s", out)
	}
	if strings.Contains(out, "main") || strings.Contains(out, "PR-42") {
		t.Errorf("filter must exclude main and PR-42, got:\n%s", out)
	}
}

func TestListBranches_HealthyOnly(t *testing.T) {
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{
		"/job/team/job/svc-x/api/json": {
			Class: multibranchClass,
			Jobs: []branchAPIJob{
				{Name: "main", LastBuild: &branchAPILastBuild{Number: 1, Result: "SUCCESS"}},
				{Name: "broken", LastBuild: &branchAPILastBuild{Number: 2, Result: "FAILURE"}},
				{Name: "unstable", LastBuild: &branchAPILastBuild{Number: 3, Result: "UNSTABLE"}},
				{Name: "new"}, // never built
			},
		},
	})
	defer srv.Close()

	res, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{
		JobPath:     "team/svc-x",
		HealthyOnly: true,
	})
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "4 total, 1 shown") {
		t.Errorf("expected '4 total, 1 shown', got:\n%s", out)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("expected main (SUCCESS) to remain, got:\n%s", out)
	}
	for _, dropped := range []string{"broken", "unstable", "new"} {
		if strings.Contains(out, dropped) {
			t.Errorf("healthy_only must drop %q (FAILURE/UNSTABLE/never-built), got:\n%s", dropped, out)
		}
	}
}

func TestListBranches_NotMultibranch(t *testing.T) {
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{
		"/job/team/job/regular-job/api/json": {
			Class: "hudson.model.FreeStyleProject",
			Jobs:  nil,
		},
	})
	defer srv.Close()

	res, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{
		JobPath: "team/regular-job",
	})
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "FreeStyleProject") {
		t.Errorf("expected short class name in hint, got:\n%s", out)
	}
	if !strings.Contains(out, "list_jobs") {
		t.Errorf("hint must point back at list_jobs, got:\n%s", out)
	}
	// A table header must not be rendered for a non-multibranch path.
	if strings.Contains(out, "last_built_at") {
		t.Errorf("did not expect branch table for non-multibranch, got:\n%s", out)
	}
}

func TestListBranches_OrgFolderRespondsWithHint(t *testing.T) {
	// OrganizationFolders contain multibranches, not branches. Distinct
	// case from a leaf job — confirms the hint path also handles folders.
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{
		"/job/orgs/job/acme/api/json": {
			Class: "jenkins.branch.OrganizationFolder",
		},
	})
	defer srv.Close()

	res, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{JobPath: "orgs/acme"})
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "OrganizationFolder") {
		t.Errorf("expected OrganizationFolder in hint, got:\n%s", out)
	}
}

func TestListBranches_InvalidNameFilter(t *testing.T) {
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{})
	defer srv.Close()

	_, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{
		JobPath:    "team/svc-x",
		NameFilter: "[invalid",
	})
	if err == nil {
		t.Fatal("expected error from invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "name_filter") {
		t.Errorf("expected error to mention name_filter, got: %v", err)
	}
}

func TestListBranches_MissingJobPath(t *testing.T) {
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{})
	defer srv.Close()

	_, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{})
	if err == nil {
		t.Fatal("expected error for empty job_path, got nil")
	}
}

func TestListBranches_EmptyMultibranch(t *testing.T) {
	d, srv := newBranchesDeps(t, map[string]multibranchAPI{
		"/job/team/job/svc-x/api/json": {
			Class: multibranchClass,
			Jobs:  nil,
		},
	})
	defer srv.Close()

	res, _, err := d.ListBranches(context.Background(), nil, ListBranchesInput{JobPath: "team/svc-x"})
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	out := resultText(t, res)

	if !strings.Contains(out, "0 total") {
		t.Errorf("expected '0 total' summary, got:\n%s", out)
	}
	if !strings.Contains(out, "(no branches matched)") {
		t.Errorf("expected '(no branches matched)' hint, got:\n%s", out)
	}
}
