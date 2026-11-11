# steermi CLI

Public Go module `github.com/steermi/cli`: the `steermi` command and its
embedded Claude Code plugin. It runs Steermi tasks in a developer's own
coding agent (Claude Code, Codex CLI, Copilot CLI, opencode). The hosted Steermi service (private repository) owns
coordination, authorization and the gateway API this CLI calls. Shared activity and launch rules come from
`github.com/steermi/agent-contracts` (sibling checkout `../agent-contracts`).

## When to change this repository

- Any `steermi` command, flag, config file, worktree/branch handling, login,
  mirroring, hooks, MCP tools exposed to agents, agent adapters
  (`internal/agent`), profiles (`internal/profile`), or plugin content
  (`plugin/steermi`: commands, skills, hooks, MCP config).
- A Steermi gateway change that the CLI must call or tolerate: change the
  service first (backward compatible), then this repository, and add the
  route to `internal/fixture` when contributors should be able to exercise
  it.
- Not here: runners (answer, code, findings), capabilities, run transitions, launch IDs, PKCE,
  branch naming. Those live in `agent-contracts`.

## Layout

`cmd/steermi` (commands), `internal/{api,config,login,gitx,launch,agent,
profile,ui,mcpserver,mirror,redact,deeplink,buildinfo,fixture}`, `plugin/` (embedded
plugin), `.claude-plugin/marketplace.json` (marketplace entry pointing at
`plugin/steermi`). Details: `docs/architecture.md`.

## Rules

- Agent adapters implement `agent.Agent`; never edit an agent's global
  config (per-run flags, env, `.steermi/agent/` files only). Answer and
  findings runs must stay read-only on every agent. Third-party flags and transcript formats
  change: keep adapter tests and parsers defensive (unknown lines skipped).
- Steermi suggests a tier + effort (`hint` in agent-contracts), never a
  model; profiles map tiers to models (`profile.Resolve`, one place).

- Keep commands, flags and config paths compatible within a minor version;
  record user-visible changes in `CHANGELOG.md` and `README.md`.
- Never log, print or mirror credentials. Uploads of user/agent text go
  through `internal/redact`. Credential files stay 0600 under
  `~/.config/steermi` (`STEERMI_CONFIG_DIR` overrides).
- Exec with argv, never a shell. Hooks are the exception: plugin sources call
  `steermi`, and `plugin.Write` rewrites them with `plugin.ShellQuote`.
- Launch state writes go through `config.UpdateLaunch`; flushes are
  serialized per launch and messages carry stable `external_id`s.
- Local completion is never acceptance or verified CI; answer/findings runs
  finish with saved outputs/findings, code runs with a pushed commit.
- Tests: testify, `t.TempDir()` homes/repos, fake binaries on `PATH`, the
  fixture gateway or `httptest`. No real tokens; assemble token-shaped
  strings at run time.
- Pre-1.0 pin: dependency versions match the Steermi service unless an
  upgrade is the point of the change.

## Local development

`../go.work` (outside every repository, never committed) links `cli` and
`agent-contracts`; it also maps the not-yet-published
`agent-contracts v0.1.0` to the checkout. `make` targets set `GOWORK=off`
like CI, so they only work once that version is published; until then run
`go` directly (it finds `../go.work`), e.g. `go test ./... -count=1`. Try commands against
`go run ./internal/fixture/server` (pairing code `stp_fixture`).

## Checks

`make check` (gofmt, vet, tests, race, `go mod tidy -diff`, docs),
`make vuln`, `make snapshot` (GoReleaser, local only). CI:
`.github/workflows/ci.yml`; tags: `release.yml` builds a draft release with
provenance (`docs/releasing.md`). Never push, tag or publish without an
explicit request.

Keep `AGENTS.md` and `CLAUDE.md` in sync (`CLAUDE.md` imports this file).
