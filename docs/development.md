# Development

## Requirements

- Go 1.27.4 or newer, `git`, Python 3 (documentation check only).
- macOS or Linux. Windows builds compile but are not supported yet.
- No Steermi account or Claude credentials: tests use temporary homes, fake
  binaries and a fixture gateway.

## Build and test

```bash
make check          # gofmt, vet, tests, race tests, go.mod tidiness, docs
make build          # ./bin/steermi with version metadata
make vuln           # govulncheck
make snapshot       # local GoReleaser build of every release archive (needs goreleaser)
```

The Makefile sets `GOWORK=off`, so your build uses the published
`agent-contracts` version, exactly like CI.

### Changing agent-contracts at the same time

Clone both repositories side by side and create a workspace file outside
both checkouts (never commit it):

```bash
cd ..   # the directory holding cli/ and agent-contracts/
go work init ./cli ./agent-contracts
cd cli && go test ./...   # plain go finds ../go.work; make targets ignore it
```

Land the contracts change first; then bump the version here with
`go get github.com/steermi/agent-contracts@vX.Y.Z`.

## Try it without an account

The fixture gateway implements pairing, token refresh, `whoami`, `next` and
claiming with synthetic data:

```bash
go run ./internal/fixture/server            # terminal 1, listens on 127.0.0.1:8787

export STEERMI_CONFIG_DIR=$(mktemp -d)      # terminal 2: keep your real login untouched
go run ./cmd/steermi login --url http://127.0.0.1:8787 --no-browser
# paste the code: stp_fixture
go run ./cmd/steermi whoami
go run ./cmd/steermi next
go run ./cmd/steermi doctor
```

Launches, MCP tools, mirroring and merge requests need a real Steermi
installation; the fixture answers them with `NOT_IMPLEMENTED_IN_FIXTURE`.
`cmd/steermi/main_test.go` runs this walkthrough automatically, including
expired and revoked device tokens.

## Tests

- `testify` (`assert`/`require`), table-driven where it helps, next to the
  code under test.
- Use `t.TempDir()` for homes, config directories and repositories; never
  touch `~/.config/steermi` or `~/.claude`.
- Git tests create bare origins and point remotes at them with `insteadOf`.
- Fixtures are synthetic. Assemble token-shaped strings at run time (see
  `internal/redact/redact_test.go`) so the secret scan stays clean.

## The plugin

`plugin/steermi` is embedded into the binary. Its files call `steermi`; at
launch `plugin.Write` rewrites the MCP command and hook commands to the
running executable (shell-quoted for hooks). Keep commands in that form so
the marketplace copy, which uses `PATH`, keeps working.
