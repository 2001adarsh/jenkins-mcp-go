package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newLifecycleHandler returns a Deps wired to a test server with a crumb
// endpoint plus the caller-provided routes.
func newLifecycleHandler(t *testing.T, routes map[string]http.HandlerFunc) (Deps, *httptest.Server) {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"crumb":             "xyz",
				"crumbRequestField": "Jenkins-Crumb",
			})
			return
		}
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		http.NotFound(w, r)
	})
	d, srv := newDepsAgainstHandler(t, h)
	return d, srv
}

func TestTriggerBuild_NoParams(t *testing.T) {
	var triggered atomic.Bool
	d, srv := newLifecycleHandler(t, map[string]http.HandlerFunc{
		"/job/team/job/api-tests/build": func(w http.ResponseWriter, r *http.Request) {
			triggered.Store(true)
			if r.Header.Get("Jenkins-Crumb") != "xyz" {
				t.Errorf("expected crumb header on trigger POST, got %q", r.Header.Get("Jenkins-Crumb"))
			}
			w.Header().Set("Location", "http://j/queue/item/77/")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer srv.Close()

	res, _, err := d.TriggerBuild(context.Background(), nil, TriggerBuildInput{JobPath: "team/api-tests"})
	if err != nil {
		t.Fatalf("TriggerBuild: %v", err)
	}
	if !triggered.Load() {
		t.Fatal("expected /build to be hit")
	}
	out := resultText(t, res)
	for _, want := range []string{"Build queued for team/api-tests", "/queue/item/77/", "Queue item id: 77"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestTriggerBuild_WithParamsAndWait(t *testing.T) {
	var seenForm string
	d, srv := newLifecycleHandler(t, map[string]http.HandlerFunc{
		"/job/team/job/deploy/buildWithParameters": func(w http.ResponseWriter, r *http.Request) {
			body := make([]byte, r.ContentLength)
			if r.ContentLength > 0 {
				_, _ = r.Body.Read(body)
			}
			seenForm = string(body)
			w.Header().Set("Location", "http://j/queue/item/123/")
			w.WriteHeader(http.StatusCreated)
		},
		"/queue/item/123/api/json": func(w http.ResponseWriter, r *http.Request) {
			// Return executable on the first poll.
			_ = json.NewEncoder(w).Encode(queueItemResponse{
				ID:         123,
				Executable: &queueItemExecutable{Number: 451, URL: "http://j/job/team/job/deploy/451/"},
			})
		},
	})
	defer srv.Close()

	res, _, err := d.TriggerBuild(context.Background(), nil, TriggerBuildInput{
		JobPath:      "team/deploy",
		Parameters:   map[string]string{"BRANCH": "main", "DRY_RUN": "true"},
		WaitForStart: true,
	})
	if err != nil {
		t.Fatalf("TriggerBuild: %v", err)
	}
	if !strings.Contains(seenForm, "BRANCH=main") || !strings.Contains(seenForm, "DRY_RUN=true") {
		t.Errorf("expected form to carry both params, got %q", seenForm)
	}
	out := resultText(t, res)
	for _, want := range []string{"Started: build #451", "Executor URL: http://j/job/team/job/deploy/451/"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestTriggerBuild_RejectsEmptyJobPath(t *testing.T) {
	d := Deps{}
	_, _, err := d.TriggerBuild(context.Background(), nil, TriggerBuildInput{})
	if err == nil {
		t.Fatal("expected error for empty job_path")
	}
}

func TestExtractQueueItemID(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"http://j/queue/item/77/", "77"},
		{"http://j/queue/item/123", "123"},
		{"", ""},
		{"http://j/job/foo/", ""},
	} {
		if got := extractQueueItemID(c.in); got != c.want {
			t.Errorf("extractQueueItemID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStopBuild_Success(t *testing.T) {
	var hit atomic.Bool
	d, srv := newLifecycleHandler(t, map[string]http.HandlerFunc{
		"/job/team/job/deploy/42/stop": func(w http.ResponseWriter, r *http.Request) {
			hit.Store(true)
			if r.Header.Get("Jenkins-Crumb") != "xyz" {
				t.Errorf("expected crumb header on stop POST, got %q", r.Header.Get("Jenkins-Crumb"))
			}
			w.WriteHeader(http.StatusOK)
		},
	})
	defer srv.Close()

	res, _, err := d.StopBuild(context.Background(), nil, StopBuildInput{JobPath: "team/deploy", BuildNumber: 42})
	if err != nil {
		t.Fatalf("StopBuild: %v", err)
	}
	if !hit.Load() {
		t.Fatal("expected /stop to be hit")
	}
	out := resultText(t, res)
	if !strings.Contains(out, "Requested stop of build team/deploy #42") {
		t.Errorf("expected stop confirmation, got:\n%s", out)
	}
}

func TestStopBuild_RejectsZeroBuildNumber(t *testing.T) {
	d := Deps{}
	_, _, err := d.StopBuild(context.Background(), nil, StopBuildInput{JobPath: "team/x", BuildNumber: 0})
	if err == nil {
		t.Fatal("expected error for build_number=0")
	}
}
