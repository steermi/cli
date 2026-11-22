# Changelog

All notable changes to the steermi CLI are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[semantic versioning](https://semver.org/).

## Unreleased

### Added

- `--pause-after-step` on `work`, `run` and `next`: asks Steermi to pause the
  flow after each step (`step_mode: ask`, sent once, on the claim). Before
  the checkout is prepared and the agent starts, the CLI prints what Zoe
  will do in a flow loop ("Zoe will run Build → Review automatically · max 3
  rounds · no new round after $20"). The line comes from the loop summary
  Steermi sends in the claim (`loop`: steps, rounds, budget, advance); the
  CLI never derives it from the run's hops. When Steermi cannot record the
  pause (the run's flow does not continue), the CLI prints one line saying
  so instead of ignoring the flag.
- `steermi done` names what follows (it waits up to five seconds for an
  automatic step): "Review starts in Steermi: <link>" for
  an automatic cloud step, the reason when it paused (for example "Paused: no
  AI key for Steermi runs"), or, for a step a person starts or one that runs
  on this machine, the work item link: starting it there continues the same
  flow (its loop and its step's skill) and gives the `steermi run` command.
  A stopped loop is reported before any successor; a successor is "started"
  only while it is ready or running; a local successor waiting for this
  machine prints `steermi run lx_…` from its `launch_public_id`; a suggested
  one waits for a person to confirm it.
  The CLI never suggests `steermi work` for a next step, which would start
  an unrelated run.
- `steermi_complete_task` checks `activity_result.outputs` with the
  contract (`OutputRef.Normalize`): a short commit SHA is refused to the
  agent right away, naming the field.

### Changed

- Go 1.27.4 or newer is required.
- Requires `github.com/steermi/agent-contracts` v0.4.0 (step modes, hop
  locations, `auto_paused_reason`, `review_inconclusive`, the new loop stall
  rules and deciding approval nodes). Needs Steermi with the flow run UX
  release.
- The launch claim always sends a JSON body (`{}` without
  `--pause-after-step`); Steermi requires it.

## [0.1.0] - 2026-11-11

First public release.

### Added

- Commands: `login`, `logout`, `whoami`, `doctor`, `next`, `work KEY`,
  `run LAUNCH_ID`, `done`, `handoff`, `profile`, `repos`, `setup` and
  `version`; `steermi` without arguments shows a status home. Flags may come
  before or after positional arguments, and `steermi COMMAND --help` prints
  the command's flags.
- Runs are skills: `steermi work KEY --skill ID` takes a built-in id such as
  `steermi:review` or a skill uuid (default `steermi:build`). The launch card
  and `done` name the run's skill as Steermi reports it. Only runners that
  change code may edit files; an unknown or missing runner is read-only.
- Agents: Claude Code, Codex CLI, GitHub Copilot CLI and opencode. Each gets
  the steermi MCP server and the launch instructions through per-run flags,
  environment and files under `.steermi/agent/`, never through its global
  configuration. Background mode for every agent; live transcript mirror and
  resume for Claude Code, Codex CLI and Copilot CLI (opencode mirrors the
  summary only).
- Profiles (`~/.config/steermi/profiles.json`, `steermi profile
  list|add|use|rm|follow|path`): an agent plus a model per tier (fast,
  standard, deep), per-runner defaults and `follow_hints ask|auto|never`.
  `.steermi/runner.yaml` may name a `profile:`.
- Model suggestions: Steermi's tier and effort hint picks the profile's
  model; `--profile`, `--agent`, `--model`, `--effort` and `--yes` on `next`,
  `work` and `run`.
- Launch card before the agent starts (checkout, agent, model and why,
  access, mirroring) with `m` / `p` / `q`; colour on terminals only
  (`NO_COLOR`).
- `steermi done` reports the agent, version, model and effort it ran with,
  passes the agent's stated assumptions to Steermi and names what follows
  from the run's delivery flow: the next step and whether it starts
  automatically in the cloud, the statuses it sets, a stopped quality loop
  or the end of the flow.
- MCP tools for agents, including work item links (blocks / is blocked by,
  relates to, duplicates, causes, clones) and follow-ups linked to their
  source item.
- `steermi version` reports version, commit and build date (`--json` for
  scripts); the gateway sees the version in the `User-Agent`.
- `steermi doctor` checks every agent, profiles, the CLI build and credential
  file permissions, separates optional warnings (`!`) from problems (`✗`)
  and exits non-zero when a problem is found.
- Transcript mirroring redacts common credential formats (API keys, tokens,
  private keys, passwords in URLs) before upload.
- A fixture gateway (`go run ./internal/fixture/server`) for trying the CLI
  without a Steermi account.
- The repository is a Claude Code marketplace:
  `/plugin marketplace add steermi/cli`.

[0.1.0]: https://github.com/steermi/cli/releases/tag/v0.1.0
