# Swarm realization and protected-path reconciliation — proposal

Status: discussion draft only. This resolves only the first two open decisions in
[execution binding](SWARM-EXECUTION-BINDING.md): a pure non-secret environment
realization, and the relation between protected manifest roles and a reservation's
runtime paths. It does not approve profile execution, generate an adapter config,
publish artifacts, specify Windows, or read a secret/provider.

## Source conflict and narrow amendment

The current legacy implementation derives `SlotDir(slot)` as
`<worker_dir>-<slot>`, then `JobDir` as `<slot>/jobs/<job-id>` and `DataHome` as
`<job>/data` (`internal/swarm/worker.go:397-409`). `Run.prepare` calls
`RefreshSlot`, which copies the *live* `worker_dir`, and `childEnv` inherits the
parent `PATH` (`run.go:592-615`, `supervise.go:416-445`). That is correct legacy
behavior, but it cannot implement the proposed protected attempt: the binding
draft makes `worker.worker_dir` source provenance only, while
`SPEC-SWARM-PROFILES.md:430-443` nevertheless says runtime paths derive from the
frozen worker description. Those statements conflict.

Amend the new profiled `nova.swarm.attempt/1` path rule only. Keep legacy worker
path derivation untouched. For a profiled launch, `context.root` is the validated,
absolute, coordinator-owned lifecycle root from the existing launch record. Its
canonical resolved spelling is `R`. Define, for its already-verified positive
canonical-decimal `slot` and safe admitted `job_id`:

```
slot_dir  = R + "/slots/" + slot + "/worker"
job_dir   = slot_dir + "/jobs/" + job_id
data_home = job_dir + "/data"
```

For `context.kind=pool`, `R` is the pool root. The new directory
`<pool>/slots/<n>/worker` deliberately coexists with the existing slot authority
file `<pool>/slots/<n>.json`; it does not replace the reservation/identify CAS
(`internal/swarm/slot.go:218-279`). For `one-shot`, the already-required private
reservation root is `R`, with the same layout. This is a profile-only storage
amendment, not a reinterpretation of existing `<worker_dir>-<slot>` directories.
It removes the stale live `worker_dir` string from all runtime path derivation.

Before materialization, reject a nonabsolute/unresolved root, non-pool/one-shot
kind, invalid job component, zero/noncanonical/out-of-range slot, bad 12-hex
nonce, or any derived path escaping `R` after clean no-follow path validation.
The implementation must verify the matching reserved slot under the same
lifecycle root before identify. The runtime write roots remain exactly `job_dir` and `data_home`, as in the current dispatcher; workers produce their task outputs there. The frozen worker `read_roots` retain their existing toolchain/source read meanings. This adds no configurable task-write roots. Reconcile the actual effective authority against protected inputs/control roots before launch; no task text changes these formulas.

## Exact pure realization

Define a pure function `RealizeProfileEnvironment` with inputs:

```
(context.kind, context.root, job_id, slot, reservation_nonce,
 execution.path[], execution.env[], env_var)
```

All inputs except a secret *value* come from the verified launch record and
protected attempt. `execution.path` is the frozen ordered absolute path list;
`execution.env` is the frozen non-secret name/value list; `env_var` is the one
selected route-secret **name**. This function reads neither process environment,
filesystem nor credentials. On the current POSIX profile scope it joins PATH
entries with `:`. An entry containing `:` or NUL refuses, as does any NUL in an environment name/value; otherwise a pathname could introduce an unadmitted PATH component or an invalid process environment. Names must satisfy the existing adapter/credential variable-name validator, with reserved-name and duplicate checks before hashing. The final supported static-name list still belongs to the adapter contract. Windows has no realization contract here and must refuse the
profile before gate/identify until its own separator, case, system-variable and
loader rules are pinned.

The function derives the three paths above and constructs exactly these public
variables: `HOME=data_home`, `XDG_DATA_HOME=data_home`,
`NOVA_SWARM_JOB=job_dir`, `PATH=strings.Join(execution.path, ":")`, plus the
static `execution.env` entries. It rejects duplicate names, an empty name/value
where the execution schema disallows it, or a static name equal to any derived
name or `env_var`. It sorts the resulting public entries by raw UTF-8 name bytes.
It does not add `Path`, `TMP`, `SystemRoot`, parent configuration, or a secret
pair.

Its canonical, number-free JSON preimage is exactly:

```
{
  "schema":"nova.swarm.realization/1",
  "context":{"kind":"pool|one-shot","root":"R"},
  "job_id":"...", "slot":"...", "reservation_nonce":"...",
  "paths":{"slot":"...","job":"...","data_home":"..."},
  "env":[{"name":"...","value":"..."}],
  "secret_env_var":"..."
}
```

Objects use the profile canonical JSON rules; arrays retain their shown order.
`env_hash` is `sha256:` plus the SHA-256 of those exact canonical bytes. The
launch record contains only that digest; coordinator and launcher separately run
the pure function and compare. After the comparison, the trusted gate/launcher
must append exactly one nonempty `env_var=<secret>` pair from the selected gated binding to the process environment. Missing or ambiguous duplicate values refuse. It never
retains, hashes or logs that value. Including the secret *name* makes a changed
route binding observable without exposing its value.

Usage readers and report adapters for a profiled job must use the same verified realized `job_dir`/`data_home`, not legacy `Worker.DataHome` derived from the provenance directory. A retry retains the earlier job's raw observations under that earlier attempt and reads the new attempt's own data home separately. This is a path-source amendment, not a change to token normalization, observed model identity or unknown-cost rules. The runtime acceptance case must put a synthetic usage database at the new realized path and a conflicting decoy at the legacy path, then prove only the correct attempt's observations are joined.

## Protected role and path reconciliation

`worker.harness` and `worker.worker_dir` are admission provenance only. Neither
is executed, copied, or used to form a slot path for a profiled attempt. After
verifying the protected attempt, `ARTIFACT.json`, `HARNESS.json`, and
`CONTROL.json`, the launcher derives every execution path from their declared
roles, not from a free protected path supplied by a record:

| Role | Protected source | Per-reservation projection | Consumer |
|---|---|---|---|
| harness | sole regular `ARTIFACT` entry whose destination equals `execution.harness` | `slot_dir/inputs/<destination>` | sandbox child command |
| worker instructions | all `worker/` `ARTIFACT` entries | `slot_dir/inputs/worker/...` | sandbox read root |
| generated configuration | verified protected `HARNESS.json` | adapter-declared exact path below `slot_dir/inputs/`; absent mapping refuses | adapter only |
| launcher | `CONTROL` entry role `launcher` | none | gate argv uses `control.root/launcher` |
| sandbox | `CONTROL` entry role `sandbox` | none | wrapper argv uses `control.root/sandbox` |

`RefreshSlot` for profiles is replaced by an owned no-follow materialization of
only the verified artifact/config roles into a fresh `slot_dir/inputs`; it
verifies every projected regular byte and mode against the manifest. It never
reads the provenance `worker_dir`, preserves no old slot input, and leaves each selected `job_dir` separately worker-writable; it does not grant writes over all `jobs/`. The sandbox receives `slot_dir/inputs` as the instruction/harness read root and `job_dir,data_home` as runtime writes, plus the frozen worker `read_roots` and every frozen `execution.path` directory as reads only. This explicit union is deduplicated after resolved placement validation; every PATH directory must pass the same read-root/credential-placement checks, and no union member may expose protected lifecycle/credential material or make the immutable inputs writable. The PATH in the example therefore grants a validated read of `/opt/opencode/bin`; it does not rely on ambient sandbox permissions. It does not grant a read of every prior job under `slot_dir`. This narrows the legacy whole-slot read root (`internal/swarm/sandbox.go:20-35`, `supervise.go:105-123`) for new profiles only. A profile cannot
use `--no-sandbox`.

This requires the binding draft's raw `version:1` manifests to change to
number-free schemas `nova.swarm.artifact/1` and `nova.swarm.control/1`; that is
a proposed draft correction, not current schema behavior. `control.root` is
valid only when it is the verified job-specific control directory and the two
literal role destinations above resolve under it. Equal bytes at an arbitrary
path do not qualify. Exact protected publication, aggregate accounting, and the
adapter's generated-config filename remain outside this decision; without a
mapped configuration path, launch refuses rather than relying on OpenCode's
ambient discovery.

Protected roots are per concrete `job_id`, never per mutable source path.  Let
`H(a)` be the lower-hex digest of the verified artifact manifest and `H(c)` the CONTROL manifest digest.  The exact
role roots are

```
artifact_root(job) = evidence_root + "/" + job + "/artifact/" + H(a)
config_file(job)   = evidence_root + "/" + job + "/HARNESS.json"
control_root(job)  = evidence_root + "/" + job + "/control/" + H(c)
```

The configuration file stays the protected prelaunch sibling already specified by execution binding; PROFILE carries its generated-config digest. There is no second config-root owner.

A re-reservation of the **same** pending job verifies and reuses those exact
paths and bytes. A `lineage.kind=retry` has a new job ID and makes
byte-identical, no-replace protected copies below that new job's roots only
after verifying the predecessor's manifest/objects. It never rereads a live
worker, harness, launcher, sandbox, or generator source. The copy paths change
because the evidence identity changes; artifact/config/control digests and role
destinations stay equal. `context.root`, the evidence-root base, usage cadence,
and the verified control role identities carry forward. This is consistent with
the existing first-launch retention rule for another reservation of the same
pending job; it clarifies that a new retry preserves immutable content rather
than incorrectly reusing another job's protected path. A missing or conflicting
predecessor object quarantines the retry before gate/identify.

## Two finite synthetic vectors

Both use `context={kind:"pool",root:"/srv/nova/pool-A"}`,
`job_id="20260914T120000Z-proof-0a1b2c"`,
`execution.path=["/opt/opencode/bin","/usr/bin","/bin"]`,
`execution.env=[{"name":"NO_COLOR","value":"1"}]`, and
`env_var="OPENCODE_API_KEY"`. Canonical JSON is compact, recursively key-sorted
profile JSON; the environment array is in raw-name order.

| Vector | slot / nonce | slot_dir | env_hash |
|---|---|---|---|
| A | `2` / `0123456789ab` | `/srv/nova/pool-A/slots/2/worker` | `sha256:c99ac104c4adda7a57c838b2c86294ea63ae5375cc442f794f10f931c41a676c` |
| B | `7` / `fedcba987654` | `/srv/nova/pool-A/slots/7/worker` | `sha256:58e4ade2659df573db2d6b906d22636c00d291694bb84de2b8d79429d234e1ae` |

A's exact public entries are `HOME` and `XDG_DATA_HOME` equal to
`/srv/nova/pool-A/slots/2/worker/jobs/20260914T120000Z-proof-0a1b2c/data`,
`NOVA_SWARM_JOB=/srv/nova/pool-A/slots/2/worker/jobs/20260914T120000Z-proof-0a1b2c`,
`NO_COLOR=1`, and `PATH=/opt/opencode/bin:/usr/bin:/bin`; raw-byte ordering puts
`NOVA_SWARM_JOB` before `NO_COLOR` because `V` precedes `_`. B proves a new nonce
and slot change both path values and hash while retaining the frozen plan.

Required refusal vectors: inherited parent PATH or extra parent variable; a colon/NUL in a PATH component; a PATH directory failing read-root validation;
static `HOME`, `PATH`, or route-secret-name collision; duplicate static names;
relative/symlink-escaping root or job ID; slot `0`, `02`, or an unmatched slot
record; malformed nonce; manifest role/path mismatch; using a live `worker_dir` as a recovery preimage after admission (a harmless change at the unused provenance path alone does not reject an otherwise valid immutable retry); stale slot `inputs`; missing generated-config role mapping; and
any `--no-sandbox` profile request. Pre-gate preparation failures observe zero gate/worker/provider calls. The launcher repeats reservation/hash/path checks after gate entry and before identify/worker spawn; such late failures can occur after the gate injected the selected secret. They still retain no secret value and start no worker/provider. Missing gated secret is necessarily detected at that later boundary. Do not claim that every launcher refusal occurs before decryption.


The [synthetic vectors](fixtures/swarm-realization-vectors.json) supply exact public inputs, expected paths, ordered environment entries and hashes. They establish finite encoding consistency only; they do not validate filesystem protection, materialization, reservation ownership or a live adapter.

Reproduce the two encoding checks from the repository root with `go run ./docs/drafts/fixtures/realization-check`. The witness builds the proposed public preimage and calls the actual `internal/records.Canonicalize`; it is not the future profile validator.
