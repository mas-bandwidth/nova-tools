# Protected artifacts — publication amendment for review

This fills the bounded-publication decision in [execution binding](SWARM-EXECUTION-BINDING.md). It is a proposal alongside the parent profile spec, not an implemented launch path or approval to decrypt credentials. The companion realization contract owns exact role/path formulas; this document owns bytes, publication and recovery.

## Reuse one canonical encoder

Change the two unshipped manifest tags from numeric `version: 1` to string `schema: "nova.swarm.artifact/1"` and `schema: "nova.swarm.control/1"`. Both contain exactly `schema` and `entries`; the entry shapes and ordered-array rules remain those of execution binding. This explicit amendment permits direct reuse of `internal/records.Canonicalize` and its number-free value model. The existing catalog's special numeric version framing remains unchanged. Neither a legacy parser nor the current protected-attempt reader silently accepts new fields.

Manifest readers reject duplicates, unknown/missing members, invalid Unicode, noncanonical bytes, wrong schema, unexpected types and trailing bytes. Hash canonical bytes with no trailing newline. A string schema tag does not replace shape validation. `ARTIFACT.json` and `CONTROL.json` never contain their own digest, source paths, secrets or reservation values. Generated `HARNESS.json` is an adapter-owned format; do not pass arbitrary adapter output through the manifest encoder or silently change its bytes.

## Bounds belong to each retained object

| Surface | Required bound and accounting |
|---|---|
| Artifact manifest | At most frozen `artifact.max_manifest_bytes`, nesting at most 32. Count every entry, including directories, against `max_files`. Validate the complete entry inventory before copying payloads. |
| Harness and worker payloads | At most `artifact.max_bytes` in aggregate regular-file bytes, using overflow-safe sums; each stream must match its manifest length and hash. |
| Control manifest | At most the lesser of `artifact.max_manifest_bytes` and 65536 bytes, nesting at most 8; exactly launcher and sandbox entries. |
| Control executables | Apply the same frozen `artifact.max_bytes` cap to their combined regular-file bytes, separately from harness/worker payloads. This separate control aggregate is explicit, not an accidental doubling hidden in artifact statistics. |
| Generated HARNESS | At most 1048576 bytes, the existing protected-snapshot byte cap, including the complete output. Generation must stop/refuse at cap plus one rather than buffering unbounded output. Adapter-specific syntax/depth validation still applies. |
| Protected attempts and launch records | Keep the parent spec's existing snapshot and launch-record caps; this amendment does not enlarge them. |

Report artifact, control and generated-config bytes separately, plus their overflow-checked total. These are retained-input/storage observations, not token usage or provider cost. Directory enumeration must enforce the entry bound while walking, not after collecting an unbounded list. A payload reader consumes its declared length plus at most one overflow-detection byte; a short read or extra byte refuses. Fixed manifest wrappers must fit configured caps before accepting the plan. Empty directories remain explicit; unsupported filesystem objects never disappear silently.

The copying/generation operation respects cancellation and the existing `--launch-timeout` as its enclosing copy/publication deadline at bounded chunk boundaries. Deadline exhaustion creates an incomplete preparation, not a launch receipt. No provider call is needed to prepare or validate these objects.

## Publish bytes before readiness

Use the validated coordinator control root and directory-relative, no-follow operations. Protection means inaccessible for writes under the worker's actual filesystem authority, including sandbox policy; coordinator ownership alone is insufficient when both processes share an OS user. Replace the earlier ambiguous “writable parents” wording with **worker-writable parents**. A filesystem lacking the required no-replace/durable operations refuses this path; do not substitute a check-then-overwrite sequence.

1. Reserve a coordinator-private staging directory under the destination filesystem. Bind its ownership to the existing preparation transaction so two preparations cannot adopt or remove each other's staging state.
2. Open only validated regular source handles and enumerate only validated directory handles. Copy precisely the inventoried bytes, no links or special files. Detect changed source identity/size/mode during copying; always verify destination length, digest and admitted mode. The manifest commits the copied bytes, not an assumption about later contents at a source pathname.
3. Flush every regular payload and directory metadata, from nested directories outward. Publish the manifest last inside staging and flush again. A manifest in staging is not a visible committed artifact.
4. Atomically publish the complete directory under its derived immutable destination with no replacement, then flush its parent. Only after this boundary may the attempt/prelaunch evidence reference it as ready. If the destination already exists, validate its entire expected inventory and payloads; identical content is idempotent reuse, differing or partial content refuses/quarantines.
5. Generate bounded slot-independent HARNESS bytes from the frozen non-secret projection and artifact digest. Retain them with TASK/PROMPT/PROFILE before the parent prelaunch MANIFEST-last boundary. Freeze its digest in PROFILE. Control publication must finish before the launch record can name the control manifest and before the credential gate is called.

There is no launch from staging, no overwrite to repair a hash mismatch, and no readiness based on a filename alone. Verification also refuses extra payload entries that could become executable/configuration inputs. If any durable step fails, keep the preparation's non-ready state and its provenance; failure after a rename but before acknowledgement must reconcile the actual destination. It is neither automatic success nor evidence of non-launch.

## Retries and recovery

The realization contract distinguishes a new reservation of the same job from an explicit linked retry job. Whichever protected destination it requires, the byte source for a retry is the prior validated immutable artifact/control set and retained HARNESS, never a current catalog, mutable instruction directory or installed executable. Copying byte-identical protected inputs to a new derived destination is distinct from regenerating them with current defaults. Any changed executable, instructions, adapter/configuration bytes or declared environment is explicit new rework with its own admission.

Recovery checks the complete reference chain before identify: selected reservation, launch record, prelaunch manifest, PROFILE, HARNESS, artifact/control manifests and their payloads. Digests establish content identity, not authorization. Missing prior bytes cannot be reconstructed by reading mutable sources with matching names. Partial preparation and unknown launch status use the existing reconciliation process; they do not trigger another provider call merely because a file is missing.

Required runtime witnesses remain unimplemented: crash at each publication/flush boundary; concurrent identical and conflicting publication; cancellation during enumeration/copy/generation; exact-limit and limit-plus-one inputs; changed source while copying; partial destination; extra executable/config input; same-job new reservation; linked retry after mutable source replacement. Every prelaunch refusal must observe zero credential-gate calls and zero worker/provider starts. Encoding fixtures alone prove none of these filesystem properties.
