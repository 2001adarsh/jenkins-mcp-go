package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// WhoamiCanInput is the schema for whoami_can.
type WhoamiCanInput struct {
	JobPath string `json:"job_path" jsonschema:"slash-separated Jenkins job (or folder) path, e.g. team/my-job"`
}

const (
	permOK      = "OK"
	permDenied  = "DENIED"
	permUnknown = "UNKNOWN"
)

type permRow struct {
	Name   string
	Status string
	Note   string // free-form suffix, e.g. "(folder)" or "(no last build)"
}

// WhoamiCan probes a job for the effective Read / Build / Cancel / Configure
// permissions of the configured token. Every probe is a GET so the tool stays
// safe even when JENKINS_MCP_READONLY is off — do not introduce POSTs here.
func (d Deps) WhoamiCan(ctx context.Context, _ *mcp.CallToolRequest, in WhoamiCanInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.JobPath) == "" {
		return nil, nil, errors.New("job_path is required")
	}

	user := probeWhoami(ctx, d.Client)
	jobAPI := jenkins.JobAPIPath(in.JobPath)

	readRow, isFolder, hasLastBuild := probePermsRead(ctx, d.Client, jobAPI)
	rows := []permRow{
		readRow,
		probePermsBuild(ctx, d.Client, jobAPI, isFolder),
		probePermsCancel(ctx, d.Client, jobAPI, isFolder, hasLastBuild),
		probePermsConfigure(ctx, d.Client, jobAPI),
	}
	return textResult(renderPermissions(user, in.JobPath, rows)), nil, nil
}

func probeWhoami(ctx context.Context, c *jenkins.Client) string {
	body, err := c.Get(ctx, "/me/api/json", map[string]string{"tree": "fullName,id"})
	if err != nil {
		return "anonymous"
	}
	var me struct {
		FullName string `json:"fullName"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return "anonymous"
	}
	switch {
	case me.ID == "" && me.FullName == "":
		return "anonymous"
	case me.ID != "" && me.FullName != "" && me.FullName != me.ID:
		return fmt.Sprintf("%s (%s)", me.ID, me.FullName)
	case me.ID != "":
		return me.ID
	default:
		return me.FullName
	}
}

func probePermsRead(ctx context.Context, c *jenkins.Client, jobAPI string) (permRow, bool, bool) {
	body, err := c.Get(ctx, jobAPI+"/api/json", map[string]string{"tree": "_class,lastBuild[number]"})
	switch {
	case err == nil:
		var meta struct {
			Class     string `json:"_class"`
			LastBuild *struct {
				Number int64 `json:"number"`
			} `json:"lastBuild"`
		}
		_ = json.Unmarshal(body, &meta)
		return permRow{Name: "read", Status: permOK}, isFolderClass(meta.Class), meta.LastBuild != nil
	case jenkins.IsHTTPStatus(err, http.StatusForbidden),
		jenkins.IsHTTPStatus(err, http.StatusNotFound):
		return permRow{Name: "read", Status: permDenied}, false, false
	default:
		return permRow{Name: "read", Status: permUnknown}, false, false
	}
}

func probePermsBuild(ctx context.Context, c *jenkins.Client, jobAPI string, isFolder bool) permRow {
	if isFolder {
		return permRow{Name: "build", Status: "N/A", Note: "(folder)"}
	}
	primary := probeStatus(ctx, c, jobAPI+"/build")
	if primary == http.StatusMethodNotAllowed {
		return permRow{Name: "build", Status: permOK}
	}
	secondary := probeStatus(ctx, c, jobAPI+"/buildWithParameters")
	if secondary == http.StatusMethodNotAllowed {
		return permRow{Name: "build", Status: permOK}
	}
	if primary == http.StatusForbidden || secondary == http.StatusForbidden {
		return permRow{Name: "build", Status: permDenied}
	}
	return permRow{Name: "build", Status: permUnknown}
}

func probePermsCancel(ctx context.Context, c *jenkins.Client, jobAPI string, isFolder, hasLastBuild bool) permRow {
	if isFolder {
		return permRow{Name: "cancel", Status: "N/A", Note: "(folder)"}
	}
	if !hasLastBuild {
		return permRow{Name: "cancel", Status: "N/A", Note: "(no last build)"}
	}
	switch probeStatus(ctx, c, jobAPI+"/lastBuild/stop") {
	case http.StatusMethodNotAllowed:
		return permRow{Name: "cancel", Status: permOK}
	case http.StatusForbidden:
		return permRow{Name: "cancel", Status: permDenied}
	default:
		return permRow{Name: "cancel", Status: permUnknown}
	}
}

func probePermsConfigure(ctx context.Context, c *jenkins.Client, jobAPI string) permRow {
	switch probeStatus(ctx, c, jobAPI+"/configure") {
	case http.StatusOK:
		return permRow{Name: "configure", Status: permOK}
	case http.StatusForbidden:
		return permRow{Name: "configure", Status: permDenied}
	default:
		return permRow{Name: "configure", Status: permUnknown}
	}
}

// probeStatus returns the HTTP status from a GET, or 0 for non-HTTP errors
// (transport, context). Callers treat the 0 sentinel as UNKNOWN.
func probeStatus(ctx context.Context, c *jenkins.Client, path string) int {
	_, err := c.Get(ctx, path, nil)
	if err == nil {
		return http.StatusOK
	}
	var herr *jenkins.HTTPError
	if errors.As(err, &herr) {
		return herr.StatusCode
	}
	return 0
}

func isFolderClass(class string) bool {
	return strings.Contains(strings.ToLower(class), "folder")
}

func renderPermissions(user, jobPath string, rows []permRow) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Permissions for %s on %s:\n", user, jobPath)
	for _, r := range rows {
		fmt.Fprintf(&out, "  %-10s %s", r.Name, r.Status)
		if r.Note != "" {
			out.WriteString(" ")
			out.WriteString(r.Note)
		}
		out.WriteString("\n")
	}
	return out.String()
}
