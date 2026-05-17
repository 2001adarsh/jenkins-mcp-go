<!-- markdownlint-disable MD033 MD041 -->
<div align="center">

# jenkins-mcp-go

**A focused, fast Model Context Protocol (MCP) server for Jenkins, written in Go.**

Give your LLM agent eyes into Jenkins: fetch console logs, inspect pipeline stages,
parse JUnit reports, and zero in on Ginkgo failures — all over a single, read-only
MCP stdio transport.

[![Go Reference](https://pkg.go.dev/badge/github.com/2001adarsh/jenkins-mcp-go.svg)](https://pkg.go.dev/github.com/2001adarsh/jenkins-mcp-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/2001adarsh/jenkins-mcp-go)](https://goreportcard.com/report/github.com/2001adarsh/jenkins-mcp-go)
[![CI](https://github.com/2001adarsh/jenkins-mcp-go/actions/workflows/ci.yml/badge.svg)](https://github.com/2001adarsh/jenkins-mcp-go/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/2001adarsh/jenkins-mcp-go?sort=semver)](https://github.com/2001adarsh/jenkins-mcp-go/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

</div>

---

## Why jenkins-mcp-go?

Most Jenkins integrations expect a human at a keyboard. LLM agents need
something different: small, structured responses; a clear path from "build
failed" to "here is the failing line"; and the ability to grep a multi-gigabyte
console log without re-downloading it on every question.

**jenkins-mcp-go** is built for that workflow:

- **Read-only by design.** Every tool is a `GET`. The server can't trigger
  builds, change configuration, or post anything back to Jenkins.
- **Single-host, single-credential.** It talks to one Jenkins URL with one
  API token, configured by environment variables. No multi-tenant surface,
  no credential vault to misuse.
- **Disk-cached console logs.** Finished builds are saved once and reused.
  The cache is keyed by job path + build number, capped by total size, and
  evicted by LRU mtime.
- **Pipeline- and Ginkgo-aware.** Beyond the raw console, dedicated tools
  parse `/wfapi/describe`, `/testReport/api/json`, and Ginkgo's
  `Summarizing N Failure` block so the agent gets pre-digested failure
  information.
- **One static binary.** Pure Go. No Python runtime, no Docker required.

## Tools

| Tool | Purpose |
| --- | --- |
| `list_jobs` | Enumerate jobs and folders under a path (or root). Optional recursion and case-insensitive RE2 name filter; capped at 500 entries. |
| `get_console_log` | Tail the build's `/consoleText`. Defaults to last 500 lines; pass `tail_lines: -1` for the full log. |
| `get_console_log_path` | Force-cache the full log for a completed build and return its on-disk path so the agent can `Read`/`Grep`/`Bash` it natively. |
| `search_console_log` | RE2 regex search over the console log with line-number-aware context windows. |
| `get_build_info` | Pretty-printed build summary: result, duration, parameters, change set. |
| `get_pipeline_stages` | List Declarative/Scripted Pipeline stages via `/wfapi/describe` with status and duration. |
| `get_stage_log` | Fetch a single pipeline stage's log via `/execution/node/<id>/wfapi/log`. |
| `get_test_report` | Structured JUnit results from `/testReport/api/json`, with failed cases and head+tail of stack traces. |
| `get_failure_summary` | Parse Ginkgo's `Summarizing N Failure` block and surface the first `[ERROR]` tagged with each spec name. |
| `list_nodes` | List Jenkins agents/nodes with status, executor counts, labels, and monitor summaries. |
| `get_node` | Per-node detail: status, per-executor idle state, labels, full monitor data. |

Build-targeted tools take a `job_path` (slash-separated, e.g.
`Builds/team/job-name`) and an optional `build_number` (`0` or omitted =
`lastBuild`). A URL like
`https://jenkins.example.com/job/Builds/job/team/job/job-name/86/` becomes
`job_path="Builds/team/job-name"`, `build_number=86`. `list_jobs` takes a
`folder_path` in the same slash-separated form (empty = root).

See [`docs/TOOLS.md`](docs/TOOLS.md) for the full parameter reference.

## Install

### Pre-built binaries

Grab the archive for your OS and architecture from the
[Releases page](https://github.com/2001adarsh/jenkins-mcp-go/releases) and put
the `jenkins-mcp` binary on your `PATH`.

### Via `go install`

```sh
go install github.com/2001adarsh/jenkins-mcp-go@latest
```

The binary lands in `$(go env GOBIN)` (or `$(go env GOPATH)/bin`).

### From source

```sh
git clone https://github.com/2001adarsh/jenkins-mcp-go.git
cd jenkins-mcp-go
make build
./bin/jenkins-mcp -h 2>/dev/null || true   # the server speaks MCP over stdio; -h prints nothing
```

## Configuration

Configuration is read from the environment at startup. There is no config file
and no command-line flags — keep credentials out of process arguments.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `JENKINS_URL` | yes | — | Base URL of the Jenkins instance, e.g. `https://jenkins.example.com`. |
| `JENKINS_USER` | yes | — | Username for HTTP Basic auth. |
| `JENKINS_API_TOKEN` | yes | — | API token (not the password). Generate one at `/me/configure` in your Jenkins UI. |
| `JENKINS_MCP_CACHE_DIR` | no | `$XDG_CACHE_HOME/jenkins-mcp` (or `~/.cache/jenkins-mcp`) | Where finished build logs are cached on disk. |
| `JENKINS_MCP_CACHE_MAX` | no | `1073741824` (1 GiB) | Soft cap on cache size in bytes. Evicts oldest-mtime files first. |
| `JENKINS_MCP_TIMEOUT` | no | `90s` | HTTP timeout (Go duration: `30s`, `2m`, etc.). |
| `JENKINS_MCP_DEBUG` | no | unset | When set to any non-empty value, emits one stderr line per outbound Jenkins request and cache event. See [`docs/DEBUGGING.md`](docs/DEBUGGING.md). |
| `JENKINS_MCP_READONLY` | no | unset | When truthy (`1`/`true`/`yes`, case-insensitive), suppresses registration of any tool that mutates Jenkins state. Active mode is logged at startup. |

> **Note** — `JENKINS_API_TOKEN` should be a Jenkins **API token**, not your
> account password. In Jenkins, navigate to your user menu → **Configure** →
> **API Token** → **Add new Token**.

## MCP client setup

The server speaks MCP over stdio. Hook it up by adding an entry to your
client's MCP server configuration.

<details open>
<summary><strong>Claude Desktop</strong> — <code>~/Library/Application Support/Claude/claude_desktop_config.json</code> (macOS) or <code>%APPDATA%\Claude\claude_desktop_config.json</code> (Windows)</summary>

```json
{
  "mcpServers": {
    "jenkins": {
      "command": "/usr/local/bin/jenkins-mcp",
      "env": {
        "JENKINS_URL": "https://jenkins.example.com",
        "JENKINS_USER": "your-username",
        "JENKINS_API_TOKEN": "your-api-token"
      }
    }
  }
}
```

</details>

<details>
<summary><strong>Claude Code</strong> (CLI)</summary>

```sh
claude mcp add jenkins /usr/local/bin/jenkins-mcp \
  --env JENKINS_URL=https://jenkins.example.com \
  --env JENKINS_USER=your-username \
  --env JENKINS_API_TOKEN=your-api-token
```

</details>

<details>
<summary><strong>Cursor / Continue / any other MCP client</strong></summary>

Any client that supports stdio MCP servers will accept a configuration of the
form:

```json
{
  "command": "jenkins-mcp",
  "args": [],
  "env": {
    "JENKINS_URL": "https://jenkins.example.com",
    "JENKINS_USER": "your-username",
    "JENKINS_API_TOKEN": "your-api-token"
  }
}
```

</details>

## Example session

Once the server is registered, ask your agent things like:

- *"What integration-test jobs do we have under `Builds/team`?"*
  → calls `list_jobs` with `folder_path: "Builds/team"`, `recursive: true`,
  `name_filter: "integration"`.
- *"What was the result of build 86 of `Builds/team/integration-tests`?"*
  → calls `get_build_info`.
- *"Show me the last 200 lines of the most recent run of `nightly`."*
  → calls `get_console_log` with `tail_lines: 200`.
- *"Find every line matching `panic|fatal` in build 4521 with five lines of context."*
  → calls `search_console_log` with `pattern: "panic|fatal"`, `context_lines: 5`.
- *"Which Ginkgo specs failed in build 92 and what was the first error each emitted?"*
  → calls `get_failure_summary`.
- *"Cache the full log for build 4521 so I can grep it locally."*
  → calls `get_console_log_path`; the agent then uses its own `Read`/`Grep`/`Bash`
  tools on the returned path.

## How caching works

- Only **finished** builds are cached. The cache writer requires Jenkins'
  `Finished: <result>` marker in the response body, so an in-flight build can
  never be persisted as if it were complete.
- Files are named `<sanitized-job-path>-<build-number>.log` in
  `JENKINS_MCP_CACHE_DIR`. The slug is sanitized so directory-traversal
  attempts can't escape the cache root.
- Whenever a cached file is read, its mtime is bumped — this gives LRU
  eviction by access time.
- After a successful write, if total cache size exceeds
  `JENKINS_MCP_CACHE_MAX`, the oldest-mtime files are deleted until the cap
  is satisfied.

## Architecture

```
cmd entrypoint
└─ main.go                      env → config, wire up server, register tools

internal/jenkins/
├─ client.go                    HTTP client (Basic auth, single base URL)
└─ cache.go                     finished-build console log cache + LRU eviction

internal/tools/
├─ common.go                    Deps struct, shared response helpers
├─ jobs.go                      list_jobs
├─ console.go                   get_console_log, get_console_log_path, search_console_log
├─ build.go                     get_build_info
├─ pipeline.go                  get_pipeline_stages, get_stage_log
├─ tests.go                     get_test_report
├─ failures.go                  get_failure_summary (Ginkgo)
└─ nodes.go                     list_nodes, get_node
```

`internal/` is intentionally not importable by external modules; the public
surface of this repository is the binary.

## Security notes

- **Read-only by construction.** Every Jenkins HTTP call is a `GET`. There is
  no `POST`/`PUT`/`DELETE` code path. Build-triggering, configuration changes,
  and credential operations are not exposed.
- **One host, one credential.** Credentials come from the environment and are
  used only against `JENKINS_URL`. There is no host argument on any tool.
- **No credential echo.** The server never includes credentials in tool
  output, error messages, or cached files.
- **Filesystem boundary.** Cache filenames are deterministic and sanitized;
  the cache directory is the only path the server writes to.

If you find a security issue, please follow [SECURITY.md](SECURITY.md) rather
than opening a public issue.

## Development

```sh
make build      # compile to ./bin/jenkins-mcp
make test       # go test ./...
make lint       # golangci-lint run (requires golangci-lint installed)
make fmt        # gofmt -s -w + go mod tidy
make clean
```

Open a topic branch off `main`, keep commits focused, and run `make test lint`
before opening a PR. See [CONTRIBUTING.md](CONTRIBUTING.md) for the full
contributor guide and [`docs/DEBUGGING.md`](docs/DEBUGGING.md) for how to
exercise the server locally with MCP Inspector.

## Compatibility

- **Go:** 1.23+
- **Jenkins:** any version that exposes the standard `/api/json`,
  `/consoleText`, `/wfapi/describe`, `/testReport/api/json` endpoints.
  Pipeline-specific tools require the Pipeline plugin.
- **MCP:** uses [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) v1.6+.

## License

[MIT](LICENSE) © Adarsh Singh
