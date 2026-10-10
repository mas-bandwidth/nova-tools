------------------------------ MODULE DeliveryTarget ------------------------------
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
\* The rebind records the new target on her nova-config friend row (row), the
\* managed configuration; friend sync carries it to what her beat answers
\* (synced, row_session=). A service reinstalled elsewhere from an old command
\* line (a state directory whose target.json never saw the rebind: local is
\* FALSE) is not refused at its start. Before any proof or turn the daemon
\* fetches the managed row (fetched). A fetch that has not succeeded holds:
\* still proving, nothing handed in, no proof through the bound session. A
\* fetched row naming another session is target-invalid with no proof through
\* the old id. A row lagging a rebind made here (local, the row's session
\* retired) is not.
\*
\* Broken selects a reversed witness: "none" is the code; "retryunchecked" a
\* retry that skips the read (the defer loop of 2026-10-06);
\* "rebindkeepsproof" a rebind that keeps the old proof and runs at once;
\* "rowignored" a daemon that never reads the row's session;
\* "provefirst" a startup proof through the bound session before the row is fetched.
EXTENDS Naturals, FiniteSets

CONSTANTS Targets, Msgs, Broken

ASSUME Broken \in {"none", "retryunchecked", "rebindkeepsproof", "rowignored", "provefirst"}

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
    intoGone,   \* a retry reached a session that was gone
    row,        \* the session her nova-config friend row names, "none" when unset
    synced,     \* the row's session as her beat answers it (friend sync carried it)
    local,      \* the daemon's target.json is the one the last rebind wrote
    intoOld,    \* a message was handed into a session the synced row does not name
    fetched     \* this run has read the managed row (her beat's answer)

vars == <<life, bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone, row, synced, local, intoOld, fetched>>

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
    /\ row \in Targets \cup {"none"}
    /\ synced \in Targets \cup {"none"}
    /\ local \in BOOLEAN
    /\ intoOld \in BOOLEAN
    /\ fetched \in BOOLEAN

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
    /\ row = "none"
    /\ synced = "none"
    /\ local = TRUE
    /\ intoOld = FALSE
    /\ fetched = FALSE

\* The row as her beat answers it names another session than the bound one,
\* and it is not a row lagging a rebind made here (Target.Supersedes).
Superseded ==
    /\ synced # "none"
    /\ synced # bound
    /\ ~(local /\ synced \in retired)

\* The daemon reads the row's session off each beat's answer; the reversed
\* witness never does.
RowRead == Broken # "rowignored"

\* The friend archives, deletes or moves a session; or brings one back herself.
Gone(t) ==
    /\ life[t] = "live"
    /\ life' = [life EXCEPT ![t] = "gone"]
    /\ UNCHANGED <<bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone, row, synced, local, intoOld, fetched>>

Revive(t) ==
    /\ life[t] = "gone"
    /\ life' = [life EXCEPT ![t] = "live"]
    /\ UNCHANGED <<bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone, row, synced, local, intoOld, fetched>>

\* The managed row, read off a beat that does not deliver into the session.
\* Until it succeeds the daemon holds: still proving, no proof, no turn.
Fetch ==
    /\ ~fetched
    /\ fetched' = TRUE
    /\ UNCHANGED <<life, bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone, row, synced, local, intoOld>>

\* The startup proof, only after the row is fetched, unless the reversed
\* witness proves first. A fetched row that supersedes, or a gone target, is
\* invalid with no proof through the old id.
Prove ==
    /\ daemon = "proving"
    /\ IF Broken = "provefirst"
       THEN /\ IF life[bound] = "live"
               THEN /\ proofFor' = bound
                    /\ daemon' = "running"
               ELSE /\ daemon' = "invalid"
                    /\ UNCHANGED proofFor
            /\ UNCHANGED fetched
       ELSE /\ fetched
            /\ IF Superseded \/ life[bound] = "gone"
               THEN /\ daemon' = "invalid"
                    /\ UNCHANGED proofFor
               ELSE /\ proofFor' = bound
                    /\ daemon' = "running"
            /\ UNCHANGED fetched
    /\ UNCHANGED <<life, bound, retired, pending, delivered, acked, told, intoGone, row, synced, local, intoOld>>

\* One hand-in of message m: the first try (running) or a retry (deferred,
\* after a turn deferred or failed). A retry reads the lifecycle first, except
\* in the reversed witness.
Checked == Broken # "retryunchecked" /\ daemon = "deferred"

HandIn(m) ==
    /\ daemon \in {"running", "deferred"}
    /\ m \in pending
    /\ ~(RowRead /\ Superseded)                         \* the step's row read comes first (RowCheck)
    /\ intoOld' = (intoOld \/ Superseded)
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
    /\ UNCHANGED <<life, bound, retired, proofFor, told, row, synced, local, fetched>>

\* Each step while the target is live: her row, as her beat last answered it,
\* names another session: target-invalid (superseded), told as a gone one.
RowCheck ==
    /\ RowRead
    /\ daemon \in {"running", "deferred"}
    /\ Superseded
    /\ daemon' = "invalid"
    /\ UNCHANGED <<life, bound, retired, proofFor, pending, delivered, acked, told, intoGone, row, synced, local, intoOld, fetched>>

\* friend sync carries the row to her friends-table entry, which her beat answers.
Sync ==
    /\ synced # row
    /\ synced' = row
    /\ UNCHANGED <<life, bound, retired, proofFor, daemon, pending, delivered, acked, told, intoGone, row, local, intoOld, fetched>>

\* tellInvalid: one blocker to the coordinator and one NOTE to the friend.
Tell ==
    /\ daemon = "invalid"
    /\ told = 0
    /\ told' = 1
    /\ UNCHANGED <<life, bound, retired, proofFor, daemon, pending, delivered, acked, intoGone, row, synced, local, intoOld, fetched>>

\* nova-friend rebind, or install with a new --session: the new target read
\* live, recorded on her row, the old one retired, the proof down, a fresh
\* check owed.
Rebind(t) ==
    /\ t # bound
    /\ life[t] = "live"
    /\ row' = t
    /\ local' = TRUE
    /\ bound' = t
    /\ retired' = (retired \cup {bound}) \ {t}
    /\ told' = 0
    /\ fetched' = FALSE
    /\ IF Broken = "rebindkeepsproof"
       THEN /\ daemon' = "running"
            /\ UNCHANGED proofFor
       ELSE /\ daemon' = "proving"
            /\ proofFor' = "none"
    /\ UNCHANGED <<life, pending, delivered, acked, intoGone, synced, intoOld>>

\* A reinstall from an old command line naming a retired target is refused:
\* nothing changes (run and install read target.json first).
Resurrect(t) ==
    /\ local
    /\ t \in retired
    /\ UNCHANGED vars

\* A service reinstalled elsewhere (another state directory, whose target.json
\* never saw the rebind) from an old command line: nothing local refuses it,
\* and it starts proving on t. Only her row stands in its way.
Elsewhere(t) ==
    /\ t # bound
    /\ bound' = t
    /\ local' = FALSE
    /\ daemon' = "proving"
    /\ proofFor' = "none"
    /\ told' = 0
    /\ fetched' = FALSE
    /\ UNCHANGED <<life, retired, pending, delivered, acked, intoGone, row, synced, intoOld>>

Next ==
    \/ \E t \in Targets : Gone(t) \/ Revive(t) \/ Rebind(t) \/ Resurrect(t) \/ Elsewhere(t)
    \/ Fetch
    \/ Prove
    \/ \E m \in Msgs : HandIn(m)
    \/ RowCheck
    \/ Sync
    \/ Tell

Spec == Init /\ [][Next]_vars /\ WF_vars(Tell) /\ WF_vars(Fetch) /\ WF_vars(Prove)

\* No retry is handed into a session that is gone.
NeverIntoGone == ~intoGone

\* The daemon runs only on a proof through the session it names.
RunsOnItsOwnProof == daemon \in {"running", "deferred"} => proofFor = bound

\* No message is acked that was not delivered.
AckedOnlyDelivered == \A m \in acked : \E t \in Targets : <<m, t>> \in delivered

\* A retired target is never the bound one on the machine that retired it.
RetiredNeverBound == local => bound \notin retired

\* Nothing is handed into a session her row, as her beat answers it, does not
\* name: a reinstall elsewhere cannot resurrect an old id past the row.
NeverIntoSuperseded == ~intoOld

\* A proof through a session exists only after this run fetched the managed row.
ProofFollowsTheRow == proofFor = "none" \/ fetched

\* Every invalidation is told, unless a rebind came first.
InvalidIsTold == (daemon = "invalid") ~> (told = 1 \/ daemon # "invalid")
=============================================================================
