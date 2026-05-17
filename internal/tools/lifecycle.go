package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// queueItemPathRe pulls the numeric queue item id out of a Location header
// of the form ".../queue/item/<id>/".
var queueItemPathRe = regexp.MustCompile(`/queue/item/(\d+)/?`)

// waitForStartTimeout caps how long trigger_build will poll the queue item
// before returning unstarted — keeps callers from blocking indefinitely.
const waitForStartTimeout = 60 * time.Second

// waitForStartInterval is the gap between successive queue-item polls.
const waitForStartInterval = 1 * time.Second

// TriggerBuildInput is the schema for trigger_build.
type TriggerBuildInput struct {
	JobPath      string            `json:"job_path" jsonschema:"Slash-separated job path."`
	Parameters   map[string]string `json:"parameters,omitempty" jsonschema:"Optional parameter map. Values are passed as raw strings — no type coercion."`
	WaitForStart bool              `json:"wait_for_start,omitempty" jsonschema:"If true, poll the queue item for up to 60s and return the assigned build number once it leaves the queue."`
}

// TriggerBuild kicks off a Jenkins build. Without parameters it POSTs to
// /build; with parameters it POSTs to /buildWithParameters with form values.
// On 201, Jenkins' Location header points at the resulting queue item, which
// can optionally be polled until a build number is assigned.
func (d Deps) TriggerBuild(ctx context.Context, _ *mcp.CallToolRequest, in TriggerBuildInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}

	var (
		path string
		form url.Values
	)
	if len(in.Parameters) > 0 {
		path = jenkins.JobAPIPath(in.JobPath) + "/buildWithParameters"
		form = url.Values{}
		for k, v := range in.Parameters {
			form.Set(k, v)
		}
	} else {
		path = jenkins.JobAPIPath(in.JobPath) + "/build"
	}

	body, status, location, err := d.Client.PostWithLocation(ctx, path, nil, form)
	if err != nil {
		return nil, nil, err
	}
	if status/100 != 2 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		return nil, nil, fmt.Errorf("trigger %s: HTTP %d: %s", in.JobPath, status, snippet)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Build queued for %s.\n", in.JobPath)
	if location != "" {
		fmt.Fprintf(&out, "Queue item URL: %s\n", location)
	}
	queueItemID := extractQueueItemID(location)
	if queueItemID != "" {
		fmt.Fprintf(&out, "Queue item id: %s\n", queueItemID)
	}

	if !in.WaitForStart {
		out.WriteString("\nPass wait_for_start=true to block until the build is assigned a number.\n")
		return textResult(out.String()), nil, nil
	}

	if queueItemID == "" {
		out.WriteString("\nwait_for_start requested but Jenkins did not return a queue item Location header — cannot poll.\n")
		return textResult(out.String()), nil, nil
	}

	num, execURL, why, err := d.pollQueueItem(ctx, queueItemID)
	if err != nil {
		return nil, nil, fmt.Errorf("poll queue item %s: %w", queueItemID, err)
	}
	switch {
	case num > 0:
		fmt.Fprintf(&out, "\nStarted: build #%d\n", num)
		if execURL != "" {
			fmt.Fprintf(&out, "Executor URL: %s\n", execURL)
		}
	default:
		fmt.Fprintf(&out, "\nStill queued after %s.", waitForStartTimeout)
		if why != "" {
			fmt.Fprintf(&out, " Jenkins reason: %s", why)
		}
		out.WriteString("\nUse list_queue to keep watching.\n")
	}
	return textResult(out.String()), nil, nil
}

type queueItemExecutable struct {
	Number int64  `json:"number"`
	URL    string `json:"url"`
}

type queueItemResponse struct {
	ID         int64                `json:"id"`
	Canceled   bool                 `json:"cancelled"` //nolint:misspell // Jenkins emits this field as "cancelled"
	Why        string               `json:"why"`
	Executable *queueItemExecutable `json:"executable"`
}

// pollQueueItem polls /queue/item/<id>/api/json until the item gets an
// executable (build assigned), is canceled, the context is canceled, or
// waitForStartTimeout elapses. Returns the build number (0 if still queued),
// the executor URL, and the last "why" reason for diagnostics.
func (d Deps) pollQueueItem(ctx context.Context, queueItemID string) (number int64, executorURL, why string, err error) {
	deadline := time.Now().Add(waitForStartTimeout)
	ticker := time.NewTicker(waitForStartInterval)
	defer ticker.Stop()

	for {
		body, getErr := d.Client.Get(ctx, "/queue/item/"+queueItemID+"/api/json", nil)
		if getErr != nil {
			return 0, "", "", getErr
		}
		var item queueItemResponse
		if jsonErr := json.Unmarshal(body, &item); jsonErr != nil {
			return 0, "", "", fmt.Errorf("parse queue item JSON: %w", jsonErr)
		}
		if item.Canceled {
			return 0, "", "build was canceled before starting", nil
		}
		if item.Executable != nil && item.Executable.Number > 0 {
			return item.Executable.Number, item.Executable.URL, item.Why, nil
		}
		why = item.Why
		if time.Now().After(deadline) {
			return 0, "", why, nil
		}
		select {
		case <-ctx.Done():
			return 0, "", why, ctx.Err()
		case <-ticker.C:
		}
	}
}

func extractQueueItemID(location string) string {
	if location == "" {
		return ""
	}
	m := queueItemPathRe.FindStringSubmatch(location)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// StopBuildInput is the schema for stop_build.
type StopBuildInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path."`
	BuildNumber int64  `json:"build_number" jsonschema:"Positive build number (lastBuild is not accepted — pick a concrete one)."`
}

// StopBuild aborts a running build via /<job>/<n>/stop. Jenkins returns
// HTTP 302 redirecting to the build page; the default http.Client follows
// the redirect and surfaces the final 2xx. A non-2xx final status is
// reported as an error with the response snippet.
func (d Deps) StopBuild(ctx context.Context, _ *mcp.CallToolRequest, in StopBuildInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	if in.BuildNumber <= 0 {
		return nil, nil, fmt.Errorf("build_number must be a positive integer")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) + "/stop"
	body, status, err := d.Client.PostWithStatus(ctx, path, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if status/100 != 2 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		return nil, nil, fmt.Errorf("stop %s #%d: HTTP %d: %s", in.JobPath, in.BuildNumber, status, snippet)
	}
	return textResult(fmt.Sprintf(
		"Requested stop of build %s #%d. Use get_build_info to confirm it reached ABORTED.",
		in.JobPath, in.BuildNumber,
	)), nil, nil
}
