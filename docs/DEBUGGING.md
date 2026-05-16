# Debugging

This guide covers how to exercise `jenkins-mcp-go` outside of a real MCP client
— useful when you're adding a tool, chasing a tool-call regression, or trying
to understand why a Jenkins endpoint isn't behaving the way you expected.

## TL;DR

```sh
# 1. Build.
make build

# 2. Set credentials.
export JENKINS_URL=https://jenkins.example.com
export JENKINS_USER=you@example.com
export JENKINS_API_TOKEN=...           # API token, not your password

# 3. Optional: turn on per-request logging.
export JENKINS_MCP_DEBUG=1

# 4. Launch MCP Inspector with the binary as its server.
npx @modelcontextprotocol/inspector \
  -e JENKINS_URL=$JENKINS_URL \
  -e JENKINS_USER=$JENKINS_USER \
  -e JENKINS_API_TOKEN=$JENKINS_API_TOKEN \
  -e JENKINS_MCP_DEBUG=$JENKINS_MCP_DEBUG \
  ./bin/jenkins-mcp
```

Then open the URL Inspector prints, click **Connect**, and use the **Tools**
panel to call any of the registered tools.

## Enabling server-side request logging

By default the server is silent during normal operation — useful tools but no
visibility into *what it's doing*. Set `JENKINS_MCP_DEBUG=1` (or any non-empty
value) to get a single line per Jenkins HTTP call and per cache event:

```
jenkins: req  GET /job/folder/job/name/lastBuild/api/json
jenkins: resp 200 GET /job/folder/job/name/lastBuild/api/json in 412ms (3821 bytes)
jenkins: cache miss <cache-dir>/folder__name-123.log
jenkins: req  GET /job/folder/job/name/123/consoleText
jenkins: resp 200 GET /job/folder/job/name/123/consoleText in 1.2s (8492311 bytes)
jenkins: cache write <cache-dir>/folder__name-123.log (8492311 bytes)
```

All lines go to **stderr** (visible in Inspector's *Server stderr* pane).
Stdout is reserved for MCP protocol frames and is never written to from the
logger.

## Common failure modes and where to look

### 1. Inspector connects but the tools list is empty / call returns nothing

The server probably died at startup. **Server stderr** will tell you why.
Usually one of:

- `JENKINS_URL, JENKINS_USER, and JENKINS_API_TOKEN must all be set` — pass
  the env vars to Inspector (see above).
- `JENKINS_MCP_TIMEOUT must be a positive Go duration` — value wasn't a Go
  duration like `30s`/`2m`.

### 2. Tool call returns HTTP 404 with a long URL containing `/job/job/job/...`

Triple `/job/` clusters mean the `job_path` you passed already contained
`/job/` separators. The server prepends `/job/` to every slash-separated
token, so a URL-shaped input gets each `job` token re-wrapped.

| Bad input | Correct input |
| --- | --- |
| `/job/folder/job/sub/job/name` | `folder/sub/name` |
| `https://.../job/folder/job/name/86/` | `job_path=folder/name`, `build_number=86` |

See `JobAPIPath` in `internal/jenkins/client.go` for the exact transform.

### 3. Tool call returns HTTP 401 / 403

- 401 — wrong token. Regenerate at Jenkins → user menu → Configure → API
  Token → Add new Token.
- 403 — token is valid but the user doesn't have read access to that job.
  `get_build_info` against a job you can open in the browser confirms basic
  auth works.

### 4. `get_console_log_path` returns a path but the file is missing or empty

The cache only persists logs from **finished** builds — it looks for the
`Finished: SUCCESS|FAILURE|ABORTED|UNSTABLE|NOT_BUILT` marker before writing
to disk. With `JENKINS_MCP_DEBUG=1` you'll see one of:

- `cache write <path> (<bytes>)` — persisted.
- `cache skip <path> (build not finished)` — build hadn't finished when
  fetched.

### 5. `get_pipeline_stages` works but `get_stage_log` returns `length=0`

Common for declarative wrapper stages — Jenkins doesn't store per-stage
output for them. Fall back to `get_console_log_path` and grep the full log.

### 6. Slow tool calls

Enable `JENKINS_MCP_DEBUG=1` and look at the `in <duration>` field. If the
duration is hitting `JENKINS_MCP_TIMEOUT` (default 90s), Jenkins itself is
slow — not the client. Try the same URL with `curl -u user:token` to
confirm.

## Alternatives to MCP Inspector

### Raw JSON-RPC on stdio

For a smoke test in a script:

```sh
export JENKINS_URL=... JENKINS_USER=... JENKINS_API_TOKEN=...
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | ./bin/jenkins-mcp
```

Sends `initialize` then `tools/list`. The binary will exit when stdin closes.
Useful for CI smoke tests that don't need Node installed.

### A real MCP client

For end-to-end validation, register the binary with Claude Desktop or Claude
Code per the [README](../README.md#mcp-client-setup). Each client captures
stderr to its own log file:

- **Claude Desktop (macOS):** `~/Library/Logs/Claude/mcp-server-jenkins.log`
- **Claude Code:** `claude --debug` surfaces server stderr inline.

## When you should add new log calls

The existing `debugf` is the right primitive. Add new calls at:

- **Layer boundaries** (HTTP in/out, cache in/out, disk read/write).
- **Branches that change observable behavior** (cache skipped, fallback
  taken, retry happened).

Don't add logs inside hot inner loops (e.g. per regex match in the failure
parser) — they will dominate the output and obscure the high-level flow.
Aggregate counts at the end of the operation instead.
