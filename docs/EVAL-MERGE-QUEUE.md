# Evaluation: GitHub's Native Merge Queue vs the Land Loop

## Summary

This page evaluates GitHub's native merge queue against the land loop
(`nova-merge run --loop`). Every row answers whether the queue can handle
a fact that decides whether an entry may land, with a yes or a no and a
reason drawn from [GitHub Docs — Merge queues](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-configurations-for-branch-protection/about-protected-branches#about-merge-queues)
and the lane specification in docs/SPEC-MERGE.md.

The four deciding facts come from Johnny (2026-09-21): the queue must be
judged on whether it can refuse on the two facts that decide landing here
— the typed HOLD at head, and the non-author friend line — and whether
it carries ALLOWED_RED and batches like the land loop does.

## Capability matrix

| capability | queue can? | reason |
|---|---|---|
| Typed non-author friend line at head | **no** | A merge queue inspects hosted CI statuses; it does not inspect reads recorded by named humans (`read --who <name> --head <sha> --verdict approve|hold`). The lane stores reads as immutable files under `<lane>/reads/`, folded into `state.json`, and a `hold` verdict blocks merge unconditionally (SPEC-MERGE.md rule 11, rule 18). The merge queue knows nothing of people or their names. |
| Refuse on a HOLD at head | **no** | There is no HOLD primitive in the merge queue. A PR in the queue merges when its merge-commit passes branch protection. Nothing lets a coordinator or reviewer insert a blocking veto mid-queue the way `nova-merge read --verdict hold` stops an entry (SPEC-MERGE.md rule 19). #1558 (held 2/10 for a self-check test that asserts bytes it wrote) would land unblocked. |
| Carry ALLOWED_RED for a named check | **no** | Required checks must pass. There is no allowlist, override, or planned-red slot in branch-protection configuration. The lane's `run --planned-red <text>` declares a temporary red before green (SPEC-MERGE.md rule 6); the queue refuses to merge any PR whose required check is failing, period. |
| Batch the way the land loop batches | **yes** | The queue creates merge commits that include all queued PRs, which is a form of batching. However, the land-loop batch is ordered, serial, and conflict-aware: each entry is gated individually, conflicts are BLOCKED with a file list (rule 7), the base is re-merged only into conflicting entries (rule 3), and the publication is an atomic compare-and-swap push (rule 21). The queue's batch is flat — all PRs merged into a single merge commit simultaneously, without per-entry gating. |

## Verdict

The land loop does something the queue cannot: it requires a **typed
non-author friend line at head** — a named human must review and record
approve or hold, keying the verdict to the exact SHA they inspected.
This is not a CI check and cannot be simulated by one. The queue
inspects automated statuses only; it has no model for a person, a name,
or a hold. Two concrete consequences follow:

1. **#1558 would land.** PR #1558 was held 2/10 at head for a test that
   asserts bytes it wrote — a human judgment call, not a failing check.
   The queue sees green and lands. The lane sees `hold` and stops.

2. **Planned reds are impossible.** The queue cannot carry
   ALLOWED_RED for a named check; the lane accepts a declared temporary
   red, prints it on every `RUN PASS`, and proceeds toward green.

These two gaps mean the queue is an automation tool and the lane is a
coordinator. When the decision requires a person, the queue defers to
automated evidence and loses. The lane owns the decision.
