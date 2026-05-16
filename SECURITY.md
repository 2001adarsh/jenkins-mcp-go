# Security Policy

## Supported versions

This project is at version `0.x` and only the latest minor release receives
fixes. Once `1.0` is cut, this section will be updated to list the supported
branches.

| Version | Supported |
| --- | --- |
| latest `0.x` | :white_check_mark: |
| earlier | :x: |

## Reporting a vulnerability

**Please do not open a public GitHub issue for security reports.** Public
disclosure before a fix is available puts users at risk.

Instead, report privately via GitHub's
[private vulnerability reporting](https://github.com/2001adarsh/jenkins-mcp-go/security/advisories/new)
flow. Include:

- A description of the issue and its impact.
- Steps to reproduce, ideally with a minimal proof of concept.
- The version (or commit) you tested against.
- Your assessment of severity, if any.

You can expect:

- An acknowledgement within **3 business days**.
- A first technical response within **7 business days**.
- A coordinated disclosure timeline once the issue is triaged.

## Design boundaries you can rely on

The project intentionally limits what it can do, and the test for
"is this a security issue?" is whether the implementation falls short of
these guarantees:

- **No write operations against Jenkins.** Every code path issues `GET`
  requests. If a tool ever issues `POST`/`PUT`/`DELETE`, that's a defect.
- **Single configured host.** Network requests target `JENKINS_URL` only.
  Tools must never accept an arbitrary URL.
- **Credentials only in HTTP Basic auth headers.** They are not echoed in
  tool output, not written to disk, and not logged.
- **Cache writes are confined to the cache directory.** Filenames are
  sanitized to a flat slug so path traversal cannot escape the cache root.
- **Cached files are only written for finished builds.** A running build's
  log can never be persisted as if it were complete.

Reports of behavior that violates any of the above are treated as security
issues, not feature requests.
