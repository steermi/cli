---
name: steermi-review
description: Use when finishing a Steermi task before /steermi:done, or in a Steermi review launch - reviewing the branch's changes against its base for correctness, tests and scope.
---

# Reviewing Steermi task changes

1. Diff the branch against its base (`git diff origin/<base>...HEAD`); the base
   is in `.steermi/TASK.md`.
2. Check each acceptance criterion in TASK.md is met and tested.
3. Look for correctness bugs, missing error handling at boundaries, security
   issues (secrets, injection, authz), and unrelated changes.
4. Run the tests.
5. Report findings by severity with `file:line` and a concrete fix. In a review
   launch, post them with `create_comment`; before `/steermi:done`, fix the
   important ones instead.
