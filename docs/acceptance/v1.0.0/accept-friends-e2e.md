# accept-friends-e2e

Verdict: FAIL

PASS, verbatim: the E2E probe (bus message -> turn in the open chat -> reply) passes for every friend not held, 10 of 10, for 2 hours; presence matches each friend's own count.

Window start: 2026-10-05T18:14:04Z
Window end: 2026-10-05T20:14:04Z
Done stamp: 2026-10-05T20:14:10Z

Build measured: nova-sprint v1.2.0-dev.d8b7e04, nova-bus v1.2.0-dev.d8b7e04.

Commands: a sampler under the job's scratch window, ten rounds, one every 12 minutes. Each round read `nova-sprint where` into friends.tsv and the dashboard at 127.0.0.1:7390 into dash-friends.tsv, then sent one nova-bus probe to each friend the round did not skip. A reply inside 10 minutes is `seen`. No reply is `timeout`. A name the bus refused is `send-failed`. The sampler did not call 100.76.29.55:6390.

## Replies

Bar: 10 of 10 replies inside 10 minutes for every friend whose where status is not held.

- emma: 10 seen, 0 timeout. Passes the reply bar.
- johnny: 10 seen, 0 timeout. Passes the reply bar.
- stella: 10 seen, 0 timeout. Passes the reply bar.
- freddy: 0 seen, 10 timeout. Fails. He was up on the dashboard in rounds 1, 5, and 10, and where listed him up in rounds 5 and 10 (working 0, then 1).
- rowan: 0 seen, 4 timeout, then not probed. Rounds 1 through 4 where listed him down and the dashboard listed him up (working 6 in round 1). Rounds 5 through 10 where listed him held, so the sampler skipped him. The reply bar fails for the four rounds he was not held.

Alex and zhi were held in all 10 rounds and were not required.

rowan-mas, rowan-next, rowan-personal, and rowan-space were send-failed in every round. They are not nova-bus names. Those refusals are not missing replies. rowan-space was up in where (working 6, then 0, then 16) and still could not be sent a bus probe.

## Presence

Bar: presence matches each friend's own count.

Round 1, where against the dashboard: emma down/0 against up/10, freddy down/0 against up/5, rowan down/0 against up/6, johnny up/5 against up/8, stella up/0 against up/2. Alex and zhi matched, held/0.

Round 5: rowan where held/1 against dashboard up/6. emma where up/0 against up/10. johnny up/4 against up/8. stella up/0 against up/2. freddy up/0 against up/5.

Round 10: where and the dashboard agreed for emma up/8, freddy up/1, johnny up/8, rowan held/0, stella up/2, alex held/0, zhi held/0.

The working count inside each reply body was not scored against the table.

## What was not measured

No daemon was installed and no launch agent was loaded. The friend's own count inside the reply was not compared to where. The four send-failed names were not retried under another address.
