<!--
Thanks for sending a PR! A few things to check before you hit "Create":

  - Run `make fmt test vet` (CI runs the same).
  - Read CONTRIBUTING.md if you haven't yet.
  - Keep the branch focused on a single concern; squash fixup commits.
-->

## Summary

<!-- 1-3 sentences on what this changes and why. -->

## Type of change

- [ ] Bug fix
- [ ] New tool / feature
- [ ] Refactor (no behavior change)
- [ ] Docs only
- [ ] Other:

## Behavior

<!--
For tool changes: name the tool(s), the inputs you added/changed, and a
sample response. For bug fixes: describe what was wrong and what now happens.
-->

## Test plan

- [ ] `make test` passes
- [ ] `make lint` passes (or N/A)
- [ ] Manually exercised against a real Jenkins (describe how)

## Scope checklist

- [ ] Respects the `JENKINS_MCP_READONLY` gate (if this is a mutating tool)
- [ ] Single-host: no per-tool host / URL argument
- [ ] No credentials in tool output, errors, or logs
- [ ] Docs updated (`README.md` and/or `docs/TOOLS.md`) if behavior changed
