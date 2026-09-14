# nova-swarm per-job profiles — proposal

This document is the normative owner for the per-job profile amendment to
[`SPEC-SWARM.md`](SPEC-SWARM.md). It generalizes the existing one-task worker
without adding a second dispatcher, provider ledger or identity layer. It is a
proposal only: it records the contract to review and the gates to implement;
it does not claim that the feature or a provider account exists, and it does
not claim friend consensus.

## Compatibility and vocabulary

An invocation with `--worker <file>` and no profile catalog keeps its current
meaning byte for byte. A run may additionally name `--profiles <file>`, a
trusted catalog. `add`, `batch` and `requeue` accept `--profiles <file>` with
`--profile <id>` and an optional `--model <id>`. A model override without a
profile is refused. A task with neither uses the legacy worker. Profiled task
admission validates the catalog, route and allow-listed model before adding the
task; invalid selection creates no queued job, reservation or provider call.
Admission records the resolved non-secret configuration and catalog hash in a
protected record. The catalog path is coordinator-owned and outside every
writable pool, job, slot, scratch, worker data home and configured `read_roots`;
if `--profiles` names a path in any of those locations, admission refuses exit 2
before a worker or provider is started. Run copies that admission into the
immutable attempt snapshot;
it does not silently re-resolve against a newer catalog. An explicit changed-profile
requeue is a new admission linked to the old task, not an automatic retry.

Workers are task workers. A profile never carries a friend, line, self,
memory, board or identity. Task prose and `RESULT.md` are data and cannot
select a profile, grant a tool, widen a path, add a network permission, expose
a key, extend a deadline or start another task.

The catalog is strict JSON, with `version: 1` and a `profiles` object. Each
entry has a complete worker description, exactly one `route`, an `env_var`
name for that route's secret, an `allowed_models` list, and a `prompt` object.
A route has a provider, endpoint or harness, and one selected credential
source; an empty or multi-route profile is an exit-2 refusal with the named
reason `profile <id> has no configured route` or `profile <id> has multiple
routes`. The selected child receives only the named route secret. The worker
description uses the fields already accepted by `--worker`; catalog entries do
not invent a second worker schema. `allowed_models`
is an allow-list, not a mutable model catalog or a promise of capacity. A
profile's model is the default requested model and an explicit override is
valid only when listed there. Unknown fields, duplicate ids, empty ids, empty
model lists, malformed entries and disallowed overrides are exit-2 refusals.

The prompt object has `mode: legacy|compact`, a literal `prefix`, and a
bounded tool allow-list. `legacy` preserves the current generated prompt.
`compact` retains the mandatory execution contract — isolation and refusal
behavior, deadline, file and token budgets, atomic result publication,
completion `## Head`, one process, no bus, notes and the result shape — while
allowing optional primers and long role prose to be omitted. The prefix is
trusted configuration, never task text, is UTF-8 and at most 4096 bytes. The
complete generated prompt is still measured against `max_input`. A harness that
cannot express the named tool profile is refused before launch.

A normative catalog example uses fake endpoints and names only; it contains no
secret value and is not a provider availability claim:

```json
{
  "version": 1,
  "profiles": {
    "opencode-go": {
      "route": {"provider": "opencode-go", "endpoint": "https://go.invalid"},
      "env_var": "OPENCODE_GO_KEY", "model": "example-go-model",
      "allowed_models": ["example-go-model"],
      "prompt": {"mode": "compact", "prefix": "Use the bounded task contract.", "tools": []}
    },
    "opencode": {
      "route": {"provider": "opencode", "endpoint": "https://zen.invalid"},
      "env_var": "OPENCODE_ZEN_KEY", "model": "example-zen-model",
      "allowed_models": ["example-zen-model"],
      "prompt": {"mode": "legacy", "prefix": "", "tools": []}
    }
  }
}
```

The coordinator constructs every child environment from an empty environment
set plus the selected profile's one secret variable. It does not inherit the
parent environment or copy variables for other routes. Tests use fake variable
names and values; real key material is never needed. The variable name may be
written to configuration and the sanitized projection, but the value may occur
only in the child environment while the harness runs. Secret values are
forbidden from task files, catalog and profile paths, snapshots, receipts,
argv, logs, worker scratch, `RESULT.md` and retained reports. A child may
overwrite its worker-writable projection, but that projection is never the
trust root.

## Frozen attempt and evidence

Before a worker starts, the coordinator writes the authoritative snapshot to
the coordinator-owned protected path
`<pool>/evidence/<job-id>/PROFILE.json`, where `<job-id>` is the concrete
swarm job/attempt id, outside every writable job directory and slot data home,
through the same temporary-and-rename rule as `RESULT.md`. Its hash is rooted
in that protected copy; a worker cannot replace the trust root. A pool-less
direct one-shot must supply a coordinator-owned protected `evidence_root` and
refuses before send when it is absent or writable by the worker. The worker may
read and overwrite a worker-writable sanitized projection at
`<job>/PROFILE.json`, but recovery verifies the protected snapshot and hash,
never the projection. The snapshot is a non-secret record of the resolved
profile and attempt: catalog/profile id and catalog hash,
requested and resolved provider/model, base URL, harness and arguments, worker
directory, key-file path and env-var name, usage source, deadline, prompt mode
and prefix hash, tool list, task id, attempt and retry origin. It never holds a
key, decrypted credential, token or response body.

The sidecar carries `profile`, `model_requested`, `model_observed`,
`snapshot_hash`, `prompt_hash`, `config_hash`, and `bench` when the caller
supplies it. The proposed `run --bench <name>` flag supplies bench metadata
for legacy sidecars. Existing sidecars with none of these fields remain valid. The
snapshot and hashes are immutable for the attempt. Recovery and retries use
the snapshot, never a changed catalog; a missing, malformed or mismatched
snapshot fails closed and is quarantined. Finalization copies the protected
snapshot and any non-secret raw provider evidence before moving or reclaiming
the job, and reclaim verifies their hashes alongside the existing usage and
report evidence. Raw evidence is retained only under the protected pool path;
receipts and worker-visible projections are sanitized, and secret values are
removed before any persistence.

Each attempt also has a receipt, retained with the finalized report and
usage. It records `task`, `bench`, `repo`, `actor`, `attribution`, `profile`, `attempt`, `retry_from`,
`provider_requested`, `provider_observed`, `model_requested`,
`model_observed`, native `receipt_id` when supplied, `prompt_hash`,
`snapshot_hash`, `coverage`, `token_basis`, `token_total`, and the five usage
kinds plus `usd`. New receipts always carry `bench`, `repo` and `actor`; when
a caller cannot supply one, the field is `-` and
`attribution=unattributed`. Requested identity comes from the snapshot;
observed identity comes from the provider's response or usage record.
Unavailable identity, allowance, balance or usage is `-`, never zero. The
existing sixteen-column usage file remains a legacy projection consumed by
v1 readers. New retained observations use the reviewed record API and mapping;
receipts add provenance without rewriting historical usage. A report selects
native records or their legacy aggregate for an overlapping scope, never both.

For a profiled job, `RUN START` and `RUN RECLAIM` add bounded fields
`profile=<id> model_requested=<id> model_observed=<id>` beside the existing
fields. An unavailable observed value is `-`. The legacy `RUN POOL` line keeps
its existing default-worker provider/model fields, so old readers continue to
parse it; the per-job fields and receipt are authoritative for a mixed run.

## Selection and provider capacity

The simple path is explicit: the coordinator names a profile and, optionally,
an allow-listed model. An optional policy selector may run inside `nova-swarm`
against the same catalog, but it is a bounded profile-selection operation,
not a second dispatcher. It may choose only an allowed profile/model and must
write the same frozen snapshot before launch. No automatic cost scheduler is
part of this amendment.

Profiles may carry generic provider-capacity metadata: model availability,
context tiers and an estimate function, retention, training, rate/cap windows,
off-peak or reserved capacity, price observations, billing source and an
explicit concurrency cap. These are dated, configurable observations; mutable
prices and model catalogs are never constants in this spec. A context estimate
is a preflight bound, not a tokenizer or a guarantee. A listed model does not
imply unlimited parallel requests; the existing `--workers` cap and any
explicit profile concurrency cap remain in force. The catalog may refresh and
diff each metadata family by name, source and observation time, but a refresh
never mutates the catalog or silently changes a queued job. A `usage: none`
refusal is evaluated per pending task after its profile is resolved; one task's
legacy worker setting cannot reject unrelated profiled tasks.

Retention and training policy are explicit provider metadata with safe defaults:
the default job data class is `private`, requiring configured, current evidence
of zero retention and no training. Unknown or expired metadata refuses before
send. Public data class is an explicit admission option; training additionally
requires a separate trusted opt-in. Neither task prose nor the selector changes
that policy or invents a provider attestation. The
same policy and secrets gate apply to explicit profile selection and to the
optional selector.

Reservations are made before sending a request and are counted in every
applicable window. A shared budget-domain lock is a kernel advisory lock at one
launcher-named path outside every pool. Every profile and pool that draws from
that provider allocation uses the same path and holds the lock from reading
available allowance through durable reservation commit; a goroutine-only mutex
is not sufficient. A two-process fork/exec fixture runs two `nova-swarm`
processes against the same fake domain and proves that only one reservation can
consume the available allowance. Per-profile concurrency does not bypass a
provider quota. A deadline, timeout, lost connection, missing usage or otherwise
ambiguous call leaves an unknown liability reserved until a person or a verified
record resolves it; expiry does not release it. Reservation is durably committed
under the shared lock before any send.

A provider send cannot be atomic with a local file transaction: a crash after
reservation but before confirmed completion is conservatively unknown. Settlement
atomically links the retained observation and reservation disposition; recovery
cannot release liability before its evidence is durable or count it twice. A
multi-call harness must expose per-call admission or reserve an enforced upper
bound for the whole attempt. If neither is supported, refuse quota-guaranteed
execution; retrospective polling alone is not a hard spend limit. Exact-once
recording uses the concrete swarm `job_id` (one job id per attempt) plus the
provider session and native receipt/call identity when supplied; `retry_from`
links lineage but never aliases two attempts. It never keys on observed counters,
a profile hash or a mutable catalog value. A source without native identity
requires a separately reviewed deterministic mapping with replay fixtures; an
arbitrary parser ordinal is not identity. A replay with the same stable key and identical counter payload is already
recorded; a different payload is `CONFLICT` and is refused. No spend is estimated into a final usage row.

Provider quota and account balance are separate domains. `opencode-go` and
`opencode` are distinct routes: Go's subscription allowance and Zen's metered
credits are not interchangeable. Go's model-specific windows, bulk or
reserved capacity, and any optional balance overflow remain provider/account
facts. Zen's pay-as-you-go credits remain separate. There is no implicit
Go-to-Zen fallback, paid overflow, top-up or account-setting change. A console
setting such as overflow cannot be proved disabled by this CLI; if the
provider does not report the route, the receipt says unknown. The tool makes no
claim of a no-charge guarantee when account state is unknown.

Provider-native ids and protocols are delegated to the selected harness. The
swarm assumes neither that every model uses chat-completions nor that a
provider's model list is current. A catalog refresh operation may fetch and
diff a provider's declared model list, bounded by bytes and time; it never
silently edits the catalog or launches a job.

## One-shot operations and Go/Zen examples

The superseded `nova-go` draft's one-shot `ask` maps to one `nova-swarm` task: task
file in, one bounded attempt through the selected profile, `RESULT.md` and a
receipt out. One task is not a promise of one HTTP call: a bounded multi-turn
harness may make several provider calls, and captures each call's stable id,
usage and evidence under the same attempt. Its chooser maps to the optional
profile selector above; its ledger is the swarm's existing usage/finalization
record, not a second ledger. Its `record` maps to finalization of the same
attempt. Its bake-off maps to a bounded batch of one task per explicitly named
profile/model, with the normal report and receipt contract; it is never an
unbounded fan-out.

An OpenCode Go profile may name `provider: opencode-go`, a native
`opencode-go/<model>` id and its native endpoint/protocol. A Zen profile may
name `provider: opencode`, a native `opencode/<model>` id and its native
endpoint/protocol. Neither
example hardcodes a price, model list, allowance, retention claim or protocol
shape. Existing key-file handling remains the swarm's responsibility; this
spec does not define a second credential store. A live-route profile launches
only under the `nova-secrets exec` gate.
`nova-swarm run` refuses exit 2 before the first worker when the selected
profile environment was not produced by that gate. The gate refuses exit 125
for a missing or malformed store shape, including anything other than one seat
key and exactly the declared route key. A fixture with three recipients must
exit 125 and start zero workers. The gate keeps key values out of profiles,
snapshots, receipts, task argv and logs, and passes recovery and scope checks
before real traffic is enabled.

## Shared accounting for every worker route

The receipt and usage pipeline is shared by profiled swarm jobs, legacy swarm
jobs, every local-model route including `nova-local`, and every direct one-shot
launcher attempt, including local and API routes. It consumes the existing
[retained-record contract](PROPOSAL-TOKENS-RECORDS.md) and preserves
[SPEC-TOKENS](SPEC-TOKENS.md)'s five token types and `-` unknown semantics;
these profile receipts are an attribution and evidence envelope, not a second
incompatible usage schema or ledger. A one-shot is one attempt in the same
accounting stream; it does not create a second ledger or a special cost path.
Every automatic retry, requeue, manual rework and bake-off attempt gets its own
attempt identity and usage row, linked by `retry_from` or the rework origin. A
retained usage row is written once and is never overwritten; exactly-once
recording uses the concrete `job_id` for each attempt plus the provider session
and native receipt/call identity described above. `retry_from` links attempts
without causing aliasing or double counting.

The five token kinds remain separately attributable (`tokens_in`,
`tokens_out`, `cache_write`, `cache_read`, `reasoning`). A source's aggregate
token total or budget spend must declare a **disjoint basis**. It never adds a
cache or reasoning subtype to a parent input/output count when that subtype is
already included by the source, and it never silently drops a subtype that the
source reports as separate. If the source cannot establish a disjoint total,
the total is `-` while the individual reported fields remain available. This
prevents cache and reasoning counters from being double-counted while
preserving the raw attribution needed by `nova-tokens`.

For `nova-local`, local input/output and any reported cache/reasoning counters
are counted through this same schema, and inference cost is explicitly
`usd=0`. That declared local API-cost zero is distinct from an absent or unreadable usage
field, which is `-`. For API providers, observed `usd` remains provider-reported; a rate-derived
estimate is a separate labelled value with a dated rate revision, never a
replacement for missing observed cost. Unknown cost remains `-`. Subscription
allowance and cash are separate, as specified in
[unified execution coverage](PROPOSAL-USAGE-COVERAGE.md). Direct one-shot usage
uses the same receipt fields, usage columns, attempt linkage and disjoint
token basis, including partial or missing observations.

Every source adapter reports coverage per field as one of `observed`, `missing`
or `unsupported`: `observed` retains the value and protected evidence,
`missing` records `-` because the source was expected but omitted it, and
`unsupported` records `-` because the adapter cannot provide that field.
Neither status becomes zero or a success claim. New receipts expose required
`bench`, `repo`, `model` and `actor` dimensions (or `-` plus
`attribution=unattributed`); legacy usage rows remain readable with their
existing columns. Daily reports may group the same rows by bench, repository,
model and actor, with coverage status visible, without introducing another
ledger or silently filling gaps.

| adapter coverage | retained value | evidence and accounting meaning |
|---|---|---|
| `observed` | provider-reported value | protected source evidence and the sanitized value are available |
| `missing` | `-` | the source was expected to report it but omitted it; liability stays unknown |
| `unsupported` | `-` | this adapter cannot provide it; no budget or cost claim is made |

## Security and lifecycle invariants

The following remain machinery invariants and cannot be weakened by a profile
or task wording: per-job worker/data homes and durable slot ownership;
read/write sandbox boundaries; an empty-built child environment containing only
the selected route secret;
written config containing a variable name rather than its value; written
deadlines and file/token budgets; bounded prompt and provider calls; one
process group and cancellation; whole `RESULT.md` publication; completion
`## Head`; note accounting; usage written before movement; retained report,
usage and snapshot evidence; and fail-closed handling of unknown, malformed,
unreadable or conflicting records.

The requested model, observed model, profile, prompt hash, retry origin and
usage must remain distinguishable in every attempt record. A provider response
that omits an id or usage field is recorded as `-`; the machinery does not
turn absence into zero or infer identity from a command line. A profile may
name paths needed by its worker, but the snapshot records paths without making
them writable or granting them to task prose.

## Migration and coverage

Every useful requirement from PR128's former `nova-go` rules has one owner in
this swarm proposal. No separate `nova-go` binary, dispatcher or usage ledger
is planned.

| PR128 rule | swarm disposition |
|---:|---|
| 1 | trusted profile catalog; strict header/version and field validation |
| 2 | bounded catalog display and capacity observations; no unbounded listing |
| 3 | bounded metadata refresh, diff only, never a silent catalog edit |
| 4 | profile/model allow-lists and task shape/prompt profiles |
| 5 | explicit selection or bounded internal policy selector; no implicit choice |
| 6 | generic dated capacity windows, provider allowance/balance source, and reservations |
| 7 | a closed window is a named refusal; no unrequested dearer fallback |
| 8 | the former local/paid/harness-name route ban is superseded by explicit trusted provider profiles; task sandbox and no-implicit-selection invariants remain |
| 9 | configurable retention/zero-day policy defaults to private and unknown safety is a pre-send refusal |
| 10 | configurable training policy defaults to private and unknown safety is a pre-send refusal |
| 11 | overflow is provider/account state; no auto-overflow, top-up or fallback |
| 12 | one task is one bounded attempt; a multi-turn harness may make several captured calls with stable session metadata |
| 13 | existing key-file contract plus the protected secrets integration gate; no secret values enter task or evidence records |
| 14 | existing `max_input` plus bounded context estimate before launch |
| 15 | dated configurable price/tier observations; no mutable price constants |
| 16 | receipt distinguishes requested/observed model/provider, task, bench, prompt and usage |
| 17 | existing finalization plus stable native event identity and counter-payload conflict detection |
| 18 | bounded explicit bake-off batch of one task per selected profile/model |
| 19 | existing `--max`, deadlines, worker cap and bounded calls |
| 20 | one-line refusal grammar and the profile catalog's strict validation |
| 21 | the former blanket local/harness-name ban is superseded; task workers still cannot load session, self, friend identity or memory, and generic sandbox boundaries remain |

The old `ask` command therefore maps to one `nova-swarm` task, its chooser to
the optional bounded selector, its `record` to finalization, and its bake-off
to an explicit mixed-profile batch. The old service/account assumptions are
observations a profile may declare, never hidden defaults.

Acceptance requires the existing SPEC-SWARM demanded tests 1–19 to remain
green, plus these named profile gates. Each gate is a deterministic fixture
with fake routes, fake stores and no provider calls; each is red before its
implementation. Quality is checked before token count, and token count before
wall clock.

1. **LEGACY-MIXED-IDENTITY.** Legacy worker behavior is unchanged; a mixed
   mocked Go/Zen pool receives only its resolved profile and records requested
   versus observed identity. A profile concurrency cap is enforced in addition
   to `--workers`.
2. **CATALOG-REFUSAL.** Unknown profile/model, model override without a profile,
   malformed catalog, no-route or multi-route profile, changed/missing snapshot,
   unsupported tool profile, a catalog under a writable pool/job/slot/scratch/
   `read_roots` path, and an untrusted worker-writable catalog all fail exit 2
   before provider launch, with no fallback.
3. **FROZEN-RECOVERY.** Dispatcher recovery and automatic retry use the frozen
   non-secret snapshot after catalog mutation; each new concrete job id has its
   own protected `<pool>/evidence/<job-id>/PROFILE.json`, while `retry_from`
   preserves lineage, and the protected receipt follows the same concrete job id.
   `run --bench` records its bench dimension without bypassing profile resolution.
   Paths and resolved model survive, key values do not.
4. **COMPACT-KNOWN-ANSWER.** Every compact profile runs a bounded fake task with
   a profile-declared known answer and mandatory invariant checks before any
   token-saving claim. It proves measured size reduction against legacy for a
   tiny task, then checks token count and finally wall clock.
5. **CHILD-ENV-SANDBOX.** Profile tools cannot grant task paths, network, key
   access or background work; mixed slots never share a data home or live
   writer. Each child starts from an empty environment plus exactly its selected
   route variable; parent extras and the other route variable are absent.
6. **CAPACITY-RESERVATION.** Quota, allowance, balance, overflow and usage
   remain separate; unknowns are `-`; catalog and metadata refreshes are bounded
   to 256 KiB and 5 seconds by default, stale/oversized results are unknown;
   reservation, bake-off and exact-once recording are bounded. A bake-off uses
   the caller's explicit task/worker cap and never unbounded fan-out.
7. **PROTECTED-EVIDENCE-CONFLICT.** The coordinator's protected snapshot survives
   a worker write attempt and a crash; recovery trusts only its hash, retains
   non-secret raw evidence, and exposes only a sanitized worker-writable
   projection. A changed counter payload for one stable attempt identity prints
   `CONFLICT` and is not accepted as a second usage row.
8. **SHARED-KERNEL-LOCK.** Two profiles and two pools sharing one provider
   allocation contend on one launcher-named kernel lock outside the pools. A
   fork/exec fixture with two `nova-swarm` processes proves one reservation at a
   time; a deadline expiry or crash leaves unknown liability reserved until
   explicit settlement, and settlement is atomic.
9. **SECRETS-EXEC-SHAPE.** Retention/training defaults to private, unknown
   provider attestations refuse before send, and an explicit profile or selector
   cannot weaken policy. A live-route profile must run under `nova-secrets exec`;
   an environment without that provenance causes `run` exit 2 before the first
   worker, while a fake three-recipient store causes `nova-secrets exec` exit 125
   and starts zero workers. No key value appears in argv, snapshots, receipts,
   raw evidence, `RESULT.md` or logs.
10. **COMMON-ACCOUNTING.** Legacy, profiled, `nova-local` and direct one-shot
    attempts use one accounting pipeline. Retries, rework and bounded multi-turn
    one-shots each retain their own attempt evidence; local inference reports
    `usd=0`, absent API cost/usage is `-`, source coverage is
    observed/missing/unsupported, and a cache or reasoning subtype is never
    added twice to a parent total. A source-declared inclusive-basis fixture (input=1000/output=800) retains
    aggregate input=1000; a source-declared exclusive-basis fixture
    (input=200/cache_read=800) retains aggregate input=1000. New receipts
    support daily bench/repo/model/actor views while legacy rows stay readable.

The exact ten gates above are the compact profile acceptance set; the existing
SPEC-SWARM tests remain separate and are not silently replaced. A review gate
for implementation is described below and records individual friend dispositions;
this document makes no claim that the gate has passed.

## Implementation decisions and review gates

This is a consolidation draft, not an approval to skip unresolved interfaces.
A child implementation may start only after every awake friend has independently
reviewed this exact revision and recorded `APPROVE`; silence or `HOLD` is not
approval. Before the runtime PR, pin the catalog JSON schema and limits, exact
tool-profile adapter contract, credential-provider interface, protected snapshot encoding and
hash canonicalization, budget-domain storage/locking protocol and stable usage
mapping. Each lands with deterministic fixtures; no mutable pricing table becomes
a program constant. The optional selector/refresh/bake-off operations may be
separate reviewed increments in nova-swarm. Their current proposed bounds are
256 KiB and 5 seconds for catalog/metadata input, explicit caller task and
worker caps for bake-off, and ten fake-harness gates with no network completing
inside two minutes; until a runtime increment pins flags, these are acceptance
bounds rather than an implementation claim. The corresponding PR128 requirements
remain tracked until their explicit acceptance gates pass. Removing the old tool
name does not mark those behaviors implemented.

PR128's old known-answer bake-off scoring remains an explicit bounded evaluation
fixture with expected answer and evidence checks; a batch that merely produces
reports does not satisfy it. Its centralized remedy-bearing refusal contract and
provider-required session/user-agent headers must be verified through the harness,
or the route must report that capability unsupported before sending. Refresh
failures/oversized responses are unknown, not up-to-date; stale metadata is visible.

References: [Go](https://opencode.ai/docs/go/),
[Zen](https://opencode.ai/docs/zen/),
[OpenCode agents](https://opencode.ai/docs/agents/), and
[the superseded nova-go draft](https://github.com/mas-bandwidth/nova-tools/pull/128).
Provider names, native ids, protocols, allowance windows and credits above are
examples and assumptions to verify with the mocked gate and at most one real
probe per route; this proposal makes no current availability or account claim.
Provider details were checked on 2026-09-13; availability, pricing and account
policies remain observations that require refresh.
