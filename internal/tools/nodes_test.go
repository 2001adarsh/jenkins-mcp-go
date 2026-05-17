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

func newDepsAgainstHandler(t *testing.T, h http.Handler) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

func TestListNodes_Render(t *testing.T) {
	body := apiComputerListing{Computer: []apiComputer{
		{
			DisplayName:    "built-in",
			NumExecutors:   2,
			Idle:           true,
			AssignedLabels: []apiLabel{{Name: "master"}, {Name: "linux"}},
		},
		{
			DisplayName:        "agent-1",
			Offline:            true,
			OfflineCauseReason: "Disconnected by admin",
			NumExecutors:       4,
			AssignedLabels:     []apiLabel{{Name: "build"}},
		},
	}}
	d, srv := newDepsAgainstHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/computer/api/json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	res, _, err := d.ListNodes(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{"built-in", "agent-1", "offline", "Disconnected by admin", "linux,master"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestGetNode_EscapesParens(t *testing.T) {
	var seenURI string
	d, srv := newDepsAgainstHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURI = r.RequestURI
		_ = json.NewEncoder(w).Encode(apiComputer{
			DisplayName:  "controller",
			NumExecutors: 1,
			Executors:    []apiExecutor{{Idle: true}},
			MonitorData: map[string]any{
				"hudson.node_monitors.ResponseTimeMonitor": map[string]any{"average": 12},
			},
		})
	}))
	defer srv.Close()

	res, _, err := d.GetNode(context.Background(), nil, GetNodeInput{Name: "(built-in)"})
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !strings.Contains(seenURI, "%28built-in%29") {
		t.Errorf("expected parenthesized node name to be URL-escaped, got URI %q", seenURI)
	}
	out := resultText(t, res)
	if !strings.Contains(out, "1 total, 1 idle") {
		t.Errorf("expected executor counts in output, got:\n%s", out)
	}
	if !strings.Contains(out, "ResponseTimeMonitor") {
		t.Errorf("expected monitor data in output, got:\n%s", out)
	}
}

func TestGetNode_RequiresName(t *testing.T) {
	d := Deps{}
	_, _, err := d.GetNode(context.Background(), nil, GetNodeInput{})
	if err == nil {
		t.Fatal("expected error when name is empty")
	}
}
