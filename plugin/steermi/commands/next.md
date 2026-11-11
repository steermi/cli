---
description: Pick the next Steermi task (your current cycle first) and set this checkout up for it
allowed-tools: Bash(steermi next:*), Bash(steermi run:*)
---

1. Run `steermi next --json` and show me the items as a short numbered list
   (identifier, title, priority, status). Mention items waiting for review if
   `in_review_count` is above zero.
2. Ask which one I want (default: the first).
3. Run `steermi next --pick <number> --here --no-agent` to claim it if needed and
   prepare this checkout (branch + `.steermi/TASK.md`).
4. Read `.steermi/TASK.md` and start working on it as described there.
