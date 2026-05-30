package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

const testJobPath = "team/my-job"

// permsMock describes the fake Jenkins responses each whoami_can probe sees.
// A zero status field means "use the most permissive default for that probe":
// /me 200, /api/json 200, /build & /buildWithParameters & /lastBuild/stop 405
// (permission granted, wrong verb), /configure 200. Tests override only the
// fields they care about.
type permsMock struct {
	meJSON       string
	meStatus     int
	apiJSON      string
	apiStatus    int
	buildStatus  int
	bwpStatus    int
	cancelStatus int
	configStatus int
}

func newPermsDeps(t *testing.T, m permsMock) (Deps, *httptest.Server) {
	t.Helper()
	jobAPI := jenkins.JobAPIPath(testJobPath)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/api/json":
			if m.meStatus != 0 && m.meStatus != http.StatusOK {
				w.WriteHeader(m.meStatus)
				return
			}
			body := m.meJSON
			if body == "" {
				body = `{"fullName":"Alice","id":"alice"}`
			}
			_, _ = w.Write([]byte(body))
		case jobAPI + "/api/json":
			if m.apiStatus != 0 && m.apiStatus != http.StatusOK {
				w.WriteHeader(m.apiStatus)
				return
			}
			body := m.apiJSON
			if body == "" {
				body = `{"_class":"hudson.model.FreeStyleProject","lastBuild":{"number":42}}`
			}
			_, _ = w.Write([]byte(body))
		case jobAPI + "/build":
			respondProbe(w, m.buildStatus, http.StatusMethodNotAllowed)
		case jobAPI + "/buildWithParameters":
			respondProbe(w, m.bwpStatus, http.StatusMethodNotAllowed)
		case jobAPI + "/lastBuild/stop":
			respondProbe(w, m.cancelStatus, http.StatusMethodNotAllowed)
		case jobAPI + "/configure":
			respondProbe(w, m.configStatus, http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{
		Client:   cli,
		Cache:    &jenkins.ConsoleCache{Dir: "/tmp/x", MaxBytes: 0},
		Version:  "test",
		ReadOnly: true,
	}, srv
}

func respondProbe(w http.ResponseWriter, override, def int) {
	code := override
	if code == 0 {
		code = def
	}
	if code == http.StatusOK {
		_, _ = w.Write([]byte("ok"))
		return
	}
	w.WriteHeader(code)
}

func TestWhoamiCan_AllOKForFullAccessUser(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"Permissions for alice (Alice) on " + testJobPath,
		"read       OK",
		"build      OK",
		"cancel     OK",
		"configure  OK",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestWhoamiCan_ReadDeniedRendersDENIED(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{apiStatus: http.StatusForbidden})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "read       DENIED") {
		t.Errorf("expected read DENIED, got:\n%s", out)
	}
	// Other probes must still run — read denial doesn't short-circuit.
	if !strings.Contains(out, "build      OK") {
		t.Errorf("expected build OK to still appear, got:\n%s", out)
	}
}

func TestWhoamiCan_BuildFallsThroughFromBuildToBuildWithParameters(t *testing.T) {
	// Parameterless job: /build is 405 (would work via POST); /buildWithParameters 404.
	// Either-success means build OK.
	d, srv := newPermsDeps(t, permsMock{bwpStatus: http.StatusNotFound})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "build      OK") {
		t.Errorf("expected build OK via fallthrough, got:\n%s", out)
	}
}

func TestWhoamiCan_BuildDeniedWhenBoth403(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{
		buildStatus: http.StatusForbidden,
		bwpStatus:   http.StatusForbidden,
	})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "build      DENIED") {
		t.Errorf("expected build DENIED, got:\n%s", out)
	}
}

func TestWhoamiCan_CancelNAWhenNoLastBuild(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{
		apiJSON: `{"_class":"hudson.model.FreeStyleProject"}`,
	})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "cancel     N/A (no last build)") {
		t.Errorf("expected cancel N/A (no last build), got:\n%s", out)
	}
}

func TestWhoamiCan_FolderMarksBuildAndCancelNA(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{
		apiJSON: `{"_class":"com.cloudbees.hudson.plugins.folder.Folder"}`,
	})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"build      N/A (folder)",
		"cancel     N/A (folder)",
		"configure  OK",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in folder output:\n%s", want, out)
		}
	}
}

func TestWhoamiCan_AnonymousUserRendersAnonymous(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{meStatus: http.StatusUnauthorized})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Permissions for anonymous on") {
		t.Errorf("expected 'Permissions for anonymous', got:\n%s", out)
	}
}

func TestWhoamiCan_Transport5xxYieldsUNKNOWN(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{configStatus: http.StatusBadGateway})
	defer srv.Close()

	res, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: testJobPath})
	if err != nil {
		t.Fatalf("WhoamiCan: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "configure  UNKNOWN") {
		t.Errorf("expected configure UNKNOWN on 502, got:\n%s", out)
	}
}

func TestWhoamiCan_MissingJobPathErrors(t *testing.T) {
	d, srv := newPermsDeps(t, permsMock{})
	defer srv.Close()

	if _, _, err := d.WhoamiCan(context.Background(), nil, WhoamiCanInput{JobPath: ""}); err == nil {
		t.Errorf("expected error for empty job_path")
	}
}
