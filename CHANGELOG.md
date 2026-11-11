# Changelog

All notable changes to the steermi CLI are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[semantic versioning](https://semver.org/).

## Unreleased

- Runs are skills, not activities: `steermi work KEY --skill ID` (a built-in
  id such as `steermi:review` or a skill uuid; default `steermi:build`)
  replaces `--kind build|review|plan`; `steermi profile use NAME --runner
  answer|code|findings` replaces `--kind`, and `profiles.json` stores
  `runners` instead of `kinds`. Launch state and API fields `kind` are
  `runner` (agent-contracts `activity.Runner`); the launch card and `done`
  name the run's skill (`skill_name` from the claim bundle; the CLI never
  derives a name). Runners are typed (`activity.Runner`) and
  `Runner.ChangesCode` is the one read-only test: an unknown or missing
  runner is read-only. The first prompt no longer says Plan/Review; it tells
  the agent to follow the run's skill and states only the edit boundary.
  `steermi done` names the next step by the flow hop's `name`.
- Work item links: the MCP tool `get_work_item_relationships` is now
  `get_work_item_links` (`GET /v1/work_items/:id/links`: blocks / is blocked
  by, relates to, duplicates, causes, clones and the Blocked state), and
  follow-ups link to their source item through `POST /v1/work_items/:id/links`.
- Agents beyond Claude Code: Codex CLI, GitHub Copilot CLI and opencode
  adapters (`internal/agent`). Each gets the steermi MCP server and the
  launch instructions through per-run flags/env/files under
  `.steermi/agent/`, read-only Plan/Review, background mode and (Codex,
  Copilot) a live transcript mirror and resume. opencode mirrors the summary
  only.
- Profiles (`~/.config/steermi/profiles.json`, `steermi profile
  list|add|use|rm|follow|path`): an agent plus a model per tier, per-kind
  defaults, `follow_hints ask|auto|never`. `.steermi/runner.yaml` may name a
  `profile:`.
- Model suggestions: the claim bundle's `model_hint` (tier, effort, reason)
  picks the profile's model; `--profile`, `--agent`, `--model`, `--effort`,
  `--yes` on `next`, `work` and `run`.
- Launch card before the agent starts (checkout, agent, model and why,
  access, mirroring) with `m` / `p` / `q`; `steermi` without arguments is a
  status home; `steermi next` filters by text; `doctor` checks every agent
  and profiles; colour on terminals only (`NO_COLOR`).
- `steermi done` reports the agent, version, model and effort it ran with
  (`runner`, self-reported).
- Launch state keeps the agent run (`agent_run`: agent, session, version,
  model, effort); older state files are read as Claude Code launches.
- Flags may come before or after positional arguments for every command.

- Builds with Go 1.27.3 (`go` directive and CI/release toolchain).

- `steermi_complete_task`: `activity_result` may carry `assumptions`; `steermi done` passes them to Steermi, which lists them on the run.

- `steermi done` names what follows a submission from the run's pinned
  delivery flow hop (`activity.Run.FlowNext`): the next step and whether it
  starts automatically in the cloud (never as a local chain), the statuses it
  sets, a stopped quality loop, or the end of the flow. It falls back to a
  generic hint when the run cannot be read.

First public release. The CLI moved out of the Steermi monorepo into
`github.com/steermi/cli`; commands, flags and configuration paths are
unchanged.

### Added

- `steermi version` reports version, commit and build date (`--json` for
  scripts); the gateway sees the version in the `User-Agent`.
- `steermi doctor` also checks the CLI build and credential file permissions,
  separates optional warnings (`!`) from problems (`✗`) and exits non-zero
  when a problem is found.
- Transcript mirroring redacts common credential formats (API keys, tokens,
  private keys, passwords in URLs) before upload.
- `steermi COMMAND --help` prints the command's flags and exits successfully.
- A fixture gateway (`go run ./internal/fixture/server`) for trying the CLI
  without a Steermi account.
- The repository is a Claude Code marketplace:
  `/plugin marketplace add steermi/cli`.

### Fixed

- Hook commands quote the CLI path as one POSIX shell word. Install paths with
  `$`, backticks or quotes could previously be interpreted by the shell.
- The marketplace copy of the plugin calls `steermi` from `PATH` instead of
  unrendered placeholders.
- `steermi work` usage listed `implement`; the kinds are `build`, `review`,
  `plan`.
