------------------------------ MODULE Hook ------------------------------
\* The hook record of one identity (hook/hook.go, hook/redis.go,
\* hook/holder.go; docs/SPEC-HOOK.md). Every tool that sends, reads or takes
\* work as an identity acts only when the gate says yes: the record is hooked,
\* and every push of its role has a live lease and a proof, both written under
\* the record's epoch. The truth lives in Redis alone: a lease is a key with a
\* TTL its holder renews, so a holder that dies loses its push on its own.
\*
\* The state:
\*   state     the record: "none" (never hooked), "hooked" or "unhooked"
\*   epoch     the record's epoch; arm (as a new role, or after an unhook) and
\*             unhook move it on, a join keeps it
\*   live[p]   the epoch the lease of push p was written under, 0 for none
\*   proof[p]  the epoch the proof of push p was written under, 0 for none
\*   chal      the epoch the outstanding challenge was set under, 0 for none
\*   seen      the session read the outstanding challenge off its hook's stream
\*   running[h] holder h runs (a daemon, a session's hook, nova-sprint hook)
\*   joined[h] the epoch holder h writes under
\*   badHeld, badRead  ghosts: a proof written for a push with no lease of the
\*             epoch, and a challenge answered by a session that never read it
\*
\* The roles' pushes are Pushes; HolderOf names the one holder of each push.
\* SessionPushes are the pushes a session's hook holds and proves only by the
\* challenge; the rest a daemon holder proves by its own round trip.
\* Time is not a variable: a running holder renews inside its lease, so its
\* lease never lapses; a stopped holder's lease lapses (Expire) at any time,
\* and under fairness surely. A proof lapses at any time (its TTL).
\*
\* What it leaves out: the role (one role per instance; arming as another
\* role is an arm with a new epoch, the same as after an unhook), the window
\* between a holder's death and its lease's expiry (bounded by LeaseTTL in the
\* code), and a second challenge replacing the first.
\*
\* Reversed witnesses (Broken):
\*   "staleepoch"  a renew does not check the holder's epoch: a holder of an
\*                 epoch since unhooked makes a push live again (LiveIsJoined)
\*   "noepoch"     unhook neither moves the epoch on nor clears the keys: an
\*                 unhooked record keeps a live push (UnhookStands)
\*   "nolease"     a daemon proves a push with no live lease (ProofOnlyHeld)
\*   "nostream"    a challenge is answered by a session that never read it
\*                 (ProofMeansRead)
\*   "noexpire"    a lease has no TTL: a dead holder keeps the gate open for
\*                 ever (DeadHolderLosesGate)
EXTENDS Naturals, FiniteSets

CONSTANTS Pushes, Holders, HolderOf, SessionPushes, MaxEpoch, Broken

VARIABLES state, epoch, live, proof, chal, seen, running, joined, badHeld, badRead

vars == <<state, epoch, live, proof, chal, seen, running, joined, badHeld, badRead>>

Epochs == 0..MaxEpoch
Held(h) == {p \in Pushes : HolderOf[p] = h}

TypeOK ==
    /\ state \in {"none", "hooked", "unhooked"}
    /\ epoch \in Epochs
    /\ live \in [Pushes -> Epochs]
    /\ proof \in [Pushes -> Epochs]
    /\ chal \in Epochs
    /\ seen \in BOOLEAN
    /\ running \in [Holders -> BOOLEAN]
    /\ joined \in [Holders -> Epochs]
    /\ badHeld \in BOOLEAN
    /\ badRead \in BOOLEAN

Init ==
    /\ state = "none"
    /\ epoch = 0
    /\ live = [p \in Pushes |-> 0]
    /\ proof = [p \in Pushes |-> 0]
    /\ chal = 0
    /\ seen = FALSE
    /\ running = [h \in Holders |-> FALSE]
    /\ joined = [h \in Holders |-> 0]
    /\ badHeld = FALSE
    /\ badRead = FALSE

\* The gate (hook.Status.Gate): hooked, and every push live and proven under
\* the record's epoch.
Gate ==
    /\ state = "hooked"
    /\ \A p \in Pushes : live[p] = epoch /\ proof[p] = epoch

\* Arm and Join (Store.Arm, Holder.Start): holder h starts and hooks in. A
\* record hooked already is joined at its epoch; otherwise the epoch moves on
\* and every lease, proof and challenge is cleared. Either way h renews its
\* pushes at once.
Arm(h) ==
    /\ ~running[h]
    /\ IF state = "hooked"
          THEN /\ joined' = [joined EXCEPT ![h] = epoch]
               /\ live' = [p \in Pushes |-> IF HolderOf[p] = h THEN epoch ELSE live[p]]
               /\ UNCHANGED <<epoch, state, proof, chal, seen>>
          ELSE /\ epoch < MaxEpoch
               /\ epoch' = epoch + 1
               /\ state' = "hooked"
               /\ joined' = [joined EXCEPT ![h] = epoch + 1]
               /\ live' = [p \in Pushes |-> IF HolderOf[p] = h THEN epoch + 1 ELSE 0]
               /\ proof' = [p \in Pushes |-> 0]
               /\ chal' = 0
               /\ seen' = FALSE
    /\ running' = [running EXCEPT ![h] = TRUE]
    /\ UNCHANGED <<badHeld, badRead>>

\* Renew (Store.Renew via Holder.Beat): a running holder renews its leases
\* under the epoch it joined, refused unless that is the record's.
Renew(h) ==
    /\ running[h]
    /\ state = "hooked"
    /\ joined[h] = epoch \/ "staleepoch" \in Broken
    /\ live' = [p \in Pushes |-> IF HolderOf[p] = h THEN epoch ELSE live[p]]
    /\ UNCHANGED <<state, epoch, proof, chal, seen, running, joined, badHeld, badRead>>

\* Rejoin (Holder.Beat): a holder whose renew was refused rejoins a record
\* hooked again since, never an unhooked one.
Rejoin(h) ==
    /\ running[h]
    /\ state = "hooked"
    /\ joined[h] # epoch
    /\ joined' = [joined EXCEPT ![h] = epoch]
    /\ UNCHANGED <<state, epoch, live, proof, chal, seen, running, badHeld, badRead>>

\* Stop: a holder exits or dies; its leases stay until they lapse.
Stop(h) ==
    /\ running[h]
    /\ running' = [running EXCEPT ![h] = FALSE]
    /\ UNCHANGED <<state, epoch, live, proof, chal, seen, joined, badHeld, badRead>>

\* Expire: a lease no running holder renews lapses (its TTL).
Expire(p) ==
    /\ "noexpire" \notin Broken
    /\ live[p] # 0
    /\ ~running[HolderOf[p]] \/ joined[HolderOf[p]] # live[p]
    /\ live' = [live EXCEPT ![p] = 0]
    /\ UNCHANGED <<state, epoch, proof, chal, seen, running, joined, badHeld, badRead>>

\* ExpireProof: a proof lapses (its TTL).
ExpireProof(p) ==
    /\ proof[p] # 0
    /\ proof' = [proof EXCEPT ![p] = 0]
    /\ UNCHANGED <<state, epoch, live, chal, seen, running, joined, badHeld, badRead>>

\* ProveDaemon (Store.Prove via Holder.Beat): a daemon holder proves its own
\* push after its own round trip, under its epoch, only while the push's
\* lease of that epoch stands.
ProveDaemon(h, p) ==
    /\ p \notin SessionPushes
    /\ HolderOf[p] = h
    /\ running[h]
    /\ state = "hooked"
    /\ joined[h] = epoch
    /\ live[p] = epoch \/ "nolease" \in Broken
    /\ proof' = [proof EXCEPT ![p] = epoch]
    /\ badHeld' = (badHeld \/ live[p] # epoch)
    /\ UNCHANGED <<state, epoch, live, chal, seen, running, joined, badRead>>

\* Challenge (Store.Challenge): a session's hook sets a challenge under its
\* epoch and prints its nonce on its stream.
Challenge(h) ==
    /\ \E p \in SessionPushes : HolderOf[p] = h
    /\ running[h]
    /\ state = "hooked"
    /\ joined[h] = epoch
    /\ chal' = epoch
    /\ seen' = FALSE
    /\ UNCHANGED <<state, epoch, live, proof, running, joined, badHeld, badRead>>

\* ChallengeLapses: an outstanding challenge passes its bound.
ChallengeLapses ==
    /\ chal # 0
    /\ chal' = 0
    /\ UNCHANGED <<state, epoch, live, proof, seen, running, joined, badHeld, badRead>>

\* Read: the session reads the challenge off its hook's stream.
Read ==
    /\ chal # 0
    /\ ~seen
    /\ seen' = TRUE
    /\ UNCHANGED <<state, epoch, live, proof, chal, running, joined, badHeld, badRead>>

\* Answer (Store.Answer, nova-hook prove): the nonce read off the stream,
\* under the record's epoch, proves every session push whose lease stands,
\* and spends the challenge.
Answer ==
    /\ chal # 0
    /\ chal = epoch
    /\ state = "hooked"
    /\ seen \/ "nostream" \in Broken
    /\ proof' = [p \in Pushes |-> IF p \in SessionPushes /\ live[p] = epoch THEN epoch ELSE proof[p]]
    /\ badRead' = (badRead \/ ~seen)
    /\ chal' = 0
    /\ seen' = FALSE
    /\ UNCHANGED <<state, epoch, live, running, joined, badHeld>>

\* Unhook (Store.Unhook, nova-hook unhook): the epoch moves on and every
\* lease, proof and challenge is cleared.
Unhook ==
    /\ state # "none"
    /\ IF "noepoch" \in Broken
          THEN /\ UNCHANGED <<epoch, live, proof, chal, seen>>
          ELSE /\ epoch < MaxEpoch
               /\ epoch' = epoch + 1
               /\ live' = [p \in Pushes |-> 0]
               /\ proof' = [p \in Pushes |-> 0]
               /\ chal' = 0
               /\ seen' = FALSE
    /\ state' = "unhooked"
    /\ UNCHANGED <<running, joined, badHeld, badRead>>

Next ==
    \/ \E h \in Holders : Arm(h) \/ Renew(h) \/ Rejoin(h) \/ Stop(h) \/ Challenge(h)
    \/ \E h \in Holders, p \in Pushes : ProveDaemon(h, p)
    \/ \E p \in Pushes : Expire(p) \/ ExpireProof(p)
    \/ ChallengeLapses \/ Read \/ Answer \/ Unhook

Spec == Init /\ [][Next]_vars /\ \A p \in Pushes : WF_vars(Expire(p))

\* LiveIsJoined: a lease of the record's epoch on a hooked record was
\* written by a holder that joined that epoch: an old holder never revives
\* a push.
LiveIsJoined ==
    state = "hooked" => \A p \in Pushes : live[p] = epoch => joined[HolderOf[p]] = epoch

\* UnhookStands: an unhooked record has no lease and no proof of its epoch,
\* so the gate is shut until someone hooks in again.
UnhookStands ==
    state = "unhooked" => \A p \in Pushes : live[p] # epoch /\ proof[p] # epoch

\* ProofOnlyHeld: no proof is ever written for a push nobody holds.
ProofOnlyHeld == ~badHeld

\* ProofMeansRead: a session's proof is the answer of a session that read its
\* challenge off its own stream.
ProofMeansRead == ~badRead

\* GateMeansHooked: the gate's yes is a hooked record with every push live
\* and proven under its epoch, each lease from a holder that joined it.
GateMeansHooked ==
    Gate => /\ state = "hooked"
            /\ \A p \in Pushes : joined[HolderOf[p]] = epoch

\* DeadHolderLosesGate: a holder that stops and stays stopped shuts the gate.
DeadHolderLosesGate ==
    \A h \in Holders : (~running[h] /\ Held(h) # {}) ~> (running[h] \/ ~Gate)
=============================================================================
