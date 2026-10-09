# Native debt after an external card end

`NativeOwnershipDebt.tla` starts with one already-admitted native card. It
separates server completion or reassignment from this daemon's local claim,
Started receipt, physical native group and lane capacity. A returned Go turn
does not prove that descendants are quiet. An auxiliary group with unreadable
identity retains debt even after its physical exit until a positive quiet
observation is obtained.

The desired contract retains the claim and capacity after an external end
until both native groups have verified quiet proof and removal of Started is
saved. A failed removal save retains the claim. The corrected contract is a
target for the runtime repair; it is not evidence that the baseline implements
it. The original 23 `NativeCardStartBoundary` and 26 `NativeLaunchHeldBridge`
cases are unchanged.

| Action | Source boundary and implementation obligation |
|---|---|
| `ExternalDone`, `ExternalReassign` | The server changes the card while this daemon may still own a running primary or inspection. |
| `NoticeExternalEnd` | `leftRow` and lane-mark observation call `endOtherLane`, set `ln.ended` and request cancellation. A server row or mark is not exit proof. |
| `NativeReturn` | `laneDone` receives a turn result, including an error that leaves native descendants unverified. |
| `PrimaryExit`, `ObservePrimaryQuiet` | Physical exit and positive RUN/birth/anchor group proof are separate facts; the model treats the verifier as an oracle. |
| `LoseAuxIdentity`, `AuxExit`, `ObserveAuxQuiet` | Auxiliary identity loss retains debt. Exit alone cannot clear unknown identity; a recovered positive proof is required. |
| `DropClaim`, `RemovalSaveFails` | `setDown` removes Started, updates GivenUp and frees the card/lane. The target requires quiet proof and a successful durable save first. |
| `ReuseCapacity` | A subsequent lane refill can consume the freed slot; old native debt must already be settled. |

At source `c006cbc8289a4cbd76beeb385158818d05d00f21`, also present in the
isolated `fb76309d17ce3a9186ba7ecabce65abcac3e51de` candidate,
`laneDone`'s `ln.ended` branch calls `setDown` before the ordinary path's
`stopRunQuiescent` check. `setDown` removes Started and clears the card even
when `saveLanes` only logs a save error. The two diagnostic cases expose those
specific paths: `BrokenEarlyDrop` violates quiet before capacity release;
`BrokenRemovalSave` violates successful durable removal before local drop.
These are executable-model witnesses of the baseline abstraction, not a
claim that TLC directly executed the Go implementation. The separately owned
repair first frozen at `2052cd0dc0fccf8f679a4fb53b01925b46c1a323` maps quiet
proof to `endedRunQuiescent`, restores Started and GivenUp when `setDown`'s
save fails, and retries retained idle-ended debt in `retryPendingLanes`.
Executable repair gates and source trace alignment remain separate evidence.

The ten registered cases include one normal safety case, both diagnostics,
and seven reachability witnesses. They cover external Done with a live
primary, reassignment with an unknown live auxiliary, a returned turn with
live debt, physical auxiliary exit without proof, release after proof,
failed removal save, and a later successful save and refill. Exact model source
`e7cbbed2a735037f631c3ae8df5179a1d6abe149` ran on serialized Linux: all ten
declared outcomes passed. The normal safety case explored 873 generated and
356 distinct states. Both baseline diagnostics and all seven reachability
cases reached their specifically named expected invariant violations.
`RUNS.tsv` appends ten exact receipts and preserves the previous 735 rows.

The source bridge plan is a real native primary with a retained descendant
and an auxiliary receipt. Change the held/server observation or lane mark,
deliver the unverified turn result, then observe `ln.ended`, Started, local
card and refill capacity before and after group proof. Inject a removal-save
failure after proof and require retention until a successful retry. Project
these source events onto the named model actions and compare the observed
claim/capacity sequence, rather than testing only the desired guards.

This finite instance contains one original card, one auxiliary and one slot
reuse. It does not prove kernel identity verification, filesystem power-loss
durability, asynchronous cross-host revocation, foreign-owner transfer,
crash recovery, arbitrary lanes, fairness or eventual cleanup. No completion
or no-starvation claim is made.
