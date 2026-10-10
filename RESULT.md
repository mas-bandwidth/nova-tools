head: f9e017ce3cd8d0d7b305ed1665c1ddedd10f6fd2
branch: sprint/presence-batch-turn-is-modelled-before-5540-lands.w3.g1.e15
verdict: not-done
gate: ->
output: ->
report: model work present in FriendPresence.tla and MCFriendPresence*.cfg but TLC verification blocked: requires Linux bench to run tlacheck for friendpresence group

## Body
TLC models present and witness configs in place (MCFriendPresenceBrokenNoCap.cfg, MCFriendPresenceBrokenBatter.cfg), but no run records in tla/RUNS.tsv because tlacheck requires Linux bench. Go tests cannot run due to inaccessible go binary (/home/nova/go/bin/go).
