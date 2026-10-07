------------------------- MODULE TableFirstContact -------------------------
\* nova-table's first contact with a store (cmd/nova-table/library.go): every
\* verb is an FCALL into the nova_sprint library; a store that holds none
\* answers "Function not found", and nova-table puts the library there with
\* LoadMissing (never REPLACE) and sends the verb again, once.
\*
\* State: the store's library ("none", "old" = a build that lacks the verb's
\* function, "new" = this build's), and per process: whether a load reached
\* an outcome (done), the loads that reached an outcome, and per verb where it is (pc), how
\* many times the store ran it (ran) and how many times it was sent (sent).
\*
\* Outside events: the deployer (nova-redis fn load, Ensure) puts "new" on the
\* store; an older binary's LoadMissing puts "old" on a store that holds none;
\* a load fails (the store stops answering); a reply is lost after the store
\* ran the command.
\*
\* Left out: the store's other keys, the verb's own refusals (a verb that ran
\* is "ok" here whatever it answered), pipelines (the code sends one again only
\* when every command in it missed, which is this model's single command), and
\* the connection's reopening (TableSession.tla). LoadMissing's read and load
\* are one step here; RedisFn.tla models them as two and proves a LoadMissing
\* never replaces under racing loaders.
\*
\* Broken names a misimplementation a reversed witness turns on:
\*   "resend-lost": a verb whose reply was lost is sent again (AtMostOnce).
\*   "replace":     the load sends FUNCTION LOAD REPLACE (NeverReplaced).
\*   "every-miss":  every miss loads, not once per process (LoadOnce).
EXTENDS Naturals, FiniteSets

CONSTANTS Procs, Verbs, Broken, Outside

VARIABLES store, done, loads, pc, ran, sent, replaced

vars == <<store, done, loads, pc, ran, sent, replaced>>

Libs == {"none", "old", "new"}
PCs == {"idle", "missed", "loaded", "ok", "lost", "refused"}

TypeOK ==
  /\ store \in Libs
  /\ done \in [Procs -> BOOLEAN]
  /\ loads \in [Procs -> Nat]
  /\ pc \in [Procs -> [Verbs -> PCs]]
  /\ ran \in [Procs -> [Verbs -> Nat]]
  /\ sent \in [Procs -> [Verbs -> Nat]]
  /\ replaced \in BOOLEAN

Init ==
  /\ store = "none"
  /\ done = [p \in Procs |-> FALSE]
  /\ loads = [p \in Procs |-> 0]
  /\ pc = [p \in Procs |-> [v \in Verbs |-> "idle"]]
  /\ ran = [p \in Procs |-> [v \in Verbs |-> 0]]
  /\ sent = [p \in Procs |-> [v \in Verbs |-> 0]]
  /\ replaced = FALSE

\* A process runs its verbs one after another: v is next when every verb
\* before it has ended.
Next1(p, v) == /\ pc[p][v] = "idle"
               /\ \A u \in Verbs : u < v => pc[p][u] \in {"ok", "lost", "refused"}

SetPC(p, v, s) == pc' = [pc EXCEPT ![p][v] = s]
Run(p, v) == ran' = [ran EXCEPT ![p][v] = @ + 1]
Sent(p, v) == sent' = [sent EXCEPT ![p][v] = @ + 1]

\* The verb is sent. The store runs it only when it holds this build's
\* library; otherwise it answers "Function not found" and runs nothing.
Send(p, v) ==
  /\ Next1(p, v)
  /\ Sent(p, v)
  /\ IF store = "new"
       THEN /\ Run(p, v) /\ SetPC(p, v, "ok")
       ELSE /\ ran' = ran /\ SetPC(p, v, "missed")
  /\ UNCHANGED <<store, done, loads, replaced>>

\* The store ran the verb and its reply was lost: never sent again.
SendLost(p, v) ==
  /\ "lost" \in Outside
  /\ Next1(p, v)
  /\ store = "new"
  /\ Sent(p, v) /\ Run(p, v) /\ SetPC(p, v, "lost")
  /\ UNCHANGED <<store, done, loads, replaced>>

\* The broken resend of a lost reply.
ResendLost(p, v) ==
  /\ Broken = "resend-lost"
  /\ pc[p][v] = "lost"
  /\ Sent(p, v) /\ Run(p, v) /\ SetPC(p, v, "ok")
  /\ UNCHANGED <<store, done, loads, replaced>>

\* ensure after a miss. A load is made when none has reached an outcome in
\* this process (or on every miss, broken). It never replaces: the store
\* takes it only when it holds no library (unless broken "replace"). After a
\* load that reached an outcome the verb is sent again; with done already,
\* the verb is refused with the deployer's remedy.
Load(p, v) ==
  /\ pc[p][v] = "missed"
  /\ ~done[p] \/ Broken = "every-miss"
  /\ loads' = [loads EXCEPT ![p] = @ + 1]
  /\ done' = [done EXCEPT ![p] = TRUE]
  /\ IF store = "none" \/ Broken = "replace"
       THEN /\ store' = "new"
            /\ replaced' = (replaced \/ store # "none")
       ELSE UNCHANGED <<store, replaced>>
  /\ SetPC(p, v, "loaded")
  /\ UNCHANGED <<ran, sent>>

\* The load fails (no answer): the verb is refused, the process is not done,
\* so a later verb tries again.
LoadFails(p, v) ==
  /\ "fail" \in Outside
  /\ pc[p][v] = "missed"
  /\ ~done[p] \/ Broken = "every-miss"
  /\ SetPC(p, v, "refused")
  /\ UNCHANGED <<store, done, loads, ran, sent, replaced>>

\* A miss after a load reached an outcome: refused, nothing loaded.
Refuse(p, v) ==
  /\ pc[p][v] = "missed"
  /\ done[p] /\ Broken # "every-miss"
  /\ SetPC(p, v, "refused")
  /\ UNCHANGED <<store, done, loads, ran, sent, replaced>>

\* The one send after the load: run when the store holds this build's
\* library, else refused (an older library: the deployer's remedy).
Resend(p, v) ==
  /\ pc[p][v] = "loaded"
  /\ Sent(p, v)
  /\ IF store = "new"
       THEN /\ Run(p, v) /\ SetPC(p, v, "ok")
       ELSE /\ ran' = ran /\ SetPC(p, v, "refused")
  /\ UNCHANGED <<store, done, loads, replaced>>

\* The deployer (Ensure) puts this build's library on the store.
Deploy ==
  /\ "deploy" \in Outside
  /\ store # "new"
  /\ store' = "new"
  /\ UNCHANGED <<done, loads, pc, ran, sent, replaced>>

\* An older binary's LoadMissing on a store that holds none.
OlderLoads ==
  /\ "older" \in Outside
  /\ store = "none"
  /\ store' = "old"
  /\ UNCHANGED <<done, loads, pc, ran, sent, replaced>>

Ended == \A p \in Procs : \A v \in Verbs : pc[p][v] \in {"ok", "lost", "refused"}

Next ==
  \/ \E p \in Procs, v \in Verbs :
       Send(p, v) \/ SendLost(p, v) \/ ResendLost(p, v) \/ Load(p, v)
       \/ LoadFails(p, v) \/ Refuse(p, v) \/ Resend(p, v)
  \/ Deploy \/ OlderLoads
  \/ (Ended /\ UNCHANGED vars)

Fair == \A p \in Procs, v \in Verbs :
          WF_vars(Send(p, v)) /\ WF_vars(Load(p, v)) /\ WF_vars(Refuse(p, v)) /\ WF_vars(Resend(p, v))

Spec == Init /\ [][Next]_vars /\ Fair

\* The store runs a verb at most once (AtMostOnce).
AtMostOnce == \A p \in Procs, v \in Verbs : ran[p][v] <= 1

\* A verb is sent at most twice, and a second time only when the first send
\* ran nothing.
SentTwiceOnlyAfterAMiss == \A p \in Procs, v \in Verbs :
  sent[p][v] <= 2 /\ (sent[p][v] = 2 => ran[p][v] <= 1)

\* No load of nova-table's replaces a library the store holds.
NeverReplaced == ~replaced

\* A process makes at most one load that reaches an outcome (a failed load
\* reaches none, and the next miss tries again).
LoadOnce == \A p \in Procs : loads[p] <= 1

\* A fresh store with no outside event: every verb of every process ends ok.
FreshStoreWorks == <>(\A p \in Procs, v \in Verbs : pc[p][v] = "ok")
=============================================================================
