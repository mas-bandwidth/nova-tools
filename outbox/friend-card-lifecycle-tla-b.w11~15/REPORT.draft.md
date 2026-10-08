Verdict: HOLD
Head: f44c8b261e8f5e0e3d9a6f6f3f3b4c7f5e1d2c3b

Fixed FriendCard.tla model issues per reader findings:
- Deliver now checks ~archived[holder[c]]
- Tick preserves daemonAge for dead daemons
- TakeBack requires goneAge >= DeadRunBound for dead runs
- Updated MCFriendCard.cfg to Bound=2 (two-tick limits)
- Added three counterexample witness cfgs (BeatUp, AnyTier, Lanes)

TLC records stale: requires bench with tla2tools.jar to run and refresh RUNS.tsv.
