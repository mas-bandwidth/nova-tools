------------------------------ MODULE LandingIssues ------------------------------
\* A landing closes the card's issues (docs/SPEC-SPRINT.md section 7, "A landing
\* closes the card's issues"; internal/sprint/land_issues.go, the closer and its
\* record step; cmd/nova-sprint/land_issues.go, the lander's pass and the server
\* loop's retry).
\*
\* Two landed cards are twins over one issue: c1 references i1 and i2, c2
\* references i2 and i3, and i3 was closed on the forge by a person before
\* anything here ran. The forge can be unreachable, and the closer's process can
\* die between the forge's close and the store's write of it.
\*
\* For each card:
\*   landed     -- the merge step moved it to landed (FieldIssues written then)
\*   inflight   -- issues this pass closed or noted, not yet written on the card
\*                 (CloseLandedIssues returns them; IssuesClosed writes them)
\*   recorded   -- FieldIssuesClosed: closed by this card's landing, or found
\*                 closed and left alone
\*   why        -- FieldIssuesWhy: the last try was refused; the next waits for
\*                 IssuesRetry
\*
\* Mapped to the code:
\*   Land       = mergeStep writing LandingIssues on the card; it reads nothing
\*                of the forge, so a forge that is down never holds a landing
\*   Try        = one issue of CloseLandedIssues: noted when a landed card has
\*                it closed (closedBy), else forge.Closer.Close, which reads the
\*                issue's state first and leaves a closed one alone (already)
\*   Record     = IssuesClosed, the store's write of the pass
\*   Retry      = IssuesRetry passing, the tick's closer trying the card again
\*   Crash      = the process dies between the forge's answer and the write
\*
\* Mode chooses the closer:
\*   "ok"        -- the code
\*   "nocheck"   -- Close posts its comment without reading the issue's state:
\*                  a close the crash left unrecorded comments twice, and the
\*                  issue a person closed gets a comment (OneComment)
\*   "errdone"   -- a refused close is written as closed (RecordedIsClosed)
\*   "noretry"   -- a refused close is never tried again (AllClose)

EXTENDS Naturals, FiniteSets

CONSTANTS Mode, MaxDowns, MaxCrashes

Cards     == {"c1", "c2"}
Issues    == {"i1", "i2", "i3"}
Refs      == [c1 |-> {"i1", "i2"}, c2 |-> {"i2", "i3"}]
PreClosed == {"i3"}

VARIABLES
  landed,    \* the card is landed
  forge,     \* each issue's state on the forge
  comments,  \* the comments the closer left on each issue
  inflight,  \* closed or noted by the pass, not yet written on the card
  recorded,  \* FieldIssuesClosed
  why,       \* FieldIssuesWhy is set: a refused close waits for its retry
  up,        \* the forge answers
  downs,     \* times the forge went away
  crashes    \* times the closer died before its write

vars == <<landed, forge, comments, inflight, recorded, why, up, downs, crashes>>

TypeOK ==
  /\ landed \in [Cards -> BOOLEAN]
  /\ forge \in [Issues -> {"open", "closed"}]
  /\ comments \in [Issues -> 0..3]
  /\ inflight \in [Cards -> SUBSET Issues]
  /\ recorded \in [Cards -> SUBSET Issues]
  /\ why \in [Cards -> BOOLEAN]
  /\ up \in BOOLEAN
  /\ downs \in 0..MaxDowns
  /\ crashes \in 0..MaxCrashes

Init ==
  /\ landed = [c \in Cards |-> FALSE]
  /\ forge = [i \in Issues |-> IF i \in PreClosed THEN "closed" ELSE "open"]
  /\ comments = [i \in Issues |-> 0]
  /\ inflight = [c \in Cards |-> {}]
  /\ recorded = [c \in Cards |-> {}]
  /\ why = [c \in Cards |-> FALSE]
  /\ up = TRUE
  /\ downs = 0
  /\ crashes = 0

\* what the closer reads as closed by a landed card: the store's, and its own pass's
ClosedBy == UNION {recorded[c] \cup inflight[c] : c \in Cards}

Land(c) ==
  /\ ~landed[c]
  /\ landed' = [landed EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<forge, comments, inflight, recorded, why, up, downs, crashes>>

Note(c, i) == inflight' = [inflight EXCEPT ![c] = @ \cup {i}]

Try(c, i) ==
  /\ landed[c]
  /\ ~why[c]
  /\ i \in Refs[c] \ (recorded[c] \cup inflight[c])
  /\ IF i \in ClosedBy
       THEN /\ Note(c, i)
            /\ UNCHANGED <<forge, comments, recorded, why>>
     ELSE IF ~up
       THEN IF Mode = "errdone"
              THEN /\ Note(c, i)
                   /\ UNCHANGED <<forge, comments, recorded, why>>
              ELSE /\ why' = [why EXCEPT ![c] = TRUE]
                   /\ UNCHANGED <<forge, comments, inflight, recorded>>
     ELSE IF forge[i] = "closed" /\ Mode # "nocheck"
       THEN /\ Note(c, i)
            /\ UNCHANGED <<forge, comments, recorded, why>>
     ELSE /\ forge' = [forge EXCEPT ![i] = "closed"]
          /\ comments' = [comments EXCEPT ![i] = @ + 1]
          /\ Note(c, i)
          /\ UNCHANGED <<recorded, why>>
  /\ UNCHANGED <<landed, up, downs, crashes>>

Record(c) ==
  /\ inflight[c] # {}
  /\ recorded' = [recorded EXCEPT ![c] = @ \cup inflight[c]]
  /\ inflight' = [inflight EXCEPT ![c] = {}]
  /\ UNCHANGED <<landed, forge, comments, why, up, downs, crashes>>

Retry(c) ==
  /\ Mode # "noretry"
  /\ why[c]
  /\ why' = [why EXCEPT ![c] = FALSE]
  /\ UNCHANGED <<landed, forge, comments, inflight, recorded, up, downs, crashes>>

Crash ==
  /\ crashes < MaxCrashes
  /\ \E c \in Cards : inflight[c] # {}
  /\ inflight' = [c \in Cards |-> {}]
  /\ crashes' = crashes + 1
  /\ UNCHANGED <<landed, forge, comments, recorded, why, up, downs>>

ForgeDown ==
  /\ up /\ downs < MaxDowns
  /\ up' = FALSE
  /\ downs' = downs + 1
  /\ UNCHANGED <<landed, forge, comments, inflight, recorded, why, crashes>>

ForgeUp ==
  /\ ~up
  /\ up' = TRUE
  /\ UNCHANGED <<landed, forge, comments, inflight, recorded, why, downs, crashes>>

Progress ==
  \E c \in Cards : Land(c) \/ Record(c) \/ Retry(c) \/ \E i \in Issues : Try(c, i)

Next == Progress \/ Crash \/ ForgeDown \/ ForgeUp

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
