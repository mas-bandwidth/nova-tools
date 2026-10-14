# Bootstrap challenge ownership

One Redis record and one challenge hash per name hold the truth. A challenge
adds no collection or cache: at 100,000 names, even a conservative 1 KiB per
record plus hash is about 100 MiB. The atomic verb reads two keys and writes
at most one hash, independent of the population. Libraries considered are the
existing Redis script API, miniredis pipe rig and HookBeat state machine.

`Bootstrap` models `Store.Challenge`, the client's initial and periodic
challenge attempt. Before a heartbeat owns the current epoch, an absent hash
permits one bootstrap. A standing hash belongs to its issuer, and a positive
`beat_at` preserves heartbeat ownership after its hash is answered or expires.
A fresh arm clears `beat_at` with the other epoch evidence. The result is
`created=false` when skipped, so the caller prints no invented challenge and
waits for the server's next delivered nonce. No sum can be turned back into a
nonce. An epoch or role mismatch remains `ErrNotHooked`.

The one-name instance abstracts nonce equality as a challenge kind: `open`
and `late` hold the server-delivered nonce, `bootstrap` the client nonce, and
`clobbered` means the standing hash no longer matches the server delivery.
The old-script witness takes `Init`, `Pass(s1)`, `Bootstrap(n1)` and violates
`BootstrapPreservesServerNonce`. This is a hand mapping to the exact source:

1. `beatLua` writes the server sum, epoch, pushes and `until`, and stamps the
   record's `beat_at`. Its returned nonce is delivered separately.
2. Old `challengeLua` validates the same role and epoch, then unconditionally
   `DEL`s that hash and writes the client's sum, epoch and pushes with a new
   TTL. It drops `until`.
3. `answerLua`, given the already delivered server nonce, compares its sum
   with the replacement and returns `-2` (`ErrWrongNonce`). The next heartbeat
   sees a standing hash and a positive previous `beat_at`, counting a miss.

In the fixed action, step 2 is disabled by heartbeat ownership (a skipped
atomic call is a stuttering step). `Answer(n1)` remains available until the
server deadline, removes the hash and resets misses. `BootstrapOnlyBeforeHeartbeat`
also excludes a bootstrap between that answer and the next server pass.
`WitnessBootstrap` confirms that initial bootstrap is still reachable.

The no-socket, fake-clock tests exercise the same ordered interleaving and
verify the entire hash and TTL are unchanged on skip. Open, late, answered
and hash-expired heartbeat states all preserve ownership; a new epoch permits
bootstrap again and a stale epoch refuses. The first client's bootstrap
nonce also survives a second starter. The open case answers the original
server nonce and observes zero misses on the next pass.

Measured model records and Linux test results are acceptance gates. A prepared
configuration or hand trace alone is not a completed TLC run; RUNS history is
preserved when the measured records are refreshed.

The existing mark-after-send negative temporal case uses a specialized
`MCHookBeatMarkAfterSend` instance constrained to the initial hook epoch.
It keeps two names, one server, Limit = 2, `LiveSpec` fairness and the same
`EveryTransitionToldAtLeastOnce` property. Two names becoming unhooked can
produce two changes: the first sent tell loses its separate mark, so its
once-key suppresses the later change. Rehook cycles are unnecessary for this
counterexample. This constraint narrows only a negative witness; positive
safety and temporal coverage remains unconstrained. The two unconstrained
110-second timeouts remain in the run history and external logs rather than
being reported as model passes.

The measured specialized trace sends version 0 after the second name is
unhooked, leaving its told mark and version unchanged. The next pass unhooks
the first name and replaces the pending tell with that new state under the
same version 0. `Tell` sees the already-sent key and suppresses it; a fair
Tick/Pass/Tell loop never hears the final state. No crash or rehook is needed
for this interleaving. The negative case reaches the declared temporal
violation in 2.669 seconds (8,333 generated / 1,568 distinct states).

All 24 HookBeat cases now have measured declared outcomes under the unchanged
110-second bound; the merged 255-case record reports no stale groups. The
bootstrap safety case explores 588 generated / 184 distinct states. Its
broken nonce-overwrite witness reaches the declared invariant violation at
depth 3 (Init, Pass, Bootstrap), and the initial-bootstrap witness is reached.
The Linux hook and nova-hook package tests pass. Restoring the old challenge
script's unconditional DEL makes both bootstrap regression tests fail.
