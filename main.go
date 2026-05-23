// Command jenkins-mcp is a local MCP server that exposes a small Jenkins
// REST surface over stdio.
//
// Configuration is read from environment variables at startup:
//
//	JENKINS_URL        Base URL of the Jenkins instance (no trailing slash).
//	JENKINS_USER       Username for HTTP Basic auth.
//	JENKINS_API_TOKEN  API token for HTTP Basic auth.
//
// Optional:
//
//	JENKINS_MCP_CACHE_DIR  Directory for cached console logs of finished
//	                       builds. Defaults to $XDG_CACHE_HOME/jenkins-mcp,
//	                       or ~/.cache/jenkins-mcp.
//	JENKINS_MCP_CACHE_MAX  Soft cap on cache size in bytes. Defaults to 1 GiB.
//	JENKINS_MCP_TIMEOUT    HTTP timeout (Go duration). Defaults to 90s.
//	JENKINS_MCP_DEBUG      When set to any non-empty value, emit one stderr
//	                       line per outbound Jenkins request and per cache
//	                       hit/write/eviction. Never writes credentials,
//	                       request headers, or response bodies. See
//	                       docs/DEBUGGING.md.
//	JENKINS_MCP_READONLY   When truthy (1/true/yes, case-insensitive), skip
//	                       registration of any tool that mutates Jenkins
//	                       state. Default off.
//
// All network access is limited to JENKINS_URL. The server speaks MCP over
// stdio and is intended to be launched by an MCP-aware client.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/2001adarsh/jenkins-mcp-go/internal/jenkins"
	"github.com/2001adarsh/jenkins-mcp-go/internal/tools"
)

// version is set at link time by -ldflags; defaults to "dev" for local builds.
var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	client, err := jenkins.NewClient(jenkins.Config{
		BaseURL: cfg.URL,
		User:    cfg.User,
		Token:   cfg.Token,
		Timeout: cfg.Timeout,
	})
	if err != nil {
		return err
	}

	cache, err := jenkins.NewConsoleCache(client, cfg.CacheDir)
	if err != nil {
		return err
	}
	if cfg.CacheMax > 0 {
		cache.MaxBytes = cfg.CacheMax
	}

	deps := tools.Deps{
		Client:   client,
		Cache:    cache,
		Version:  version,
		ReadOnly: cfg.ReadOnly,
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "jenkins",
		Version: version,
	}, nil)

	mode := "read-write"
	if cfg.ReadOnly {
		mode = "read-only"
	}
	log.Printf("jenkins-mcp %s (%s)", version, mode)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "health_check",
		Description: "Run a fixed battery of read-only probes against the configured Jenkins and " +
			"return a one-line-per-check report: reachability, Jenkins version, authenticated user, " +
			"CSRF crumb issuer, Pipeline/JUnit plugin presence, online/offline node counts, and clock " +
			"skew. Useful for validating a fresh install or debugging 'the agent says it can't see Jenkins'.",
	}, deps.HealthCheck)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_jobs",
		Description: "Enumerate jobs and folders in Jenkins. Pass folder_path to scope the listing " +
			"(empty = root), recursive=true to walk into sub-folders, and name_filter (case-insensitive " +
			"RE2 regex) to match leaf names. Use this first when the caller doesn't already know a job_path. " +
			"Capped at 500 entries — narrow with folder_path or name_filter for large instances.",
	}, deps.ListJobs)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_branches",
		Description: "Enumerate the branches of a Jenkins WorkflowMultiBranchProject with per-branch " +
			"last-build state (number, result, duration, timestamp). Optional name_filter (case-insensitive " +
			"RE2) narrows by branch name; healthy_only=true keeps only SUCCESS branches. Returns a hint " +
			"and points back to list_jobs if job_path isn't a multibranch container.",
	}, deps.ListBranches)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_console_log",
		Description: "Fetch Jenkins build console output. Returns last 500 lines by default; " +
			"pass tail_lines explicitly (negative = full log). The response footer reports the " +
			"on-disk cache path so the full log can be inspected via Read/Grep/Bash without re-fetching.",
	}, deps.GetConsoleLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_console_log_path",
		Description: "Download (if needed) the full console log for a completed build and return " +
			"its on-disk cache path. Lets you Read/Grep/Bash the full log natively — useful when " +
			"one test's failure is caused by an earlier test, and you need to see everything that came before.",
	}, deps.GetConsoleLogPath)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_build_info",
		Description: "Get a Jenkins build's status, duration, parameters, and change set.",
	}, deps.GetBuildInfo)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_scm_context",
		Description: "Return the per-commit change history for one build: commit id, author, " +
			"timestamp, message subject, and each commit's touched paths with a single-letter edit " +
			"code (A/M/D). Pipeline builds may produce multiple change sets (one per checkout step); " +
			"they are flattened in order with a per-set header. Optional path_filter (case-insensitive " +
			"RE2) narrows to commits that touch a matching path; max_commits caps rendering (default 50).",
	}, deps.GetSCMContext)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "compare_builds",
		Description: "Diff two builds of the same job across result, duration, parameters, SCM " +
			"commits, pipeline stages, and JUnit tests. Inputs are explicit build numbers (build_a, " +
			"build_b — lastBuild not accepted). SCM diff is direct (commits in build_b's change set " +
			"not in build_a's); intermediate builds are not walked. Set include_tests=false to skip " +
			"the per-test diff on large suites.",
	}, deps.CompareBuilds)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_console_log",
		Description: "Regex-search (RE2) a Jenkins build's console log and return matches with surrounding context.",
	}, deps.SearchConsoleLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_failure_summary",
		Description: "Parse a Ginkgo `Summarizing N Failure` block from the build's console log " +
			"and, for each failing spec, return the first [ERROR] line tagged with that spec name " +
			"plus surrounding context. Ginkgo-specific — returns a hint if the log doesn't look like Ginkgo.",
	}, deps.GetFailureSummary)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_test_report",
		Description: "Fetch structured JUnit test results from Jenkins' /testReport/api/json. " +
			"Returns failed cases with className, name, duration, errorDetails, and head+tail of " +
			"the stack trace. Returns a hint if the build has no JUnit publisher (HTTP 404).",
	}, deps.GetTestReport)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_pipeline_stages",
		Description: "List Declarative/Scripted Pipeline stages for a build with status and " +
			"duration via /wfapi/describe. Use this first to find which stage failed before " +
			"grabbing the full log.",
	}, deps.GetPipelineStages)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_stage_log",
		Description: "Fetch a single pipeline stage's log via /execution/node/<id>/wfapi/log. " +
			"Many declarative wrapper stages have empty stage logs — fall back to " +
			"get_console_log_path when this returns length=0.",
	}, deps.GetStageLog)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_nodes",
		Description: "List all Jenkins agents/nodes with online/offline status, executor counts, " +
			"labels, and monitor summaries. Use this to diagnose 'why is this build still queued' — " +
			"often the matching agents are all offline.",
	}, deps.ListNodes)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_node",
		Description: "Get a single Jenkins node's status, per-executor idle state, labels, and full " +
			"monitor data. Use \"(built-in)\" or \"(master)\" for the controller depending on Jenkins version.",
	}, deps.GetNode)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_queue",
		Description: "List pending Jenkins queue items with the block reason for each — useful for " +
			"diagnosing 'why hasn't this build started yet'. Optional job_path_prefix narrows the " +
			"listing to items whose task URL contains the substring.",
	}, deps.ListQueue)

	addWriteTool(srv, cfg, &mcp.Tool{
		Name: "cancel_queue_item",
		Description: "Drop a pending Jenkins queue item by id before it starts. Mutates Jenkins state. " +
			"Returns a clear 'already left queue' message when the item has already been built or canceled.",
	}, deps.CancelQueueItem)

	addWriteTool(srv, cfg, &mcp.Tool{
		Name: "trigger_build",
		Description: "Queue a Jenkins build, optionally with parameters (raw strings — no type coercion). " +
			"With wait_for_start=true, polls the queue item for up to 60s and returns the assigned " +
			"build number. Mutates Jenkins state.",
	}, deps.TriggerBuild)

	addWriteTool(srv, cfg, &mcp.Tool{
		Name: "stop_build",
		Description: "Abort a running Jenkins build by job_path + build_number. Mutates Jenkins state. " +
			"Confirm the abort took effect with get_build_info (expect result=ABORTED).",
	}, deps.StopBuild)

	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

type config struct {
	URL      string
	User     string
	Token    string
	CacheDir string
	CacheMax int64
	Timeout  time.Duration
	ReadOnly bool
}

// addWriteTool registers a write tool only when read-only mode is off.
// Keeps the read-only gate in one place as the write-tool surface grows.
func addWriteTool[In, Out any](srv *mcp.Server, cfg config, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	if cfg.ReadOnly {
		return
	}
	mcp.AddTool(srv, t, h)
}

// truthyEnv reports whether v looks like an opt-in boolean: 1/true/yes,
// case-insensitive. Empty and everything else are false.
func truthyEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func loadConfig() (config, error) {
	var cfg config
	cfg.URL = os.Getenv("JENKINS_URL")
	cfg.User = os.Getenv("JENKINS_USER")
	cfg.Token = os.Getenv("JENKINS_API_TOKEN")
	if cfg.URL == "" || cfg.User == "" || cfg.Token == "" {
		return cfg, fmt.Errorf("JENKINS_URL, JENKINS_USER, and JENKINS_API_TOKEN must all be set")
	}

	dir := os.Getenv("JENKINS_MCP_CACHE_DIR")
	if dir == "" {
		dir = defaultCacheDir()
	}
	cfg.CacheDir = dir

	if v := os.Getenv("JENKINS_MCP_CACHE_MAX"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("JENKINS_MCP_CACHE_MAX must be a positive integer, got %q", v)
		}
		cfg.CacheMax = n
	}

	if v := os.Getenv("JENKINS_MCP_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return cfg, fmt.Errorf("JENKINS_MCP_TIMEOUT must be a positive Go duration, got %q", v)
		}
		cfg.Timeout = d
	}

	cfg.ReadOnly = truthyEnv(os.Getenv("JENKINS_MCP_READONLY"))

	return cfg, nil
}

// defaultCacheDir returns $XDG_CACHE_HOME/jenkins-mcp (or ~/.cache/jenkins-mcp).
// Falls back to a temp dir if the user's cache dir cannot be resolved.
func defaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "jenkins-mcp")
	}
	return filepath.Join(os.TempDir(), "jenkins-mcp")
}
