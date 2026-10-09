# Generic harness setup lifecycle

`FriendSetupLifecycle.tla` models the generic runner in
`internal/friend/setup.go` and `setup_exec.go`, over the existing native launch
and process-group cleanup contract in `adapter.go`. The model is bounded safety
and reachability evidence; it does not claim refinement of arbitrary executions
or unconditional progress through filesystem errors and unknown live groups.

The truth lives in the private profile root's `OWNER`, `LOCK` and `RUN` files and
in the kernel's lock and native group state. One runner holds one RUN record;
its fixed metadata is a few hundred bytes, not a table or growing log. At one
hundred times the number of independent profiles, each root still owns one lock
and one RUN record. The pattern is one locked profile, ordered native steps,
and one receipt before each launch boundary.

`Plan` changes no resource. `Apply` takes the profile lock without waiting,
`Inspect` checks the selection bound by OWNER and the preceding RUN, and
`Prepare` writes a fresh run id and command hash. `SpawnGated` creates a blocked
child. `BindReceipt` durably records its PID and birth identity, and `Launch`
releases the native gate. `LeaderExit`, `CleanupUnknown`, `GroupDead` and
`CleanupVerified` distinguish the leader's exit, bounded cleanup failure and
verified group quiescence. `SaveEnded` and `Validate` precede the next ordered
step. `Publish` atomically writes the public PROFILE selection and confirms its exact
readback only after every native step validates; a failed save or readback
refuses completion. `Finish` releases the lock. `AbortBeforeLaunch`, `Crash` and `Replan` cover
failed prelaunch persistence and interrupted invocations. Closing a gated
child's inherited launch pipe aborts that child; an already launched group can
survive its caller, and every retry is refused until a fresh observation proves
it has no runnable member. A dead old receipt permits reinitialization from the
first step; it never certifies a step of a new invocation.

The safety case runs with two competing callers, two native steps, at most
three distinct run identities and two applies per caller. Both callers choose
the same profile owner so lock contention and retries exercise a shared root.
The owner-overwrite defect uses two distinct selections. The interrupted-retry
witness uses one caller and permits three applies, so that caller can crash
after launch, be refused
while its group lives, and replan after verified exit. Run identities abstract
unique nonces and unique PID/birth pairs; command hashes use a per-step `Commands` mapping and may repeat both across
steps and across retries. No credential value, native
stdout, provider turn, daemon activation or presence claim is a model input. PROFILE content is the public versioned
selection and credential names; installation/run profile validation is a
separate local layer outside this model.

| Check | Rule |
|---|---|
| `PlanReadOnly` | Planning writes no resource or starts a child. |
| `ProfileMutualExclusion` | At most one caller is inside the profile lock. |
| `OwnerNeverChanges` | Existing OWNER never moves to another selection. |
| `LaunchAfterDurableBirth` | Every launched group has its durable PID/birth receipt. |
| `ReceiptIdentity` | The bound receipt retains the run's command, PID and birth identity. |
| `AdvanceAfterGroupExit` | Native validation and advancement follow verified group quiescence. |
| `FailureStopsSequence` | Native errors, cancellation, output overflow, save errors and validation failure stop advancement. |
| `RetryAfterGroupExit` | A live, draining or unknown group holds retries; at most one runnable group exists. |
| `PublishAfterNative` | A completed PROFILE publication follows every native validation. |
| `DoneHasEveryStep` | A successful invocation validates every required native step. |

The three reversed reachability witnesses deliberately fail when a full apply,
a cleanup followed by validation, and interrupted/live-refused/dead-adopted
retry respectively occur. Eight defect configurations deliberately break
planning purity, locking, owner preservation, the durable launch gate, group
exit before advancement, failure handling, live-group retry refusal and premature PROFILE publication. Each
case's expected property is named in `CASES.tsv`; any other failure is red.

Filesystem path/private-mode checks, actual atomic-file durability, process
identity lookup, anchor pinning, nonce randomness, environment allowlisting,
output byte counts and the two-minute execution bound are concrete code/test
obligations. The group state abstracts the process-group module's proof rather
than reimplementing its signals, membership and PID recycling. The plan's
native commands must perform only local initialization or inspection; the model
cannot prove the behavior of an arbitrary executable. There is no fairness
assumption that an unknown group eventually becomes provably dead.

Run the `friendsetup` group through `tools/tlacheck` on a Linux bench, with the
pinned jar, bounded timeout, private cache/output directories and the shared
bench gate. The model is complete only after current hash-bound TLC records
and an actual native receipt trace check exist. A unit-test receipt or a final
RUN snapshot alone is not native trace evidence.

## Measured evidence, 2026-10-08

The twelve `friendsetup` cases pass at model source `c385943c3`; their
hash-bound rows are in `RUNS.tsv`. TLC ran on Linux with the pinned
`936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`
jar and Java 21.0.12.1, under the shared bench lock. The complete group took
about 36 seconds within its 43-second bound.

| Case | Distinct states | Expected result |
|---|---:|---|
| Safety control | 1,371,424 | No invariant violation |
| Full apply witness | 96,986 | `WitnessApplied` reached |
| Cleanup witness | 1,181 | `WitnessCleanup` reached |
| Interrupted retry witness | 31,804 | `WitnessInterruptedRetry` reached |
| Eight defects | 2–1,213 each | Exactly the named safety invariant violated |

Native source `d5580b4f723b4134ad24707b77fb7c96b5a8fdba` supplies the actual
`TestNativeSetupReceiptTraceAndAllowedExitKeepStreamsSeparate` trace. Its two
fixture commands perform local inspection only. Eleven observations contain
six successful durable RUN writes, two native returns observing their groups
not runnable, two validations, and final PROFILE publication after public
readback. The extracted JSONL has SHA-256
`15b8b5a8f29a4fff564886a00e47168130559e89800c43e70bb93e81b1d64e7f`.

The RUN-change projection reaches `CompleteNativeTraceWitness` after 163
distinct states (expected invariant failure, TLC exit 12). Removing the first
durable PID/birth binding makes the complete witness unreachable: 11 distinct
states, no invariant failure, TLC exit 0. This checks compatibility of the
actual receipt changes with a complete modeled apply and detects that missing
binding; it does not independently observe the model's lock, gate release or
cleanup internals. Group quiescence, validation and publication chronology are
separately checked against the native observations.

Logs, extracted receipts, provenance, projection generator and standalone
projection configurations are retained in
`release-evidence-v1.2/recovery-20261008/friendsetup-model/`, with projection
artifacts under `native-d558/`. These standalone evidence configurations are
outside the registered `tla/` suite. Earlier timeout and syntax-error logs are
retained under `run-01/`; only `run-fixed/` produced the current twelve ledger
rows. This evidence certifies neither installation/dev ancestry nor login,
provider access, daemon activation or friend presence.
