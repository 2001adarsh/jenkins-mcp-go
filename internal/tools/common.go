// Package tools implements the MCP tool handlers exposed by the server.
//
// Each file groups related tools. All handlers share a Deps value carrying
// the Jenkins client and the on-disk console-log cache.
package tools

import (
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
)

// Deps is the shared dependency set passed to every tool handler.
type Deps struct {
	Client *jenkins.Client
	Cache  *jenkins.ConsoleCache
	Config EffectiveConfig
}

// EffectiveConfig is a read-only snapshot of the runtime configuration the
// process resolved at startup. It is surfaced by health_check so users can
// confirm what the server is actually running with.
type EffectiveConfig struct {
	Version  string
	ReadOnly bool
	CacheDir string
	CacheMax int64
	Timeout  time.Duration
}

// textResult wraps a string as an mcp.CallToolResult.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// pathFooter formats the trailing breadcrumb that points callers at the
// on-disk cache. Empty string when the build is not cached.
func pathFooter(cachePath string, totalBytes int) string {
	if cachePath == "" {
		return ""
	}
	return fmt.Sprintf(
		"\n\n— full log cached on disk at: %s (%d bytes)\n"+
			"Use Read/Grep/Bash on this path to inspect the entire log, including content not shown above.",
		cachePath, totalBytes,
	)
}
