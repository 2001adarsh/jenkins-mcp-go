package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// GetBuildInfoInput is the schema for get_build_info.
type GetBuildInfoInput struct {
	JobPath     string `json:"job_path" jsonschema:"Slash-separated job path"`
	BuildNumber int64  `json:"build_number" jsonschema:"Build number. Use 0 or omit for the latest build."`
}

// buildInfoTree is the `tree` query selector for the build summary. Listed
// explicitly so the response is small and stable across Jenkins versions.
const buildInfoTree = "number,result,building,duration,estimatedDuration,timestamp,url," +
	"actions[parameters[name,value]]," +
	"changeSet[items[author[fullName],msg,commitId]]"

// GetBuildInfo returns the build's pretty-printed summary (result, duration,
// parameters, change set).
func (d Deps) GetBuildInfo(ctx context.Context, _ *mcp.CallToolRequest, in GetBuildInfoInput) (*mcp.CallToolResult, any, error) {
	if in.JobPath == "" {
		return nil, nil, fmt.Errorf("job_path is required")
	}
	path := jenkins.JobAPIPath(in.JobPath) + "/" + jenkins.BuildRef(in.BuildNumber) + "/api/json"
	body, err := d.Client.Get(ctx, path, map[string]string{"tree": buildInfoTree})
	if err != nil {
		return nil, nil, err
	}
	var pretty any
	if err := json.Unmarshal(body, &pretty); err == nil {
		if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			return textResult(string(b)), nil, nil
		}
	}
	return textResult(string(body)), nil, nil
}
