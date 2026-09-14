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
it does not silently re-resolve against a newer catalog. If `run --profiles` is
supplied, its catalog hash must match the queued admission for the selected
profiled task. Select the task before checking its admission hash; a mismatch
retains that task as pending and refuses its launch before reservation or provider
activity.
The flag checks the admission, never replaces it. An explicit changed-profile
requeue is a new admission linked to the old task, not an automatic retry.

Workers are task workers. A profile never carries a friend, line, self,
memory, board or identity. Task prose and `RESULT.md` are data and cannot
select a profile, grant a tool, widen a path, add a network permission, expose
a key, extend a deadline or start another task.

The catalog is strict JSON, with `version: 1` and a `profiles` object. Each
entry has the worker execution description below, exactly one `route`, an `env_var`
name for that route's secret, an `allowed_models` list, and a `prompt` object.
A route has a provider, an optional endpoint, and one selected credential
source; an empty or multi-route profile is an exit-2 refusal with the named
reason `profile <id> has no configured route` or `profile <id> has multiple
routes`. The selected child receives only the named route secret. The worker
description shares execution-field types and validation with `--worker`; the
profile field ownership below prevents duplicate route and credential sources.
`allowed_models`
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

### Live-route credential binding and field ownership

This amendment separates two different keys. Legacy `worker.key_file` is a
plaintext provider-key file. Profile `route.credentials.age_key` is the private
age identity passed to `nova-secrets --key`; it is never a provider-key file.
Legacy invocations keep their existing parser and key-file behavior. A profiled
launch must not call the legacy provider-key reader, materialize a decrypted
key file, or use a dummy `key_file` to get past legacy validation.

These are the required live-route fields. JSON objects reject duplicate and
unknown members at every depth. Optional capacity/policy metadata and the
remaining interfaces below still need their own pinned schema before the whole
catalog is implementation-ready.

| Object | Fields and types | Single source of truth |
|---|---|---|
| profile | `worker` object; `route` object; `env_var` string; `model` string; `allowed_models` nonempty string array; `prompt` object | `model` is the default; an admitted override must appear in `allowed_models` |
| worker | Required strings `name`, `usage`, `harness`, `worker_dir`, `deadline`; required string array `harness_args`; optional string arrays `read_roots`, `input_limit_phrases` | Same execution meanings and validators as the legacy worker; profile harness paths must be absolute |
| route | Required `provider` string and `credentials` object; optional `endpoint` string | Provider and base URL come only from here; an omitted endpoint uses only the named adapter's declared default |
| credentials | Required strings `kind`, `store`, `seat`, `age_key`, `sops`, `gate`, `launcher` | `kind` is exactly `nova-secrets`; no alternate plaintext or inherited-environment credential source |
| prompt | Required `mode` string, `prefix` string, `tools` string array | Existing `legacy`/`compact`, byte bound and adapter allow-list rules above |

`worker.provider`, `worker.model`, `worker.base_url`, `worker.env_var`,
`worker.key_file` and `worker.board` are forbidden in a profile. In particular,
even a duplicate that agrees with the canonical field is refused. The common
execution validator is reused; resolution supplies provider/model/base URL and
environment-variable name from their single owners. This produces a resolved
execution description plus a typed credential binding, not a legacy Worker
with a pretend plaintext key path. Profile workers still carry no friend/board
identity.

`store`, `age_key`, `sops`, `gate`, `launcher`, `worker.harness` and
`worker.worker_dir` are explicit absolute paths; `read_roots` keeps its existing
absolute-path rules. `seat` uses nova-secrets' existing seat-name validator.
`env_var` names one variable, never `all`, a comma-separated list, or a runtime
variable such as PATH/HOME. Its syntax and reserved-name rules must agree with
the selected adapter and nova-secrets. Paths undergo the resolved placement and
worker-write-authority checks below at admission and again before launch.

The coordinator constructs this argv as separate literal arguments, without a
shell and without catalog-provided gate flags:

```text
<credentials.gate> exec --store <credentials.store> --as <credentials.seat>
  --key <credentials.age_key> --sops <credentials.sops>
  --only <profile.env_var> --require <profile.env_var> --
  <credentials.launcher> profile-supervise --launch <absolute-launch-record>
  --launch-hash <sha256:canonical-launch-record>
```

The generated launcher arguments identify only the protected launch record below.
They cannot carry secrets or choose a different route. The launcher revalidates the protected
attempt and constructs the empty-based child environment specified below. It
never inherits a plaintext-provider-key fallback. Credential binding freezes
paths, seat and variable selection; it does not cache decrypted values or claim
that a provider credential can never be rotated in the committed store.

This complete *required-field projection* uses fake names and paths. It is a
schema example, not live configuration: provider policy/capacity attestations
and a supported protected launcher are still required before launch.

```json
{
  "version": 1,
  "profiles": {
    "go-small": {
      "worker": {
        "name": "hosted-small", "usage": "opencode",
        "harness": "/opt/example/bin/opencode",
        "harness_args": ["run", "--model", "{model}", "--", "{prompt}"],
        "worker_dir": "/opt/example/worker", "deadline": "5m"
      },
      "route": {
        "provider": "opencode-go", "endpoint": "https://go.invalid",
        "credentials": {
          "kind": "nova-secrets", "store": "/secure/example/store",
          "seat": "worker", "age_key": "/secure/example/worker.agekey",
          "sops": "/opt/example/bin/sops",
          "gate": "/opt/example/bin/nova-secrets",
          "launcher": "/opt/example/bin/isolated-worker-launcher"
        }
      },
      "env_var": "OPENCODE_GO_KEY", "model": "example-go-model",
      "allowed_models": ["example-go-model"],
      "prompt": {"mode": "compact", "prefix": "Use the bounded task contract.", "tools": []}
    }
  }
}
```

A Zen route uses its own `provider: opencode`, endpoint, variable and selected
credential binding. Reusing a seat does not authorize passing all of its keys;
`--only` and `--require` still name that route's one variable. For a local route
that needs no credential, do not invent a fake API key or bypass this live-route
contract: its adapter and explicit no-secret variant must be pinned separately
under the existing shared-accounting scope.

Add these cases to CATALOG-REFUSAL and SECRETS-EXEC-SHAPE: plaintext `key_file`
in a profile, duplicate field owners, `kind` other than `nova-secrets`, missing
binding fields, relative paths and `env_var=all` all refuse before decrypt or
launch. A mixed legacy/profile fixture proves the legacy reader sees only the
legacy task's file; a profiled task invokes the pinned gate with exactly the
mapped argv and never opens that file. A fake gate injects only the named route
value; tests inspect it inside the fake child without retaining it in production
artifacts. The age-key path must never reach provider configuration as an API
key or be opened by the sandboxed harness.

The isolated-worker launcher constructs every harness environment from an empty
environment set, adds only adapter-declared non-secret runtime variables (such
as absolute PATH entries and isolated HOME/XDG homes), and takes exactly the
selected profile's one secret variable from the gated environment supplied by
`nova-secrets exec`. The coordinator pins the launch configuration and does not
read the secret value into its own process. Runtime variables cannot alias a secret name or
import arbitrary parent values. It never copies the parent environment wholesale
or variables for other routes. Tests use fake variable
names and values; real key material is never needed. The variable name may be
written to configuration and the sanitized projection, but the value may occur
transiently in gate and launcher process memory during preparation and in the
child environment for harness execution. This does not promise guaranteed memory
zeroization. Secret values are
forbidden from task files, catalog and profile paths, snapshots, receipts,
argv, logs, worker scratch, `RESULT.md` and retained reports. A child may
overwrite its worker-writable projection, but that projection is never the
trust root.

## Frozen attempt and evidence

### Encoding and admission bounds — proposed, awaiting friend review

Pin the following shared encoding before implementing profile admission. This
proposal does not complete the remaining policy, launcher or quota interfaces.

Read a catalog from one validated regular-file handle, at most 262144 bytes
plus one overflow-detection byte, within five seconds. Reject trailing JSON,
duplicate members (including equivalent escaped names), unknown members,
invalid UTF-8 or lone surrogates before resolution. Limit object/array nesting
to 32, counting the root object as one. Do not read a FIFO while waiting to
discover that it is not regular. These bounds cover parsing, not a promise of
provider response time. Existing path-placement checks still apply.

The admitted catalog has exactly `version` and `profiles` at its root.
`version` is the integer token `1`; `1.0`, `1e0`, `"1"` and other versions
refuse. Hash the validated catalog's [RFC8785 canonical JSON](https://www.rfc-editor.org/rfc/rfc8785)
bytes, with no trailing newline, as `sha256:<64 lowercase hex>`. The current
schema has no other JSON numbers. Do not pass the whole catalog blindly to
`internal/records.Canonicalize`: that helper deliberately rejects numbers.
Reuse its validated number-free string/array/object encoding for `profiles`,
then frame the root exactly as `{"profiles":<canonical profiles>,"version":1}`.
Future numeric metadata needs an explicit schema/encoding amendment; no generic
float conversion or silent string conversion is permitted.

Object member order, whitespace and equivalent JSON escapes do not change this
digest. Array order and explicit optional-member presence do: preserve both;
do not sort arrays, inject defaults, normalize Unicode or canonicalize paths
as part of hashing. Resolution and path validation happen separately. A model
override belongs to the admission snapshot, not an edit of the catalog hash.
The catalog digest identifies the complete admitted catalog, including unused
profiles; the existing `run --profiles` match therefore checks that same whole
catalog, not an undocumented selected-profile hash.

An attempt's proposed protected snapshot encoding uses
`schema: "nova.swarm.attempt/1"`, with identities, counts and duration values
represented as strings, not floating-point numbers. Its digest uses the same
number-free canonical JSON helper, over the body only; `snapshot_hash` belongs
in its containing receipt, never inside its own hashed body. Bound serialized
snapshot input/output to 1048576 bytes and nesting to 32; overflow refuses
before launch. These are distinct catalog/snapshot caps, not permission to
truncate a selected profile or lose an argument. A digest establishes content
identity, not origin, authority or filesystem protection.
The proposed exact live-profile snapshot shape below supplies member names,
types and presence rules. Its review, launcher binding, and runtime round-trip
fixtures remain required; an encoding fixture alone does not clear those gates.

Required deterministic fixtures: reordered members/whitespace/escaped spellings
hash identically; reordered arrays, changed optional presence and a changed
model hash differently; astral member names sort by UTF-16 code units; duplicate
escaped members, lone surrogates, unsupported numbers, depth 33 and each byte
limit plus one refuse. Exactly-at-limit inputs pass if otherwise valid. Tests
exercise the actual read boundary and require zero queued tasks, reservations,
gate invocations or worker starts on every refusal. Snapshot round trips retain
every accepted field; a hash mismatch or changed protected body quarantines the
attempt. Tiny encoding vectors may use an empty `profiles` object, but do not
establish that an empty catalog can admit a job.
The [encoding-only vectors](fixtures/swarm-profile-catalog-encoding.json)
pin canonical bytes and SHA-256 values, including UTF-16 member ordering.
They are proposed acceptance inputs, not evidence that profile admission exists.

### Protected attempt body — proposed, awaiting friend review

This shape applies to a new live-profile attempt. It does not retrofit snapshots
onto legacy jobs or invent a no-secret local-route variant. Every member in the
table is required. Every object rejects unknown/duplicate members and `null`;
there are no runtime-injected JSON defaults. Arrays retain order. Strings must
be valid Unicode. Empty arrays mean no entries, not inherited configuration.

| Member | Exact type and meaning |
|---|---|
| `schema` | String, exactly `nova.swarm.attempt/1` |
| `job_id` | String, the concrete `Sidecar.ID` for this attempt, validated by the existing job-ID rules; also the evidence-directory ID |
| `lineage` | Object with exactly string `kind` and `previous`; rules below |
| `profile_id` | String, the admitted catalog member name |
| `catalog_hash` | String, the full catalog digest defined above |
| `requested` | Object with exactly nonempty strings `provider` and `model`, using the existing provider/model validators and admitted allow-list, before adapter resolution; explicit override or the admitted default, never observed identity |
| `resolved` | Object with exactly strings `provider`, `model`, `endpoint`, after the selected adapter resolves the route; provider/model must pass its nonempty native-ID validators; empty endpoint allowed only if that adapter explicitly declares no configurable endpoint |
| `worker` | Object with exactly strings `name`, `usage`, `harness`, `worker_dir`, and string arrays `harness_args`, `read_roots`, `input_limit_phrases`; the execution-field validation above applies |
| `credentials` | Exactly the live-route credential object defined above: `kind`, `store`, `seat`, `age_key`, `sops`, `gate`, `launcher`; paths and names only |
| `env_var` | String, the single admitted secret-variable name |
| `limits` | Object with exactly strings `files`, `tokens`, `deadline_ns`, `max_input`; rules below |
| `input_bytes` | Object with exactly strings `task` and `prompt`, the measured original payload lengths as nonnegative base-10 integers fitting signed 64 bits, with no leading zero except `0` |
| `prompt` | Object with exactly strings `mode`, `prefix`, `template`, and string array `tools`; catalog mode/prefix/tools rules apply; template is the selected existing template name, or `-` for none |
| `hashes` | Object with exactly strings `task`, `prompt`, `config`, `prefix`; each is `sha256:<64 lowercase hex>` with preimages below |
| `attribution` | Object with exactly strings `bench`, `repo`, `actor`, `basis`; rules below |

`lineage.kind` is `none`, `retry`, `rework`, or `unknown`. `none` requires
`previous: "-"`; every other kind requires a distinct validated predecessor
job ID. `retry` means the same admitted task text and execution choices are
being retried; `rework` means an explicit changed task, profile, model or budget.
The originating operation records the kind. Legacy `Sidecar.From` alone cannot
prove it: that field covers both automatic retries and changed manual requeues.
If migrating such lineage without sufficient evidence, preserve the predecessor
as `unknown`, not a guessed retry. Refuse a self-reference or an attempt to
change an already retained job ID's immutable predecessor binding. Rework may
change task/profile/model/budgets; it preserves the predecessor's record, not
equality with its execution choices. A new attempt always gets a new `job_id`.
The legacy usage column `attempt` currently records only 1 or 2 according to
`Requeued`; it is neither an ordinal nor identity and is not copied into this
body. Existing usage files remain unchanged.

`files` and `deadline_ns` are positive base-10 integers without a sign or leading
zero, fitting signed 64 bits and the receiving platform's applicable budget
types. `tokens` is such a positive integer or exactly `unmetered`; the existing
metering rules still apply. `max_input` is such a positive integer or `-` when
the caller set no input limit. The deadline is the resolved effective duration,
in nanoseconds; do not recover it from a mutable worker default. The snapshot
worker therefore has no second `deadline` member. `harness_args` freezes the
admitted argument templates in order; expansion remains the named adapter's
contract, not a shell evaluation or a fresh catalog read.

Each attribution value is an explicit coordinator binding or `-`. `basis` is
`caller` only if all three are supplied, otherwise `unattributed`. These are
accounting labels, not a worker identity or a claim that the provider measured
them. Raw source attribution stays separate in retained observations.

Hash `task` over the exact admitted task-file bytes and `prompt` over the exact
prepared prompt bytes, both without newline normalization. Hash `prefix` over
the UTF-8 bytes of `prompt.prefix`, including an empty prefix. Hash `config`
over the canonical JSON object containing exactly `requested`, `resolved`,
`worker`, `credentials`, `env_var`, `limits`, and `prompt`, copied from this
body. This is a resolved configuration digest, not a hash of arbitrary harness
files. The launcher must separately validate its generated configuration under
its pinned adapter contract. None of these hashes contains itself.

Generate and retain the protected task/prompt evidence, then publish this body
and its snapshot hash after slot preparation but before credential-gate launch.
Under the protected `<pool>/evidence/<job_id>/` (or the explicitly supplied
one-shot evidence root), retain exactly named `TASK.txt` and `PROMPT.md` with
their original bytes, `PROFILE.json` containing the canonical body bytes with
no trailing newline, and `MANIFEST.json` containing the canonical object
`{"schema":"nova.swarm.prelaunch/1","job_id":"<id>","snapshot_hash":"sha256:<digest>"}`.
The displayed object is a member inventory; canonical serialization determines
its actual member order. These three manifest strings are required, and no
other members are accepted. This manifest is the prelaunch expected-hash
reference outside the body; a final receipt is not required to recover it.

Create files only within the validated coordinator-owned directory, with
same-directory temporary writes and atomic no-replace publication. Never
follow an existing symlink or overwrite an existing evidence file. Write,
flush and verify TASK, PROMPT and PROFILE, then flush their directory before
publishing MANIFEST last and flushing the directory again. An existing complete
set with identical verified bytes is an idempotent replay; differing content
under the same job ID refuses. An incomplete set is not a committed snapshot
and must go through existing preparation/launch reconciliation before any retry;
presence of a partial directory is not proof of either launch or non-launch.
Unsupported atomic/durable publication refuses before gate invocation.

Record `input_bytes` from the same bytes used for task/prompt hashing; compare
the prepared prompt length against `limits.max_input` when it is set. Recovery
streams each payload up to its recorded length and probes at most one extra
byte, with overflow-safe arithmetic and bounded memory; shorter or longer files
refuse even if some unrelated hash is well formed. The recorded length is not
an allocation size or permission to expand the worker's input allowance.

Recovery opens bounded regular files at these constant names, verifies manifest
job ID against its directory and selected sidecar, verifies PROFILE against
the manifest digest, and recomputes task/prompt/config/prefix hashes before
using the snapshot. Manifest body and record input share the snapshot size and
depth bounds; task/prompt payload readers use the recorded lengths above.
The worker-writable
copies are projections, never recovery preimages. Missing evidence, mismatched
body/config/prefix hashes, or a
job-ID/path/lineage conflict quarantines before launch/reclaim. Retrying retains
the old body and admission; it creates a new body and prepared prompt for the
new job ID, never edits an old snapshot or blindly copies its prompt hash.
Template/generator and adapter compatibility must be verified by the launcher;
if it cannot reproduce the admitted choices, it refuses rather than silently
substituting current defaults. The exact executable/adapter binding remains a
launcher-protocol gate.

Provider-observed identity, native call IDs, token/cost observations, timestamps,
outcomes, PIDs, slot numbers, launch nonces and exit attestations are not fields
of this body. They belong to runtime evidence and receipts, joined by `job_id`
and the existing protected launch checks. Never expose the launch nonce or exit
attestation in the worker projection to make snapshot validation easier.

Add round-trip/refusal fixtures for every member/type/presence rule, automatic
retry versus changed requeue and ambiguous legacy lineage, mutated defaults,
changed task/prompt bytes, config digest mismatch, and swapping snapshots between
two job IDs. A legacy configured model does not become `model_observed`: that
receipt field still requires source evidence. This proposal awaits friend
review and does not assert any of these runtime fixtures pass yet.
The [complete synthetic body](fixtures/swarm-attempt-body.json) pins all members
and hash preimages for encoding checks only. Its fake paths, endpoint and prompt
are not a live launch configuration or proof that launch validation is ready.

### Protected launch record and command boundary

The argv above is the complete proposed internal launcher interface: no extra
positional arguments, alternate route/model flags, shell expansion or inherited
environment override. Its hash is supplied by the coordinator that published the
record, not read from a worker projection. `profile-supervise` is an internal
entry point of the configured trusted launcher, not a second dispatcher.
`--launch-hash` is exactly `sha256:<64 lowercase hex>` over all canonical
launch-record bytes, without a trailing newline. The record contains no self-hash.

For each reservation, publish canonical JSON at
`<evidence_root>/<job_id>/launch/<reservation_nonce>.json` using the protected,
no-replace and durable publication rules above. A later reservation gets a new
nonce and a new record even if the same pending job is retried after proven
non-launch. Never overwrite or reuse the earlier launch record. The launcher
opens one bounded regular no-follow file, at most 65536 bytes plus one byte for
overflow detection, within the remaining launch timeout; nesting is at most 8.
Reject invalid UTF-8, duplicates, unknown members, missing members, trailing
JSON, noncanonical bytes or a digest mismatch before identifying or spawning.

All members are required and all values are strings, except `context`:

| Member | Meaning and validation |
|---|---|
| `schema` | Exactly `nova.swarm.launch/1` |
| `context` | Exactly strings `kind` and `root`; kind is `pool` or `one-shot`; root is the absolute coordinator-owned lifecycle root selected for this run, checked against its protected reservation owner |
| `evidence_root` | Absolute protected evidence root; for a pool, exactly its configured evidence directory; a direct one-shot uses its explicitly supplied protected root |
| `job_id` | Exact admitted job/attempt ID; matches the protected manifest, snapshot, reservation and enclosing job directory |
| `slot` | Positive canonical decimal string, checked against the supported slot integer range and the actual reservation |
| `reservation_nonce` | Exactly the reservation's twelve lowercase hexadecimal characters |
| `manifest_hash` | `sha256:<64 lowercase hex>` over the canonical prelaunch `MANIFEST.json` bytes |
| `sandbox` | Absolute protected sandbox executable path; no implicit PATH lookup or no-sandbox fallback for this profile interface |
| `usage_every_ns` | Positive canonical decimal nanoseconds, checked against the duration range; fixes the sampling cadence, not the job's token or deadline limits |

`context.root` locates existing protected lifecycle state. A pool-less one-shot
must maintain one private reservation using the same identify, ownership and
finalization protocol; it must not invent a nonce or skip the reservation check
because it has only one worker. It remains pool-less at the user interface.
The one-shot adapter's concrete reservation storage is still an implementation
gate; this record does not claim that adapter exists.

Before identify, verify the record path, digest and protected placement, then
the matching reserved slot and complete prelaunch manifest/snapshot/files.
The coordinator constructs `context` and `sandbox` from the run's validated
lifecycle and confinement settings, never from a queued task or worker output.
Reconcile the record with that configured root and its reservation; an unrelated
protected directory is not a substitute merely because it has a matching name.
For another reservation of the same pending job, retain `context`,
`evidence_root`, `sandbox` and `usage_every_ns` from its first protected launch
record. Only the slot and nonce may change. Reject inconsistent prior records;
changing these runtime settings requires an explicit new linked attempt, not
overwriting the old record. Sandbox executable compatibility/integrity belongs
to the same pending execution-binding gate as the harness and launcher.
Derive slot/job/data-home paths from the validated context and frozen worker
description; derive route, arguments, read roots, prompt and effective limits
only from the snapshot. No launch-record member can replace those choices.
The dispatcher retains global worker caps, run duration, backoff, launch timeout
and output handling; these are not per-worker override arguments. Do not put
secret values, launch nonces or this control record in worker-visible files.

The existing `worker.usage` identifies an accounting source. It does not select
an execution adapter or establish support for a tool allow-list. Before the
runtime implementation, pin the execution adapter's identity and compatibility
contract, generated configuration and declared non-secret environment (including
PATH); current `childEnv` inheritance is insufficient for profiles. Likewise,
current `RefreshSlot` copies a live `worker_dir`: freezing the path does not
freeze its contents. The adapter contract must pin the worker artifact used by
an admitted attempt, or explicitly delimit and validate mutable inputs. A
retry may not silently switch worker instructions, executable or generator.
Hashing a path immediately before use is not proof against replacement races.
These are remaining compatibility gates, not claims that this command record
alone solves executable or artifact integrity.

The [synthetic launch record](fixtures/swarm-launch-record.json) pins this
encoding and argv boundary. Runtime tests must cover stale nonce, wrong slot,
swapped manifest, changed record/hash, duplicate publication, invalid duration,
path alias/worker-write access, unknown argv and both lifecycle contexts, with
zero identify/provider calls on refusal. Keep the separate gate-observation
tests: a launch-validation refusal after `SECRETS EXEC OK` is not proven
pre-gate non-launch merely because its exit status is 125.

Before a worker starts, the coordinator writes the authoritative snapshot to
the coordinator-owned protected path
`<pool>/evidence/<job-id>/PROFILE.json`, where `<job-id>` is the concrete
swarm job/attempt id, outside every writable job directory and slot data home,
through the manifest-last publication protocol above. Its hash is rooted
in that protected copy; a worker cannot replace the trust root. A pool-less
direct one-shot must supply a coordinator-owned protected `evidence_root` and
refuses before send when it is absent or writable by the worker. The worker may
read and overwrite a worker-writable sanitized projection at
`<job>/PROFILE.json`, but recovery verifies the protected snapshot and hash,
never the projection. The snapshot is a non-secret record of the resolved
profile and attempt: catalog/profile id and catalog hash,
requested and resolved provider/model, base URL, harness and arguments, worker
directory, credential-binding paths and seat (or legacy key-file path), env-var
name, usage source, deadline, prompt mode
and prefix hash, tool list, task id, attempt and retry origin. It never holds a
key, decrypted credential, token or response body.

The sidecar carries `profile`, `model_requested`, `model_observed`,
`snapshot_hash`, `prompt_hash`, `config_hash`, and `bench` when the caller
supplies it. The proposed `run --bench <name>` flag supplies bench metadata
for legacy sidecars. Existing sidecars with none of these fields remain valid. The
new profile projection maps `profile` to body `profile_id`, `model_requested`
to `requested.model`, `prompt_hash`/`config_hash` to `hashes.prompt`/`hashes.config`,
and `snapshot_hash` to the verified prelaunch manifest value. `model_observed`
is populated only from later runtime evidence, never from `resolved.model`.
The
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
shape. Legacy key-file handling remains unchanged; live profiles use the
credential binding above and do not define a second credential store. A live-route profile launches
only through a coordinator-controlled invocation of `nova-secrets exec
--only <route-env> --require <route-env> -- <isolated-worker-launcher>`. The
coordinator invokes the gate for each selected live-route worker; an environment
flag claiming a parent wrapper ran is not proof. The immutable profile pins
non-secret store/seat/age-key/sops and executable bindings using the field mapping
above; the protected launcher protocol remains a pre-implementation gate. The launch order is `run` ->
`nova-secrets exec` -> isolated-worker launcher -> sandbox -> harness; decryption
and store validation run outside the worker sandbox. Pinned seat key and store
root paths must satisfy the same resolved-filesystem placement checks as secret
`key_file` paths in SPEC-SWARM.md, including aliases and not-yet-created slots,
and must be outside all worker-readable or writable pool/job/slot/scratch/data
home/read-root locations. The pinned gate executable must not be replaceable by
the worker. Admission and prelaunch validate its resolved path and every parent
against the worker's effective write authority: both the configured sandbox write
roots and the execution identity's filesystem permissions must be considered.
If the launcher cannot establish that the worker cannot modify the executable or
replace it through a writable parent, it refuses; a safe-looking path alone is
not proof. Admission and prelaunch validation refuse unsafe placement exit 2
before decrypting or starting a harness. `nova-swarm run` refuses
exit 2 before the first worker if that binding is absent or unsupported. The gate refuses exit 125
for a missing or malformed store shape, including a missing/malformed recovery.pub or a recipient rule other than one
seat age public key plus exactly the recovery age public key declared there.
Encryption recipients are not API keys: --only and --require select the one
API-key environment variable needed by the route. A fixture with three recipients must
exit 125 and start zero workers for that attempted launch. On a proven gate
refusal, `run` emits one bounded `RUN REFUSED profile=<id> reason=secrets_gate
code=125` line, retains the selected task pending (not failed), and disables
further launches of that profile for this run. Return the task from a claim only
after the direct child exits 125, its complete privately captured stderr
contains no `SECRETS EXEC OK` event, and the protected slot proves no identify
under that attempt's reservation nonce. These three observations are required
non-launch evidence. `nova-secrets exec` replaces itself with the launcher;
PID or exit code alone cannot identify which executable exited. An uncertain
exit follows existing quarantine/reconciliation rules, never blind requeue.
Already-running jobs continue under normal accounting/finalization; unrelated
profiles may run only under their own valid gates. Reservation release requires
confirmed non-launch. The gate keeps key values out of profiles,
snapshots, receipts, task argv and logs, and passes recovery and scope checks
before real traffic is enabled.

### Gate observation — proposed, awaiting friend review

The coordinator owns the gate's stderr pipe, observes its direct child through
the OS process handle, and drains the pipe while waiting. It never infers an
event from task text, worker logs, worker-supplied status or an environment flag.
Match SPEC-SECRETS' `SECRETS EXEC OK` grammar; even a suspicious line beginning
with that prefix defeats an assertion that no OK event was emitted. A forged
positive event can cause reconciliation, never authorize launch or a grant.

Use a streaming detector with a 65536-byte maximum line, 1048576-byte total
observation limit and the launch timeout. If a limit, timeout or pipe error
prevents complete observation, mark completeness false and keep draining without
retaining bytes until the bounded process wait/cancellation ends. Oversized or
malformed events are unknown, not absent. Do not persist raw stderr or repeat
arbitrary child output in a refusal; retain the typed observation and bounded
static remedy. Classification requires no credential value in stored evidence.

After direct-child termination, persist an immutable coordinator-owned
`<evidence>/<job_id>/gate/<reservation-nonce>.json`, outside worker-readable and
writable paths. Its canonical object has exactly: string `schema` equal to
`nova.swarm.gate/1`; strings `job_id`, `reservation_nonce`, `manifest_hash`,
`exit_code`, `identity`; and booleans `output_complete`, `ok_event_seen`.
`manifest_hash` hashes the exact canonical prelaunch manifest bytes.
It uses `sha256:<64 lowercase hex>`; `reservation_nonce` uses the existing
twelve-lowercase-hex launch token, not a newly invented task identity.
`exit_code` is a nonnegative decimal status or `-` for unknown/signal termination.
`identity` is `absent`, `present` or `unknown`. Use the snapshot's strict JSON,
regular-file, size and no-replace publication rules. This is protected runtime
evidence, never a nonce-bearing worker projection.

Latch `identity: present` when the launch handshake observes matching
`SlotLaunched` with this job and nonce. Hand off to normal asynchronous
supervision then; do not wait for worker completion before dispatching other
jobs. Persist that latched fact with the final typed observation before freeing
or reclaiming its protected slot/evidence. A later empty slot cannot erase it.
If a crash loses the latch and the protected evidence cannot establish it,
record `unknown`; do not infer absence from an already freed slot.

`identity: absent` requires a successful read of the still-reserved protected
slot with this job and nonce after the direct child is observed dead. A missing
or unreadable slot, nonce mismatch, cleared slot or uncertain lifecycle is
`unknown`. No process may clear a matching identify record before its
finalization/reconciliation consumes it. A snapshot-aware launcher performs
the existing reserve-to-identify CAS before any harness or provider action.
The legacy supervisor shows this ordering, but its key-file reader is not a
profile implementation. The profiled launcher consumes only the named
gate-delivered secret and constructs the empty-based child environment.

| Observed state | Disposition |
|---|---|
| Exit 125, output complete, no OK event, identity absent | Proven gate refusal: task pending, profile disabled for this run; release only confirmed non-launch reservation |
| OK event, identity absent or unknown | Post-gate failure/uncertainty: reconcile or quarantine, no blind requeue or liability release |
| Identity present, including child exit 125 | Launched attempt: normal process/exit/usage evidence applies |
| Incomplete output or unknown exit/identity without stronger launch evidence | Unknown: retain liability and reconcile |

The OK event alone proves neither harness startup nor correct secret scope.
Admission, protected snapshot verification, selected environment construction
and launch identity remain separate gates. Failed gates do not create provider
spend or successful worker usage rows. Lost provenance remains unknown.

Required fake-process fixtures: gate refuses 125 without OK/identify; gate emits
OK then execs a child returning 125 before identify; harness exits 125 after
identify; oversized/truncated OK line; broken capture pipe; missing/stale-nonce
slot; forged OK prefix. Only the first may use the gate-refusal pending/release
path. Use synthetic values, zero live decryptions and zero provider calls.

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

A numeric harness `cost` field is not by itself a provider billing observation.
Each cost value must retain its source and basis: provider-reported cash,
harness estimate, dated rate-derived estimate, or declared local API-cost zero.
Unknown basis stays unknown; do not promote an OpenCode step cost to observed
provider USD merely because it is numeric. Preserve the raw harness value and
receipt separately when its rate revision is unavailable, with that gap explicit.
Do not combine estimates and observed cash into an unlabeled total, and do not
rewrite historical receipts to invent provenance. The same rule applies to
swarm, local and one-shot adapters.

An adapter regression must supply synthetic provider responses containing token
usage but no money field, while the harness emits a nonzero cost. Expected:
native tokens survive, provider USD remains unknown, and the harness cost survives
with its own provenance. Also exercise explicit provider zero, declared local
zero, missing cost and unknown legacy provenance. Synthetic fixture usage is
excluded from real spend and adoption measurements.

Every source adapter reports coverage per field as one of `observed`, `missing`
or `unsupported`: `observed` retains the value and protected evidence,
`missing` records `-` because the source was expected but omitted it, and
`unsupported` records `-` because the adapter cannot provide that field.
Neither status becomes zero or a success claim. A harness-normalized number is
an observation of that harness, not automatically a provider-native observation.
Bind `token_basis` and field coverage to the adapter/harness revision and its
inclusive/subset mapping. If normalization erased missingness or clamped an
inconsistent residual, retain that limitation; a manufactured zero does not
establish a provider-reported zero. Preserve native totals and step/call identity
when available so a derived total can be checked without adding overlapping
message and step projections. A last-step message projection cannot stand for a
whole multi-step attempt unless the adapter proves the aggregation scope.

Adapter fixtures must cover absent input/reasoning/cache fields normalized to
zero, inconsistent parent/subtype counts, one message per step, multiple steps
per message, and a numeric zero harness cost with no provider money field.
Where the source supplies an aggregate total and observations are complete,
assert that the declared disjoint components reconcile to it. An absent source
total stays absent; incomplete observations retain raw fields and an explicit
coverage gap. These exercise the existing provenance and aggregation contract,
not a second accounting ledger. New receipts expose required
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
adapter-declared non-secret runtime variables and the selected route secret;
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
   before provider launch, with no fallback. Include key/store paths inside each
   protected placement, case/symlink aliases, future slots, and worker-replaceable
   gate executables. Assert no decrypt or harness call. A `run --profiles` hash
   mismatch cannot silently replace an admitted snapshot.
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
   writer. Each child starts from an empty environment plus its declared non-secret
   runtime variables and exactly its selected route secret; parent extras and
   other route secrets are absent. The fixture also proves the isolated harness
   still starts with its required runtime environment.
6. **CAPACITY-RESERVATION.** Quota, allowance, balance, overflow and usage
   remain separate; unknowns are `-`; catalog and metadata refreshes are bounded
   to 256 KiB and 5 seconds by default, stale/oversized results are unknown;
   reservation, bake-off and exact-once recording are bounded. A bake-off uses
   the caller's explicit task/worker cap and never unbounded fan-out.
7. **PROTECTED-EVIDENCE-CONFLICT.** The coordinator's protected snapshot survives
   a worker write attempt and a crash; recovery trusts only its hash, retains
   non-secret raw evidence, and exposes only a sanitized worker-writable
   projection. Profiled reclaim refuses before removing job files when the retained
   profile snapshot/receipt is missing or its snapshot hash mismatches; success
   retains the matching protected evidence and usage/report after reclaim. Include
   a worker-modified sanitized projection that cannot satisfy this precondition.
   A changed counter payload for one stable attempt identity prints
   `CONFLICT` and is not accepted as a second usage row.
8. **SHARED-KERNEL-LOCK.** Two profiles and two pools sharing one provider
   allocation contend on one launcher-named kernel lock outside the pools. A
   fork/exec fixture with two `nova-swarm` processes proves one reservation at a
   time; a deadline expiry or crash leaves unknown liability reserved until
   explicit settlement, and settlement is atomic.
9. **SECRETS-EXEC-SHAPE.** Retention/training defaults to private, unknown
   provider attestations refuse before send, and an explicit profile or selector
   cannot weaken policy. The coordinator launches a live-route worker through the pinned
   `nova-secrets exec --only <route-env> --require <route-env>` invocation;
   missing bindings cause `run` exit 2 before the first worker, a forged parent
   environment marker cannot bypass it, and a fake three-recipient store causes
   `nova-secrets exec` exit 125
   and starts zero workers for that launch. Assert direct-child exit 125,
   complete captured stderr with no OK event, and a still-reserved matching
   protected slot with no identify. OK followed by exit 125 before identify is
   post-gate uncertainty, not proven gate refusal. A harness
   that itself exits 125 after identifying is not this non-launch case and must
   retain normal attempt finalization/accounting. Exercise a mid-pool 125: the selected
   task remains pending, one profile-scoped refusal is printed, no later job on
   that profile starts, other running jobs retain accounting, and no task is
   falsely failed. An uncertain launch is quarantined and not requeued.
   No key value appears in argv, snapshots, receipts,
   raw evidence, `RESULT.md` or logs.
10. **COMMON-ACCOUNTING.** Legacy, profiled, `nova-local` and direct one-shot
    attempts use one accounting pipeline. Retries, rework and bounded multi-turn
    one-shots each retain their own attempt evidence; local inference reports
    `usd=0`, absent API cost/usage is `-`, source coverage is
    observed/missing/unsupported, and a cache or reasoning subtype is never
    added twice to a parent total. A source-declared inclusive-basis fixture (input=1000/cache_read=800)
    retains aggregate input=1000; a source-declared exclusive-basis fixture
    (input=200/cache_read=800) retains aggregate input=1000. New receipts
    support daily bench/repo/model/actor views while legacy rows stay readable.

The exact ten gates above are the compact profile acceptance set; the existing
SPEC-SWARM tests remain separate and are not silently replaced. A review gate
for implementation is described below and records individual friend dispositions;
this document makes no claim that the gate has passed.

## Implementation decisions and review gates

This is a consolidation draft, not an approval to skip unresolved interfaces.
A child implementation may start only after every awake friend has independently
reviewed this exact revision and recorded a disposition. Every required
review must be APPROVE or an explicit ABSTAIN under the agreed policy; silence
stays pending and a HOLD must be resolved by its author. Specialist rest is not
an implicit review or an instruction to wake a reserved friend. Before the runtime PR, pin the catalog JSON schema and limits, exact
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

## Planned trial: lightweight hosted workers alongside local inference

Include OpenCode Go DeepSeek Flash as a candidate for bounded lightweight coding
jobs, alongside existing local and metered worker routes. The observed motivating
constraint is bench occupancy: local inference can consume the bench's usable
GPU/memory capacity and produce tokens slowly. Do not generalize that observation
into an unmeasured claim about every local or hosted model.

After the credential gate and profile contract are ready, trial comparable small
fixes, test scaffolding and focused reviews through the existing harness/profile
path. Pin actual model/version, route, prompt, cache/session behavior and accepted
quality. Record time to first token, tokens per second where observable, completed
accepted tasks per minute, queue time, total operational tokens including review
and rework, subscription allowance consumption, cash/reference cost and bench
capacity left available for other jobs. A faster token stream or covered marginal
charge alone does not establish a better completed-work cost.

Start with a bounded task count and concurrency within observed allowance; no
unlimited-capacity assumption, paid overflow or silent route fallback. Discover
current model/rate/window metadata rather than hardcoding a promotional allowance.
Preserve coding-client identity and stable per-conversation session routing where
the provider requires it. Failures or unavailable capacity produce observations
and reconciled jobs, not repeated blind launches. Provider-specific API behavior
is tested with fixtures before live use. No key handling or live launch is granted
by this plan entry.

Adopt the route for task classes where measured quality is maintained and total
operational cost/latency improve. Keep local execution available where it wins or
where the configured data boundary requires it. Record negative or inconclusive
results; repeat only when changed conditions justify another bounded trial.
