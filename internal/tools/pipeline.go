package tools

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// wfapiStage is one stage entry in /wfapi/describe.
type wfapiStage struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	DurationMillis  int64  `json:"durationMillis"`
	StartTimeMillis int64  `json:"startTimeMillis"`
	ExecNode        string `json:"execNode"`
}

// wfapiDescribe is the top-level shape of /wfapi/describe.
type wfapiDescribe struct {
	Status         string       `json:"status"`
	DurationMillis int64        `json:"durationMillis"`
	Stages         []wfapiStage `json:"stages"`
}

// wfapiLog is the shape returned by /execution/node/<id>/wfapi/log.
type wfapiLog struct {
	NodeID     string `json:"nodeId"`
	NodeStatus string `json:"nodeStatus"`
	Length     int    `json:"length"`
	HasMore    bool   `json:"hasMore"`
	Text       string `json:"text"`
	ConsoleURL string `json:"consoleUrl"`
}

// GetPipelineStagesInput is the schema for get_pipeline_stages.
type GetPipelineStagesInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number" jsonschema:"Build number. Use 0 or omit for the latest build."`
}

// GetPipelineStages lists Declarative/Scripted Pipeline stages with status and
// duration via /wfapi/describe.
func (d Deps) GetPipelineStages(ctx context.Context, _ *mcp.CallToolRequest, in GetPipelineStagesInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) + "/wfapi/describe"
	body, err := d.Client.Get(ctx, path, nil)
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return textResult(
				"No pipeline stage data (HTTP 404 on /wfapi/describe). " +
					"This is not a Declarative/Scripted Pipeline build.",
			), nil, nil
		}
		return nil, nil, err
	}
	var desc wfapiDescribe
	if err := json.Unmarshal(body, &desc); err != nil {
		return nil, nil, fmt.Errorf("parse wfapi describe: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Pipeline status: %s (total %.1fs)\n\n", desc.Status, float64(desc.DurationMillis)/1000.0)
	fmt.Fprintf(&out, "%-4s  %-12s  %10s  %s\n", "id", "status", "duration", "name")
	fmt.Fprintf(&out, "%-4s  %-12s  %10s  %s\n", "----", "------------", "----------", "----")
	for _, s := range desc.Stages {
		fmt.Fprintf(&out, "%-4s  %-12s  %9.1fs  %s\n", s.ID, s.Status, float64(s.DurationMillis)/1000.0, s.Name)
	}
	out.WriteString(
		"\nUse get_stage_log with the stage id to fetch that stage's log " +
			"(some stages — especially declarative wrapper stages — have empty stage logs; " +
			"use get_console_log_path for the full log in that case).",
	)
	return textResult(out.String()), nil, nil
}

// GetStageLogInput is the schema for get_stage_log.
type GetStageLogInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number" jsonschema:"Build number. Use 0 or omit for the latest build."`
	StageID     string `json:"stage_id" jsonschema:"Stage id from get_pipeline_stages (e.g. \"188\")"`
}

// GetStageLog returns the log for a single pipeline stage.
func (d Deps) GetStageLog(ctx context.Context, _ *mcp.CallToolRequest, in GetStageLogInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" || in.StageID == "" {
		return nil, nil, fmt.Errorf("job_path and stage_id are required")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) +
		"/execution/node/" + in.StageID + "/wfapi/log"
	body, err := d.Client.Get(ctx, path, nil)
	if err != nil {
		if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
			return textResult(fmt.Sprintf(
				"No stage log at /execution/node/%s/wfapi/log (HTTP 404). "+
					"Stage id may be wrong or build is not a pipeline.",
				in.StageID,
			)), nil, nil
		}
		return nil, nil, err
	}
	var lg wfapiLog
	if err := json.Unmarshal(body, &lg); err != nil {
		return nil, nil, fmt.Errorf("parse wfapi log: %w", err)
	}
	if lg.Length == 0 {
		return textResult(fmt.Sprintf(
			"Stage %s (%s) has no captured log output (length=0). "+
				"This is common for declarative wrapper stages — "+
				"use get_console_log_path for the full build log.",
			in.StageID, lg.NodeStatus,
		)), nil, nil
	}
	return textResult(fmt.Sprintf(
		"Stage %s log (%s, %d bytes, hasMore=%v):\n\n%s",
		in.StageID, lg.NodeStatus, lg.Length, lg.HasMore, lg.Text,
	)), nil, nil
}

// GetPipelineScriptInput is the schema for get_pipeline_script.
type GetPipelineScriptInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number to pin the script to. Use 0 or omit for lastBuild."`
}

// configXML mirrors the slice of <flow-definition> Jenkins serves on
// /<job>/config.xml that the fallback needs. The `class` attribute tells
// CpsFlowDefinition (inline script) from CpsScmFlowDefinition (Jenkinsfile
// pulled from SCM at run-time).
type configXML struct {
	Definition struct {
		Class      string `xml:"class,attr"`
		Script     string `xml:"script"`
		ScriptPath string `xml:"scriptPath"`
		SCM        struct {
			UserRemoteConfigs struct {
				Configs []struct {
					URL string `xml:"url"`
				} `xml:"hudson.plugins.git.UserRemoteConfig"`
			} `xml:"userRemoteConfigs"`
			Branches struct {
				Specs []struct {
					Name string `xml:"name"`
				} `xml:"hudson.plugins.git.BranchSpec"`
			} `xml:"branches"`
		} `xml:"scm"`
	} `xml:"definition"`
}

// GetPipelineScript returns the Jenkinsfile a specific build actually ran.
// Two-tier fallback: the Replay plugin endpoint (faithful to the build),
// then the job-level config.xml (current Jenkinsfile, may differ from
// what the build ran — provenance is surfaced in the output).
func (d Deps) GetPipelineScript(ctx context.Context, _ *mcp.CallToolRequest, in GetPipelineScriptInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	jobAPI := jenkins.JobAPIPath(in.JobPath)
	buildRef := jenkins.BuildRef(in.BuildNumber)

	replayPath := jobAPI + "/" + buildRef + "/replay/"
	replayBody, replayErr := d.Client.Get(ctx, replayPath, nil)
	if replayErr == nil {
		if script, ok := extractReplayScript(string(replayBody)); ok {
			return textResult(fmt.Sprintf(
				"Pipeline script for %s build #%s (source: replay)\n\n%s",
				in.JobPath, buildRef, script,
			)), nil, nil
		}
		replayErr = fmt.Errorf("replay page had no <textarea name=\"mainScript\">")
	}

	configPath := jobAPI + "/config.xml"
	configBody, configErr := d.Client.Get(ctx, configPath, nil)
	if configErr != nil {
		return nil, nil, fmt.Errorf(
			"could not load pipeline script for %s build #%s: replay: %v; config.xml: %w",
			in.JobPath, buildRef, replayErr, configErr,
		)
	}
	var cfg configXML
	if err := xml.Unmarshal(configBody, &cfg); err != nil {
		return nil, nil, fmt.Errorf("parse config.xml for %s: %w", in.JobPath, err)
	}

	if strings.Contains(cfg.Definition.Class, "CpsScmFlowDefinition") {
		return textResult(renderSCMHint(in.JobPath, buildRef, cfg)), nil, nil
	}
	if script := strings.TrimSpace(cfg.Definition.Script); script != "" {
		return textResult(fmt.Sprintf(
			"Pipeline script for %s (source: job-config-fallback)\n"+
				"NOTE: build-pinned source unavailable — this is the current job-level Jenkinsfile, not what build #%s actually ran.\n\n%s",
			in.JobPath, buildRef, script,
		)), nil, nil
	}
	return nil, nil, fmt.Errorf(
		"could not load pipeline script for %s build #%s: replay: %v; config.xml has no inline <script> and is not Pipeline-from-SCM",
		in.JobPath, buildRef, replayErr,
	)
}

// extractReplayScript pulls the body of `<textarea name="mainScript">…</textarea>`
// from the Replay HTML page and HTML-unescapes it. Returns ok=false when
// the textarea isn't present (e.g. the page is a login redirect or the
// Replay plugin isn't installed).
func extractReplayScript(body string) (string, bool) {
	const needle = `name="mainScript"`
	idx := strings.Index(body, needle)
	if idx < 0 {
		return "", false
	}
	openEnd := strings.Index(body[idx:], ">")
	if openEnd < 0 {
		return "", false
	}
	start := idx + openEnd + 1
	closeIdx := strings.Index(body[start:], "</textarea>")
	if closeIdx < 0 {
		return "", false
	}
	return html.UnescapeString(body[start : start+closeIdx]), true
}

// renderSCMHint formats the "Pipeline from SCM" fallback message — the
// job's Jenkinsfile lives in a git repo, so we surface the coordinates
// the agent needs to fetch it independently.
func renderSCMHint(jobPath, buildRef string, cfg configXML) string {
	url := "(unknown)"
	if cs := cfg.Definition.SCM.UserRemoteConfigs.Configs; len(cs) > 0 {
		url = cs[0].URL
	}
	branch := "(unknown)"
	if bs := cfg.Definition.SCM.Branches.Specs; len(bs) > 0 {
		branch = bs[0].Name
	}
	scriptPath := cfg.Definition.ScriptPath
	if scriptPath == "" {
		scriptPath = "Jenkinsfile"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Pipeline script for %s build #%s: build-pinned source unavailable.\n", jobPath, buildRef)
	out.WriteString("Job uses Pipeline from SCM:\n")
	fmt.Fprintf(&out, "  repo:   %s\n", url)
	fmt.Fprintf(&out, "  branch: %s\n", branch)
	fmt.Fprintf(&out, "  path:   %s\n", scriptPath)
	out.WriteString("Use get_scm_context to find the commit, then clone+read the Jenkinsfile.\n")
	return out.String()
}
