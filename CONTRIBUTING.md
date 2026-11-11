# Contributing to the steermi CLI

The general rules (DCO sign-off, AI-assisted contributions, review) are in the
[organization guide](https://github.com/steermi/.github/blob/main/CONTRIBUTING.md).
This page covers the development loop; details are in
[docs/development.md](docs/development.md).

## Quick start

```bash
git clone https://github.com/steermi/cli && cd cli
make check                       # what CI runs
go run ./internal/fixture/server # a synthetic gateway; see docs/development.md
```

No Steermi account, Claude subscription or credentials are needed for any
test.

## Guidelines

- Keep commands, flags and configuration paths backward compatible within a
  minor version. Document user-visible changes in `CHANGELOG.md` and the
  README.
- Never print, log or mirror credentials. New output that might carry user
  data goes through `internal/redact`.
- Filesystem and process code must handle paths with spaces and shell
  metacharacters. Pass arguments as argv, never through a shell; when a shell
  is unavoidable (hooks), quote with `plugin.ShellQuote`.
- Activity rules (capabilities, transitions, launch IDs) live in
  [agent-contracts](https://github.com/steermi/agent-contracts). Change them
  there, not here.
- New API calls need a fixture route (`internal/fixture`) or an `httptest`
  server in the package's tests.
- Tests: `testify`, temporary directories for every home and repository,
  synthetic fixtures only.

## Pull request checklist

`make check` passes, behavior changes have tests, docs are updated, commits
are signed off.
