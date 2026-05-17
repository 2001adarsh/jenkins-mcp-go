# Tool reference

Build-targeted tools take a `job_path` and a `build_number`. The path is
slash-separated; folder boundaries are converted to Jenkins' nested
`/job/...` form internally.

A URL like

```
https://jenkins.example.com/job/Builds/job/team/job/job-name/86/
```

becomes

```json
{ "job_path": "Builds/team/job-name", "build_number": 86 }
```

`build_number: 0` (or omitted) resolves to `lastBuild`. `list_jobs` is the
discovery tool — it takes a `folder_path` in the same slash-separated form
(empty = root).

---

## `list_jobs`

Enumerate jobs and folders under `folder_path` (root when empty). Use this
when the caller doesn't already know a `job_path`.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `folder_path` | string | no | `""` | Slash-separated folder path (e.g. `team/integration`). Empty = Jenkins root. |
| `recursive` | bool | no | `false` | Walk into sub-folders (Folder, OrganizationFolder, WorkflowMultiBranchProject). |
| `name_filter` | string | no | — | Case-insensitive RE2 regex matched against each job's leaf name. A plain substring works too. |

Output is a compact table of `type`, `status` (Jenkins `color`), last build
number/result, `job_path`, and the entry's `url`. Folders are always
traversed when `recursive=true` regardless of whether their own name
matches `name_filter` — matching jobs may live inside them.

The response is capped at **500 entries**. If the cap is hit, the output
ends with a hint to narrow with `folder_path` or `name_filter` (Jenkins'
`/api/json` does not paginate).

## `get_console_log`

Fetch the build's `/consoleText`. Returns the last 500 lines by default; pass
`tail_lines` explicitly to control the size, or use a negative value for the
full log.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. `0` = `lastBuild`. |
| `tail_lines` | integer | no | `500` | Lines from the end to return. Use a negative value for the full log. |

The response ends with a `— full log cached on disk at: <path>` footer when
the build is finished and the log is on disk.

## `get_console_log_path`

Download (if needed) the full console log for a **completed** build and return
its on-disk path. Lets the agent `Read`/`Grep`/`Bash` the full log natively —
useful when an earlier test contaminates a later one and you need to inspect
everything that came before.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | yes | — | Must be `> 0`. Only completed, immutable builds are cached. |

## `search_console_log`

Run a Go `regexp` (RE2 syntax) over the console log and return matches with
surrounding context.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. |
| `pattern` | string | yes | — | RE2 regex. |
| `context_lines` | integer | no | `3` | Lines of context before/after each match. |
| `max_matches` | integer | no | `50` | Cap on matches returned. |

Each match is rendered with a 1-indexed line number and an arrow marker on the
hit line.

## `get_build_info`

Pretty-printed build summary: number, result, duration, parameters, change
set. The Jenkins `tree` selector is explicit so the response stays small.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. |

## `get_pipeline_stages`

List Declarative/Scripted Pipeline stages for a build via `/wfapi/describe`.
Returns status and duration per stage. Use this first to find the failing
stage; if `get_stage_log` returns `length=0` for that stage (common for
declarative wrappers), fall back to `get_console_log_path`.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. |

## `get_stage_log`

Fetch a single pipeline stage's log via
`/execution/node/<stage_id>/wfapi/log`.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. |
| `stage_id` | string | yes | — | Stage id from `get_pipeline_stages` (e.g. `"188"`). |

## `get_test_report`

Fetch structured JUnit results from `/testReport/api/json`. Failed cases are
returned with `className`, `name`, `duration`, `errorDetails`, and a head+tail
snippet of the stack trace.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. |
| `stack_trace_lines` | integer | no | `30` | Head + tail lines to show from each stack trace. |

Returns a hint message if the build has no JUnit publisher (HTTP 404).

## `get_failure_summary`

Parse Ginkgo's `Summarizing N Failure` block and, for each failing spec,
return the first `[ERROR]` line tagged with that spec name plus surrounding
context.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. |
| `context_lines` | integer | no | `20` | Lines of context around each spec's first `[ERROR]` line. |

Ginkgo-specific. Returns a hint when the log doesn't look like Ginkgo (no
summary block found).

## `list_nodes`

List all Jenkins agents/nodes via `/computer/api/json`. Returns a compact
table of name, status (`online`/`offline`/`temp-off`), executor count, idle
flag, and labels, plus a per-node offline-cause section when any agent is
offline with a reason. Use this to diagnose "why is this build still queued"
— typically the matching agents are all offline.

No inputs.

## `get_node`

Single-node detail via `/computer/<name>/api/json`. Adds per-executor idle
state and the full monitor data map on top of the listing fields.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | string | yes | — | Node name. Use `"(built-in)"` or `"(master)"` for the controller depending on Jenkins version. URL-encoding is handled internally. |

## `list_queue`

List pending Jenkins queue items via `/queue/api/json`. Each entry shows the
queued task, how long it has waited, state flags (`buildable`, `blocked`,
`stuck`), and the Jenkins "why" reason — the most useful field for
diagnosing "why hasn't this build started yet".

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path_prefix` | string | no | — | Case-sensitive substring matched against each item's task URL. Use to narrow the listing to one folder. |

## `cancel_queue_item`

Drop a pending queue item by id before it starts. **Mutating** — suppressed
when `JENKINS_MCP_READONLY` is truthy. Uses the Jenkins `/queue/cancelItem`
endpoint, which (by design) returns HTTP 404 on success with an empty body;
a 404 with a body is treated as "item already left the queue".

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `item_id` | integer | yes | — | Queue item id (from `list_queue`). Must be positive. |
