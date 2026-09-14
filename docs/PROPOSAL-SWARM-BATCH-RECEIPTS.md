# Optional batch admission receipts

**Status: proposal, not implemented or approved for build.** Maintainer review and independent reviewers' dispositions must name the exact revision before implementation. This extends the existing `batch` admission boundary; it introduces no dispatcher, model-call batching, accounting ledger, dependency scheduler or cancellation behavior. The current `batch` invocation without the new option remains compatible.

The companion [proposed batch admission implementation contract](PROPOSAL-SWARM-BATCH-ADMISSION-CONTRACT.md) specifies proposed v1 encoding, reservation transitions, platform and recovery limits, and required witnesses. It remains subject to exact-revision maintainer/all-friend review and the existing build gates; it does not establish implementation or durability approval.

## Problem and evidence

At public main `86785fd0accf997662c1cd5356f60acaf7a0821c`, [batch admission](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/cmd/nova-swarm/main.go#L365) reads all task files and then writes jobs sequentially. It does not preserve the input filename in the sidecar or return a filename/job mapping. [IDs](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/internal/swarm/pool.go#L136) contain second-resolution time and randomness: sorting them does not reconstruct card order. [Pool.Add](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/internal/swarm/pool.go#L182) writes the task before its sidecar; a later write failure does not roll back earlier jobs.

Workshop capability probes used source `1a14c7a52b80`, version `v0.15.3-0.20260914142654-1a14c7a52b80`, binary SHA256 `1fc75157070cf7b98151dc967aa2f419fa2ea3f67173a98f9d6ed27d36c9b83b`. The retained `20260914-batch-receipt-gap/` evidence includes exact input bytes, binary identity, argv, stdout/stderr, source copies and a passing artifact verifier; the setup and commands below make these witnesses independently reproducible. An independent reviewer also reproduced the first three at public main `86785fd0`, as recorded in [review 5670745380](https://github.com/mas-bandwidth/nova-tools/pull/326#issuecomment-5670745380). These are synthetic admission/parser probes, not production review or operational savings evidence:

Set `SWARM` to the absolute path of a binary built from either cited source revision, and `BASE` to a new disposable absolute directory. Record the chosen source revision and binary SHA256; a rebuild need not reproduce the recorded workshop binary hash. The partial-write helper uses POSIX `resource`/SIGXFSZ; the following Python setup uses only local synthetic files:

```sh
export SWARM=/absolute/path/to/nova-swarm
export BASE=/absolute/path/to/new-disposable-witness-directory
python3 - <<'PYSETUP'
import os
from pathlib import Path
b = Path(os.environ['BASE'])
b.mkdir(parents=True, exist_ok=False)
for name in ('duplicate-tasks', 'duplicate-pool', 'partial-tasks', 'partial-pool'):
    (b / name).mkdir()
for name in ('card-alpha.txt', 'card-beta.txt'):
    (b / 'duplicate-tasks' / name).write_bytes(
        b'Synthetic identity probe only. Do not execute.\n')
(b / 'partial-tasks' / '01-small.txt').write_bytes(
    b'First synthetic card; do not execute.\n')
(b / 'partial-tasks' / '02-large.txt').write_bytes(
    b'Second synthetic card; do not execute.\n' + b'x' * 16384)
(b / 'limit-exec.py').write_text(
    'import os,resource,signal,sys\n'
    'resource.setrlimit(resource.RLIMIT_FSIZE,(4096,4096))\n'
    'signal.signal(signal.SIGXFSZ,signal.SIG_IGN)\n'
    'os.execv(sys.argv[1],sys.argv[1:])\n')
PYSETUP
```

Run rows 1–4 in order. Shell quotes delimit argv, not task contents. Save stdout/stderr and exit status separately for each row; `BATCH_ID` below must be exported as the literal ID from row 1's `BATCH OK`, not a guessed value.

| Witness | Exact command / argv after the setup | Observed result |
| --- | --- | --- |
| 1. Two named files with identical LF-terminated bytes; SHA256 `878f6d603d1a46843a7251593c344c22d5802d137cccb34bd6fe261e6e4e3b8b`; common label | `"$SWARM" batch --pool "$BASE/duplicate-pool" --tasks "$BASE/duplicate-tasks" --files 1 --tokens 100 --label identity-probe` | Two unique jobs, same prompt hash/label/batch; either named card matches both jobs in `pending/*.json` and `pending/*.task`. |
| 2. Repeat identical admission | `"$SWARM" batch --pool "$BASE/duplicate-pool" --tasks "$BASE/duplicate-tasks" --files 1 --tokens 100 --label identity-probe` | A new batch and two more jobs; pending count 2 becomes 4. |
| 3. Sequential partial write; child process alone has `RLIMIT_FSIZE=4096`, SIGXFSZ ignored | `python3 "$BASE/limit-exec.py" "$SWARM" batch --pool "$BASE/partial-pool" --tasks "$BASE/partial-tasks" --files 1 --tokens 100 --label partial-probe` | First task and sidecar survive; second task write returns `file too large`, exit 2. The helper only installs the child limit and execs the displayed nova-swarm argv. |
| 4. Every admitted job remains pending | `"$SWARM" triage --pool "$BASE/duplicate-pool" --batch "$BATCH_ID" --no-state --max 20`; `"$SWARM" status --pool "$BASE/duplicate-pool" --max 20` | Triage exits 1 because its selection excludes pending. Status/sidecars retain their identities. |
| 5. Hand-built retained report at two byte revisions, one absent report, empty usage directory; fixture setup below | `"$SWARM" triage --pool "$BASE/synthetic-results-pool" --batch "$BATCH_ID" --no-state --max 1`; `"$SWARM" result --pool "$BASE/synthetic-results-pool" --id "$REPORT_JOB_ID"`; `"$SWARM" cost --pool "$BASE/synthetic-results-pool" --max 20` | Triage names the changed hash prefix and counts the missing report. Cost says `tasks=0`; aggregate zeros are not observed per-job usage. No worker ran. |

For row 5, set `BATCH_ID` to row 1's ID, then create a separate parser fixture. Its `done` records are fabricated reader inputs, not completed executions:

```sh
python3 - <<'PYFIXTURE'
import json, os, shutil
from pathlib import Path
b = Path(os.environ['BASE']); p = b / 'synthetic-results-pool'
(p / 'done').mkdir(parents=True, exist_ok=False)
rows = sorted((json.loads(f.read_text())
               for f in (b / 'duplicate-pool' / 'pending').glob('*.json')),
              key=lambda row: row['id'])
rows = [row for row in rows if row['batch'] == os.environ['BATCH_ID']]
assert len(rows) == 2
for row in rows:
    ident = row['id']
    (p / 'done' / (ident + '.json')).write_text(json.dumps(row) + '\n')
    shutil.copyfile(b / 'duplicate-pool' / 'pending' / (ident + '.task'),
                    p / 'done' / (ident + '.task'))
report = p / 'reports' / rows[0]['id'] / 'RESULT.md'
report.parent.mkdir(parents=True)
report.write_bytes(b'# Synthetic fixture report\n\n## Head\nfindings: 0\n'
                   b'Synthetic parser fixture; no job was executed.\n')
(b / 'report-job-id.txt').write_text(rows[0]['id'] + '\n')
print('report-job-id=' + rows[0]['id'])
print('report-path=' + str(report))
PYFIXTURE
export REPORT_JOB_ID="$(cat "$BASE/report-job-id.txt")"
```

Run row 5 once, then change only `Synthetic fixture report` to `Synthetic revised fixture report` in the printed report path and rerun the same argv. Preserve both raw revisions and outputs. These commands admit synthetic cards and inspect files only; none uses `run`, `supervise` or a provider.

A wrapper using distinct labels and durable correlation can solve parts of this problem. It is not impossible. The native choice preserves the caller's label and places recovery at the boundary that assigns IDs and publishes task/sidecar files, including orphan-file cases.

## Finite opt-in interface and identity

Propose an admission identity option and one explicit retained-intent recovery form:

```
nova-swarm batch --pool <dir> --tasks <dir> --files <n> --tokens <n>|unmetered \
  [existing batch options] --submission <id>
nova-swarm batch --pool <dir> --submission <id> --resume
```

`--submission` is a caller-chosen stable identifier, scoped to this pool: 1–128 ASCII letters, digits, period, underscore or hyphen. It is an idempotency identity, not authority. Omission preserves legacy behavior. No per-card profile/model fields are added.

For this mode, take one bounded input snapshot before any job publication: 1–256 regular, non-symlink task files directly under `--tasks`, sorted by exact UTF-8 filename bytes; each filename is a distinct card key, at most 255 bytes. Reject other entry types and invalid names rather than silently changing membership. Raw and admitted prompt bytes are each limited to 1 MiB/card and 16 MiB/submission. Preserve bytes exactly; apply an explicitly selected existing template once, and hash its resulting admitted bytes separately. No prompt marker is inserted. Directory membership or file changes detected during capture refuse before admission; the receipt describes the captured snapshot, not a promise that source files remain unchanged later.

The immutable intent records: schema/version, submission ID, canonical pool binding, normalized source-directory locator, the ordered card keys/source filenames, raw and admitted SHA256/byte counts, exact common admission parameters (including original label), template identity when used, one preassigned batch ID and distinct preassigned job IDs. Bind retained admitted bytes to their hashes so recovery does not reread mutable task sources. Bind the canonical pool path and stable filesystem directory identity in `<pool>/admissions/pool.json`, created exclusively and synchronized on the first opt-in invocation. Refuse a changed path/directory identity or unavailable identity; no copy/move migration is introduced. The supported-platform representation of that identity must be reviewed before build.

Store metadata and admitted byte snapshots under `<pool>/admissions/<sha256-of-submission-id>/`, a coordinator-owned admission namespace outside worker-writable job/slot paths. Its immutable `intent.json` and `prompts/<job-id>.task` snapshots precede numbered `outcomes/<revision>.json` files. Preserve an intent hash/card-key reference in each new job's sidecar without overloading its label. The intent is persisted **before any task, sidecar or dispatchable pool write**; admission-metadata writes are the necessary exception to “before pool writes.” The metadata is an admission receipt, not a spend ledger. Hash the submission ID for its storage name; retain the original inside the intent. The exact metadata encoding is a required reviewed implementation contract, not invented compatibility with PR264 receipts.

## Publication, replay and recovery

Serialize same-submission admission with an exclusive lock; a competing invocation receives a bounded busy response without writing jobs. Persist the complete immutable intent and byte snapshots before assigning any card a publishing phase. The ordinary form, without `--resume`, always captures and compares the current source files and all admission parameters before replay or continuation. Identical captured content and parameters use the existing IDs. Changed content, label, template, budget or source/card mapping refuses without mutation; a missing or unreadable task directory also refuses, even if all cards were previously accepted. It never falls back implicitly to retained bytes. The same bytes under a different submission ID deliberately describe a different admission.

The explicit `--resume` form requires an existing complete intent and verified retained admitted-byte snapshots. Only `--pool`, `--submission` and `--resume` are allowed: reject `--tasks`, budgets, label, template and all other admission overrides. It neither reads nor compares the original task directory, whether unchanged, changed, gone or unreadable. It resumes only the originally recorded intent and IDs using retained admitted bytes without wrapping again; it never admits changed source content. Missing/corrupt intent or snapshots refuse. This explicit choice permits source-independent reconciliation/continuation; it does not relax pool binding, publication or outcome eligibility. A completed intent returns replay without new outcome writes.

Use immutable, numbered outcome revisions with atomic publication and no overwrite of intent or prior revisions. Bound admission metadata to 1 MiB/intent and 1 MiB/outcome revision, excluding bounded prompt snapshots; allow at most 1024 outcome revision slots. Before any job/sidecar write, preflight and durably reserve enough available slots for the invocation's whole worst-case transition plan under the submission lock. For `R` cards requiring reconciliation and `P` eligible cards that may publish, reserve `R + 2P + 2` slots: one reservation revision, at most one reconciliation outcome per `R`, one publishing-phase and one terminal outcome per `P`, and one final summary/release revision. A card may count in both `R` and `P`; no card is retried twice in one invocation. All outcome changes, including failure recording, use these reserved slots; no unbudgeted diagnostic revisions are allowed. If capacity is insufficient, refuse before any job/sidecar write and leave prior outcomes intact. Completed read/replay needs zero slots.

The reservation records its slot range and transition budget. Write revisions in increasing slot order. A normal final summary uses the next available reserved slot and releases only the never-written trailing suffix; written revisions are immutable. After a crash, reserved slots stay unavailable to other invocations until the prior owner's absence and written revisions are reconciled under the lock. Use that reservation's remaining terminal-outcome/reconciliation slot and final-summary slot to record the interrupted card and close the reservation before considering a new publication plan; do not spend its unused publication allowance on fresh jobs. If safe ownership or remaining allowance cannot be proved, refuse without new job writes. New invocations then preflight afresh. Thus hitting the global limit cannot be a reason to publish a task without its reserved recording capacity. Storage/synchronization failures remain separately unresolved under the failure contract below.

Intent publication, prompt-snapshot publication and pool-binding initialization each occur at most once; they are not outcome revisions. Each planned card publication has at most one sidecar publication and one task publication, both preceded by its reserved publishing-phase revision. Temporary-file writes/cleanup and lock operations create no outcome revisions. The implementation must not add another outcome-writing boundary without updating this budget and its acceptance witnesses. One outcome revision describes every card, with one of:

| Outcome | Meaning |
| --- | --- |
| `unattempted` | No publication began for this card; retained intent permits a later explicit same-submission resume. |
| `accepted` | This preassigned job was published with matching bytes and sidecar identity. It says nothing about launch or completion. |
| `refused` | Admission failed and absence of publication is proved; retain a concrete reason. |
| `unresolved` | Publication/reconciliation may have happened but cannot be proved complete or absent. Never resubmit blindly. |

`replay=true` is a response attribute for an already-recorded outcome, not a replacement lifecycle state. A new invocation resumes only `unattempted` cards or a `refused` card whose retained reason proves no publication and permits retry of unchanged intent. It must not retry `accepted` or `unresolved` cards. Stop at the first failure; later cards remain `unattempted`. Failure after an earlier accepted card is partial admission, never whole-batch rollback.

Record a durable per-card publishing phase before writes. Publish the complete matching sidecar before making the task discoverable, using an exclusive, atomic, no-replace task publication. Existing [List/ClaimNext](https://github.com/mas-bandwidth/nova-tools/blob/86785fd0accf997662c1cd5356f60acaf7a0821c/internal/swarm/pool.go#L223) can discover a task even without a sidecar; continuing task-first publication would therefore not meet this contract. Do not overwrite any existing job path. A collision or mismatched sidecar/task is unresolved and stops admission. The receipt lock does not serialize legacy writers or dispatchers; correctness must come from preassigned unique IDs, exclusive publication and reconciliation, not an assumption that the whole pool is locked.

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
| Ordinary same-submission invocation with identical/changed/gone/unreadable source | Identical content replays/continues with the same IDs; every other case refuses without falling back to snapshots. |
| Explicit `--resume` with changed/gone/unreadable source | Uses verified retained bytes and original parameters without touching the source; overrides or absent/corrupt snapshots refuse. |
| Revision capacity below / exactly at worst-case budget | Below: no job/sidecar writes; exact: every reconciliation, publishing phase, terminal/failure outcome and final summary fits. |
| Crash after dispatchable publication near revision limit | Reserved recording/reconciliation capacity survives; no new publication until reservation recovery and fresh preflight succeed. |
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

This is design cost. The CLI-delta baseline is current batch+triage plus necessary status/result reads. A caller's historical one-card route may be measured separately as an adoption comparison; label it separately rather than substituting it for the implementation baseline.

The comparison remains a hypothesis until measured: predeclare paired runs of the same frozen cards with independent shadow reviews, or predeclare matched cards and label that comparison observational; record the chosen design before collecting outcomes.

Before either operational comparison, freeze exact prompt and transport bytes (raw/admitted), source IDs/revisions, worker/model/settings, concurrency, output contract and equal accepted workload. Count parent and child preparation/dispatch/collection plus retries, repair, review and validation through the same acceptance gate on both sides; retain shared/unknown overhead. Preserve producer-defined cache/read/write/miss and reasoning semantics, price coverage and unavailable observations. Local tokens remain counted with zero declared inference API charge. Record local CLI count, model turns, wall time and artifact bytes separately. Design/implementation cost is not an operational saving, nor is reduced command count by itself.

Before build: exact encoding and pool-binding rules, supported synchronization/no-replace behavior, crash injection tests, and exact-revision maintainer and independent-reviewer dispositions remain required. No production receipt test or token-saving result is claimed by this draft.
