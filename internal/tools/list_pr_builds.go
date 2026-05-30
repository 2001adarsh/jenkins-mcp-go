package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

const (
	defaultListPRBuildsMax = 20
	maxListPRBuildsMax     = 100
	listPRBuildsTimeWidth  = 20
)

// prBranchNameTemplates lists the canonical PR branch names in priority
// order. PR-<n> is the most common modern multibranch convention;
// pull/<n>/head is GitHub's pull-request ref; change-<n> is Gerrit;
// pr/<n> is occasionally used. First one that resolves wins.
var prBranchNameTemplates = []string{
	"PR-%d",
	"pull/%d/head",
	"change-%d",
	"pr/%d",
}

// ListPRBuildsInput is the schema for list_pr_builds.
type ListPRBuildsInput struct {
	JobPath   string `json:"job_path" jsonschema:"Slash-separated multibranch job path."`
	PRNumber  int    `json:"pr_number" jsonschema:"PR / change number."`
	MaxBuilds int    `json:"max_builds,omitempty" jsonschema:"Cap on builds returned. Default 20, capped at 100."`
}

// prBranchProbeResult records whether one candidate branch name exists.
type prBranchProbeResult struct {
	Template string // human-friendly name like "PR-123"
	Found    bool
	HardErr  error // non-404 transport / parse error
}

// ListPRBuilds resolves the canonical PR branch under a multibranch job
// and returns its build history. Probes the well-known PR branch naming
// conventions in parallel, picks the first match in priority order, then
// fetches that branch's builds list.
func (d Deps) ListPRBuilds(ctx context.Context, _ *mcp.CallToolRequest, in ListPRBuildsInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	if in.PRNumber <= 0 {
		return nil, nil, fmt.Errorf("pr_number must be > 0")
	}
	maxBuilds := in.MaxBuilds
	if maxBuilds <= 0 {
		maxBuilds = defaultListPRBuildsMax
	}
	if maxBuilds > maxListPRBuildsMax {
		maxBuilds = maxListPRBuildsMax
	}

	jobAPI := jenkins.JobAPIPath(in.JobPath)
	classBody, err := d.Client.Get(ctx, jobAPI+"/api/json", map[string]string{"tree": "_class"})
	if err != nil {
		return nil, nil, fmt.Errorf("inspect %s: %w", in.JobPath, err)
	}
	var meta struct {
		Class string `json:"_class"`
	}
	if err := json.Unmarshal(classBody, &meta); err != nil {
		return nil, nil, fmt.Errorf("parse class for %s: %w", in.JobPath, err)
	}
	if meta.Class != multibranchClass {
		return textResult(fmt.Sprintf(
			"%s is not a WorkflowMultiBranchProject (got %q). Use list_branches for a generic listing.",
			in.JobPath, meta.Class,
		)), nil, nil
	}

	candidates := make([]string, len(prBranchNameTemplates))
	for i, tpl := range prBranchNameTemplates {
		candidates[i] = fmt.Sprintf(tpl, in.PRNumber)
	}
	probes := fetchPerItem(candidates, func(branch string) prBranchProbeResult {
		return d.probeOnePRBranch(ctx, jobAPI, branch)
	})
	for _, p := range probes {
		if p.HardErr != nil {
			return nil, nil, p.HardErr
		}
	}
	var resolved string
	for i, p := range probes {
		if p.Found {
			resolved = candidates[i]
			break
		}
	}
	if resolved == "" {
		return textResult(noPRBranchHint(in.PRNumber, candidates)), nil, nil
	}

	branchPath := jobAPI + "/job/" + resolved
	tree := fmt.Sprintf("builds[number,result,timestamp,duration,url]{0,%d}", maxBuilds)
	body, err := d.Client.Get(ctx, branchPath+"/api/json", map[string]string{"tree": tree})
	if err != nil {
		return nil, nil, fmt.Errorf("builds for branch %s: %w", resolved, err)
	}
	var resp struct {
		Builds []struct {
			Number    int64  `json:"number"`
			Result    string `json:"result"`
			Timestamp int64  `json:"timestamp"`
			Duration  int64  `json:"duration"`
			URL       string `json:"url"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("parse builds for %s: %w", resolved, err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Builds for PR #%d of %s (branch: %s):\n\n", in.PRNumber, in.JobPath, resolved)
	if len(resp.Builds) == 0 {
		out.WriteString("  (no builds yet for this branch)\n")
	} else {
		fmt.Fprintf(&out, "  %-6s  %-9s  %-20s  %s\n", "build", "result", "finished", "duration")
		fmt.Fprintf(&out, "  %s  %s  %s  %s\n",
			strings.Repeat("-", 6), strings.Repeat("-", 9),
			strings.Repeat("-", listPRBuildsTimeWidth), strings.Repeat("-", 8))
		for _, b := range resp.Builds {
			finished := "-"
			if b.Timestamp > 0 {
				finished = time.UnixMilli(b.Timestamp).UTC().Format("2006-01-02 15:04 UTC")
			}
			result := b.Result
			if result == "" {
				result = "(running)"
			}
			fmt.Fprintf(&out, "  #%-5d  %-9s  %-20s  %s\n",
				b.Number, result, finished, formatBuildDuration(b.Duration))
		}
	}
	fmt.Fprintf(&out, "\n%d builds across the PR's lifetime.\n", len(resp.Builds))
	return textResult(out.String()), nil, nil
}

func (d Deps) probeOnePRBranch(ctx context.Context, jobAPI, branch string) prBranchProbeResult {
	path := jobAPI + "/job/" + branch + "/api/json"
	_, err := d.Client.Get(ctx, path, map[string]string{"tree": "name"})
	if err == nil {
		return prBranchProbeResult{Template: branch, Found: true}
	}
	if jenkins.IsHTTPStatus(err, http.StatusNotFound) {
		return prBranchProbeResult{Template: branch}
	}
	return prBranchProbeResult{Template: branch, HardErr: err}
}

func noPRBranchHint(pr int, candidates []string) string {
	return fmt.Sprintf(
		"no branch found for PR #%d — checked %s. List all branches with list_branches to see what's actually there (branch naming for PR #%s may use a non-standard convention).",
		pr, strings.Join(candidates, ", "), strconv.Itoa(pr),
	)
}
