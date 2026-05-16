# Contributing to jenkins-mcp-go

Thanks for your interest in contributing! This document covers the
practicalities — how to set up your environment, what conventions to follow,
and how to get your change reviewed and merged.

## Ways to contribute

- **Report a bug.** Open an issue with a minimal reproduction and the Jenkins
  endpoint(s) involved. If you can paste an anonymized snippet of the
  response that surprised the tool, even better.
- **Propose a feature.** Open an issue describing the use case first — small
  scope is the project's most important design constraint, so let's agree on
  whether something belongs before code is written.
- **Send a pull request.** See below.

## Development setup

```sh
git clone https://github.com/2001adarsh/jenkins-mcp-go.git
cd jenkins-mcp-go
make build         # produces ./bin/jenkins-mcp
make test          # runs go test ./...
make lint          # golangci-lint, optional but recommended
```

Go 1.23 or newer is required. There are no other build-time dependencies.

To test against a real Jenkins, export the credentials and run the binary
directly — it speaks MCP over stdio, so it will hang waiting for a client.
Hook it up to your MCP client (Claude Desktop, Claude Code, Cursor, etc.)
following the instructions in [README.md](README.md#mcp-client-setup).

## Pull request guidelines

1. **Branch off `main`.** Keep the topic branch focused on a single concern.
2. **Run `make fmt test vet` before opening the PR.** CI runs the same.
3. **Add or update tests** when you change behavior. Pure logic lives in
   `internal/` packages and should be exercised by unit tests; network
   interactions can be exercised against `net/http/httptest`.
4. **Keep commits clean.** Squash fixup commits before requesting review.
5. **Sign your commits if you can.** `git commit -s` adds the
   `Signed-off-by` trailer.

## Project conventions

- **Read-only.** The server must never expose write operations against
  Jenkins (no triggering builds, no posting comments, no configuration
  changes). New tools must be `GET`-only.
- **One Jenkins per process.** All requests target the host configured at
  startup. There is no per-tool host argument — this keeps the credential
  story simple.
- **Environment over flags.** Credentials and runtime knobs come from
  environment variables. Process arguments are visible to other users on the
  same machine.
- **`internal/` for everything.** The Go module exports the `main` package
  only. Library reuse is an explicit non-goal.
- **Errors include enough context to act on.** Wrap with `fmt.Errorf(...:
  %w, err)` and include the URL path or input that failed.
- **Comment the *why*, not the *what*.** Code already explains the latter.

## Coding style

- `gofmt -s` clean (`make fmt` does this).
- `go vet ./...` and `golangci-lint run` clean.
- Exported identifiers carry a doc comment that starts with the identifier
  name.
- Prefer small, focused functions; the existing tool handlers are a good
  reference for response shape and error handling.

## Reporting security issues

Please don't open a public issue for security reports. See
[SECURITY.md](SECURITY.md) for the disclosure process.

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE).
