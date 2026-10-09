# Native card start and primary group identity

`NativeCardStartBoundary.tla` is a bounded ordering contract for one one-shot
card on a private Sprint server and one blocked native process group. It models
the r4 failure in which a usage export replaced the primary card's RUN/anchor
identity, and the separate r4 failure in which a remote card began native work
while the server still held it Ready. The model is intentionally new: the
existing `StopGroupAnchor` and `CompletionBoundary` modules each have one group
and cannot prove that an auxiliary export preserves a different primary group.

The source boundary to refine is `internal/friend/adapter.go`'s existing gated
shell: launch blocked, durably write its primary PID/birth RUN, start its verified
anchor, obtain exact server StartAck, then release the shell to execute the
provider. StartAck is a server Ready-to-Working operation with `StartsWork=true`,
not a beat or delayed poll. The server must validate the canonical card, job, generation, epoch and
actor in its pure Plan before first commit. A fresh retry also checks the exact
current Ready or Working card; a valid unstarted Working card is stamped Started.
A same-operation replay returns
only its historical receipt, even after the card finishes; changed arguments under that operation ID conflict. The daemon
never uses the historical reply to open the shell. After an ambiguous answer,
it retries the same exact tuple with a fresh operation ID, forcing a current
Plan. A committed first start appears as the same Working card with Started;
an uncommitted first attempt can still find Ready and commit on the fresh op. A card finished, epoch-cleared,
stopped or redealt before this fresh retry refuses. A Working card not
yet marked Started can legitimately be stamped by the fresh Plan; no synthetic
Working-with-cleared-Started revocation is assumed. An unknown answer never
releases the shell. A failed anchor precedes StartAck and cannot
mark the card Working. A failed gate release after a committed StartAck leaves
Working with no native work; failed finish or STOP return must settle it.
An ordinary external take can also put the card Working without this native
ACK. Its subsequent external finish is not a settlement by this native run;
the model requires an exact committed start before its own failed finish or
STOP return, and assigns native stop debt only to that owned start.

`OpenCodePriced`'s baseline or in-turn `export` is an auxiliary native
command with its own RUN,
anchor and process-group identity. It must never call the primary started
callback or overwrite the primary receipts. Quiescence of the export group is
not evidence that the original provider group is quiet. The model separates
physical primary group state from a verified quiet observation and makes
capacity release require both physical primary quiet and verified auxiliary
quiet (or no auxiliary run), plus a completed same-card server settlement.
Unreadable or still-live auxiliary identity retains settlement and capacity debt. Its broken-overwrite case shows the false observation possible
when an auxiliary group replaces the primary receipt.

A PAUSE before StartAck prevents a new Ready-to-Working start; a PAUSE after
the first committed ACK permits its directly delivered response and current
work. An ambiguous reply retried under PAUSE with a fresh operation fails
closed and leaves Working to settle as a failed gated launch. STOP before StartAck closes the
blocked gate and leaves the card Ready. STOP after StartAck blocks native launch
or cancels running work, then owes a quiet-group StopReturn for the Working card.
The fast-turn witness permits provider completion between ticks because server
Working already precedes native execution. A changed card, job, generation,
epoch or actor refuses the original start.


| Model action | Source boundary / executable obligation |
|---|---|
| `LaunchBlocked`, `StorePrimary`, `StartAnchor` | `RealExec` starts the pipe-blocked shell, writes primary RUN PID/birth, then obtains the sidecar anchor before calling StartAck. |
| `CommitStartAck`, `DeliverFirstAck` | `FriendNativeStartStep` commits exact Ready-to-Working with the current Started field; the native callback receives its direct answer. |
| `LoseFirstReply`, `FreshRetryAck`, `FreshRetryRefuse` | The daemon retries ambiguous delivery under a new caller operation ID with the same exact tuple; a current Ready or Working card can be stamped Started, while PAUSE, STOP, clear, finish and redeal refuse. |
| `ReplayStartAck` | Same-operation CLI replay is a historical receipt only; the daemon never uses it to release the blocked shell. |
| `LaunchAux`, `LoseAuxIdentity`, `VerifyAuxQuiet` | Baseline or in-turn export uses separate RUN/anchor; unknown auxiliary cleanup retains lane debt until verified quiet. |
| `ReleaseGate`, `Finish`, `StopReturn`, `ReleaseCapacity` | Native work starts after accepted current ACK; settlement and capacity wait for original and auxiliary groups to be quiet. |

The normal case checks primary and auxiliary receipt isolation, ACK-before-work,
fresh current authority for ambiguous retries, exact binding, anchor-before-gate,
no early capacity release, and truthful finish/STOP return. Diagnostic mutations
break receipt isolation, release before ACK, exact tuple validation, auxiliary
quiescence, historical-replay isolation, current Working/Started admission, and same-operation
argument binding.
Reachability cases cover fast completion, lost committed and uncommitted
answers plus fresh-op retry,
committed ACK with no native work, stale redeal/epoch-clear refusal, STOP debt
settlement, replay
after another finish, recovery of a crash before native release, and PAUSE
before/after ACK. `CASES.tsv` declares these outcomes; `RUNS.tsv` must contain measured Linux TLC receipts before the
model can be called green.

This model does not implement or prove Redis operation semantics, kernel PID
birth/PGID validation, anchor FIFO signaling, cross-host native work-root accessibility,
filesystem durability, provider output, or a time bound. Those require the
server source tests and real native group/fast-turn regressions. It models one
card, one original operation plus one fresh retry ID, one original group and
one auxiliary export (either before the provider or during its turn); multiple lanes and arbitrary retries/restarts remain outside this finite
instance. Later server revocation after a fresh ACK is asynchronous: the model
does not claim instantaneous cross-host STOP or a lease. That interval requires
the source cancellation and stop-return path, plus a real native regression.
