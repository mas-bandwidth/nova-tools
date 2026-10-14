------------------------------ MODULE Hook ------------------------------
\* The hook record of one identity (hook/hook.go, hook/redis.go,
\* hook/holder.go, cmd/nova-hook/main.go; docs/SPEC-HOOK.md). Every tool that
\* sends, reads or takes work as an identity acts only when the gate says yes:
\* the record is hooked as a role, and every push that role needs has a live
\* lease and a proof, both written under the record's epoch. The truth lives in
\* Redis alone: a lease is a key with a TTL its holder renews, so a holder that
\* dies loses its push on its own.
\*
\* The state:
\*   state     the record: "none" (no record), "hooked" or "unhooked"
\*   role      the role it is hooked as ("none" while there is no record)
\*   epoch     the record's epoch; a new arm and an unhook move it on, a join
\*             keeps it; a record lost (Flush) counts from 0 again
\*   chosen    the identity's own word: the role it last hooked in as by
\*             nova-hook hook, "unhooked" after nova-hook unhook, "none" when
\*             it never spoke (or the record holding its word was lost)
\*   live[p]   the epoch the lease of push p was written under, 0 for none
\*   proof[p]  the epoch the proof of push p was written under, 0 for none
\*   chal      the outstanding challenge: its nonce's id (0 for none) and the
\*             epoch it was set under
\*   nonce     the id of the last nonce drawn; ids never repeat (a nonce is
\*             random), so a nonce read before never answers a later challenge
\*   knows     the challenge whose nonce the session read off its hook's stream
\*   running[h] holder h runs (a friend daemon, a session's hook, nova-sprint hook)
\*   joined[h] the epoch holder h writes under
\*
\* Each holder h holds the pushes HeldOf[h] as the role RoleOf[h]; the holders
\* in SessionHolders are sessions' hooks (nova-hook hook under a Monitor),
\* whose pushes are proven only by an answered challenge, and whose start is
\* the identity's own word. The rest are daemons that prove their pushes by
\* their own round trip and whose start never undoes the identity's word.
\* Time is not a variable: a running holder renews inside its lease, so its
\* lease never lapses; a stopped holder's lease lapses (Expire) at any time,
\* and under fairness surely. A proof lapses at any time (its TTL).
\*
\* The gate judges the act's own Need (GateOK): an act that needs the bus push
\* opens when that push is live and proven, whatever the role's other pushes do
\* (a friend working by hand, with no daemon holding her work push, still
\* sends on the bus), and an act that needs no push in particular (nova-hook
\* status) is judged on every push of the role, as the gate did for every act
\* before.
\*
\* What it leaves out: the window between a holder's death and its lease's
\* expiry (bounded by LeaseTTL in the code), the store's clock, and the gate's
\* Want.Roles (a verb's own list of the roles it is for, a filter on the
\* role this machine already carries).
\*
\* Reversed witnesses (Broken):
\*   "staleepoch"  a renew does not check the holder's epoch: a holder of an
\*                 epoch since unhooked makes a push live again (LiveIsJoined)
\*   "norole"      a renew does not check the holder's role: after a record is
\*                 lost and armed again from nothing its epoch counts from 1
\*                 again, and a holder of another role that joined the old
\*                 epoch 1 makes its push live (LiveIsJoined)
\*   "noepoch"     unhook neither moves the epoch on nor clears the keys: an
\*                 unhooked record keeps a live push (UnhookStands)
\*   "nolease"     a daemon proves a push with no live lease: the push it holds
\*                 again after a rejoin is proven before it is live (ProofOnlyHeld)
\*   "nostream"    a challenge is answered by a session that never read its
\*                 nonce (ProofMeansRead)
\*   "takeover"    a daemon's start takes over a record hooked as another role
\*                 (a seat's own friend daemon re-arming it as a friend)
\*                 (HookedByItsWord)
\*   "rearm"       a daemon's start arms an unhooked record again: a restart
\*                 undoes an unhook (HookedByItsWord)
\*   "noexpire"    a lease has no TTL: a dead holder keeps the gate open for
\*                 ever (DeadHolderLosesGate)
\*   "allpushes"   the gate judges every push of the role for an act that needs
\*                 one (the old rule): a bus-only act is refused for a friend
\*                 whose work push is down though the bus push it needs is
\*                 proven (NeedIsEnough)
\*   "anyproven"   the gate opens for an act once any push of the role is
\*                 proven, not the pushes the act needs: an act that needs the
\*                 work push opens on a record whose work push is down
\*                 (GateOKMeansHooked)
EXTENDS Naturals, FiniteSets

CONSTANTS Pushes, Roles, NeedOf, Holders, RoleOf, HeldOf, SessionHolders, WordRoles, MaxEpoch, MaxNonce, Broken

VARIABLES state, role, epoch, chosen, live, proof, chal, nonce, knows, running, joined

vars == <<state, role, epoch, chosen, live, proof, chal, nonce, knows, running, joined>>

Epochs == 0..MaxEpoch
Nonces == 0..MaxNonce
NoChal == [n |-> 0, e |-> 0]
Chals == [n : Nonces, e : Epochs]

\* HoldersOf(p, r): the holders of push p as role r.
HoldersOf(p, r) == {h \in Holders : RoleOf[h] = r /\ p \in HeldOf[h]}
\* SessionPushes(r): the pushes of role r a session's hook holds, proven only by a challenge.
SessionPushes(r) == UNION {HeldOf[h] : h \in {s \in SessionHolders : RoleOf[s] = r}}

TypeOK ==
    /\ state \in {"none", "hooked", "unhooked"}
    /\ role \in Roles \cup {"none"}
    /\ epoch \in Epochs
    /\ chosen \in Roles \cup {"none", "unhooked"}
    /\ live \in [Pushes -> Epochs]
    /\ proof \in [Pushes -> Epochs]
    /\ chal \in Chals
    /\ nonce \in Nonces
    /\ knows \in Chals
    /\ running \in [Holders -> BOOLEAN]
    /\ joined \in [Holders -> Epochs]

Init ==
    /\ state = "none"
    /\ role = "none"
    /\ epoch = 0
    /\ chosen = "none"
    /\ live = [p \in Pushes |-> 0]
    /\ proof = [p \in Pushes |-> 0]
    /\ chal = NoChal
    /\ nonce = 0
    /\ knows = NoChal
    /\ running = [h \in Holders |-> FALSE]
    /\ joined = [h \in Holders |-> 0]

\* The gate (hook.Status.Gate): hooked, and every push of its role live and
\* proven under the record's epoch.
Gate ==
    /\ state = "hooked"
    /\ \A p \in NeedOf[role] : live[p] = epoch /\ proof[p] = epoch

\* GateOK(need) (hook.Status.Gate): the gate for an act that needs the pushes
\* need: hooked, the role carries every push the act needs, and the pushes the
\* act needs are live and proven under the record's epoch. An act that needs
\* none in particular (need = {}) is judged on every push of the role: that is
\* Gate. Gate stays the all-pushes rule: nova-hook status, and a daemon's own
\* gate over every push of its role.
GateOK(need) ==
    /\ state = "hooked"
    /\ need \subseteq NeedOf[role]
    /\ IF "anyproven" \in Broken
          THEN \E p \in NeedOf[role] : live[p] = epoch /\ proof[p] = epoch
          ELSE \A p \in (IF need = {} \/ "allpushes" \in Broken THEN NeedOf[role] ELSE need) :
                  live[p] = epoch /\ proof[p] = epoch

\* ArmNew(r, h): the record is armed as role r at a new epoch (armLua's arm):
\* every lease, proof and challenge of the old one is cleared, and the holder
\* h that armed it (if any: h \in Holders) holds its pushes under the new one.
ArmNew(r, hs) ==
    /\ epoch < MaxEpoch
    /\ epoch' = epoch + 1
    /\ state' = "hooked"
    /\ role' = r
    /\ live' = [p \in Pushes |-> IF \E h \in hs : p \in HeldOf[h] THEN epoch + 1 ELSE 0]
    /\ proof' = [p \in Pushes |-> 0]
    /\ chal' = NoChal
    /\ joined' = [h \in Holders |-> IF h \in hs THEN epoch + 1 ELSE joined[h]]

\* Join(h): holder h joins the record hooked as its role, at its epoch, and
\* renews its pushes at once.
Join(h) ==
    /\ joined' = [joined EXCEPT ![h] = epoch]
    /\ live' = [p \in Pushes |-> IF p \in HeldOf[h] THEN epoch ELSE live[p]]
    /\ UNCHANGED <<state, role, epoch, proof, chal>>

\* Arm (Holder.Start; armLua in holder mode, or nova-hook hook's HookAs for a
\* session's hook): holder h starts. A record hooked as its role is joined.
\* A daemon arms only where there is no record at all; it leaves a record
\* hooked as another role, and an unhooked one, as they are (it holds nothing;
\* ErrOtherRole, ErrNotHooked). A session's hook is the identity's own word:
\* it arms a new epoch over either, and its word is recorded.
Arm(h) ==
    /\ ~running[h]
    /\ IF state = "hooked" /\ role = RoleOf[h]
          THEN /\ Join(h)
               /\ chosen' = IF h \in SessionHolders THEN RoleOf[h] ELSE chosen
          ELSE /\ \/ h \in SessionHolders
                  \/ state = "none"
                  \/ state = "unhooked" /\ "rearm" \in Broken
                  \/ state = "hooked" /\ "takeover" \in Broken
               /\ ArmNew(RoleOf[h], {h})
               /\ chosen' = IF h \in SessionHolders THEN RoleOf[h] ELSE chosen
    /\ running' = [running EXCEPT ![h] = TRUE]
    /\ UNCHANGED <<nonce, knows>>

\* Word(r) (nova-hook hook --as <name> --role r --once; Store.HookAs): the
\* identity hooks in as r and holds nothing itself: a record hooked as r is
\* joined, otherwise a new epoch as r; its word is recorded.
Word(r) ==
    /\ chosen' = r
    /\ IF state = "hooked" /\ role = r
          THEN UNCHANGED <<state, role, epoch, live, proof, chal, joined>>
          ELSE ArmNew(r, {})
    /\ UNCHANGED <<nonce, knows, running>>

\* ReArm (Holder.Beat finding no record; nova-hook's rearm): a running holder
\* whose record was lost arms it again, as its start would.
ReArm(h) ==
    /\ running[h]
    /\ state = "none"
    /\ ArmNew(RoleOf[h], {h})
    /\ chosen' = IF h \in SessionHolders THEN RoleOf[h] ELSE chosen
    /\ UNCHANGED <<nonce, knows, running>>

\* Renew (Store.Renew via Holder.Beat): a running holder renews its leases
\* under the epoch it joined and its role, refused unless they are the record's.
Renew(h) ==
    /\ running[h]
    /\ state = "hooked"
    /\ joined[h] = epoch \/ "staleepoch" \in Broken
    /\ role = RoleOf[h] \/ "norole" \in Broken
    /\ live' = [p \in Pushes |-> IF p \in HeldOf[h] THEN epoch ELSE live[p]]
    /\ UNCHANGED <<state, role, epoch, chosen, proof, chal, nonce, knows, running, joined>>

\* Rejoin (Holder.Beat): a holder whose renew was refused rejoins a record
\* hooked again since as its own role, never an unhooked one, never another
\* role's. It renews at its next Renew.
Rejoin(h) ==
    /\ running[h]
    /\ state = "hooked"
    /\ role = RoleOf[h]
    /\ joined[h] # epoch
    /\ joined' = [joined EXCEPT ![h] = epoch]
    /\ UNCHANGED <<state, role, epoch, chosen, live, proof, chal, nonce, knows, running>>

\* Stop: a holder exits or dies; its leases stay until they lapse.
Stop(h) ==
    /\ running[h]
    /\ running' = [running EXCEPT ![h] = FALSE]
    /\ UNCHANGED <<state, role, epoch, chosen, live, proof, chal, nonce, knows, joined>>

\* Expire: a lease no running holder of the record's role renews lapses (its TTL).
Expire(p) ==
    /\ "noexpire" \notin Broken
    /\ live[p] # 0
    /\ ~\E h \in HoldersOf(p, role) : running[h] /\ joined[h] = live[p]
    /\ live' = [live EXCEPT ![p] = 0]
    /\ UNCHANGED <<state, role, epoch, chosen, proof, chal, nonce, knows, running, joined>>

\* ExpireProof: a proof lapses (its TTL).
ExpireProof(p) ==
    /\ proof[p] # 0
    /\ proof' = [proof EXCEPT ![p] = 0]
    /\ UNCHANGED <<state, role, epoch, chosen, live, chal, nonce, knows, running, joined>>

\* ProveDaemon (Store.Prove via Holder.Beat): a daemon holder proves its own
\* push after its own round trip, under its epoch and role, only while the
\* push's lease of that epoch stands.
ProveDaemon(h, p) ==
    /\ h \notin SessionHolders
    /\ p \in HeldOf[h]
    /\ running[h]
    /\ state = "hooked"
    /\ role = RoleOf[h]
    /\ joined[h] = epoch
    /\ live[p] = epoch \/ "nolease" \in Broken
    /\ proof' = [proof EXCEPT ![p] = epoch]
    /\ UNCHANGED <<state, role, epoch, chosen, live, chal, nonce, knows, running, joined>>

\* Challenge (Store.Challenge): a session's hook sets a challenge under its
\* epoch and role, with a fresh nonce, and prints the nonce on its stream.
Challenge(h) ==
    /\ h \in SessionHolders
    /\ running[h]
    /\ state = "hooked"
    /\ role = RoleOf[h]
    /\ joined[h] = epoch
    /\ nonce < MaxNonce
    /\ nonce' = nonce + 1
    /\ chal' = [n |-> nonce + 1, e |-> epoch]
    /\ UNCHANGED <<state, role, epoch, chosen, live, proof, knows, running, joined>>

\* ChallengeLapses: an outstanding challenge passes its bound.
ChallengeLapses ==
    /\ chal.n # 0
    /\ chal' = NoChal
    /\ UNCHANGED <<state, role, epoch, chosen, live, proof, nonce, knows, running, joined>>

\* Read: the session reads the outstanding challenge's nonce off its hook's stream.
Read ==
    /\ chal.n # 0
    /\ knows # chal
    /\ knows' = chal
    /\ UNCHANGED <<state, role, epoch, chosen, live, proof, chal, nonce, running, joined>>

\* Answer (Store.Answer, nova-hook prove): the nonce read off the stream,
\* under the record's epoch, proves every session push of the role whose lease
\* stands, and spends the challenge.
Answer ==
    /\ chal.n # 0
    /\ chal.e = epoch
    /\ state = "hooked"
    /\ knows = chal \/ "nostream" \in Broken
    /\ proof' = [p \in Pushes |-> IF p \in SessionPushes(role) /\ live[p] = epoch THEN epoch ELSE proof[p]]
    /\ chal' = NoChal
    /\ UNCHANGED <<state, role, epoch, chosen, live, nonce, knows, running, joined>>

\* Unhook (Store.Unhook, nova-hook unhook): the epoch moves on and every
\* lease, proof and challenge is cleared; the identity's word is unhooked.
Unhook ==
    /\ state # "none"
    /\ IF "noepoch" \in Broken
          THEN UNCHANGED <<epoch, live, proof, chal>>
          ELSE /\ epoch < MaxEpoch
               /\ epoch' = epoch + 1
               /\ live' = [p \in Pushes |-> 0]
               /\ proof' = [p \in Pushes |-> 0]
               /\ chal' = NoChal
    /\ state' = "unhooked"
    /\ chosen' = "unhooked"
    /\ UNCHANGED <<role, nonce, knows, running, joined>>

\* Flush: the record is lost (the store flushed): every hook key is gone, the
\* epoch counts from 0 again, and the word it held is gone with it.
Flush ==
    /\ state # "none"
    /\ state' = "none"
    /\ role' = "none"
    /\ epoch' = 0
    /\ chosen' = "none"
    /\ live' = [p \in Pushes |-> 0]
    /\ proof' = [p \in Pushes |-> 0]
    /\ chal' = NoChal
    /\ UNCHANGED <<nonce, knows, running, joined>>

Next ==
    \/ \E h \in Holders : Arm(h) \/ ReArm(h) \/ Renew(h) \/ Rejoin(h) \/ Stop(h) \/ Challenge(h)
    \/ \E h \in Holders, p \in Pushes : ProveDaemon(h, p)
    \/ \E p \in Pushes : Expire(p) \/ ExpireProof(p)
    \/ \E r \in WordRoles : Word(r)
    \/ ChallengeLapses \/ Read \/ Answer \/ Unhook \/ Flush

Spec == Init /\ [][Next]_vars /\ \A p \in Pushes : WF_vars(Expire(p))

\* LiveIsJoined: a lease of the record's epoch on a hooked record was
\* written by a holder of the record's role that joined that epoch: an old
\* holder, or one of another role, never revives a push.
LiveIsJoined ==
    state = "hooked" =>
        \A p \in Pushes : live[p] = epoch => \E h \in HoldersOf(p, role) : joined[h] = epoch

\* UnhookStands: an unhooked record has no lease and no proof of its epoch,
\* so the gate is shut until someone hooks in again.
UnhookStands ==
    state = "unhooked" => \A p \in Pushes : live[p] # epoch /\ proof[p] # epoch

\* HookedByItsWord: a hooked record is hooked as the role the identity last
\* chose, or the identity never spoke and a holder's start armed it from
\* nothing. A holder never takes a record over and never undoes an unhook,
\* whether it runs or restarts.
HookedByItsWord ==
    state = "hooked" => chosen \in {role, "none"}

\* ProofOnlyHeld: a running holder that joined the record's epoch as its
\* role has a live lease for every push of it that is proven: no push is
\* proven ahead of its lease.
ProofOnlyHeld ==
    state = "hooked" =>
        \A p \in Pushes : \A h \in HoldersOf(p, role) :
            (proof[p] = epoch /\ running[h] /\ joined[h] = epoch) => live[p] = epoch

\* ProofMeansRead: a session push proven under the record's epoch was proven
\* by a session that read a challenge of that epoch off its own stream.
ProofMeansRead ==
    state = "hooked" =>
        \A p \in SessionPushes(role) : proof[p] = epoch => knows.e = epoch

\* GateMeansHooked: the gate's yes is a hooked record with every push of its
\* role live and proven under its epoch, each lease from a holder of that
\* role that joined it.
GateMeansHooked ==
    Gate => /\ state = "hooked"
            /\ \A p \in NeedOf[role] : \E h \in HoldersOf(p, role) : joined[h] = epoch

\* GateOKMeansHooked: an act's yes is a hooked record whose role carries every
\* push the act needs, each live and proven under the record's epoch (every push
\* of the role when the act names none), each lease from a holder of that role
\* that joined it. No act opens on a push that is down or unproven.
GateOKMeansHooked ==
    \A need \in SUBSET Pushes :
        GateOK(need) =>
            /\ state = "hooked"
            /\ need \subseteq NeedOf[role]
            /\ \A p \in (IF need = {} THEN NeedOf[role] ELSE need) :
                  /\ live[p] = epoch /\ proof[p] = epoch
                  /\ \E h \in HoldersOf(p, role) : joined[h] = epoch

\* NeedIsEnough: an act that needs some pushes of the role opens once those are
\* live and proven, whatever the role's other pushes do: a friend with her bus
\* push proven and her work push down can send on the bus.
NeedIsEnough ==
    \A need \in SUBSET Pushes :
        (/\ need # {}
         /\ state = "hooked"
         /\ need \subseteq NeedOf[role]
         /\ \A p \in need : live[p] = epoch /\ proof[p] = epoch) => GateOK(need)

\* DeadHolderLosesGate: a holder of the record's role that stops and stays
\* stopped shuts the gate.
DeadHolderLosesGate ==
    \A h \in Holders :
        (~running[h] /\ Gate /\ role = RoleOf[h]) ~> (running[h] \/ ~Gate \/ role # RoleOf[h])
=============================================================================
