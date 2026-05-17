package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listQueueTree is the `tree` selector for /queue/api/json. Kept explicit so
// the response stays small and stable across Jenkins versions.
const listQueueTree = "items[id,task[name,url],inQueueSince,why,stuck,blocked,buildable,params]"

type apiQueueTask struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type apiQueueItem struct {
	ID           int64        `json:"id"`
	Task         apiQueueTask `json:"task"`
	InQueueSince int64        `json:"inQueueSince"`
	Why          string       `json:"why"`
	Stuck        bool         `json:"stuck"`
	Blocked      bool         `json:"blocked"`
	Buildable    bool         `json:"buildable"`
	Params       string       `json:"params"`
}

type apiQueueListing struct {
	Items []apiQueueItem `json:"items"`
}

// ListQueueInput is the schema for list_queue.
type ListQueueInput struct {
	JobPathPrefix string `json:"job_path_prefix,omitempty" jsonschema:"Optional case-sensitive substring matched against each item's task URL — useful for narrowing to one folder."`
}

// ListQueue lists pending Jenkins queue items with the block reason for each.
func (d Deps) ListQueue(ctx context.Context, _ *mcp.CallToolRequest, in ListQueueInput) (*mcp.CallToolResult, any, error) {
	body, err := d.Client.Get(ctx, "/queue/api/json", map[string]string{"tree": listQueueTree})
	if err != nil {
		return nil, nil, err
	}
	var listing apiQueueListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, nil, fmt.Errorf("parse /queue listing: %w", err)
	}

	var out strings.Builder
	matched := 0
	now := time.Now()
	for _, it := range listing.Items {
		if in.JobPathPrefix != "" && !strings.Contains(it.Task.URL, in.JobPathPrefix) {
			continue
		}
		matched++
		waited := "-"
		if it.InQueueSince > 0 {
			waited = formatDuration(now.Sub(time.UnixMilli(it.InQueueSince)))
		}
		flags := queueFlags(it)
		fmt.Fprintf(&out, "[id=%d] %s\n", it.ID, it.Task.Name)
		fmt.Fprintf(&out, "  url:     %s\n", it.Task.URL)
		fmt.Fprintf(&out, "  waited:  %s\n", waited)
		fmt.Fprintf(&out, "  state:   %s\n", flags)
		if it.Why != "" {
			fmt.Fprintf(&out, "  why:     %s\n", it.Why)
		}
		if it.Params != "" {
			fmt.Fprintf(&out, "  params:  %s\n", strings.TrimSpace(it.Params))
		}
		out.WriteString("\n")
	}

	header := fmt.Sprintf("Queue items: %d total", len(listing.Items))
	if in.JobPathPrefix != "" {
		header += fmt.Sprintf(", %d matched job_path_prefix=%q", matched, in.JobPathPrefix)
	}
	header += "\n\n"
	if matched == 0 {
		return textResult(header + "(no items)\n"), nil, nil
	}
	return textResult(header + out.String() + "Use cancel_queue_item with an id to drop a pending item before it starts.\n"), nil, nil
}

// CancelQueueItemInput is the schema for cancel_queue_item.
type CancelQueueItemInput struct {
	ItemID int64 `json:"item_id" jsonschema:"Queue item id (from list_queue)."`
}

// CancelQueueItem drops a pending queue item by id. Jenkins returns 404 on
// success for this endpoint (treated here as the canonical success signal);
// other non-2xx responses surface as errors. An item that has already left
// the queue (started or was canceled by someone else) returns a clear
// "already left queue" message rather than a generic 404.
func (d Deps) CancelQueueItem(ctx context.Context, _ *mcp.CallToolRequest, in CancelQueueItemInput) (*mcp.CallToolResult, any, error) {
	if in.ItemID <= 0 {
		return nil, nil, fmt.Errorf("item_id must be a positive integer")
	}
	body, status, err := d.Client.PostWithStatus(ctx, "/queue/cancelItem",
		map[string]string{"id": strconv.FormatInt(in.ItemID, 10)}, nil)
	if err != nil {
		return nil, nil, err
	}
	// 2xx and 404-with-empty-body both mean "Jenkins acknowledged the cancel
	// or the item is no longer in queue". Distinguish them only when there's
	// a body that suggests an actual error.
	trimmed := strings.TrimSpace(string(body))
	switch {
	case status/100 == 2:
		return textResult(fmt.Sprintf("Canceled queue item %d.", in.ItemID)), nil, nil
	case status == 404 && trimmed == "":
		return textResult(fmt.Sprintf(
			"Canceled queue item %d (Jenkins returned HTTP 404 with empty body, "+
				"which this endpoint uses as the success signal).", in.ItemID)), nil, nil
	case status == 404:
		return textResult(fmt.Sprintf(
			"Queue item %d has already left the queue (build started or item was canceled by another caller).",
			in.ItemID)), nil, nil
	default:
		snippet := trimmed
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		return nil, nil, fmt.Errorf("cancel queue item %d: HTTP %d: %s", in.ItemID, status, snippet)
	}
}

func queueFlags(it apiQueueItem) string {
	flags := make([]string, 0, 3)
	if it.Buildable {
		flags = append(flags, "buildable")
	}
	if it.Blocked {
		flags = append(flags, "blocked")
	}
	if it.Stuck {
		flags = append(flags, "stuck")
	}
	if len(flags) == 0 {
		return "-"
	}
	return strings.Join(flags, ",")
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
