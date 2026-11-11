---
description: Submit the current Zoe activity and its evidence
allowed-tools: Bash(steermi done:*), Bash(git status:*), Bash(git diff:*), Bash(git log:*)
---

Read the run's skill and what it hands back in `.steermi/TASK.md` before submitting.

- **Project Plan session without a work item:** Save structured suggestions using `propose_foundation_artifact`; a person reviews and publishes them in Steermi. Call `get_foundation_artifact` and record its exact `artifact_id`, `revision` and `artifact_hash` in `activity_result: {"outputs":[{"kind":"artifact","resource_id":"<artifact_id>","revision":"<revision>","hash":"<artifact_hash>"}]}`, then run `steermi done`. No direct domain writes, commit or MR are required; a summary alone is insufficient.
- **Answer (for example the built-in Plan):** Save the outputs in their authorized Steermi destination.
  Call `steermi_complete_task` with `summary` and `activity_result: {"outputs":[{"kind":"docs","resource_id":"<saved-page-id>"}]}`,
  then run `steermi done`. No commit, push or merge request is required.
  List anything you assumed instead of knowing (the product, users, scope) in
  `"assumptions": ["..."]` (up to 12, one sentence each); Steermi shows them
  on the run so people can correct them.
- **Code (for example the built-in Build):** Review the implementation against the requirements and run relevant
  tests. Commit the implementation when the developer authorizes it, call
  `steermi_complete_task` with the changes and evidence, then run `steermi done`
  to push and open the merge request when applicable.
- **Findings (for example the built-in Review):** Save findings and the
  revision/evidence assessed. Do not fix source code: changes need a separate
  code run. Call `steermi_complete_task` with `summary` and `activity_result: {"recommendation":"changes_requested","findings":[{"id":"f1","severity":"high","description":"Explain the finding"}]}`, then run `steermi done`. No new commit or merge request is required.

Show the submission result. A local submission never constitutes human acceptance
or provider-verified CI.
