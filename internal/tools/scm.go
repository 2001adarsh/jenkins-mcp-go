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

// scmContextTree pulls just enough of each change-set item to identify the
// commit and the files it touched. `kind` lets the per-set header tell git
// from svn/hg when a pipeline checks out from multiple SCMs.
const scmContextTree = "changeSets[kind,items[commitId,timestamp,author[fullName],msg,paths[file,editType]]]," +
	"culprits[fullName]"

// defaultMaxCommits bounds rendering when the caller doesn't pass an explicit
// max_commits. Picked to fit a typical "what changed in this build" answer
// without dumping a 500-commit force-push.
const defaultMaxCommits = 50

// GetSCMContextInput is the schema for get_scm_context.
type GetSCMContextInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path."`
	BuildNumber int64  `json:"build_number,omitempty" jsonschema:"Build number. Use 0 or omit for the latest build."`
	MaxCommits  int    `json:"max_commits,omitempty" jsonschema:"Cap on commits returned (default 50). A footer notes if the cap was hit."`
	PathFilter  string `json:"path_filter,omitempty" jsonschema:"Case-insensitive RE2 regex; only commits that touch a matching path are returned."`
}

type scmPath struct {
	File     string `json:"file"`
	EditType string `json:"editType"`
}

type scmAuthor struct {
	FullName string `json:"fullName"`
}

type scmItem struct {
	CommitID  string    `json:"commitId"`
	Timestamp int64     `json:"timestamp"`
	Author    scmAuthor `json:"author"`
	Msg       string    `json:"msg"`
	Paths     []scmPath `json:"paths"`
}

type scmChangeSet struct {
	Kind  string    `json:"kind"`
	Items []scmItem `json:"items"`
}

type scmCulprit struct {
	FullName string `json:"fullName"`
}

type scmBuildAPI struct {
	ChangeSets []scmChangeSet `json:"changeSets"`
	Culprits   []scmCulprit   `json:"culprits"`
}

// GetSCMContext returns the per-commit change history for one build: commit
// id, author, timestamp, message subject, and each commit's touched paths
// with a single-letter edit code (A/M/D). Pipeline builds may produce
// multiple change sets (one per checkout step); they are flattened in order
// with a per-set header.
func (d Deps) GetSCMContext(ctx context.Context, _ *mcp.CallToolRequest, in GetSCMContextInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	maxCommits := in.MaxCommits
	if maxCommits <= 0 {
		maxCommits = defaultMaxCommits
	}
	var pathRe *regexp.Regexp
	if in.PathFilter != "" {
		re, err := regexp.Compile("(?i)" + in.PathFilter)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid path_filter: %w", err)
		}
		pathRe = re
	}

	buildRef := jenkins.BuildRef(in.BuildNumber)
	apiPath := jenkins.JobAPIPath(in.JobPath) + "/" + buildRef + "/api/json"
	body, err := d.Client.Get(ctx, apiPath, map[string]string{"tree": scmContextTree})
	if err != nil {
		return nil, nil, fmt.Errorf("scm context for %s build %s: %w", in.JobPath, buildRef, err)
	}
	var b scmBuildAPI
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, nil, fmt.Errorf("parse build JSON: %w", err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "SCM context for %s build %s\n", in.JobPath, buildRef)

	if names := culpritNames(b.Culprits); len(names) > 0 {
		fmt.Fprintf(&out, "Culprits: %s\n", strings.Join(names, ", "))
	}

	totalCommits := 0
	for _, cs := range b.ChangeSets {
		totalCommits += len(cs.Items)
	}
	if totalCommits == 0 {
		out.WriteString("\n(no commits in change set)\n")
		return textResult(out.String()), nil, nil
	}

	rendered, matched := 0, 0
	truncated := false
	multiSet := len(b.ChangeSets) > 1
	for csIdx, cs := range b.ChangeSets {
		keep := filterCommitsByPath(cs.Items, pathRe)
		matched += len(keep)
		if len(keep) == 0 {
			continue
		}
		if multiSet {
			kind := cs.Kind
			if kind == "" {
				kind = "scm"
			}
			fmt.Fprintf(&out, "\n— change set %d (%s) — %d commit(s)\n", csIdx+1, kind, len(keep))
		} else {
			out.WriteByte('\n')
		}
		for _, it := range keep {
			if rendered >= maxCommits {
				truncated = true
				break
			}
			renderCommit(&out, it)
			rendered++
		}
		if truncated {
			break
		}
	}

	if pathRe != nil {
		fmt.Fprintf(&out, "\n%d of %d commits matched path_filter %q\n", matched, totalCommits, in.PathFilter)
	}
	if truncated {
		fmt.Fprintf(&out, "(stopped at max_commits=%d — pass max_commits to raise the cap)\n", maxCommits)
	}
	return textResult(out.String()), nil, nil
}

func culpritNames(cs []scmCulprit) []string {
	if len(cs) == 0 {
		return nil
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.FullName != "" {
			names = append(names, c.FullName)
		}
	}
	return names
}

func filterCommitsByPath(items []scmItem, re *regexp.Regexp) []scmItem {
	if re == nil {
		return items
	}
	keep := make([]scmItem, 0, len(items))
	for _, it := range items {
		for _, p := range it.Paths {
			if re.MatchString(p.File) {
				keep = append(keep, it)
				break
			}
		}
	}
	return keep
}

func renderCommit(w *strings.Builder, c scmItem) {
	short := c.CommitID
	switch {
	case short == "":
		short = "(no id)"
	case len(short) > 7:
		short = short[:7]
	}
	author := c.Author.FullName
	if author == "" {
		author = "(unknown)"
	}
	when := "(no timestamp)"
	if c.Timestamp > 0 {
		when = time.UnixMilli(c.Timestamp).UTC().Format("2006-01-02 15:04")
	}
	msg := firstLine(c.Msg)
	fmt.Fprintf(w, "%s %s %s  %q\n", short, author, when, msg)
	for _, p := range c.Paths {
		fmt.Fprintf(w, "  %s  %s\n", editCode(p.EditType), p.File)
	}
}

// firstLine returns s up to (but not including) the first \r or \n. Commit
// messages are often multi-line with a subject + body; we want only the
// subject for the rendered table.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// editCode maps Jenkins EditType strings ("add"/"edit"/"delete") to the
// single-letter codes git callers expect (A/M/D). Unknown values fall back
// to the upper-cased first letter so plugin-specific types remain visible.
func editCode(s string) string {
	switch strings.ToLower(s) {
	case "add":
		return "A"
	case "edit", "modify":
		return "M"
	case "delete":
		return "D"
	case "":
		return "?"
	default:
		return strings.ToUpper(s[:1])
	}
}
