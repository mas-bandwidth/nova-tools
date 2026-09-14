# Execution binding — proposed amendment

Status: discussion draft. This document proposes changes to
[SPEC-SWARM-PROFILES](../SPEC-SWARM-PROFILES.md); it does not activate new fields
in that spec, update encoding fixtures, or approve an implementation. The proposed
copy and hash design has coordinator review, but no new independent friend approval.
Two bounded DeepSeek cold reads produced no reports and confer no approval.

The [realization contract](SWARM-REALIZATION.md) and
[artifact publication contract](SWARM-ARTIFACT-PUBLICATION.md) now supply concrete
proposals for the first three decisions below, including synthetic vectors.
They amend this discussion draft as stated; parent tables/readers remain unchanged.

The [native OpenCode source and launch proposal](SWARM-OPENCODE-NATIVE.md)
specifies the stdin/argv boundary and records the remaining compatibility gate
in [issue 296](https://github.com/mas-bandwidth/nova-tools/issues/296). A custom
config file alone does not establish isolated effective inputs.

Pending amendment to `SPEC-SWARM-PROFILES.md`; it is neither a runtime claim nor a change to legacy `--worker`
readers. New members apply only to a profiled `nova.swarm.attempt/1` after that reader is upgraded.
`worker.usage` stays an accounting-source selector; it never selects an adapter or executable.
## Profile plan and admission artifact
Keep the present source owners: `worker.harness` supplies the native harness source and `worker.worker_dir`
supplies the instruction tree. A profiled `worker` additionally has this required object; unknown or duplicate
fields refuse. Existing readers reject it until their schema is amended.

```json
"execution": {
  "adapter": "opencode-native/1", "adapter_revision": "1",
  "path": ["/usr/bin", "/bin"], "env": [{"name":"NO_COLOR","value":"1"}],
  "artifact": {"max_files":"4096", "max_bytes":"1073741824",
               "max_manifest_bytes":"16777216"}
}
```

`adapter`/`adapter_revision` are closed implementation identifiers, not provider/model names. `path` is
nonempty, ordered, distinct, absolute directories validated outside every worker-writable and configured
task-write root. `env` is ordered, distinct adapter-declared names and literal non-secret values; it cannot name
the route secret, `PATH`, `HOME`, `XDG_DATA_HOME`, `NOVA_SWARM_JOB`, or a platform-reserved name. `artifact`
values are positive canonical decimal signed 64-bit integers. `max_files` is at most 4096; `max_bytes` and
`max_manifest_bytes` are independently bounded by the signed range. Each source path component is at most 4096
UTF-8 bytes. There is no shell, PATH lookup, ambient config/environment discovery, or automatic update.

At admission, stream no more than `max_manifest_bytes` plus an overflow byte and build canonical `ARTIFACT.json`
(UTF-8 JSON, no trailing bytes) with this exact record shape:

```json
{"schema":"nova.swarm.artifact/1","entries":[
 {"role":"harness","destination":"harness/opencode","type":"regular",
  "mode":"0755","bytes":"123","sha256":"sha256:<64 lowercase hex>"},
 {"role":"worker","destination":"worker/INSTRUCTIONS.md","type":"regular",
  "mode":"0644","bytes":"456","sha256":"sha256:<64 lowercase hex>"},
 {"role":"worker","destination":"worker/skills","type":"dir","mode":"0755"}
]}
```

Every regular entry has exactly `role,destination,type,mode,bytes,sha256`; every dir has exactly
`role,destination,type,mode` and no bytes/hash. There is exactly one `harness` regular entry. `worker` entries
form the complete `worker_dir` tree. `harness` and `worker` are implicit role roots, not entries; all other
parents must be explicit `dir` entries, so an empty directory is retained deterministically. Destinations are
unique, clean, relative slash paths, lexicographically ordered by raw UTF-8 bytes, bounded by 4096 bytes, and
have the shown role prefix. Regular `bytes` are canonical decimal, their sum is at most `max_bytes`, and entry
count is at most `max_files`. Modes are exactly `0644|0755` for regulars and `0755` for dirs. Links, devices,
FIFOs, sockets, set-id modes, unknown types, missing parents, extra harness files, worker-writable source/control
parents, or aggregate/manifest bound failure refuse.

Copy precisely those regular files, no replace, into a coordinator-owned, worker-nonwritable protected artifact
named by the hash of canonical `ARTIFACT.json`; verify bytes and modes after copy. Existing `worker.harness` and
`worker.worker_dir` absolute strings stay in the snapshot only as admission provenance; launch selects solely
the protected role destinations. This protects against a worker replacing inputs, not a malicious coordinator or
root. OS runtime libraries remain the trusted platform base; this does not pin every source file or program a
task may execute.

`opencode-native/1` is first: it consumes separately resolved `opencode-go` and `opencode`
route/provider/endpoint/model bindings, and deterministically generates OpenCode configuration. Go and Zen are
distinct routes, not adapter aliases or accounting choices. A callback/generator must accept the exact canonical
frozen input projection stated below and produce either exact bytes or refusal. Local no-secret and
direct-one-shot variants remain separate existing gates.

## Frozen attempt and hashes
The protected attempt body gains required `execution` and `hashes.generated_config`:

```json
"execution": {"adapter":"opencode-native/1", "adapter_revision":"1",
 "artifact_hash":"sha256:<64 lowercase hex>", "harness":"harness/opencode",
 "worker_root":"worker", "path":["/usr/bin","/bin"],
 "env":[{"name":"NO_COLOR","value":"1"}],
 "artifact":{"max_files":"4096","max_bytes":"1073741824",
             "max_manifest_bytes":"16777216"}},
"hashes":{"config":"sha256:<...>","generated_config":"sha256:<...>"}
```

Profile `worker.execution` is normalized into this top-level body `execution`. The body `worker` retains its old
reader schema and explicitly has no `execution` member, so the frozen top-level object is the only
execution-plan owner.

At admission the adapter generates non-secret, slot-independent `HARNESS.json` from the canonical object
`{requested,resolved,worker,credentials,env_var,limits, prompt,execution}` plus canonical `ARTIFACT.json` hash.
Retain its exact bytes as a protected sibling; `generated_config` is their SHA-256. `config` is SHA-256 of the
first object alone: it includes the execution plan and `artifact_hash`, but never generated bytes/hash, a
secret, a slot, nonce, launch record, or itself. Thus no hash is cyclic. The adapter identity/revision, artifact
role map, and callback input are all bound by the two noncyclic preimages. For profiled attempts, `RefreshSlot`
must materialize only the protected copies; a live `worker_dir` or slot copy is never a recovery preimage.

The generator may consume only those canonical non-secret values: `credentials` contributes its retained names
and paths, never opened credential contents or a gated environment. `ARTIFACT.json` and `CONTROL.json` use number-free string schema tags under the publication amendment, so their validated values can use the same canonical encoder as the protected attempt. The existing catalog framing is unchanged.

## Authenticated launch realization
Admission freezes a plan before a slot exists. After the launcher verifies the protected attempt and
authenticated reservation, it alone derives `HOME`, `XDG_DATA_HOME`, and `NOVA_SWARM_JOB` from validated
context, job id, and selected slot. It constructs PATH only from frozen `execution.path`, applies frozen static
`env`, and accepts from `nova-secrets` exactly the selected route-secret variable. No secret value enters
configuration, argv, hashes, retained artifacts, or logs.

The pending launch record keeps its existing `sandbox` member as the protected execution destination and adds
required `control` and `realization` members:

```json
"control":{"root":"/protected/control/sha256-<...>",
 "manifest_hash":"sha256:<64 lowercase hex>",
 "launcher":{"source":"/configured/launcher","path":"/protected/launcher",
             "sha256":"sha256:<64 lowercase hex>"},
 "sandbox_source":"/configured/nova-sandbox"},
"realization":{"env_hash":"sha256:<64 lowercase hex>"}
```

Before gate invocation, coordinator validates and no-replace copies the configured `credentials.launcher` and
lifecycle sandbox source into a worker-nonwritable control artifact at
`<evidence_root>/<job_id>/control/<manifest-digest>/`. It publishes canonical `CONTROL.json` there last,
no-replace and durable, with exact shape:

```json
{"schema":"nova.swarm.control/1","entries":[
 {"role":"launcher","destination":"launcher","type":"regular","mode":"0755","bytes":"<canonical decimal>","sha256":"sha256:<64 lowercase hex>"},
 {"role":"sandbox","destination":"sandbox","type":"regular","mode":"0755","bytes":"<canonical decimal>","sha256":"sha256:<64 lowercase hex>"}
]}
```

It contains exactly those entries in role order and no source path or own hash; `manifest_hash` is the SHA-256
of its canonical bytes. `control.launcher.path`, rather than `credentials.launcher`, is the exact existing
gate-argv program; top-level `sandbox` is the copied sandbox path that child argv executes. Sources are
provenance only. This gives each program one source owner and one protected execution path, not two launcher
owners. Launcher validates attempt/artifact/HARNESS/control hashes before identify and refuses unknown
adapter/revision/platform/loader/interpreter compatibility. The derived job/data write roots and explicitly frozen read roots remain task surfaces,
distinct from immutable control inputs; profiles add no task-selected write roots.

`realization.env_hash` is required. Its exact number-free preimage, profile-only
slot/job path formulas, and independent coordinator/launcher recomputation are in
[SWARM-REALIZATION](SWARM-REALIZATION.md). This supersedes the earlier abbreviated
`{reservation_nonce,slot,env}` preimage: context, paths, job and secret-variable
name are explicitly bound too; secret values remain excluded. The realization
hash stays outside attempt/config hashes. Another reservation of the same job
keeps its protected roots; a linked retry job gets byte-identical protected copies
under its new evidence identity. Changed inputs require explicit rework.

## Tables, fixtures, and decisive tests
Update the profile worker table (current lines 77–100), protected-attempt body and `config` preimage table
(263–313), protected artifact rules (315–359), gate argv (102–115), launch-record table and first-launch
retention (377–435). Extend `swarm-profile-catalog-encoding.json`, `swarm-attempt-body.json`, and
`swarm-launch-record.json` with the exact new shapes and their expected canonical preimages/hashes; retain
old-reader fixtures unchanged.

1. Decoders reject missing, unknown, duplicate, malformed, and legacy-unrecognized execution/control members while legacy profiles still decode.
2. Go and Zen fixtures yield distinct route configs with the same adapter grammar; no credential value appears in retained bytes.
3. Parent PATH/config mutation and source harness/worker replacement after admission cannot change generated config, PATH, role destination, or executed bytes.
4. Symlink/device/FIFO/set-id/writable-parent, bad role/destination, bad bounds, or unsafe loader/interpreter refuses before gate/identify.
5. Manifest/control/HARNESS digest or adapter-revision mismatch refuses before launch; partial artifacts quarantine with zero provider call.
6. Gate argv executes copied `control.launcher.path` and child argv executes copied top-level `sandbox`, never the mutable configured sources.
7. Each reservation's new nonce and selected slot produce a recomputed realization `env_hash`; no slot path exists in admission/config preimages, and first launch controls persist through retry.
8. Unsupported platform, adapter, or environment-name collision refuses rather than inheriting parent values.

Open gates: platform-specific copy/execute semantics (notably Windows case-folding and loader variables);
supported interpreter/runtime-library compatibility; implementation of protected launcher/sandbox copying; local
no-secret and one-shot adapters; plus existing gate-observation, accounting, profile-encoding, and
source-of-truth review gates. This proposal does not claim those compatible or implemented.

## Integration and remaining decisions before incorporation

- The realization companion proposes the exact relationship among `control.root`, the two manifest role
  destinations, `control.launcher.path`, top-level `sandbox`, and their source
  provenance. Cross-check every duplicate digest against the one manifest
  entry; never accept an arbitrary protected path because its bytes hash.
- The realization companion proposes one pure environment-realization function shared by coordinator and
  launcher. The coordinator must calculate the expected hash before publishing
  the launch record; the launcher independently recomputes it before identify.
  The current phrase “it alone derives” describes launcher enforcement, not a
  prohibition on coordinator calculation. Exact context/job/slot path mappings now have synthetic compatibility vectors; actual launcher materialization remains unimplemented.
- The publication companion proposes bounded readers and durable publication order for ARTIFACT, CONTROL and
  HARNESS, including aggregate byte accounting and the realization companion specifies how first-launch control bytes
  are reused across a new retry job ID. These still require incorporation and runtime verification. An interrupted copy is not a committed
  artifact, and a changed source is not a recovery preimage.
- Specify exact generated OpenCode configuration bytes, allowed static variable
  names and supported native adapter compatibility. A named revision plus a
  callback promise alone does not finish that adapter contract.
- Amend the parent tables and synthetic fixtures together, with independently
  recomputed canonical preimages/hashes, then obtain explicit friend dispositions
  on that revision. Do not merge this discussion draft as an implementation-ready
  contract by leaving these decisions implicit.
