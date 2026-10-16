---------------------------- MODULE ClaudeHookNotice ----------------------------
\* The Claude Code route of nova-hook (cmd/nova-hook/claude.go,
\* pkg/harness/notice/claude.go, claude_hooks.go): how a notice, and the
\* heartbeat challenge it may hold, reaches the model of one Claude Code
\* session, busy or idle, and what the session's own hooks may do about it.
\* Only the model proves; no hook, watcher or script does.
\*
\* note      the pending notice (0: none); notes counts the notices made
\* noteShown the newest notice a watcher printed (PUSH, or PENDING at arming)
\*           or a hook put in front of the model: the delivery the watcher
\*           records in the state directory
\* noteBlock the notices the Stop hook has blocked a stop for (once each)
\* nonce     the newest challenge the heartbeat issued (0: none yet)
\* pend      the challenge the pending notice holds (0: none). The Core
\*           rewrites the notice file in place when one arrives (Fold).
\* shown     the newest challenge put in front of the model
\* surfaced  the challenge PostToolUse last surfaced (once per nonce)
\* proved    the newest challenge the model proved (nova-hook prove records
\*           it in the claude state directory, which the hooks read)
\* blocked   the challenges the Stop hook has blocked a stop for (once each)
\* nagged    the Stop hook has blocked once for a missing watcher since the
\*           watcher was last armed
\* gen, seen the generation file, and its value when the notice was pushed
\* turn      "busy" or "idle"
\* watch     a watcher (the session's Monitor running --claude-watch) holds
\*           the watch lock
\*
\* Broken names the reversed witness each failing configuration runs:
\*   "noblock"       the Stop hook never blocks for an unproved challenge
\*   "blockproved"   the Stop hook blocks for a challenge already proved
\*   "childadvances" a headless claude child's Stop advances the generation
\*   "nowatchnag"    the Stop hook lets the session idle with no watcher
\*   "noreplay"      the Stop hook ends a turn over a notice no one showed
EXTENDS Naturals, FiniteSets
CONSTANTS MaxN, MaxNotes, MaxGen, Broken
VARIABLES note, notes, noteShown, noteBlock, nonce, pend, shown, surfaced, proved, blocked, nagged, gen, seen, turn, watch

vars == <<note, notes, noteShown, noteBlock, nonce, pend, shown, surfaced, proved, blocked, nagged, gen, seen, turn, watch>>

TypeOK ==
    /\ note \in 0..MaxNotes /\ notes \in 0..MaxNotes /\ noteShown \in 0..MaxNotes
    /\ noteBlock \subseteq 1..MaxNotes
    /\ nonce \in 0..MaxN /\ pend \in 0..MaxN /\ shown \in 0..MaxN
    /\ surfaced \in 0..MaxN /\ proved \in 0..MaxN
    /\ blocked \subseteq 1..MaxN /\ nagged \in BOOLEAN
    /\ gen \in 0..MaxGen /\ seen \in 0..MaxGen
    /\ turn \in {"busy", "idle"} /\ watch \in BOOLEAN

Init ==
    /\ note = 0 /\ notes = 0 /\ noteShown = 0 /\ noteBlock = {}
    /\ nonce = 0 /\ pend = 0 /\ shown = 0 /\ surfaced = 0 /\ proved = 0
    /\ blocked = {} /\ nagged = FALSE
    /\ gen = 0 /\ seen = 0
    /\ turn = "busy" /\ watch = FALSE

Unproved(n) == n # 0 /\ n > proved

\* A push reaches the model only through an armed watcher, which records the
\* notice as shown; it wakes an idle session (the Monitor's notification).
Told(id, n) ==
    IF watch THEN /\ noteShown' = id
                  /\ shown' = IF n # 0 THEN n ELSE shown
                  /\ turn' = "busy"
             ELSE UNCHANGED <<noteShown, shown, turn>>

\* A new notice: saved, named pending and pushed (rule 1, one at a time).
Make(n) ==
    /\ notes < MaxNotes
    /\ notes' = notes + 1
    /\ note' = notes + 1
    /\ seen' = gen
    /\ Told(notes + 1, n)

\* An ordinary message: a new notice when none is pending, else folded into
\* the pending one's count.
Msg ==
    /\ IF note = 0
         THEN /\ Make(0)
              /\ UNCHANGED pend
         ELSE UNCHANGED <<notes, note, seen, noteShown, shown, turn, pend>>
    /\ UNCHANGED <<noteBlock, nonce, surfaced, proved, blocked, nagged, gen, watch>>

\* The heartbeat issues a challenge: a new notice holding it, or the pending
\* notice's file rewritten in place to hold it (Fold).
Issue ==
    /\ nonce < MaxN
    /\ nonce' = nonce + 1
    /\ pend' = nonce + 1
    /\ IF note = 0
         THEN Make(nonce + 1)
         ELSE UNCHANGED <<notes, note, seen, noteShown, shown, turn>>
    /\ UNCHANGED <<noteBlock, surfaced, proved, blocked, nagged, gen, watch>>

\* Rule 3a: the pending notice now holds a newer challenge than it showed, so
\* the Core pushes it again (same notice).
Repush ==
    /\ note # 0 /\ Unproved(pend) /\ pend # shown
    /\ Told(note, pend)
    /\ UNCHANGED <<note, notes, noteBlock, nonce, pend, surfaced, proved, blocked, nagged, gen, seen, watch>>

\* The Core observes consumption: a proof (the model's act on the bus), or a
\* turn boundary since the push. The notice ends (Clear removes the pending
\* name) and the challenge it showed goes with it (notice.go settle); a newer
\* challenge folded in behind it is pushed in a new notice.
Settle ==
    /\ note # 0
    /\ \/ (pend # 0 /\ proved >= pend)
       \/ gen > seen
    /\ IF pend = 0 \/ proved >= pend \/ pend = shown
         THEN /\ note' = 0
              /\ pend' = 0
              /\ UNCHANGED <<notes, seen, noteShown, shown, turn>>
         ELSE /\ Make(pend)
              /\ UNCHANGED pend
    /\ UNCHANGED <<noteBlock, nonce, surfaced, proved, blocked, nagged, gen, watch>>

\* PostToolUse, in a busy turn: an unproved pending challenge not yet surfaced
\* goes into the turn as additional context, once.
Tool ==
    /\ turn = "busy"
    /\ note # 0 /\ Unproved(pend) /\ surfaced # pend
    /\ surfaced' = pend
    /\ shown' = pend
    /\ UNCHANGED <<note, notes, noteShown, noteBlock, nonce, pend, proved, blocked, nagged, gen, seen, turn, watch>>

\* The model proves the newest challenge it was shown.
Prove ==
    /\ turn = "busy"
    /\ shown > proved
    /\ proved' = shown
    /\ UNCHANGED <<note, notes, noteShown, noteBlock, nonce, pend, shown, surfaced, blocked, nagged, gen, seen, turn, watch>>

\* The model arms the watcher in a busy turn. Arming prints the pending
\* notice (HOOK PENDING): the log is read from its end, so a notice pushed
\* before it would otherwise never be shown.
Arm ==
    /\ turn = "busy" /\ ~watch
    /\ watch' = TRUE
    /\ nagged' = FALSE
    /\ noteShown' = IF note # 0 THEN note ELSE noteShown
    /\ shown' = IF note # 0 /\ pend # 0 THEN pend ELSE shown
    /\ UNCHANGED <<note, notes, noteBlock, nonce, pend, surfaced, proved, blocked, gen, seen, turn>>

\* The Monitor expires (or dies). Its expiry is itself a notification, so an
\* idle session wakes to re-arm it.
Expire ==
    /\ watch
    /\ watch' = FALSE
    /\ turn' = "busy"
    /\ UNCHANGED <<note, notes, noteShown, noteBlock, nonce, pend, shown, surfaced, proved, blocked, nagged, gen, seen>>

BlockFor(n) ==
    /\ Broken # "noblock"
    /\ n \notin blocked
    /\ (Unproved(n) \/ Broken = "blockproved")

Unseen == note # 0 /\ noteShown # note /\ note \notin noteBlock /\ Broken # "noreplay"

\* The session's Stop hook. It blocks the stop once for a pending challenge
\* not proved, naming it; then once for a pending notice no watcher showed,
\* or for a missing watcher, naming both; else the turn ends and the
\* generation advances.
SessionStop ==
    /\ turn = "busy"
    /\ gen < MaxGen
    /\ IF note # 0 /\ pend # 0 /\ BlockFor(pend)
         THEN /\ blocked' = blocked \cup {pend}
              /\ shown' = pend
              /\ noteShown' = note
              /\ UNCHANGED <<noteBlock, nagged, gen, turn>>
       ELSE IF Unseen \/ (~watch /\ ~nagged /\ Broken # "nowatchnag")
         THEN /\ noteBlock' = IF note # 0 THEN noteBlock \cup {note} ELSE noteBlock
              /\ noteShown' = IF note # 0 /\ Broken # "noreplay" THEN note ELSE noteShown
              /\ nagged' = (nagged \/ ~watch)
              /\ UNCHANGED <<blocked, shown, gen, turn>>
       ELSE /\ gen' = gen + 1
            /\ turn' = "idle"
            /\ UNCHANGED <<blocked, shown, nagged, noteShown, noteBlock>>
    /\ UNCHANGED <<note, notes, nonce, pend, surfaced, proved, seen, watch>>

\* A headless claude child (a lane's card, a read, the harness proof) whose
\* config directory holds the session's Stop hook ends a turn of its own.
ChildStop ==
    /\ gen < MaxGen
    /\ IF Broken = "childadvances" THEN gen' = gen + 1 ELSE UNCHANGED gen
    /\ UNCHANGED <<note, notes, noteShown, noteBlock, nonce, pend, shown, surfaced, proved, blocked, nagged, seen, turn, watch>>

\* SessionStart (a restart, resume or compaction): monitors died with the old
\* process; the hook puts the pending notice and the arm line in front of the
\* model.
Start ==
    /\ watch' = FALSE
    /\ turn' = "busy"
    /\ noteShown' = IF note # 0 THEN note ELSE noteShown
    /\ shown' = IF note # 0 /\ pend # 0 THEN pend ELSE shown
    /\ nagged' = FALSE
    /\ UNCHANGED <<note, notes, noteBlock, nonce, pend, surfaced, proved, blocked, gen, seen>>

Next == Msg \/ Issue \/ Repush \/ Settle \/ Tool \/ Prove \/ Arm \/ Expire \/ SessionStop \/ ChildStop \/ Start

Spec == Init /\ [][Next]_vars

GoesIdle == turn = "busy" /\ turn' = "idle"

\* The session never goes idle with an unproved pending challenge the model
\* was not shown, while its watcher is armed. noblock violates it.
IdleKnows ==
    [][ (GoesIdle /\ watch' /\ note' # 0 /\ Unproved(pend')) => shown' = pend' ]_vars

\* The session never ends a turn (which consumes the notice) over a pending
\* notice never put in front of the model. noreplay violates it.
NoticeSeen ==
    [][ (GoesIdle /\ note' # 0) => noteShown' = note' ]_vars

\* The session idles with no watcher only after one block that told it so.
\* nowatchnag violates it.
IdleArmed ==
    turn = "idle" => (watch \/ nagged)

\* A child's end is no turn of the session's. childadvances violates it.
ChildNoBoundary ==
    [][ ChildStop => gen' = gen ]_vars

\* The Stop hook blocks only for a challenge the model has not proved, and
\* once per challenge. blockproved violates it.
BlockOnlyUnproved ==
    [][ (blocked' # blocked) => \A n \in blocked' \ blocked : n > proved ]_vars

=============================================================================
