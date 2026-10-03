# nova-decide read fixture draft

`fixtures.json` is a compact 20-example draft for the built-in `read` decision. It keeps the five question IDs and answer types expected by nova-decide:

- `does_task`, `lines_changed`, `inside_paths`, `defect`: `yes` or `no`
- `verdict`: `LAND`, `BOUNCE`, or `UNSURE`

These are evidence-backed dispositions from independent AI source reviews and the dated sprint audit, not model predictions. No probabilities or predicted answers are supplied.

The ten BOUNCE examples reconstruct wrong-card work packets from saved sprint card snapshots and immutable GitHub diffs. The ten LAND examples use independently source-reviewed candidates. Each row includes its task excerpt, PATHS, exact base/head, changed-file patches, expected answers, and question evidence. Ancillary ledger permissions and the authorized rename target are explicit where needed. External context required to establish a defect is marked separately from what task and diff alone show.

The live calibration appendix confirms all twenty IDs with round and review label: the wrong examples are r1/wrong, and the LAND candidates have the rounds recorded per example with label ok. Exact identity with original Jev inputs remains unverified because the `set.json` rows and immutable packet/head hashes were not relayed. This is a reconstructed historical source-review fixture, not an exact replay of the original calibration packets.

Wrong-card task excerpts run from `THE TASK` through the complete listed-target block before `STEP 1`. Execution/JOB boilerplate and local absolute paths are omitted; the task requirements, targets, and repository-relative paths are kept. Each row records the raw source artifact hashes and the current card revision. The captured current brief may differ from the historical brief at dispatch.

Personal and host aliases are deterministically pseudonymized throughout tasks, patches, context, evidence, and target identifiers. The original-source hashes refer to unmodified input artifacts; transformed strings are not byte-identical to those sources. The exact mapping is in `pseudonym-map.local.md` for local audit only and must not be included in a published artifact.

The packet contents are embedded in `fixtures.json`; the `source_refs` are optional audit-trail pointers to the current workspace evidence. The fixture adds data only; no provider calls were made during its construction or review. A consuming unit test remains part of the tool implementation.
