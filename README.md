<!-- markdownlint-disable MD033 MD041 -->
<div align="center">

# jenkins-mcp-go

**A focused, fast Model Context Protocol (MCP) server for Jenkins, written in Go.**

Connect Jenkins to Claude Desktop, Claude Code, Cursor, or any MCP-compatible
AI agent: fetch console logs, inspect pipeline stages, parse JUnit and Ginkgo
test reports, diff two builds, rank flaky tests, trigger and abort builds, and
manage the build queue — all over a single MCP stdio transport, from one
static Go binary.

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

- **Read-first, opt-out writes.** Read tools are always on. Write tools
  (`trigger_build`, `stop_build`, `cancel_queue_item`) are gated by
  `JENKINS_MCP_READONLY`: set the env var and the server registers only
  the read surface.
- **Single-host, single-credential.** It talks to one Jenkins URL with one
  API token, configured by environment variables. No multi-tenant surface,
  no credential vault to misuse.
- **Triage-shaped, not API-shaped.** Tools answer the questions agents
  actually ask — *"what changed between build A and B?"* (`compare_builds`),
  *"which tests in this job are flaky?"* (`get_flaky_candidates`), *"what
  commits and files touched this build?"* (`get_scm_context`) — instead of
  mirroring Jenkins endpoints one-for-one.
- **Built for context windows.** Every listing tool takes an RE2 filter and
  a cap. `get_console_log_path` returns the on-disk path of a finished
  build's log so the agent can `Read`/`Grep`/`Bash` it natively instead of
  streaming gigabytes through MCP.
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
| `health_check` | Validate the server's setup: Jenkins reachability and version, authenticated user, CSRF crumb issuer, Pipeline/JUnit plugin presence, online/offline node counts, clock skew, and the effective config the process is running with. |
| `get_plugin_versions` | List installed Jenkins plugins with versions, pinned flag, and pending-update flag. Optional `name_filter` (RE2) and `include_inactive`. 403 degrades to a clear hint. |
| `whoami_can` | Probe a job for the configured token's effective Read / Build / Cancel / Configure permissions via read-only `GET` requests. Useful up-front to avoid a 403 on a trigger or cancel. Stays read-only even when `JENKINS_MCP_READONLY=false`. |
| `list_jobs` | Enumerate jobs and folders under a path (or root). Optional recursion and case-insensitive RE2 name filter; capped at 500 entries. |
| `list_branches` | Enumerate the branches of a `WorkflowMultiBranchProject` with per-branch last-build number, result, duration, and timestamp. Optional `name_filter` (RE2) and `healthy_only`. |
| `get_console_log` | Tail the build's `/consoleText`. Defaults to last 500 lines; pass `tail_lines: -1` for the full log. |
| `get_console_log_path` | Force-cache the full log for a completed build and return its on-disk path so the agent can `Read`/`Grep`/`Bash` it natively. |
| `search_console_log` | RE2 regex search over the console log with line-number-aware context windows. |
| `get_build_info` | Pretty-printed build summary: result, duration, parameters, change set. |
| `get_scm_context` | Per-commit history for one build: commit id, author, timestamp, message subject, and each commit's touched paths with `A`/`M`/`D` edit codes. Pipeline change sets are flattened with per-set headers. Optional RE2 `path_filter`. |
| `last_green_build` | Report the most recent successful build of a job. Returns number, finish time (UTC), and URL — the start point for bisect-from-green. |
| `changes_since_last_green` | Union the commits across every completed build since the job's last green. Walks `previousCompletedBuild`, dedupes by `commitId`, supports `path_filter` and `max_commits`. Surfaces 'all green' and wide-window footers. |
| `compare_builds` | Diff two builds of the same job across result, duration, parameters, SCM commits, pipeline stages, and JUnit tests. The agent answers "what changed between A and B?" in one call. |
| `get_pipeline_stages` | List Declarative/Scripted Pipeline stages via `/wfapi/describe` with status and duration. |
| `get_stage_log` | Fetch a single pipeline stage's log via `/execution/node/<id>/wfapi/log`. |
| `get_test_report` | Structured JUnit results from `/testReport/api/json`, with failed cases and head+tail of stack traces. |
| `get_flaky_candidates` | Rank flaky tests across the latest N completed builds of one job by counting pass↔fail flips. Returns a sorted table of test name, flip count, pass/fail tallies, and last-seen build. |
| `get_ginkgo_failure_summary` | Parse Ginkgo's `Summarizing N Failure` block and surface the first `[ERROR]` tagged with each spec name. |
| `list_nodes` | List Jenkins agents/nodes with status, executor counts, labels, and monitor summaries. |
| `get_node` | Per-node detail: status, per-executor idle state, labels, full monitor data. |
| `list_queue` | List pending Jenkins queue items with the block reason for each. |
| `cancel_queue_item` | Drop a pending queue item by id. **Mutating**; suppressed when `JENKINS_MCP_READONLY` is set. |
| `trigger_build` | Queue a build, optionally with parameters; can block until the build is assigned a number. **Mutating**. |
| `stop_build` | Abort a running build. **Mutating**. |

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

### Via Docker

Pre-built multi-arch images are published to GitHub Container Registry:

```sh
docker pull ghcr.io/2001adarsh/jenkins-mcp-go:latest
```

Tags:

- `:latest` — most recent release
- `:vX.Y.Z` — pinned to a specific release
- `:vX.Y.Z-amd64` / `:vX.Y.Z-arm64` — per-arch (the unsuffixed tags above are multi-arch manifests; `docker pull` resolves the right one automatically)

The image is built on `gcr.io/distroless/static:nonroot` — runs as a non-root
user, ships with CA roots so HTTPS to Jenkins works out of the box, and is
under 20 MB compressed. See the [Docker MCP client setup](#mcp-client-setup)
section for a `docker run`-based Claude Desktop config.

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
<summary><strong>Claude Desktop via Docker</strong> — same config file, no binary on PATH required</summary>

```json
{
  "mcpServers": {
    "jenkins": {
      "command": "docker",
      "args": [
        "run", "--rm", "-i",
        "-e", "JENKINS_URL",
        "-e", "JENKINS_USER",
        "-e", "JENKINS_API_TOKEN",
        "ghcr.io/2001adarsh/jenkins-mcp-go:latest"
      ],
      "env": {
        "JENKINS_URL": "https://jenkins.example.com",
        "JENKINS_USER": "your-username",
        "JENKINS_API_TOKEN": "your-api-token"
      }
    }
  }
}
```

`-i` keeps stdin open (MCP speaks stdio); `--rm` cleans up the container
after Claude Desktop disconnects. The console-log cache lives inside the
container by default, so it's lost on restart — add
`-v "$HOME/.cache/jenkins-mcp:/home/nonroot/.cache/jenkins-mcp"` and
`-e XDG_CACHE_HOME=/home/nonroot/.cache` to the `args` array if you want
the cache to survive across sessions.

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
- *"Build 91 passed but 92 failed — what changed?"*
  → calls `compare_builds` with `build_a: 91`, `build_b: 92`.
- *"Which tests in `Builds/team/integration-tests` have been flipping between pass and fail recently?"*
  → calls `get_flaky_candidates`.
- *"Which commits in build 86 touched anything under `internal/auth/`?"*
  → calls `get_scm_context` with `path_filter: "^internal/auth/"`.
- *"Which Ginkgo specs failed in build 92 and what was the first error each emitted?"*
  → calls `get_ginkgo_failure_summary`.
- *"Cache the full log for build 4521 so I can grep it locally."*
  → calls `get_console_log_path`; the agent then uses its own `Read`/`Grep`/`Bash`
  tools on the returned path.

## Caching

Only finished builds are cached (the writer requires Jenkins' `Finished:`
marker), and the cache is evicted by LRU mtime once it exceeds
`JENKINS_MCP_CACHE_MAX`. Files live in `JENKINS_MCP_CACHE_DIR`.

## Security notes

- **No credential echo.** The server never includes credentials in tool
  output, error messages, or cached files.
- **Filesystem boundary.** Cache filenames are sanitized; the cache
  directory is the only path the server writes to.

If you find a security issue, please follow [SECURITY.md](SECURITY.md) rather
than opening a public issue.

## Development

`make build / test / lint / fmt`. See [CONTRIBUTING.md](CONTRIBUTING.md) for the
full contributor guide and [`docs/DEBUGGING.md`](docs/DEBUGGING.md) for how to
exercise the server locally with MCP Inspector.

## Compatibility

- **Go:** 1.23+
- **Jenkins:** any version that exposes the standard `/api/json`,
  `/consoleText`, `/wfapi/describe`, `/testReport/api/json` endpoints.
  Pipeline-specific tools require the Pipeline plugin.
- **MCP:** uses [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) v1.6+.

## Alternatives

How `jenkins-mcp-go` compares to other ways of exposing Jenkins to an LLM:

| Option | Runtime | Read-only mode | Console-log cache | Pipeline / JUnit / Ginkgo tools |
| --- | --- | --- | --- | --- |
| **`jenkins-mcp-go`** (this repo) | Single static Go binary | Yes, gated by `JENKINS_MCP_READONLY` | LRU disk cache for finished builds | First-class, pre-digested responses |
| Python-based Jenkins MCP servers | Python interpreter + deps | Varies by project | Typically none — re-fetches each call | Usually raw `/api/json` pass-through |
| Generic HTTP-to-MCP gateways | Node / Python runtime | Whatever the gateway enforces | None | None — agent has to parse raw JSON |
| Direct `curl` / shell tools from the agent | Shell | Manual | None | None — agent reasons over raw text |

If you want a small, predictable surface tailored to "the agent is debugging a
Jenkins build", this project is for you. If you need a generic JSON proxy or
multi-tenant credential routing, a generic HTTP-MCP gateway is a better fit.

## FAQ

### How do I connect Jenkins to Claude?

Install the `jenkins-mcp` binary, then add it to your Claude Desktop or
Claude Code MCP configuration with your `JENKINS_URL`, `JENKINS_USER`, and
`JENKINS_API_TOKEN`. See [MCP client setup](#mcp-client-setup) above for the
exact JSON / CLI snippets.

### Does this work with Cursor, Continue, or Windsurf?

Yes. Any MCP client that supports stdio servers will accept the same
`command` + `env` configuration shown in the [MCP client setup](#mcp-client-setup)
section.

### Is the server read-only?

Reads are always on. Write tools (`trigger_build`, `stop_build`,
`cancel_queue_item`) are registered by default but can be suppressed entirely
by setting `JENKINS_MCP_READONLY=1` — the server then never registers a
mutating tool, so an agent literally cannot call one.

### Do I need a Jenkins plugin to use this?

No extra plugin is required for the core read tools — they hit standard
Jenkins endpoints. Pipeline-specific tools (`get_pipeline_stages`,
`get_stage_log`) require the Pipeline plugin, which most Jenkins installations
already have.

### Can the LLM agent grep through a multi-gigabyte console log?

Yes. Use `get_console_log_path` to force-cache the full log for a finished
build to disk; the tool returns a local path the agent can then `Read`,
`Grep`, or `Bash` natively. The cache is LRU-evicted and capped by
`JENKINS_MCP_CACHE_MAX`.

### Does it handle Ginkgo test failures specifically?

Yes. `get_ginkgo_failure_summary` parses Ginkgo's `Summarizing N Failure` block and
surfaces the first `[ERROR]` line tagged with each spec name plus surrounding
context — much faster than asking the agent to scan the whole log.

### Is it safe to give an LLM API token access to Jenkins?

The server uses a single Jenkins user's API token (not a password), confined
to one `JENKINS_URL`. Combine with `JENKINS_MCP_READONLY=1` and a least-privilege
Jenkins user for the safest default. See [SECURITY.md](SECURITY.md) for the
full threat model.

## License

[MIT](LICENSE) © Adarsh Singh
