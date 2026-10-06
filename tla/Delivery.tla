------------------------------ MODULE Delivery ------------------------------
\* A gone session target is invalid, never retried (docs/SPEC-FRIEND.md,
\* "A gone session target"; internal/friend/target.go and daemon.go,
\* deliverBatch and batchDone; cmd/nova-friend rebind).
\*
\* The daemon delivers a friend's pending messages into the session it names
\* (bound). The harness keeps each session's lifecycle (life): the friend may
\* archive, delete or move one at any time, and may bring one back herself;
\* the daemon never does. run reads the bound target's lifecycle at its start,
\* and the daemon before every retry (a turn after one that was deferred or
\* failed): gone is target-invalid, said once (one blocker and one NOTE), and
\* nothing more is handed in. A first try after a turn that ended well reads
\* nothing, so it may reach a session gone since; it fails or defers, and the
\* retry finds it. Only a rebind (or an install with a new session) names a target
\* again: the old one retired, the push proof down, and a fresh session check
\* through the new one before the daemon runs.
\*
\* Broken selects a reversed witness: "none" is the code; "retryunchecked" a
\* retry that skips the read (the defer loop of 2026-10-06);
\* "rebindkeepsproof" a rebind that keeps the old proof and runs at once.
EXTENDS Naturals, FiniteSets

CONSTANTS Targets, Msgs, Broken

ASSUME Broken \in {"none", "retryunchecked", "rebindkeepsproof"}

VARIABLES
    life,       \* the harness's word on each session: live or gone
    bound,      \* the session the daemon names
    retired,    \* the sessions a rebind replaced: run and install refuse them
    proofFor,   \* the session the push proof was proven through, "none" when down
    daemon,     \* proving, running, deferred (a turn in hand), invalid
    pending,    \* messages on her stream, not acked
    delivered,  \* <<message, session>> handed in and taken
    acked,
    told,       \* blocker and NOTE sent for the present invalidation
    intoGone    \* a retry reached a session that was gone

vars == <<life, bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone>>

TypeOK ==
    /\ life \in [Targets -> {"live", "gone"}]
    /\ bound \in Targets
    /\ retired \subseteq Targets
    /\ proofFor \in Targets \cup {"none"}
    /\ daemon \in {"proving", "running", "deferred", "invalid"}
    /\ pending \subseteq Msgs
    /\ delivered \subseteq (Msgs \X Targets)
    /\ acked \subseteq Msgs
    /\ told \in 0..1
    /\ intoGone \in BOOLEAN

First == CHOOSE t \in Targets : TRUE

Init ==
    /\ life = [t \in Targets |-> "live"]
    /\ bound = First
    /\ retired = {}
    /\ proofFor = "none"
    /\ daemon = "proving"
    /\ pending = Msgs
    /\ delivered = {}
    /\ acked = {}
    /\ told = 0
    /\ intoGone = FALSE

\* The friend archives, deletes or moves a session; or brings one back herself.
Gone(t) ==
    /\ life[t] = "live"
    /\ life' = [life EXCEPT ![t] = "gone"]
    /\ UNCHANGED <<bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone>>

Revive(t) ==
    /\ life[t] = "gone"
    /\ life' = [life EXCEPT ![t] = "live"]
    /\ UNCHANGED <<bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone>>

\* run's start: the push proof, a session check through the bound session;
\* a gone target is invalid at the start (run reads it first).
Prove ==
    /\ daemon = "proving"
    /\ IF life[bound] = "live"
       THEN /\ proofFor' = bound
            /\ daemon' = "running"
       ELSE /\ daemon' = "invalid"
            /\ UNCHANGED proofFor
    /\ UNCHANGED <<life, bound, retired, pending, delivered, acked, told, intoGone>>

\* One hand-in of message m: the first try (running) or a retry (deferred,
\* after a turn deferred or failed). A retry reads the lifecycle first, except
\* in the reversed witness.
Checked == Broken # "retryunchecked" /\ daemon = "deferred"

HandIn(m) ==
    /\ daemon \in {"running", "deferred"}
    /\ m \in pending
    /\ IF Checked /\ life[bound] = "gone"
       THEN /\ daemon' = "invalid"
            /\ UNCHANGED <<pending, delivered, acked, intoGone>>
       ELSE \/ /\ life[bound] = "live"                 \* the turn ends at exit 0: acked
               /\ delivered' = delivered \cup {<<m, bound>>}
               /\ acked' = acked \cup {m}
               /\ pending' = pending \ {m}
               /\ daemon' = "running"
               /\ UNCHANGED intoGone
            \/ /\ daemon' = "deferred"                 \* the session cannot take it now, or the turn failed
               /\ intoGone' = (intoGone \/ (daemon = "deferred" /\ life[bound] = "gone"))
               /\ UNCHANGED <<pending, delivered, acked>>
    /\ UNCHANGED <<life, bound, retired, proofFor, told>>

\* tellInvalid: one blocker to the coordinator and one NOTE to the friend.
Tell ==
    /\ daemon = "invalid"
    /\ told = 0
    /\ told' = 1
    /\ UNCHANGED <<life, bound, retired, proofFor, daemon, pending, delivered, acked, intoGone>>

\* nova-friend rebind, or install with a new --session: the new target read
\* live, the old one retired, the proof down, a fresh check owed.
Rebind(t) ==
    /\ t # bound
    /\ life[t] = "live"
    /\ bound' = t
    /\ retired' = (retired \cup {bound}) \ {t}
    /\ told' = 0
    /\ IF Broken = "rebindkeepsproof"
       THEN /\ daemon' = "running"
            /\ UNCHANGED proofFor
       ELSE /\ daemon' = "proving"
            /\ proofFor' = "none"
    /\ UNCHANGED <<life, pending, delivered, acked, intoGone>>

\* A reinstall from an old command line naming a retired target is refused:
\* nothing changes (run and install read target.json first).
Resurrect(t) ==
    /\ t \in retired
    /\ UNCHANGED vars

Next ==
    \/ \E t \in Targets : Gone(t) \/ Revive(t) \/ Rebind(t) \/ Resurrect(t)
    \/ Prove
    \/ \E m \in Msgs : HandIn(m)
    \/ Tell

Spec == Init /\ [][Next]_vars /\ WF_vars(Tell) /\ WF_vars(Prove)

\* No retry is handed into a session that is gone.
NeverIntoGone == ~intoGone

\* The daemon runs only on a proof through the session it names.
RunsOnItsOwnProof == daemon \in {"running", "deferred"} => proofFor = bound

\* No message is acked that was not delivered.
AckedOnlyDelivered == \A m \in acked : \E t \in Targets : <<m, t>> \in delivered

\* A retired target is never the bound one.
RetiredNeverBound == bound \notin retired

\* Every invalidation is told, unless a rebind came first.
InvalidIsTold == (daemon = "invalid") ~> (told = 1 \/ daemon # "invalid")
=============================================================================
