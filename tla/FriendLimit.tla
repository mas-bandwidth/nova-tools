---------------------------- MODULE FriendLimit -----------------------------
EXTENDS Naturals, FiniteSets
CONSTANTS MaxClock, MaxEpisodes, MaxNonces, Rest, Broken
VARIABLE s
vars == <<s>>

\* One batch deliverer and one beat loop, derived from internal/friend/limit.go.
\* State fits one record; hooks are atomic observations of see/Refuse. Parsing,
\* pacing and one-shot lanes are outside this model. The bounded environment
\* permits two episodes and three fresh wake nonces. For liveness it eventually
\* stops introducing limits and lets the last wake answer successfully; failed
\* or unanswered earlier wakes may return and retry. No fairness is owed by a
\* real absent session: without those assumptions the limit may persist forever.
Init == s = [clock |-> 0, limited |-> FALSE, until |-> 0,
 episodes |-> 0, waking |-> 0, answered |-> FALSE, judged |-> FALSE,
 downs |-> 0, unreads |-> 0, holdUnread |-> 0, kind |-> 0, reason |-> 0,
 used |-> 0, busy |-> FALSE, wakeEpisode |-> 0, answerNonce |-> 0,
 looked |-> "idle", sending |-> FALSE, early |-> FALSE,
 wrongUp |-> FALSE, clockUp |-> FALSE, lateDown |-> FALSE]

TypeOK == /\ s.clock \in 0..MaxClock /\ s.until \in 0..MaxClock
 /\ s.episodes \in 0..MaxEpisodes /\ s.downs \in 0..MaxEpisodes
 /\ s.unreads \in 0..MaxEpisodes /\ s.holdUnread \in 0..MaxEpisodes
 /\ s.used \in 0..MaxNonces /\ s.waking \in 0..MaxNonces
 /\ s.answerNonce \in 0..MaxNonces /\ s.wakeEpisode \in 0..MaxEpisodes
 /\ s.kind \in 0..2 /\ s.reason \in 0..2
 /\ s.looked \in {"idle", "down", "up"}
 /\ \A f \in {"limited", "answered", "judged", "busy", "sending",
               "early", "wrongUp", "clockUp", "lateDown"}: s[f] \in BOOLEAN

\* see ignores a repeat reset; unnamed output uses Rest and judges once per hold.
TurnHitsLimit(reset, named) ==
 /\ s.episodes < MaxEpisodes /\ s.used < MaxNonces
 /\ reset \in 1..MaxClock
 /\ (named \/ reset = s.clock + Rest)
 /\ IF s.limited /\ reset = s.until THEN UNCHANGED s
    ELSE s' = [s EXCEPT !.limited = TRUE, !.until = reset,
      !.episodes = @ + 1, !.downs = @ + 1, !.kind = 1, !.reason = 1,
      !.waking = 0, !.answered = FALSE,
      !.judged = @ \/ ~named,
      !.unreads = @ + IF ~named /\ ~s.judged THEN 1 ELSE 0,
      !.holdUnread = @ + IF ~named /\ ~s.judged THEN 1 ELSE 0]

\* Refuse deduplicates kind and reason, even if its supplied reset changes.
Refuse(kind, reason, reset) ==
 /\ ~s.busy /\ s.episodes < MaxEpisodes /\ s.used < MaxNonces
 /\ IF s.limited /\ s.kind = kind /\ s.reason = reason
    THEN UNCHANGED s
    ELSE s' = [s EXCEPT !.limited = TRUE, !.until = reset,
      !.episodes = @ + 1, !.downs = @ + 1, !.kind = kind, !.reason = reason,
      !.waking = 0, !.answered = FALSE]

DeliverBeforeReset ==
 /\ ~s.busy /\ s.limited /\ s.clock < s.until
 /\ UNCHANGED s \* Deferred; neither the wake nor the batch payload runs.
Deliver == /\ ~s.busy /\ ~s.limited
 /\ s' = [s EXCEPT !.early = @ \/ (s.limited /\ s.clock < s.until)]
WakeSent == /\ ~s.busy /\ s.limited /\ s.clock >= s.until
 /\ s.used < MaxNonces
 /\ s' = [s EXCEPT !.used = @ + 1, !.waking = s.used + 1,
    !.answered = FALSE, !.answerNonce = 0, !.busy = TRUE,
    !.wakeEpisode = s.episodes, !.early = @ \/ s.clock < s.until]
SessionAnswers(nonce) == /\ s.busy /\ s.waking # 0 /\ nonce \in 1..s.used
 /\ IF nonce = s.waking \/ Broken = "StaleNonce"
    THEN s' = [s EXCEPT !.answered = TRUE, !.answerNonce = nonce]
    ELSE UNCHANGED s
WakeHitsLimitAgain == /\ s.busy
 /\ \E reset \in 1..MaxClock, named \in BOOLEAN: TurnHitsLimit(reset, named)

\* A successful return requires the current episode as well as the answer.
\* beatMu excludes a looked-down beat until its send has completed.
WakeEnds == /\ s.busy /\ s.answered /\ s.wakeEpisode = s.episodes
 /\ (Broken = "BeatUnlocked" \/ s.looked # "down")
 /\ s' = [s EXCEPT !.limited = FALSE, !.waking = 0, !.judged = FALSE,
    !.holdUnread = 0, !.busy = FALSE,
    !.wrongUp = @ \/ s.answerNonce # s.waking]
\* An error/nonzero exit, no answer, or a newer episode keeps the limit.
\* The last bounded nonce is reserved for the eventually responsive session.
WakeDeferred == /\ s.busy
 /\ (s.used < MaxNonces \/ s.wakeEpisode # s.episodes)
 /\ s' = [s EXCEPT !.busy = FALSE]
BeatLook == /\ s.looked = "idle"
 /\ s' = [s EXCEPT !.looked = IF s.limited THEN "down" ELSE "up",
                   !.sending = TRUE]
BeatSendDown == /\ s.looked = "down" /\ s.sending
 /\ s' = [s EXCEPT !.looked = "idle", !.sending = FALSE,
                   !.lateDown = @ \/ ~s.limited]
BeatSendUp == /\ s.looked = "up" /\ s.sending
 /\ s' = [s EXCEPT !.looked = "idle", !.sending = FALSE]
Tick == /\ s.clock < MaxClock
 /\ s' = [s EXCEPT !.clock = @ + 1,
    !.limited = IF Broken = "UpOnClock" /\ s.limited /\ s.clock + 1 >= s.until
                THEN FALSE ELSE @,
    !.clockUp = @ \/ (Broken = "UpOnClock" /\ s.limited /\ s.clock + 1 >= s.until)]
Next == (\E reset \in 1..MaxClock, named \in BOOLEAN:
           ~s.busy /\ TurnHitsLimit(reset, named))
 \/ (\E kind, reason \in 1..2, reset \in 1..MaxClock: Refuse(kind, reason, reset))
 \/ DeliverBeforeReset \/ Deliver \/ WakeSent
 \/ (\E nonce \in 1..MaxNonces: SessionAnswers(nonce))
 \/ WakeHitsLimitAgain \/ WakeEnds \/ WakeDeferred
 \/ BeatLook \/ BeatSendDown \/ BeatSendUp \/ Tick

DownOncePerEpisode == s.downs = s.episodes
NoDeliveryBeforeReset == ~s.early
UpOnlyOnCurrentWakeNonce == ~s.wrongUp
NoDownBeatAfterUp == ~s.lateDown
UnreadOncePerHold == s.holdUnread <= 1
NeverUpByClock == ~s.clockUp
LimitEnds == s.limited ~> ~s.limited
Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(WakeSent)
 /\ WF_vars(SessionAnswers(s.waking)) /\ SF_vars(WakeEnds)
 /\ WF_vars(WakeDeferred) /\ WF_vars(BeatSendDown) /\ WF_vars(BeatSendUp)
=============================================================================
