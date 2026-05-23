package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// multibranchClass is Jenkins' _class for a WorkflowMultiBranchProject.
// Paths that resolve to anything else are sent back to list_jobs via a hint.
const multibranchClass = "org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject"

// listBranchesTree keeps the per-branch payload small and stable across
// Jenkins versions.
const listBranchesTree = "_class,jobs[name,url,lastBuild[number,result,duration,timestamp]]"

// ListBranchesInput is the schema for list_branches.
type ListBranchesInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated path to a WorkflowMultiBranchProject (e.g. 'Builds/team/svc-x'). Multibranches show up as type=folder in list_jobs."`
	NameFilter  string `json:"name_filter,omitempty" jsonschema:"Case-insensitive RE2 regex matched against each branch name."`
	HealthyOnly bool   `json:"healthy_only,omitempty" jsonschema:"Exclude branches whose last build was not SUCCESS (also excludes never-built branches). Default false."`
}

type branchAPILastBuild struct {
	Number    int64  `json:"number"`
	Result    string `json:"result"`
	Duration  int64  `json:"duration"`
	Timestamp int64  `json:"timestamp"`
}

type branchAPIJob struct {
	Name      string              `json:"name"`
	URL       string              `json:"url"`
	LastBuild *branchAPILastBuild `json:"lastBuild"`
}

type multibranchAPI struct {
	Class string         `json:"_class"`
	Jobs  []branchAPIJob `json:"jobs"`
}

// ListBranches enumerates per-branch state inside a WorkflowMultiBranchProject.
func (d Deps) ListBranches(ctx context.Context, _ *mcp.CallToolRequest, in ListBranchesInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}

	var nameRe *regexp.Regexp
	if in.NameFilter != "" {
		re, err := regexp.Compile("(?i)" + in.NameFilter)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid name_filter: %w", err)
		}
		nameRe = re
	}

	apiPath := jenkins.JobAPIPath(in.JobPath) + "/api/json"
	body, err := d.Client.Get(ctx, apiPath, map[string]string{"tree": listBranchesTree})
	if err != nil {
		return nil, nil, fmt.Errorf("list branches under %s: %w", in.JobPath, err)
	}
	var mb multibranchAPI
	if err := json.Unmarshal(body, &mb); err != nil {
		return nil, nil, fmt.Errorf("parse multibranch listing: %w", err)
	}

	if mb.Class != multibranchClass {
		return textResult(fmt.Sprintf(
			"Path %s is a %s, not a WorkflowMultiBranchProject. "+
				"Use list_jobs to find multibranches — they appear with type=folder; "+
				"drill into one and call list_branches on its path.",
			in.JobPath, shortClass(mb.Class),
		)), nil, nil
	}

	total := len(mb.Jobs)
	shown := make([]branchAPIJob, 0, total)
	for _, j := range mb.Jobs {
		if nameRe != nil && !nameRe.MatchString(j.Name) {
			continue
		}
		if in.HealthyOnly && (j.LastBuild == nil || j.LastBuild.Result != "SUCCESS") {
			continue
		}
		shown = append(shown, j)
	}

	name := lastSegment(in.JobPath)
	var out strings.Builder
	fmt.Fprintf(&out, "Branches in %s (multibranch) — %d total", name, total)
	if len(shown) != total {
		fmt.Fprintf(&out, ", %d shown", len(shown))
	}
	out.WriteString("\n\n")

	if len(shown) == 0 {
		out.WriteString("(no branches matched)\n")
		return textResult(out.String()), nil, nil
	}

	fmt.Fprintf(&out, "%-30s  %-7s  %-9s  %-10s  %-20s  %s\n",
		"branch", "last#", "result", "duration", "last_built_at", "url")
	fmt.Fprintf(&out, "%-30s  %-7s  %-9s  %-10s  %-20s  %s\n",
		"------------------------------", "-------", "---------",
		"----------", "--------------------", "---")
	for _, j := range shown {
		lastNum, result, duration, builtAt := "-", "-", "-", "-"
		if j.LastBuild != nil {
			lastNum = fmt.Sprintf("%d", j.LastBuild.Number)
			if j.LastBuild.Result != "" {
				result = j.LastBuild.Result
			}
			if j.LastBuild.Duration > 0 {
				duration = formatBuildDuration(j.LastBuild.Duration)
			}
			if j.LastBuild.Timestamp > 0 {
				builtAt = time.UnixMilli(j.LastBuild.Timestamp).UTC().Format(time.RFC3339)
			}
		}
		fmt.Fprintf(&out, "%s  %-7s  %-9s  %-10s  %-20s  %s\n",
			padRight(truncate(j.Name, 30), 30), lastNum, result, duration, builtAt, j.URL)
	}
	return textResult(out.String()), nil, nil
}

// formatBuildDuration renders Jenkins' millisecond duration as a compact
// Go-style string truncated to whole seconds (e.g. "2m15s", "1h2m3s").
func formatBuildDuration(millis int64) string {
	return (time.Duration(millis) * time.Millisecond).Truncate(time.Second).String()
}

// shortClass strips the Jenkins-internal package prefix from a _class value
// so error messages stay readable. Empty class is reported as unknown rather
// than as an empty token in the rendered hint.
func shortClass(c string) string {
	if c == "" {
		return "(unknown class)"
	}
	if dot := strings.LastIndex(c, "."); dot >= 0 {
		return c[dot+1:]
	}
	return c
}

func lastSegment(p string) string {
	p = strings.Trim(p, "/")
	if slash := strings.LastIndex(p, "/"); slash >= 0 {
		return p[slash+1:]
	}
	return p
}
