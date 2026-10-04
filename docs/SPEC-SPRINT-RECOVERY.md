# Failed attempt recovery tracer

This is a declared tracer, not a production apply command. Its pure planner prepares existing
`ReworkReq` values, including the existing rework tier field. It neither executes rework nor
claims a checkpoint was discovered, preserved, pushed, accepted or completed.

The sprint store owns state and operation receipts. The provider owns job/artifact observations.
One supplied snapshot contains the exact cohort IDs, epoch, primary revision, work generation,
attempt, state, current head, failure, tier, fix, provider outcome, observed artifacts and diff.
No second store or local cache owns truth. At 2,000 attempts of at most 16 KiB of observation strings each this is 32 MiB,
100 times the observed 20-card recovery cohort, fitting one process. The planner caps the cohort
at 2,000. Large briefs/logs stay outside this projection; the planner refuses over 16 KiB of strings or 256 artifacts per attempt.

## States and verbs

`Build` is Plan: read an exact cohort, reject incomplete/duplicate identities or absent caps,
canonicalize ID order, classify every member, hash the complete input and return one result.
`Prepare` is an apply preparation, not Apply: regenerate the saved plan to detect alteration,
recognize a matching durable receipt as AlreadyApplied, otherwise compare a fresh snapshot's
identity and refuse stale input. It returns all existing rework requests only when every member
is eligible. The eventual store verb must check epoch and every revision/generation atomically
with rework and its operation receipt; a check followed by an unfenced write is not an apply.
The operation cannot record an applied receipt before a store commit. Repeated preparation
without a committed receipt returns the same intent, not successful execution.

The matching receipt permits an idempotent retry after the original attempt has moved. It is a
trusted store receipt, never a client assertion. Rework retains dependencies, base and ordinary
acceptance/read gates. Its existing `BaseOf` selects only earlier ok=yes full40 pushed heads;
a failed reported head alone is not permission to stage there.

## Classification and limits

A live, accepted or non-review card is held. An unknown provider outcome is held because the
provider may have accepted work. Structured provider causes are infrastructure, including
rate limit, auth, credit and timeout; repair availability first. These never promote a tier.
The exact harness malformed commit-SHA finding is mechanical. It requires a sound brief and
literal bounded correction; repeated formatting failure still preserves its tier.
Free-form semantic prose does not establish capability. Pro is proposed only after a sound
brief, a targeted flash retry, an explicit independent capability disposition, flash tier and
no model pin. A bounded concrete fix is still required. All other failures need diagnosis.
Tokens, deadline seconds and maximum micro-currency cost must be explicit positive limits.
Preparation carries them; production apply must enforce them against the selected retry route
before admission, and refuse if it cannot. This tracer does not silently claim route limits.

## Checkpoint boundary

Unknown artifact inspection holds every otherwise eligible action. Existing artifacts or a
nonempty diff hold preparation until a real provider adapter preserves the checkpoint. The
tracer intentionally has no guessed artifact directory, host SSH, wildcard push, automatic
patch application or failed-work success fiction. Empty inspected artifacts/diff can prepare
rework at the existing verified successful base. A future checkpoint adapter must prove owned
job identity, full40 source head, base ancestry, tracked/untracked path scope and patch identity,
then use an owned recovery ref or scoped patch. Failed commits do not change `PushedHead`.

## Validation

Unit probes cover malformed SHA repeats without promotion, unsupported capability promotion,
sound targeted pro intent, infrastructure/unknown-outcome holds, uninspected/dirty artifact
holds, exact cohort identity, tampered-plan refusal, fresh generation/head/revision/limit drift,
all-or-none preparation and replay receipts after movement. `SprintRecovery.tla` models plan,
external drift, prepare and commit receipt; no fabricated checkpoint or completion transition.
The concrete fleet examples come from the observed flash50 progress reports, not invented
API responses. Tests/TLC require a coordinator bench slot; source formatting is not validation.
