# nova-test — specification (draft 1, 2026-09-17)

`nova-test` is a proposed binary at the **validation layer** (name
provisional, nova-tools #247). It makes repository validation reproducible
across local and hosted runs: a versioned manifest names what is validated
and how, an equivalent prior run is reused instead of dispatched twice, and
a failure arrives as a compact receipt rather than a log to reread. It is
distinct from `nova-check`'s record checks, `nova-swarm`'s workers and
`nova-release`'s publication; it does not start a declared instance (that
name belongs to the proposed `nova-run`).

This spec is normative. If the code and this document disagree, one of them
has a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md),
whose **Conventions** section — exit codes, no guessed paths, the one-line
output grammar, the cap-and-count rule, `internal/oneline` and
`internal/bounded` — applies here unchanged and is not restated.

This draft covers the first slice only: read-only `plan`, run lookup,
`status --since` and `failures`/`receipt` extraction for one repository.
Execution (dispatch of a new run) comes later, after the receipt contract
below is reviewed. Nothing here is a new gate for current release or Fixed
Tables work. Related: #185 (over-budget CI samples, tier-routing repair),
#83 #183 #229, specs #236/#237.

## Verbs

- `plan` reads a versioned repository validation manifest — source,
  dependency and environment identity, affected scope, required fast checks
  and explicitly requested/full/nightly tiers — and prints the concrete plan
  before execution. Configured recipes do not expand caller authority.
- `run` finds an equivalent running or completed local or hosted run before
  dispatch and reuses it. Equivalence includes source, dependencies,
  workflow/recipe revision, environment, policy, event/trust context and
  required coverage; matching SHA alone is insufficient.
- `status` surveys recent runs (`status --since`): queue, cancellation drain,
  execution and end-to-end latency per run, with attempt identities and
  prior failures preserved.
- `failures` lists the bounded failing steps of a run with source-backed
  excerpts; full logs remain retrievable. Successful checks are
  distinguished from unexecuted, skipped or missing ones.
- `receipt` prints the compact failure receipt for one run: identity,
  equivalence key, bounded failing steps and excerpts, and latency.

## Rules

1. The manifest is versioned; `plan` names its revision in the first line.
2. Reuse requires full equivalence (verb `run`); a SHA match with a changed
   dependency, recipe revision, environment, policy, event/trust context or
   required coverage is not equivalent and must not reuse.
3. Receipts are bounded: at most the failing steps plus excerpts, one MORE
   line naming the remedy (the log holding the whole list).
4. Per-change CI belongs in fast tiers (one minute ideal, two minutes
   maximum); longer work belongs to explicit/nightly certification without
   discarding coverage.
5. Deadlines bound every subprocess; bounded output, supervisor receipts
   and safe ambiguous-dispatch reconciliation reuse the swarm execution
   substrate rather than creating another supervisor.

## Tests

Duplicate request reuses the run; stale green does not cover a newer
commit; a newer failed attempt supersedes; a partial rerun covers only its
scope; a changed dependency breaks equivalence; a timeout, a missing job
and a superseded cancellation each read as their own receipt. Measure
coordinator turns and duplicate execution with unchanged coverage.
