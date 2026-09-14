# Optional batch admission receipts

**Status: proposal, not implemented or approved for build.** Root cold review and each participating friend's independent disposition must name the exact revision before implementation. This extends the existing `batch` admission boundary; it introduces no dispatcher, model-call batching, accounting ledger, dependency scheduler or cancellation behavior. The current `batch` invocation without the new option remains compatible.

## Problem and evidence

At public main `86785fd0accf997662c1cd5356f60acaf7a0821c`, [batch admission](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/cmd/nova-swarm/main.go#L365) reads all task files and then writes jobs sequentially. It does not preserve the input filename in the sidecar or return a filename/job mapping. [IDs](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/internal/swarm/pool.go#L136) contain second-resolution time and randomness: sorting them does not reconstruct card order. [Pool.Add](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/internal/swarm/pool.go#L182) writes the task before its sidecar; a later write failure does not roll back earlier jobs.

Workshop capability probes used source `1a14c7a52b80`, version `v0.15.3-0.20260914142654-1a14c7a52b80`, binary SHA256 `1fc75157070cf7b98151dc967aa2f419fa2ea3f67173a98f9d6ed27d36c9b83b`. The root retains `20260914-batch-receipt-gap/` with exact input bytes, binary identity, argv, stdout/stderr, source copies and a passing artifact verifier. These are synthetic admission/parser probes, not production review or operational savings evidence:

| Witness | Observed result |
| --- | --- |
| `card-alpha.txt` and `card-beta.txt`, both exactly `Synthetic identity probe only. Do not execute.` followed by LF; SHA256 `878f6d603d1a46843a7251593c344c22d5802d137cccb34bd6fe261e6e4e3b8b`; common label | Two unique jobs, same prompt hash/label/batch; either named card matches both jobs. |
| Repeat the exact batch argv | A new batch and two more jobs; pending count 2 becomes 4. |
| Sorted small first card and second card larger than 4096 bytes; child process alone has `RLIMIT_FSIZE=4096`, SIGXFSZ ignored | First task and sidecar survive; second task write returns `file too large`, exit 2. |
| `triage --batch ID --no-state` while every admitted job is pending | Exit 1: triage's job selection excludes pending. Status/sidecars retain their identities. |
| Hand-built retained report, then changed bytes; another job has no report; empty usage directory | Triage names the new hash prefix and counts the missing report. Cost says `tasks=0`; aggregate zeros are not observed per-job usage. No worker ran. |

A wrapper using distinct labels and durable correlation can solve parts of this problem. It is not impossible. The native choice preserves the caller's label and places recovery at the boundary that assigns IDs and publishes task/sidecar files, including orphan-file cases.

## Finite opt-in interface and identity

Propose one admission option:

```
nova-swarm batch --pool <dir> --tasks <dir> --files <n> --tokens <n>|unmetered \
  [existing batch options] --submission <id>
```

`--submission` is a caller-chosen stable identifier, scoped to this pool: 1–128 ASCII letters, digits, period, underscore or hyphen. It is an idempotency identity, not authority. Omission preserves legacy behavior. No per-card profile/model fields are added.

For this mode, take one bounded input snapshot before any job publication: 1–256 regular, non-symlink task files directly under `--tasks`, sorted by exact UTF-8 filename bytes; each filename is a distinct card key, at most 255 bytes. Reject other entry types and invalid names rather than silently changing membership. Raw and admitted prompt bytes are each limited to 1 MiB/card and 16 MiB/submission. Preserve bytes exactly; apply an explicitly selected existing template once, and hash its resulting admitted bytes separately. No prompt marker is inserted. Directory membership or file changes detected during capture refuse before admission; the receipt describes the captured snapshot, not a promise that source files remain unchanged later.

The immutable intent records: schema/version, submission ID, canonical pool binding, normalized source-directory locator, the ordered card keys/source filenames, raw and admitted SHA256/byte counts, exact common admission parameters (including original label), template identity when used, one preassigned batch ID and distinct preassigned job IDs. Bind retained admitted bytes to their hashes so recovery does not reread mutable task sources. Bind the canonical pool path and stable filesystem directory identity in `<pool>/admissions/pool.json`, created exclusively and synchronized on the first opt-in invocation. Refuse a changed path/directory identity or unavailable identity; no copy/move migration is introduced. The supported-platform representation of that identity must be reviewed before build.

Store metadata and admitted byte snapshots under `<pool>/admissions/<sha256-of-submission-id>/`, a coordinator-owned admission namespace outside worker-writable job/slot paths. Its immutable `intent.json` and `prompts/<job-id>.task` snapshots precede numbered `outcomes/<revision>.json` files. Preserve an intent hash/card-key reference in each new job's sidecar without overloading its label. The intent is persisted **before any task, sidecar or dispatchable pool write**; admission-metadata writes are the necessary exception to “before pool writes.” The metadata is an admission receipt, not a spend ledger. Hash the submission ID for its storage name; retain the original inside the intent. The exact metadata encoding is a required reviewed implementation contract, not invented compatibility with PR264 receipts.

## Publication, replay and recovery

Serialize same-submission admission with an exclusive lock; a competing invocation receives a bounded busy response without writing jobs. Persist the complete immutable intent and byte snapshots before assigning any card a publishing phase. An existing submission with identical captured content and parameters uses its existing IDs. Any content, label, template, budget or source/card mapping change under that ID refuses without mutation. The same bytes under a different submission ID deliberately describe a different admission.

Use immutable, numbered outcome revisions with atomic publication and no overwrite of intent or prior revisions. Bound admission metadata to 1 MiB/intent and 1 MiB/outcome revision, excluding bounded prompt snapshots; allow at most 1024 outcome revisions. Refuse further mutating invocations at that limit before publication, retaining all evidence and allowing read/replay of completed outcomes. One revision describes every card, with one of:

| Outcome | Meaning |
| --- | --- |
| `unattempted` | No publication began for this card; retained intent permits a later explicit same-submission resume. |
| `accepted` | This preassigned job was published with matching bytes and sidecar identity. It says nothing about launch or completion. |
| `refused` | Admission failed and absence of publication is proved; retain a concrete reason. |
| `unresolved` | Publication/reconciliation may have happened but cannot be proved complete or absent. Never resubmit blindly. |

`replay=true` is a response attribute for an already-recorded outcome, not a replacement lifecycle state. A new invocation resumes only `unattempted` cards or a `refused` card whose retained reason proves no publication and permits retry of unchanged intent. It must not retry `accepted` or `unresolved` cards. Stop at the first failure; later cards remain `unattempted`. Failure after an earlier accepted card is partial admission, never whole-batch rollback.

Record a durable per-card publishing phase before writes. Publish the complete matching sidecar before making the task discoverable, using an exclusive, atomic, no-replace task publication. Existing [List/ClaimNext](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/internal/swarm/pool.go#L220) can discover a task even without a sidecar; continuing task-first publication would therefore not meet this contract. Do not overwrite any existing job path. A collision or mismatched sidecar/task is unresolved and stops admission. The receipt lock does not serialize legacy writers or dispatchers; correctness must come from preassigned unique IDs, exclusive publication and reconciliation, not an assumption that the whole pool is locked.

After response loss or crash, reconcile by preassigned ID and exact intent/card correlation, matching prompt and sidecar data across pending/running/terminal locations and retained records. A complete matching job is accepted even if it already moved. Conflicting copies, a task-only orphan, unreadable evidence, or a concurrent move that prevents a consistent per-job read is unresolved. A sidecar-only staged job may be completed only when exact ownership and absence of task publication are proved. Missing paths alone do not prove a job never ran or was reclaimed. Repeated ambiguous reads do not authorize new IDs. Preserve existing retry/rework links; each actual descendant remains its own job/attempt, not another admission of the original card.

For the opt-in mode, success requires intent/snapshot/outcome file synchronization and containing-directory synchronization at the stated publication boundaries. Unsupported filesystems/platforms or failed synchronization refuse before dispatchable publication where possible, otherwise report unresolved. Do not claim power-loss durability from rename alone. The exact no-replace primitive and crash-recovery proof on supported platforms are implementation gates. Admission receipts must outlive job reclaim; no automatic retention/deletion policy is added here. Unknown/stale lock ownership refuses rather than guessing that its process died.

## Bounded response and collection

Return one summary: submission, batch, outcome revision/full hash, accepted/refused/unattempted/unresolved counts, replay flag and receipt artifact location. On ordinary partial failure return exit 1; invalid invocation or unavailable admission infrastructure returns exit 2; exit 0 requires all cards accepted/replayed. An error response must identify a persisted receipt when one exists; inability to publish an outcome is explicitly unresolved, never a success inferred from stdout. Cap stdout/stderr at 8 KiB with explicit truncation indication; details belong in the bounded receipt. All card mappings fit the bounded artifact; no new pagination protocol is needed.

No new collect verb is proposed. Read the receipt and compose existing status/sidecars, `triage --batch --no-state`, result and cost/usage readers. A collector must enumerate receipt jobs first, including pending and missing-report jobs that triage omits, and preserve full report hashes and per-job read outcomes. Triage still writes its report page; `--no-state` means no consumed-revision update, not no filesystem effects. Each report is read consistently, but the resulting collection is an observation interval, not a globally atomic pool snapshot. Changed or ambiguous reads are named as such.

Missing per-job usage is unavailable, not zero; an empty cost aggregate cannot fill job rows. Keep known zero distinct. Retain source identities, attempt/retry links and coverage gaps, and select native records or an overlapping legacy aggregate once, never both. The receipt references existing accounting evidence; it creates no new accounting observations. Heterogeneous profiles, native runtime receipts and all [PR264](https://github.com/mas-bandwidth/nova-tools/pull/264) admission/encoding/runtime gates remain separate and closed until independently cleared.

## Acceptance matrix for implementation review

| Case | Required observable result |
| --- | --- |
| Identical prompt bodies, distinct filenames, original label | Distinct card/job mappings; same raw hashes; label unchanged; admitted hashes exact. |
| Explicit template | Raw and admitted hashes/lengths differ as expected; bytes wrapped once, including recovery. |
| Input limits/nonregular entry/mutation | Bounded refusal before job publication; no silently dropped card. |
| Same submission replay / changed intent | Zero extra jobs and same IDs / refusal with prior receipt intact. |
| Concurrent same submission / different submissions | One owner or bounded busy; independent admissions cannot overwrite each other's jobs. |
| Crash before intent sync / after intent sync | No dispatchable job / recover same preassigned IDs from retained bytes. |
| Response lost after task publication, job moved or reclaimed | Recover accepted from sufficient matching evidence, otherwise unresolved; no duplicate admission. |
| Second-card write failure | First mapping survives; failed card refused only if nonpublication proved, otherwise unresolved; later cards unattempted. |
| Sidecar-only / task-only / conflicting orphan | Complete only proven-owned, unpublished staging; task-only/conflict unresolved; no overwrite. |
| Dispatcher observes publication | Never sees this mode's task without its complete intended sidecar. |
| Directory sync failure / stale lock / unsupported no-replace | Refused or unresolved per actual publication point; no durability claim or forced unlock. |
| Pending/no report/quarantine/changed report/unknown usage | Every receipt job remains named; current full hashes and gaps preserved; no fabricated usage or completion. |
| Retry/rework, overlapping usage sources | Separate existing job identities and one accounting contribution per scope. |
| Legacy batch, run, stop, profile gates | Existing unadorned behavior and cancellation semantics unchanged; no new dispatch/profile authority. |

## Measurement and remaining gates

This is design cost. The CLI-delta baseline is current batch+triage plus necessary status/result reads. Rowan's actual historical one-card route may be measured separately as an adoption comparison; label it separately rather than substituting it for the implementation baseline.

Before either operational comparison, freeze exact prompt and transport bytes (raw/admitted), source IDs/revisions, worker/model/settings, concurrency, output contract and equal accepted workload. Count parent and child preparation/dispatch/collection plus retries, repair, review and validation through the same acceptance gate on both sides; retain shared/unknown overhead. Preserve producer-defined cache/read/write/miss and reasoning semantics, price coverage and unavailable observations. Local tokens remain counted with zero declared inference API charge. Record local CLI count, model turns, wall time and artifact bytes separately. Design/implementation cost is not an operational saving, nor is reduced command count by itself.

Before build: exact encoding and pool-binding rules, supported synchronization/no-replace behavior, crash injection tests, and exact-revision root/friend review remain required. No production receipt test or token-saving result is claimed by this draft.
