---
description: Start a specific Steermi task by key (e.g. STE-123) in this checkout
argument-hint: <KEY>
allowed-tools: Bash(steermi work:*)
---

Run `steermi work $ARGUMENTS --here --no-agent` to prepare this checkout for the
task (branch + `.steermi/TASK.md`). Then read `.steermi/TASK.md` and start
working on it as described there.
