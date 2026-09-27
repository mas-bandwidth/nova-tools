# tla: the models of nova-tools' state machines, and their runners

The TLA+ modules here are the specifications of the state machines this repo implements (rowan-new SPEC-COORDINATOR section 8: the backend is the state machine, the verbs are its actions; Glenn 2026-09-27: TLA+ for every state machine, every project). The findings each model produced, verified against the code by hand, are in rowan-new `specs/tla/FINDINGS.md`; the model documents (`TABLE-MODEL.md`, `MEMBER-TABLE-MODEL.md`) are copied here beside the modules they describe.

| Module | Instance | What it is |
|---|---|---|
| `CardMachine.tla` | `MCCardMachine` | the card's life over cells (the copy model of 02_card_move.lua), the corrected design after its three findings |
| `LandWatch.tla` | `MCLandWatch` | the land watch's watcher (land_watch.lua): stamps, slow and wall, one note per stay (findings L1 to L3) |
| `TableMachine.tla` | `MCTable*` | nova-table as table.lua is today at f7745885, with its actual gaps (Stella; the strict gate fails on purpose) |
| `MemberTable.tla`, `EpochMemberTable.tla` | `MCMember*`, `MCEpochMember*` | the corrected member placement and epoch protocol (Stella): one place per table inside the epoch, lossless shape, no owned alias, stale writers refused |

Runners (each under one 120 s budget; a timeout is a failure, never a green):

```sh
python3 tla/check_table.py  --jar /path/to/tla2tools.jar --mode all --out /tmp/table-results
python3 tla/check_member.py --jar /path/to/tla2tools.jar --out /tmp/member-results
python3 tla/check_lua_witnesses.py <(git show f77458853af46fdbbafd6881a4b46006431f266f:internal/nsprint/fn/lua/table.lua)
java -cp /path/to/tla2tools.jar tlc2.TLC -workers 8 -deadlock tla/MCCardMachine.tla
java -cp /path/to/tla2tools.jar tlc2.TLC -workers 2 -deadlock tla/MCLandWatch.tla
```

tla2tools v1.7.4 (TLC 2.19); the fleet keeps a copy on space at `~/tla/tla2tools.jar`. The runners live here and not in rowan-new because the self repo is text only (its hook refuses machinery); the CI functional leg will run them under the two-minute cap once the runners carry the jar (owed).
