------------------------------ MODULE LandingIssues ------------------------------
\* A landing closes the card's issues (docs/SPEC-SPRINT.md section 7, "A landing
\* closes the card's issues"; internal/sprint/land_issues.go, the closer, its
\* lease and its record step; cmd/nova-sprint/land_issues.go, the lander's pass
\* and the server loop's retry).
\*
\* Two landed cards are twins over one issue: c1 references i1 and i2, c2
\* references i2 and i3, and i3 was closed on the forge by a person before
\* anything here ran. Two closers run: the lander's pass as a landing ends (k1)
\* and the server loop's (k2), in two processes. A closer reads an issue's state
\* and then closes it, two calls with the other closer free to run between them.
\* The forge can be unreachable, and a closer's process can die anywhere, its
\* lease then lapsing (IssuesLease).
\*
\* State:
\*   landed     -- the merge step moved the card to landed (FieldIssues written)
\*   lease      -- PropIssuesCloser: the closer holding the one pass, or none
\*   seen       -- issues a closer read open and has not closed yet
\*   inflight   -- issues a closer's pass closed or noted, not yet written on
\*                 the card (CloseLandedIssues returns them; IssuesClosed writes)
\*   recorded   -- FieldIssuesClosed: closed by this card's landing, or found
\*                 closed and left alone
\*   why        -- FieldIssuesWhy: the last try was refused; the next waits for
\*                 IssuesRetry
\*
\* Mapped to the code:
\*   Land       = mergeStep writing LandingIssues on the card; it reads nothing
\*                of the forge, so a forge that is down never holds a landing
\*   Acquire    = IssuesLeaseTake, planned on the work table as the queue
\*                leaves it: refused while another closer's lease is live
\*   Read       = one issue of CloseLandedIssues: noted when a landed card has
\*                it closed (closedBy), else github.Closer.Close's first call,
\*                the issue's state (a closed one is left alone: already)
\*   Close      = Close's second call, the close with the one comment
\*   Record     = IssuesClosed, the store's write of the pass for one card
\*   Release    = IssuesLeaseGive, as the pass ends
\*   Retry      = IssuesRetry passing, the tick's closer trying the card again
\*   Crash      = a closer's process dies; its lease lapses
\*
\* Mode chooses the closer:
\*   "ok"        -- the code
\*   "nocheck"   -- Close posts its comment without reading the issue's state:
\*                  a close the crash left unrecorded comments twice, and the
\*                  issue a person closed gets a comment (OneComment)
\*   "errdone"   -- a refused close is written as closed (RecordedIsClosed)
\*   "noretry"   -- a refused close is never tried again (AllClose)
\*   "nolease"   -- the two closers run with no lease: both read an issue open
\*                  and both comment (OneComment)

EXTENDS Naturals, FiniteSets

CONSTANTS Mode, MaxDowns, MaxCrashes

Cards     == {"c1", "c2"}
Issues    == {"i1", "i2", "i3"}
Closers   == {"k1", "k2"}
Refs      == [c1 |-> {"i1", "i2"}, c2 |-> {"i2", "i3"}]
PreClosed == {"i3"}

VARIABLES
  landed,    \* the card is landed
  forge,     \* each issue's state on the forge
  comments,  \* the comments the closers left on each issue
  lease,     \* the closer holding the lease, or "none"
  did,       \* the closer has read since it took the lease
  seen,      \* read open by the closer, its close not yet asked
  inflight,  \* closed or noted by the closer's pass, not yet written on the card
  recorded,  \* FieldIssuesClosed
  why,       \* FieldIssuesWhy is set: a refused close waits for its retry
  up,        \* the forge answers
  downs,     \* times the forge went away
  crashes    \* times a closer died

vars == <<landed, forge, comments, lease, did, seen, inflight, recorded, why, up, downs, crashes>>

TypeOK ==
  /\ landed \in [Cards -> BOOLEAN]
  /\ forge \in [Issues -> {"open", "closed"}]
  /\ comments \in [Issues -> 0..3]
  /\ lease \in Closers \cup {"none"}
  /\ did \in [Closers -> BOOLEAN]
  /\ seen \in [Closers -> [Cards -> SUBSET Issues]]
  /\ inflight \in [Closers -> [Cards -> SUBSET Issues]]
  /\ recorded \in [Cards -> SUBSET Issues]
  /\ why \in [Cards -> BOOLEAN]
  /\ up \in BOOLEAN
  /\ downs \in 0..MaxDowns
  /\ crashes \in 0..MaxCrashes

Init ==
  /\ landed = [c \in Cards |-> FALSE]
  /\ forge = [i \in Issues |-> IF i \in PreClosed THEN "closed" ELSE "open"]
  /\ comments = [i \in Issues |-> 0]
  /\ lease = "none"
  /\ did = [k \in Closers |-> FALSE]
  /\ seen = [k \in Closers |-> [c \in Cards |-> {}]]
  /\ inflight = [k \in Closers |-> [c \in Cards |-> {}]]
  /\ recorded = [c \in Cards |-> {}]
  /\ why = [c \in Cards |-> FALSE]
  /\ up = TRUE
  /\ downs = 0
  /\ crashes = 0

\* the closer may work: it holds the lease (in "nolease", always)
Holds(k) == Mode = "nolease" \/ lease = k

\* what closer k reads as closed by a landed card: the store's, and its own pass's
ClosedBy(k) == UNION {recorded[c] \cup inflight[k][c] : c \in Cards}

Pending(c) == landed[c] /\ ~why[c] /\ Refs[c] \ recorded[c] # {}

Idle(k) == \A c \in Cards : seen[k][c] = {} /\ inflight[k][c] = {}

Land(c) ==
  /\ ~landed[c]
  /\ landed' = [landed EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<forge, comments, lease, did, seen, inflight, recorded, why, up, downs, crashes>>

Acquire(k) ==
  /\ Mode # "nolease"
  /\ lease = "none"
  /\ \E c \in Cards : Pending(c)
  /\ lease' = k
  /\ did' = [did EXCEPT ![k] = FALSE]
  /\ UNCHANGED <<landed, forge, comments, seen, inflight, recorded, why, up, downs, crashes>>

Note(k, c, i) == inflight' = [inflight EXCEPT ![k][c] = @ \cup {i}]

Read(k, c, i) ==
  /\ Holds(k)
  /\ \A d \in Cards : seen[k][d] = {}  \* a pass asks one issue at a time
  /\ landed[c]
  /\ ~why[c]
  /\ i \in Refs[c] \ (recorded[c] \cup inflight[k][c] \cup seen[k][c])
  /\ did' = [did EXCEPT ![k] = TRUE]
  /\ IF i \in ClosedBy(k)
       THEN /\ Note(k, c, i)
            /\ UNCHANGED <<seen, why>>
     ELSE IF ~up /\ Mode # "nocheck"
       THEN IF Mode = "errdone"
              THEN /\ Note(k, c, i)
                   /\ UNCHANGED <<seen, why>>
              ELSE /\ why' = [why EXCEPT ![c] = TRUE]
                   /\ UNCHANGED <<seen, inflight>>
     ELSE IF forge[i] = "closed" /\ Mode # "nocheck"
       THEN /\ Note(k, c, i)
            /\ UNCHANGED <<seen, why>>
     ELSE /\ seen' = [seen EXCEPT ![k][c] = @ \cup {i}]
          /\ UNCHANGED <<inflight, why>>
  /\ UNCHANGED <<landed, forge, comments, lease, recorded, up, downs, crashes>>

Close(k, c, i) ==
  /\ i \in seen[k][c]
  /\ seen' = [seen EXCEPT ![k][c] = @ \ {i}]
  /\ IF ~up
       THEN IF Mode = "errdone"
              THEN /\ Note(k, c, i)
                   /\ UNCHANGED <<forge, comments, why>>
              ELSE /\ why' = [why EXCEPT ![c] = TRUE]
                   /\ UNCHANGED <<forge, comments, inflight>>
       ELSE /\ forge' = [forge EXCEPT ![i] = "closed"]
            /\ comments' = [comments EXCEPT ![i] = @ + 1]
            /\ Note(k, c, i)
            /\ UNCHANGED why
  /\ UNCHANGED <<landed, lease, did, recorded, up, downs, crashes>>

Record(k, c) ==
  /\ seen[k][c] = {}
  /\ inflight[k][c] # {}
  /\ recorded' = [recorded EXCEPT ![c] = @ \cup inflight[k][c]]
  /\ inflight' = [inflight EXCEPT ![k][c] = {}]
  /\ UNCHANGED <<landed, forge, comments, lease, did, seen, why, up, downs, crashes>>

Release(k) ==
  /\ lease = k
  /\ did[k]
  /\ Idle(k)
  /\ lease' = "none"
  /\ UNCHANGED <<landed, forge, comments, did, seen, inflight, recorded, why, up, downs, crashes>>

Retry(c) ==
  /\ Mode # "noretry"
  /\ why[c]
  /\ why' = [why EXCEPT ![c] = FALSE]
  /\ UNCHANGED <<landed, forge, comments, lease, did, seen, inflight, recorded, up, downs, crashes>>

Crash(k) ==
  /\ crashes < MaxCrashes
  /\ ~Idle(k) \/ lease = k
  /\ seen' = [seen EXCEPT ![k] = [c \in Cards |-> {}]]
  /\ inflight' = [inflight EXCEPT ![k] = [c \in Cards |-> {}]]
  /\ lease' = IF lease = k THEN "none" ELSE lease
  /\ crashes' = crashes + 1
  /\ UNCHANGED <<landed, forge, comments, did, recorded, why, up, downs>>

ForgeDown ==
  /\ up /\ downs < MaxDowns
  /\ up' = FALSE
  /\ downs' = downs + 1
  /\ UNCHANGED <<landed, forge, comments, lease, did, seen, inflight, recorded, why, crashes>>

ForgeUp ==
  /\ ~up
  /\ up' = TRUE
  /\ UNCHANGED <<landed, forge, comments, lease, did, seen, inflight, recorded, why, downs, crashes>>

Progress ==
  \/ \E c \in Cards : Land(c) \/ Retry(c)
  \/ \E k \in Closers : Acquire(k) \/ Release(k)
  \/ \E k \in Closers, c \in Cards : Record(k, c) \/ \E i \in Issues : Read(k, c, i) \/ Close(k, c, i)

Next == Progress \/ ForgeDown \/ ForgeUp \/ \E k \in Closers : Crash(k)

Spec == Init /\ [][Next]_vars /\ WF_vars(Progress) /\ WF_vars(ForgeUp)

\* one comment on an issue a landing closed, none on one a person closed first
OneComment == \A i \in Issues : comments[i] <= IF i \in PreClosed THEN 0 ELSE 1

\* what a card says is closed is closed on the forge
RecordedIsClosed == \A c \in Cards : \A i \in recorded[c] : forge[i] = "closed"

\* only a landing closes an issue, and only one its card references
ClosedOnlyByLanding ==
  \A i \in Issues \ PreClosed :
    forge[i] = "closed" => \E c \in Cards : landed[c] /\ i \in Refs[c]

\* a card records only its own issues, and only once landed
RecordedIsReferenced == \A c \in Cards : recorded[c] \subseteq Refs[c] /\ (recorded[c] # {} => landed[c])

\* every card lands, and every issue it references ends recorded as closed
AllClose == <>(\A c \in Cards : landed[c] /\ Refs[c] \subseteq recorded[c])
=============================================================================
