# Troubleshooting

Start with `steermi doctor`. It checks the CLI version, the configuration
directory and credential permissions, Claude Code, Git, your login and the
current checkout's link, and exits non-zero when something required is
missing.

| Message | Cause and fix |
|---|---|
| `not logged in — run steermi login` | No stored device. Run `steermi login`. |
| `DEVICE_REVOKED … run steermi login again` | The device was revoked in Steermi or your membership ended. Log in again. |
| `cannot reach Steermi at …` | Network or wrong URL. Check `STEERMI_URL` and `steermi whoami`. |
| `LOCAL_LAUNCH_DISABLED` | The Steermi installation has local launches turned off. Ask an administrator. |
| `SESSION_EXECUTOR_LOCKED` | Another run (cloud or another device) holds the session. Wait, or take it over in Steermi. |
| `… was just taken by someone else` | Another person claimed the item first. Run `steermi next` again. |
| `… is not linked` (doctor) | Run `steermi repos link` inside the checkout so launches find it. |
| `… is readable by other users` (doctor) | `chmod 600` the credentials file shown. |
| Launch expired | Pending launches expire after 15 minutes. Create a new one. |

The mirror reads Claude Code's transcript from `~/.claude/projects`. If the
Steermi session stays empty, run `steermi hook flush` inside the worktree and
check the error it prints.

Still stuck? Open an issue with `steermi version` and `steermi doctor` output
(redact names you consider private).
