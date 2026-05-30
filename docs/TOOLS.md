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

## `health_check`

Run a fixed battery of read-only probes against the configured Jenkins and
return a one-line-per-check report. Use this to validate a fresh install or
to debug "the agent says it can't see Jenkins".

No inputs.

Checks (each row is independently labelled `OK` / `WARN` / `ERROR`):

- **Jenkins reachable** — the `X-Jenkins` header version on `/api/json`.
- **Authenticated** — the user resolved from `/me/api/json`.
- **CSRF crumb issuer** — `enabled`, or `disabled` (HTTP 404 from
  `/crumbIssuer/api/json`) which downgrades POSTs to crumb-less.
- **Pipeline plugin / JUnit plugin** — presence and active flag from
  `/pluginManager/api/json`. Non-admin tokens that 403 on this endpoint
  surface as WARN (`plugin status unknown`), not ERROR.
- **Nodes** — online / offline counts from `/computer/api/json`.
- **Clock skew** — server `Date` header vs local clock, WARN above 60s.

Trailing **Effective configuration** block reads from each value's natural
home — `d.Cache.Dir` / `d.Cache.MaxBytes` for the cache, `d.Client.Timeout()`
for the HTTP timeout, plus the server-policy fields (binary version, read-only
mode) the process resolved at startup.

## `get_plugin_versions`

List installed Jenkins plugins with their versions via
`/pluginManager/api/json`. Use this to answer "is plugin X loaded, at what
version?", "which plugins have updates pending?", or "what's the active
git-plugin version?" — questions that `health_check` (which probes a fixed
two-plugin set) cannot.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name_filter` | string | no | — | Case-insensitive RE2 regex matched against plugin `shortName`. |
| `include_inactive` | bool | no | `false` | Include disabled or failed plugins. Default keeps the listing focused on what's actually running. |

Output is a sorted table of `shortName | longName | version | pinned |
hasUpdate`, with `active | enabled` columns added when
`include_inactive=true`. The header reports `N of M plugins shown` and the
active scope so truncation is unambiguous.

Capped at **200 rows**. The cap is rarely hit on real instances; when it is,
the footer points the caller at `name_filter`. A 403 (token lacks
`Overall/Read` on `/pluginManager`) degrades to a clear hint rather than an
error — same shape as `health_check`'s `WARN` plugin row.

## `whoami_can`

Resolve the configured token's effective Read / Build / Cancel / Configure
permissions on a job. Jenkins doesn't expose a direct "list my permissions"
endpoint, so the tool infers from a small set of read-only `GET` probes and
HTTP status codes. Use this up-front before triggering or cancelling a build
to avoid burning a turn on a 403.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job (or folder) path. |

Probes:

- **read** — `GET /<job>/api/json`. `200` ⇒ OK; `403` or `404` ⇒ DENIED.
- **build** — `GET /<job>/build`, falling through to
  `/<job>/buildWithParameters`. `405` on either ⇒ OK (permission held, just
  the wrong verb); `403` on both ⇒ DENIED.
- **cancel** — `GET /<job>/lastBuild/stop`. `405` ⇒ OK; `403` ⇒ DENIED.
  Surfaces as `N/A (no last build)` when there isn't one to stop.
- **configure** — `GET /<job>/configure`. `200` ⇒ OK; `403` ⇒ DENIED.

For folders (detected via `_class`), `build` and `cancel` render as
`N/A (folder)`. Any other response (5xx, transport error) maps to UNKNOWN.

This tool stays read-only even when `JENKINS_MCP_READONLY=false` — every
probe is a `GET`, no endpoint is ever POSTed to.

Output:

```
Permissions for alice (Alice) on team/my-job:
  read       OK
  build      OK
  cancel     DENIED
  configure  DENIED
```

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

## `list_branches`

Enumerate the branches of a `WorkflowMultiBranchProject` with per-branch
last-build state. Multibranch jobs are the standard way to model
PR + long-lived-branch CI in Jenkins; this tool gives the agent a focused
view that `list_jobs` (which is generic) does not.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated path to a `WorkflowMultiBranchProject` (e.g. `Builds/team/svc-x`). Multibranches show up as `type=folder` in `list_jobs`. |
| `name_filter` | string | no | — | Case-insensitive RE2 regex matched against each branch name. |
| `healthy_only` | bool | no | `false` | Exclude branches whose last build was not `SUCCESS`. Never-built branches are also excluded under this flag. |

Output is a compact table: `branch | last# | result | duration | last_built_at | url`.
Duration is rendered as a Go duration string truncated to whole seconds
(e.g. `2m15s`); `last_built_at` is the build start time in UTC RFC 3339.
Branches that have never been built render `-` in the build-related columns.

When `job_path` resolves to a `_class` that isn't a `WorkflowMultiBranchProject`
(for example, a regular `FreeStyleProject` or an `OrganizationFolder`), the
tool returns a hint that points back at `list_jobs` instead of an empty
table.

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

## `get_scm_context`

Return the per-commit change history for one build: commit id, author,
timestamp, message subject, and each commit's touched paths with a
single-letter edit code (`A` add, `M` edit/modify, `D` delete; other plugin
edit-type strings fall back to their upper-cased first letter). Pipeline
builds may produce multiple change sets (one per `checkout` step); they are
flattened in order with a per-set header.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. `0` = `lastBuild`. |
| `max_commits` | integer | no | `50` | Cap commits rendered; a footer notes when the cap is hit. |
| `path_filter` | string | no | — | Case-insensitive RE2 regex; only commits touching a matching path are returned. |

Each commit renders as a header line followed by one indented line per
touched file:

```
abc1234 alice 2026-05-16 12:04  "Fix flake in PaymentSpec"
  M  internal/payment/processor.go
  A  internal/payment/processor_test.go
```

A top-level `Culprits: …` line is included when Jenkins reports culprits
for the build. Builds with no SCM changes render `(no commits in change
set)`.

## `last_green_build`

Report the most recent successful build of a job via
`/<job>/lastSuccessfulBuild/api/json`. Use as the "start point" for triage
— pair with `changes_since_last_green` to see what's landed since.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |

Output:

```
Last green build of team/svc: #42
  Finished: 2025-05-16 12:00 (UTC)
  URL:      https://jenkins.example.com/job/team/job/svc/42/
```

A `no successful build yet for <job>` hint is returned (not an error)
when Jenkins reports 404 on the endpoint — i.e. the job has never had a
green build.

## `changes_since_last_green`

Union the commits across every completed build since the job's last
successful one. Walks `previousCompletedBuild` from the latest completed
build down to (but not including) the last green, skipping aborted and
in-progress builds. Dedupes by `commitId` (first sighting wins).

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `max_commits` | integer | no | `100` | Cap on commits rendered; a footer notes when the cap is hit. |
| `path_filter` | string | no | — | Case-insensitive RE2 regex; only commits touching a matching path are returned. Mirrors `get_scm_context`. |

Output: a one-line header followed by `get_scm_context`-style commit
rows.

```
3 commits across 2 builds since last green #40 (latest: #42)
abc1234 alice 2025-05-16 12:00  "Fix payment retry"
  M  internal/payment/processor.go
def5678 bob 2025-05-16 12:00  "Refactor cache"
  M  internal/jenkins/cache.go
```

Special cases:

- **All green** — when the latest completed build is the last green, the
  tool returns `all green — last completed build #C is the same as last
  successful build for <job>` instead of an empty rendering.
- **No green ever** — returns `no successful build yet for <job>` hint.
- **Wide window** — when more than 50 builds sit between the last green
  and the latest completed build, a `(wide window: N builds between
  greens — review carefully)` footer is appended.

## `compare_builds`

Diff two builds of the same job. Use this to answer "build B failed but A
passed — what changed?" in a single call instead of fanning out across
`get_build_info`, `get_pipeline_stages`, `get_test_report`, and
`get_scm_context` twice each.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_a` | integer | yes | — | Older / baseline build number. Must be `> 0`; `lastBuild` is not accepted. |
| `build_b` | integer | yes | — | Newer / candidate build number. Must be `> 0` and different from `build_a`. |
| `include_tests` | bool | no | `true` | Set false to skip the per-test diff on large suites (saves one `/testReport/api/json` call per build). |

The SCM diff is **direct**: commits in `build_b`'s change set whose
`commitId` is not in `build_a`'s change set. Intermediate builds between A
and B are not walked. For closely-spaced builds this is the same as the
full delta; for builds far apart, expect only B's own change set to render
under the SCM section.

Output sections (in order):

- **Header**: `Result: A → B`, `Duration: A → B (Δ ±t)`.
- **Parameters**: `+`, `-`, and `~` (changed) lines. Unchanged parameters
  are omitted.
- **SCM**: commits in B-but-not-A.
- **Stages (changed only)**: stages whose status flipped, plus stages
  present in only one build (renamed pipelines).
- **Tests**: four buckets — `pass → fail`, `fail → pass`, `new`,
  `removed` — capped at 100 names each (counts above the lists are exact).
  New tests carry an inline `[PASSED]`/`[FAILED]` annotation since the
  bucket itself mixes statuses.

If either build is missing pipeline data (HTTP 404 on `/wfapi/describe`)
or test data (HTTP 404 on `/testReport/api/json`), the affected section
renders a hint instead of failing the whole comparison.

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

## `tail_running_build`

Stream a capped slice of an in-flight build's console via Jenkins'
`/<job>/<build>/logText/progressiveText` endpoint. Designed for
progressive tailing: the response footer echoes `Next since_byte=N`
which the agent passes back on the follow-up call to advance through
the log without re-fetching the prefix.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build to tail. `0` = `lastBuild`. |
| `since_byte` | integer | no | `0` | Start byte offset. Echo back from the previous call's footer. |
| `max_bytes` | integer | no | `65536` | Cap on bytes returned per call. Hard-capped at 1 MB. |

**Cache invariant:** the result of this tool is never written to the
on-disk cache. The cache contract is *finished builds only*; the
progressive endpoint can produce partial content for in-flight builds,
which would corrupt the cache.

Output is the byte chunk followed by a one-line footer:

```
<chunk text>
--- bytes 32768..98304 (more=true). Next since_byte=98304 ---
```

When the build has finished and there are no more bytes, the footer
points at `get_console_log_path` for the cached full log:

```
<chunk text>
--- bytes 32768..120480 (more=false; build finished). Use get_console_log_path for the cached full log. ---
```

Special cases:

- **No new bytes** — when `since_byte == X-Text-Size`, the body
  renders `(no new bytes; build still running)` (or `…; build
  finished`) and the footer leaves the offset unchanged.
- **Truncated** — when the wire returned more than `max_bytes`, the
  chunk is truncated and `more=true` regardless of build state. The
  next offset is `since_byte + max_bytes`.

## `get_build_info`

Pretty-printed build summary: number, result, duration, parameters, change
set. The Jenkins `tree` selector is explicit so the response stays small.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build number. |

## `get_build_environment`

Return three labelled sections — `Cause`, `Parameters`, and `Injected
Env Vars` — for a single build. Common cause of failures is a wrong env
var or unexpected upstream trigger; `get_build_info` shows parameters
but not env or trigger reason. This is the deep tool.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build to inspect. `0` = `lastBuild`. |
| `name_filter` | string | no | — | Case-insensitive RE2 regex applied to injected env var names only. Cause and parameters always render in full. |

Endpoints:

- **Cause + Parameters** — `/<job>/<build>/api/json` with an `actions[…]`
  selector. Cause is rendered verbatim from Jenkins' `shortDescription`
  (e.g. *"Started by user alice"*, *"Started by upstream project foo
  build 42"*, *"Started by an SCM change"*, *"Started by timer"*).
- **Injected env vars** — `/<job>/<build>/injectedEnvVars/api/json`,
  provided by the **EnvInject plugin**. When the endpoint returns 404,
  this section degrades to a one-line hint that the plugin isn't
  installed; the other two sections still render.

Output:

```
Cause:
  Started by user Alice Example

Parameters:
  BRANCH=main
  RELEASE_VERSION=1.2.3
  API_TOKEN=(masked)

Injected Env Vars (122 total, 8 after filter):
  GIT_BRANCH=main
  GIT_COMMIT=abc1234
  ...
```

Notes:

- Secret-typed parameter values that Jenkins masks server-side render
  as `(masked)`. No unmasking is attempted.
- Empty Cause / Parameters sections render `(none)` rather than being
  omitted, so the section structure is stable.
- Env vars are sorted alphabetically for deterministic output.

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

## `get_pipeline_script`

Return the Jenkinsfile a specific build actually ran. Critical for
triaging old builds — `main` may have moved on since, so the job-level
Jenkinsfile is the wrong answer.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | no | `0` | Build to pin to. `0` = `lastBuild`. |

Two-tier fallback, source provenance surfaced in the output header:

1. **Replay plugin** — `GET /<job>/<build>/replay/` returns the HTML
   replay page; the build-pinned Jenkinsfile lives in `<textarea
   name="mainScript">`. This is the faithful source. Tagged
   `(source: replay)`.
2. **Job-level `config.xml`** — `GET /<job>/config.xml`. If the job
   defines an inline pipeline script (`CpsFlowDefinition`), it's
   returned tagged `(source: job-config-fallback)` with a NOTE that the
   build-pinned source was unavailable.

For Pipeline-from-SCM jobs (`CpsScmFlowDefinition`) where the
Jenkinsfile lives in git rather than in `config.xml`, the tool returns a
hint with the SCM repo URL, branch, and `scriptPath` so the agent can
clone and read it independently:

```
Pipeline script for team/svc build #42: build-pinned source unavailable.
Job uses Pipeline from SCM:
  repo:   git@github.com:foo/bar.git
  branch: main
  path:   ci/Jenkinsfile
Use get_scm_context to find the commit, then clone+read the Jenkinsfile.
```

If both tiers fail, the tool returns an error describing both failure
reasons. The Replay endpoint requires the Replay plugin and the
`Run/Replay` permission on the build — `whoami_can` is the right tool
to check that up-front.

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

## `get_flaky_candidates`

Rank flaky tests across the latest `sample_size` completed builds of one
job by counting pass↔fail flips in each test's status sequence. Use this
to surface "which tests are flaky here?" — the discovery step before
drilling into one test's history with `get_test_report`.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `sample_size` | integer | no | `20` | Number of most-recent completed builds to inspect. Capped at 50; in-progress builds at the head don't count. |
| `min_flips` | integer | no | `2` | Minimum pass↔fail transitions for a test to appear in the output. |
| `include_skipped` | bool | no | `false` | When true, SKIPPED counts as a state in the flip sequence (a PASS→SKIP→FAIL pattern then registers 2 flips). When false, SKIPPED records are ignored. |

A "flip" is an adjacent state change in a test's status sequence ordered
by build number ascending. `PASSED`/`FIXED` collapse to `PASS`;
`FAILED`/`REGRESSION` collapse to `FAIL`. Unknown statuses are treated
as absent.

Builds without a test report (HTTP 404 on `/testReport/api/json`) are
skipped and counted in a footer line. The tool emits a hint instead of
the table when fewer than two completed builds exist under the job.

Output: header with the effective inputs, optionally the "no test
report" footer, then a sorted table:

```
test                                                          flips  passes  failures  last_seen_build
------------------------------------------------------------  -----  ------  --------  ---------------
pkg.payment.AuthorizeCard                                         5       8         7               86
pkg.refund.RefundFlow                                             3      10         5               85
```

Rows are sorted by flips desc, then failures desc, then test name asc
for stable ordering. The `test` column is rune-truncated at 60 chars.

## `get_test_history`

Per-build trend of a single test across the last N completed builds —
the natural follow-up to `get_flaky_candidates` once a suspect test is
known. Answers *"when did this start flipping?"* in one call.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `test_full_name` | string | yes | — | Fully-qualified test name. Accepts both `className.name` (e.g. `com.example.FooTest.bar`) and `className/name`. |
| `sample_size` | integer | no | `20` | Builds to scan. Capped at 50; in-progress builds at the head don't count. |
| `include_skipped` | bool | no | `false` | When true, `SKIPPED` appears in the timeline and counts toward the flip total. When false, `SKIPPED` rows are suppressed. |

Output: a header, the per-build timeline (newest first), and a summary
line:

```
History of com.example.FooTest.bar in svc (20 builds):

  build  result    status       duration  error head
  -----  --------  -----------  --------  --------------------------
   #91   SUCCESS   PASS         0.42s
   #90   FAILURE   FAIL         1.10s     AssertionError: expected ...
   #89   SUCCESS   PASS         0.38s
   ...

Summary: 17 PASS, 3 FAIL, 0 SKIP. 4 status flips in window.
```

Special rows:

- **`(no report)`** — the build had no `/testReport/api/json` (HTTP 404).
- **`(missing)`** — the build had a report but didn't include this test
  (added later, renamed, or moved between suites).

Returns a hint instead of the table when the test wasn't seen in *any*
build in the window. Parallel-fetches the per-build test reports
(concurrency 6) and shares the build-discovery helper with
`get_flaky_candidates`.

## `find_test_by_name`

Locate which job runs a given test. Walks the job tree under
`folder_path` (or root) recursively, then fans out per-job probes
against `/<job>/lastCompletedBuild/testReport` and matches test full
names (`className.name`) against a case-insensitive substring.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `substring` | string | yes | — | Case-insensitive substring matched against fully-qualified test names. |
| `folder_path` | string | no | `""` | Scope the search to a folder. Empty = Jenkins root. |
| `max_results` | integer | no | `50` | Cap on hits. Capped further at 200. |

Each job probe makes two requests (`lastCompletedBuild/api/json` for
the build number+result, then `…/testReport/api/json` for the cases),
wrapped in a 5s per-job timeout so a single stuck job doesn't block
the whole search. Probes run with concurrency 6 (shared `fetchPerItem`
helper).

Output:

```
Tests matching "must_return_404" under "" (root, recursive):

  job_path                     test full name                          last_seen_build  result
  ---------------------------  --------------------------------------  ---------------  -------
  Builds/team/integration      com.example.FooSpec.must_return_404     #4521            SUCCESS
  Builds/team/smoke            com.example.api.HealthSpec.must_…       #832             FAILURE

Inspected 47 jobs (3 skipped: no test report).
```

Footer reports how many jobs were inspected and how many were skipped
across three buckets:

- `N skipped: no test report` — `/testReport/api/json` returned 404.
- `N skipped: no completed build` — `/lastCompletedBuild/api/json`
  returned 404 (job hasn't completed once).
- `N timed out` — per-job probe exceeded the 5s budget.

When the substring matches no test in any inspected job, the body
renders `(no matches)` instead of the table.

## `find_recent_failures`

Survey failed builds across the jobs under `folder_path` within a
lookback window. Answers *"what broke overnight under Builds/team?"*
without an agent loop over `list_jobs` + `get_build_info`.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `folder_path` | string | no | `""` | Scope the search. Empty = Jenkins root. |
| `since` | string | no | `"24h"` | Lookback window. Go duration syntax (`24h`, `30m`, `1h30m`) plus `Nd` for days (`7d`, `14d`). |
| `result_filter` | string | no | `"FAILURE"` | One of `FAILURE`, `UNSTABLE`, `ABORTED`, or `ANY_NON_SUCCESS`. |
| `max_results` | integer | no | `100` | Cap on rows. Capped further at 500. |

Walks the job tree recursively, then fans out per-job
`/api/json?tree=builds[number,result,timestamp,duration,url]{0,5}`
probes (5 builds per job covers the 24h common case). Filters each
build by `timestamp >= now - since` and `result` matching
`result_filter`. Probes run with concurrency 6 (shared `fetchPerItem`
helper).

Output:

```
Recent failures under "Builds/team" (last 24h0m0s, filter=FAILURE):

  job_path                                build  result    finished              duration
  --------------------------------------- ------ --------- --------------------- --------
  Builds/team/integration-tests           #92    FAILURE   2026-05-23 22:14 UTC  4m32s
  Builds/team/smoke-tests                 #831   FAILURE   2026-05-23 18:02 UTC  1m12s

2 results across 47 jobs scanned.
```

Footers:

- **Truncation** — `(stopped at max_results=N — narrow folder_path,
  since, or result_filter)` when results were capped.
- **Wide window** — when `since > 7d`, a hint that only the last 5
  builds per job were inspected and older failures within the window
  may be missed.

Sort is `timestamp` desc. Jobs whose `/api/json` returns 404 (or 403)
contribute zero rows but still count toward `N jobs scanned`.

## `get_ginkgo_failure_summary`

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

## `trigger_build`

Queue a build. With no `parameters`, POSTs to `/<job>/build`; with
parameters, POSTs to `/<job>/buildWithParameters` with each entry sent as
a raw form field (no type coercion). Returns the queue item URL from the
response `Location` header. **Mutating** — suppressed when
`JENKINS_MCP_READONLY` is truthy.

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `parameters` | object<string,string> | no | — | Form fields for `buildWithParameters`. Values are sent as-is. |
| `wait_for_start` | bool | no | `false` | When true, poll `/queue/item/<id>/api/json` for up to 60s and return the assigned build number once the item leaves the queue. |

## `stop_build`

Abort a running build via `/<job>/<n>/stop`. **Mutating** — suppressed
when `JENKINS_MCP_READONLY` is truthy. Confirm the abort took effect with
`get_build_info` (expect `result=ABORTED`).

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `job_path` | string | yes | — | Slash-separated job path. |
| `build_number` | integer | yes | — | Must be positive; `lastBuild` is not accepted. |
