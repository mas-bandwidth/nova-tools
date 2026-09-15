# Token accounting across every way we work

Status: implementation requirement and review draft. This is a coverage amendment to
[retained records](PROPOSAL-TOKENS-RECORDS.md), the
[record format](PROPOSAL-TOKENS-FORMAT.md), and [nova-tokens](SPEC-TOKENS.md),
not a new ledger or a claim that all adapters are shipped.

An AI friend should be able to ask where the tokens went, whether the work ran in
a friend's session, a swarm, a one-shot, or a local model. All four belong in the
same reports. Friends can choose different models, harnesses and collection methods;
the shared requirement is truthful, verifiable accounting, not uniform tooling.

## One contract, four paths

| Execution path | Evidence producer | Required accounting |
|---|---|---|
| AI friend session | Reviewed adapter for the friend's actual harness/export | Retained native observations, actor and execution bench, model basis, repository attribution and source coverage |
| Swarm job | Harness/provider observations joined to protected swarm attempt metadata | The same observations plus task, attempt, profile and parent-work links; include failed and retried attempts |
| One-shot | Its runner's native response or usage export | The same contract even with no pool or worker slot; one task may contain several provider calls |
| Local model | Local engine response or reviewed harness usage source | Observed tokens retained, explicitly zero local inference API charge, model/engine and execution bench |

A worker is attributed as a task worker; its coordinator or parent friend is a
separate relationship, not a fabricated friend identity. A direct provider one-shot adapter
is an acceptance case for the generic one-shot path, not a provider-specific
exception. A local worker launched through a swarm follows both rows without creating
two spend events. `nova-local` may remain an engine/configuration tool: it must
emit the billing classification and usage-source configuration needed by the actual
runner, rather than inventing inference tokens for `status` or `worker` commands.

New integrations use the reviewed retained-observation schema and mapping API.
Do not invent another TSV, discard native event identity into a day total, or
silently extend v1's header. Existing aggregate records remain readable with their
original reduced granularity and coverage limitations.

## Tokens, cost and attribution

Retain the finest available observations and raw numeric presence. The mapping
states whether cache counts are included in input and reasoning in output. Reports
must not blindly add input, output, cache and reasoning columns. Unknown is distinct
from measured zero; unsupported counters and partial streams remain visible.

Record source time/interval, actor or task-worker identity, requested and observed
model/provider with their evidence basis, execution bench, repository or
`unattributed`, and task/attempt identifiers where available. Preserve parent and
retry relationships. Missing historical attribution stays unknown; a collector's
current machine is not the execution bench. Reports may group by day, month,
friend, model, bench, repository or work/task without losing the other dimensions.

Cost has an explicit basis, currency and rate/source revision where relevant:

- `local-free`: inference API charge is USD 0 by declared local execution policy.
  This holds even when token counters are missing, but does not turn those counters
  into zero. It does not assert electricity or hardware has no cost.
- Observed provider charge: retain the reported amount and its units. Missing or
  unverified units yield unknown cash cost, never zero.
- Rate-derived estimate: a recomputable view using a dated rate table and reviewed
  counter semantics. It never overwrites an observed charge or raw token record.
- Subscription allowance consumption: retain separately from cash charges and
  subscription fees. Included calls are not silently priced as pay-as-you-go cash;
  optional subscription allocation is a labelled report policy, counted once.

A route must be explicitly configured as local and verified by the local adapter;
missing credentials, a model name containing `local`, or a loopback proxy address
alone does not establish free inference. Go/Zen routing and reservations follow
[the swarm amendment](SPEC-SWARM-PROFILES.md).

All incurred work counts, including failures, timeouts, retries, discarded answers,
review and correction. Reservations and estimated liabilities are admission state,
not measured tokens or cash. An interrupted call with no usage has a coverage gap,
not a zero-token event. Report known cost subtotals alongside unknown counts;
never label a partial subtotal as the complete cost.

## Count each actual event once

Keep native observations and wrapper receipts as distinct evidence objects. Join
through proven native call/session identity and the declared adapter mapping. A
swarm total, one-shot total, provider export or daily bus summary may overlap those
calls; choose one counting source for that scope and keep the others as provenance.

Repeated ingestion or copying between benches is idempotent. Equal token values do
not prove two calls are the same. Different counters under the same stable event
identity are a revision only with evidenced ordering; otherwise they are a visible
conflict. No time-nearness, text similarity, largest-counter or last-file-wins
heuristic resolves overlap. Unknown overlap prevents a falsely complete total.

## Coverage makes the word "all" testable

Maintain configured source coverage for every participating friend and execution
path, including each bench's local and one-shot runners. Report source/build/mapping,
inspected interval, latest successful collection, and `complete-within-scope`,
`partial`, `unavailable` or `unsupported`, with reasons. Not reporting is not zero
spend. Offline friends preserve a visible gap until they return and backfill.

A source registration is not coverage proof. Before declaring adoption, each
configured runner supplies one retained contribution and a report that traces back
to its source. Use sanitized fixtures for correctness; reuse existing authorized
run receipts for adoption evidence rather than spending tokens just to measure them.
September backfill and live collection share identities, retain source evidence and
never count the same historical event twice.

The ledger stays private under the retained-record publication contract. Extract
allowlisted usage metadata, not prompts, responses, credentials or private paths.
Periodic collection should be mechanical, incremental where validated, and quiet
when unchanged; it needs no AI polling turn. Scheduling remains an explicit deployment
choice, not a hidden timer added by a source adapter.

## Acceptance gates

1. Fixtures for friend, swarm, one-shot and local paths yield the same retained
   schema; report by friend/model/bench/repository and reproduce by day/month.
2. Local measured tokens appear with zero inference API cost. Missing local tokens
   stay unknown; a paid provider with missing USD stays unknown. A loopback remote
   proxy is not automatically classified as free.
3. Inclusive input 1000 with cache-read 800 and output 100 with reasoning 40 yields
   1100 total, not 1940. An exclusive-input fixture is mapped independently. Missing
   subtype relationships do not produce a guessed total.
4. One native call appearing in harness, swarm and one-shot evidence counts once;
   two distinct equal-valued calls count twice. Ambiguous overlap and conflicting
   revisions remain visible, and re-import/backfill/cross-bench copies are idempotent.
5. A failed attempt followed by success retains both costs. A timed-out attempt
   without usage creates a gap. Go allowance, Zen cash and estimates stay separate.
6. Every configured friend/bench/runner has an explicit coverage row. An offline or
   unsupported source cannot disappear from the completeness denominator.
7. Secret/prompt/path sentinels never enter retained shared observations or logs.
   Existing v1 files remain byte-identical and are never counted with their source
   events in the same scope.

Implementation should extend the existing record/mapping machinery, then add bounded
runner adapters. The source mappings and exact revisions need independent review;
a green fixture for one provider does not establish coverage for another.
