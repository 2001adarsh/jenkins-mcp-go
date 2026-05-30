package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// pluginsMock describes a fake /pluginManager/api/json response.
type pluginsMock struct {
	status int    // when non-zero, returned as the HTTP code (body ignored).
	body   string // raw JSON returned when status is zero or 200.
}

func newPluginsDeps(t *testing.T, m pluginsMock) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pluginManager/api/json" {
			http.NotFound(w, r)
			return
		}
		if m.status != 0 && m.status != http.StatusOK {
			w.WriteHeader(m.status)
			return
		}
		_, _ = w.Write([]byte(m.body))
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{
		Client: cli,
		Cache:  &jenkins.ConsoleCache{Dir: "/tmp/x"},
	}, srv
}

func TestGetPluginVersions_NameFilterNarrows(t *testing.T) {
	d, srv := newPluginsDeps(t, pluginsMock{
		body: `{"plugins":[
			{"shortName":"git","longName":"Git plugin","version":"5.0.0","active":true,"enabled":true},
			{"shortName":"git-client","longName":"Git client plugin","version":"4.5.0","active":true,"enabled":true},
			{"shortName":"junit","longName":"JUnit","version":"1234.v","active":true,"enabled":true}
		]}`,
	})
	defer srv.Close()

	res, _, err := d.GetPluginVersions(context.Background(), nil, GetPluginVersionsInput{
		NameFilter: "^git",
	})
	if err != nil {
		t.Fatalf("GetPluginVersions: %v", err)
	}
	text := resultText(t, res)

	if !strings.Contains(text, "git ") || !strings.Contains(text, "git-client") {
		t.Errorf("expected git and git-client rows, got:\n%s", text)
	}
	if strings.Contains(text, "junit ") {
		t.Errorf("junit should be filtered out, got:\n%s", text)
	}
	if !strings.Contains(text, "2 of 3 plugins shown") {
		t.Errorf("expected 2-of-3 footer, got:\n%s", text)
	}
	if !strings.Contains(text, `name_filter="^git"`) {
		t.Errorf("expected name_filter in header, got:\n%s", text)
	}
}

func TestGetPluginVersions_ActiveOnlyByDefault(t *testing.T) {
	d, srv := newPluginsDeps(t, pluginsMock{
		body: `{"plugins":[
			{"shortName":"alive","version":"1.0","active":true,"enabled":true},
			{"shortName":"zombie","version":"0.1","active":false,"enabled":false}
		]}`,
	})
	defer srv.Close()

	res, _, err := d.GetPluginVersions(context.Background(), nil, GetPluginVersionsInput{})
	if err != nil {
		t.Fatalf("GetPluginVersions: %v", err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "alive") {
		t.Errorf("active plugin missing, got:\n%s", text)
	}
	if strings.Contains(text, "zombie") {
		t.Errorf("inactive plugin should be hidden by default, got:\n%s", text)
	}
}

func TestGetPluginVersions_IncludeInactiveShowsAll(t *testing.T) {
	d, srv := newPluginsDeps(t, pluginsMock{
		body: `{"plugins":[
			{"shortName":"alive","version":"1.0","active":true,"enabled":true},
			{"shortName":"zombie","version":"0.1","active":false,"enabled":false}
		]}`,
	})
	defer srv.Close()

	res, _, err := d.GetPluginVersions(context.Background(), nil, GetPluginVersionsInput{
		IncludeInactive: true,
	})
	if err != nil {
		t.Fatalf("GetPluginVersions: %v", err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "zombie") {
		t.Errorf("inactive plugin should appear when include_inactive=true, got:\n%s", text)
	}
	if !strings.Contains(text, "active") || !strings.Contains(text, "enabled") {
		t.Errorf("expected active/enabled columns under include_inactive, got:\n%s", text)
	}
}

func TestGetPluginVersions_403DegradesGracefully(t *testing.T) {
	d, srv := newPluginsDeps(t, pluginsMock{status: http.StatusForbidden})
	defer srv.Close()

	res, _, err := d.GetPluginVersions(context.Background(), nil, GetPluginVersionsInput{})
	if err != nil {
		t.Fatalf("403 should not error, got: %v", err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "lacks admin permission") {
		t.Errorf("expected admin-permission hint, got:\n%s", text)
	}
}

func TestGetPluginVersions_InvalidFilterErrors(t *testing.T) {
	d, srv := newPluginsDeps(t, pluginsMock{body: `{"plugins":[]}`})
	defer srv.Close()

	_, _, err := d.GetPluginVersions(context.Background(), nil, GetPluginVersionsInput{
		NameFilter: "(unclosed",
	})
	if err == nil {
		t.Fatalf("expected error for invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "name_filter") {
		t.Errorf("expected error to mention name_filter, got: %v", err)
	}
}

func TestGetPluginVersions_CapsAt200(t *testing.T) {
	// Generate 250 fake plugins active and matching no filter.
	var b strings.Builder
	b.WriteString(`{"plugins":[`)
	for i := range 250 {
		if i > 0 {
			b.WriteString(",")
		}
		// Pad name to keep names sortable in lexical order.
		fmt.Fprintf(&b,
			`{"shortName":"plug-%03d","version":"1.0","active":true,"enabled":true}`,
			i)
	}
	b.WriteString(`]}`)

	d, srv := newPluginsDeps(t, pluginsMock{body: b.String()})
	defer srv.Close()

	res, _, err := d.GetPluginVersions(context.Background(), nil, GetPluginVersionsInput{})
	if err != nil {
		t.Fatalf("GetPluginVersions: %v", err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "200 of 250 plugins shown") {
		t.Errorf("expected '200 of 250 plugins shown' in header, got:\n%s", firstLines(text, 3))
	}
	if !strings.Contains(text, "truncated to 200 rows") {
		t.Errorf("expected truncation footer, got:\n%s", text)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func TestGetPluginVersions_RendersActiveSorted(t *testing.T) {
	d, srv := newPluginsDeps(t, pluginsMock{
		body: `{"plugins":[
			{"shortName":"workflow-aggregator","longName":"Pipeline","version":"600.v","active":true,"enabled":true,"hasUpdate":false,"pinned":false},
			{"shortName":"git","longName":"Git plugin","version":"5.0.0","active":true,"enabled":true,"hasUpdate":true,"pinned":true},
			{"shortName":"junit","longName":"JUnit","version":"1234.v","active":true,"enabled":true,"hasUpdate":false,"pinned":false}
		]}`,
	})
	defer srv.Close()

	res, _, err := d.GetPluginVersions(context.Background(), nil, GetPluginVersionsInput{})
	if err != nil {
		t.Fatalf("GetPluginVersions: %v", err)
	}
	text := resultText(t, res)

	// Three plugins, sorted by shortName: git, junit, workflow-aggregator.
	gitIdx := strings.Index(text, "git ")
	junitIdx := strings.Index(text, "junit ")
	wfIdx := strings.Index(text, "workflow-aggregator")
	if gitIdx < 0 || junitIdx < 0 || wfIdx < 0 {
		t.Fatalf("missing plugin row in output:\n%s", text)
	}
	if gitIdx >= junitIdx || junitIdx >= wfIdx {
		t.Errorf("rows not sorted by shortName:\n%s", text)
	}
	if !strings.Contains(text, "3 of 3 plugins shown") {
		t.Errorf("expected count footer, got:\n%s", text)
	}
}
