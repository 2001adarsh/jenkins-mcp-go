# Tool reference

All tools take a `job_path` and a `build_number`. The path is slash-separated;
folder boundaries are converted to Jenkins' nested `/job/...` form internally.

A URL like

```
https://jenkins.example.com/job/Builds/job/team/job/job-name/86/
```

becomes

```json
{ "job_path": "Builds/team/job-name", "build_number": 86 }
```

`build_number: 0` (or omitted) resolves to `lastBuild`.

---

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
