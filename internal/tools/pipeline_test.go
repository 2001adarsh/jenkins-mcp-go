package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

func newPipelineDeps(t *testing.T, handler http.HandlerFunc) (Deps, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	cli, err := jenkins.NewClient(jenkins.Config{BaseURL: srv.URL, User: "u", Token: "t"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return Deps{Client: cli}, srv
}

func TestGetPipelineScript_ReplaySucceedsAndUnescapes(t *testing.T) {
	htmlBody := `<html><body><form>
<textarea name="mainScript" rows="10">pipeline {
  agent any
  stages {
    stage(&#39;Build&#39;) {
      steps { sh &#39;make build&#39; }
    }
  }
}</textarea>
</form></body></html>`
	d, srv := newPipelineDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/job/team/job/svc/42/replay/" {
			_, _ = w.Write([]byte(htmlBody))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	res, _, err := d.GetPipelineScript(context.Background(), nil, GetPipelineScriptInput{
		JobPath: "team/svc", BuildNumber: 42,
	})
	if err != nil {
		t.Fatalf("GetPipelineScript: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"Pipeline script for team/svc build #42 (source: replay)",
		"stage('Build')",
		"sh 'make build'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestGetPipelineScript_Replay404FallsThroughToConfigXml(t *testing.T) {
	xmlBody := `<?xml version="1.0" encoding="UTF-8"?>
<flow-definition>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition" plugin="workflow-cps@2.45">
    <script>pipeline { agent any }</script>
    <sandbox>true</sandbox>
  </definition>
</flow-definition>`
	d, srv := newPipelineDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/svc/lastBuild/replay/":
			http.NotFound(w, r)
		case "/job/svc/config.xml":
			_, _ = w.Write([]byte(xmlBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, _, err := d.GetPipelineScript(context.Background(), nil, GetPipelineScriptInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetPipelineScript: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"(source: job-config-fallback)",
		"build-pinned source unavailable",
		"pipeline { agent any }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestGetPipelineScript_Replay403FallsThroughToConfigXml(t *testing.T) {
	xmlBody := `<?xml version="1.0"?>
<flow-definition>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition">
    <script>node { echo &quot;hi&quot; }</script>
  </definition>
</flow-definition>`
	d, srv := newPipelineDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/svc/lastBuild/replay/":
			w.WriteHeader(http.StatusForbidden)
		case "/job/svc/config.xml":
			_, _ = w.Write([]byte(xmlBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, _, err := d.GetPipelineScript(context.Background(), nil, GetPipelineScriptInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetPipelineScript: %v", err)
	}
	out := resultText(t, res)
	if !strings.Contains(out, `node { echo "hi" }`) {
		t.Errorf("expected XML-unescaped script in fallback, got:\n%s", out)
	}
}

func TestGetPipelineScript_PipelineFromSCMReturnsHint(t *testing.T) {
	xmlBody := `<?xml version="1.0"?>
<flow-definition>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition" plugin="workflow-cps@2.45">
    <scm class="hudson.plugins.git.GitSCM">
      <userRemoteConfigs>
        <hudson.plugins.git.UserRemoteConfig>
          <url>git@github.com:foo/bar.git</url>
        </hudson.plugins.git.UserRemoteConfig>
      </userRemoteConfigs>
      <branches>
        <hudson.plugins.git.BranchSpec>
          <name>main</name>
        </hudson.plugins.git.BranchSpec>
      </branches>
    </scm>
    <scriptPath>ci/Jenkinsfile</scriptPath>
  </definition>
</flow-definition>`
	d, srv := newPipelineDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/svc/lastBuild/replay/":
			http.NotFound(w, r)
		case "/job/svc/config.xml":
			_, _ = w.Write([]byte(xmlBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, _, err := d.GetPipelineScript(context.Background(), nil, GetPipelineScriptInput{JobPath: "svc"})
	if err != nil {
		t.Fatalf("GetPipelineScript: %v", err)
	}
	out := resultText(t, res)
	for _, want := range []string{
		"build-pinned source unavailable",
		"Pipeline from SCM",
		"git@github.com:foo/bar.git",
		"ci/Jenkinsfile",
		"main",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in SCM-hint output:\n%s", want, out)
		}
	}
}

func TestGetPipelineScript_BothFailReturnsError(t *testing.T) {
	d, srv := newPipelineDeps(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, _, err := d.GetPipelineScript(context.Background(), nil, GetPipelineScriptInput{
		JobPath: "svc", BuildNumber: 42,
	})
	if err == nil {
		t.Fatal("expected error when both Replay and config.xml fail")
	}
}

func TestGetPipelineScript_MissingJobPath(t *testing.T) {
	d := Deps{}
	if _, _, err := d.GetPipelineScript(context.Background(), nil, GetPipelineScriptInput{}); err == nil {
		t.Fatal("expected error for empty job_path")
	}
}
