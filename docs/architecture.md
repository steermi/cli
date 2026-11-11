# Architecture

The CLI runs a Steermi skill (the built-in Plan, Build or Review, or any
skill the organization adds) in the developer's own
coding agent (Claude Code, Codex CLI, Copilot CLI or opencode) and
subscription. Steermi supplies authorized
context, scoped MCP tools, an execution lease and a live transcript mirror.
The developer keeps control of local filesystem permissions and Git
credentials.

```text
 steermi login ──PKCE──▶ Steermi            device refresh token (0600)
 steermi run lx_… ──claim──▶ Steermi        launch token + TASK.md bundle
        │
        ├─ git worktree ~/.steermi/worktrees/<repo>/<KEY>
        └─ claude --plugin-dir ~/.config/steermi/plugin/steermi
               ├─ MCP: steermi mcp ──launch token──▶ Steermi (scoped tools)
               └─ hooks: steermi hook flush ──▶ session mirror (redacted)
 steermi done ──git push (your credentials)──▶ Git provider; MR via Steermi
```

## Packages

| Path | Responsibility |
|---|---|
| `cmd/steermi` | Command-line parsing and subcommands |
| `internal/config` | Paths, credentials, repository links and per-launch state under `~/.config/steermi` (0700 directory, 0600 files, file locks) |
| `internal/api` | Gateway client: envelope decoding, device-token refresh and retry |
| `internal/login` | Browser PKCE pairing with a loopback callback, or paste-a-code |
| `internal/gitx` | Remote normalization, worktrees, branches, push |
| `internal/launch` | `run`, `work`, `next`, `done`, `handoff`, hooks and transcript flushing |
| `internal/agent` | Agent adapters (`agent.Agent`): Claude Code, Codex CLI, Copilot CLI, opencode — command line, MCP/instructions injection, session discovery, transcript parser |
| `internal/profile` | `profiles.json` and `Resolve`: which profile, agent, model and effort a launch runs with |
| `internal/ui` | TTY-aware styles, rows and prompts |
| `internal/mcpserver` | stdio MCP server exposing the launch's scoped tools |
| `internal/mirror` | Agent JSONL transcripts (Claude Code, Codex rollout, Copilot events) → Steermi chat messages |
| `internal/redact` | Removes known credential formats before upload |
| `internal/deeplink` | `steermi://launch/<id>` handler registration |
| `internal/buildinfo` | Version, commit and build date |
| `internal/fixture` | Synthetic gateway for tests and local experiments |
| `plugin/` | The embedded Claude Code plugin (MCP config, hooks, commands, skills) |

Activity definitions, capability allowlists, run transitions, launch IDs and
PKCE come from
[`github.com/steermi/agent-contracts`](https://github.com/steermi/agent-contracts),
the module the hosted service also uses. The MCP server only exposes tools the
activity's capability set allows.

## Agents and model choice

The claim bundle carries `model_hint` (agent-contracts `hint.For`: tier
`fast|standard|deep`, effort, reason) from the run's skill effort, loop round
and a failed predecessor. `profile.Resolve` picks the profile (`--profile` →
`--agent` → runner.yaml `profile:` → per-kind → default → first installed
agent) and the model (`--model` → hint tier through the profile's ladder →
profile default → agent default). The launch card shows the result and the
reason. Agents without hooks are mirrored by the CLI's heartbeat every 15 s
and once on exit; Codex and Copilot sessions are found after start (Codex by
working directory, Copilot by start time) and stored in the launch state's
`agent_run` (agent, session, version, model, effort, start; a different
agent starts a new one). Adapters implement `agent.Agent`; mirroring and
resume come from the optional `agent.Transcripts`, session discovery from
`agent.SessionFinder`. `steermi done` reports `runner {agent, agent_version,
model, effort}`; Steermi stores it in the launch result as self-reported.

## Launch lifecycle

1. `steermi login` pairs the device through browser PKCE. Only a refresh token
   and a short-lived access token are stored.
2. A launch is created in Steermi ("Work locally") or by `steermi work KEY`.
   `steermi next` lists eligible work. Pending launches expire after 15
   minutes. Only the developer starts or resumes a launch; nothing starts
   Claude automatically.
3. Claiming a launch returns a bundle: a launch token scoped to that one task,
   `TASK.md`, the system prompt and the Claude session ID. The CLI prepares
   the worktree and writes `.steermi/` (identifiers only, excluded through
   Git's `info/exclude`).
4. Claude Code runs with the plugin. MCP tool calls use the launch token; the
   service checks live membership, source access, current revisions and the
   session lease on every call.
5. Hooks (`Stop`, `PostToolUse` on `TodoWrite`, `SessionEnd`) flush new
   transcript lines. Each message carries a stable `external_id`, so retries
   and concurrent flushes never duplicate. Flushes are serialized per launch
   with a file lock; the mirror offset only advances after a batch is stored.
6. `steermi_complete_task` saves a structured result, then `steermi done`
   finishes: a code run pushes with your Git credentials and opens a merge
   request; answer and findings runs submit saved outputs or findings without
   commits. The runner (answer, code, findings) follows from what the skill
   hands back.
   `steermi handoff` stops and leaves a note for an explicit continuation.

Every launch is bound to an activity run, its input digest and a generation.
Taking a run local or back to the cloud starts a new generation, which fences
old launch tokens. Finishing a launch is never human acceptance, and locally
reported tests are never provider-verified CI.

## What the hosted service does

Coordination, authorization, tenant isolation, launch bundles, execution
leases and Git provider integration run in the hosted Steermi service, which
is not open source. The CLI cannot grant itself access: launch tokens admit
only the endpoints that launch needs.

## Security boundaries

- Steermi API scopes do not sandbox your machine. Claude Code's own
  permission settings decide what the agent may do locally; `--background`
  uses `acceptEdits` plus the repository's `allowed_tools`.
- Hook commands run through a shell. The plugin quotes the CLI path as a
  single POSIX word, so unusual install paths cannot inject commands.
- Credential files are 0600; `steermi doctor` flags looser permissions.
- Transcript redaction is best effort. It covers common key and token
  formats, not every secret.
