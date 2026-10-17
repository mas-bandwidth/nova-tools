# Aspect 1 (the bus envelope): acceptance record

Aspect 1 of the schema-table adoption, run once at the tip of `sprint/v15-schema-tables` with every piece landed. Bench: a Linux amd64 bench, go1.26.6, `GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1`, shared GOCACHE. Tips: nova-tools `b581ec0d2e50`, nova-sprint `4227d3dea683`. Pre-aspect references: nova-tools `ee3ebe146` (parent of the first envelope commit `0e90a5716`), nova-sprint `166356a9` (first parent of the dual-read landing).

## 1. The pieces have landed

| piece | repository | landing commit |
|---|---|---|
| l1-defs-bus-envelope-2 | nova-tools | `3144c1fac` |
| l1-nova-bus-native-binary-form-2 | nova-tools | `39d28a6c4` |
| l1-nova-bus-reader-builder-and-show-2 | nova-tools | `b581ec0d2` (the tip) |
| l1-bus-envelope-dual-read-2 | nova-sprint | `4227d3de` (the tip); go.mod pins nova-tools `3144c1fac` |

## 2. The focused tests of each piece (nova-tools, tip)

```
go test -count=1 -timeout 600s ./pkg/buswire -run TestLayer1Envelope
--- PASS: TestLayer1EnvelopeRefusals / TestLayer1EnvelopeDualRead / TestLayer1Envelope
ok  	github.com/mas-bandwidth/nova-tools/pkg/buswire	0.091s

go test -count=1 -timeout 600s ./pkg/bus -run TestEnvelopeNativeBinaryField
ok  	github.com/mas-bandwidth/nova-tools/pkg/bus	0.044s

go test -count=1 -timeout 600s ./cmd/nova-bus -run 'TestSendBuilderRefusals|TestShowRendersText|TestRecvDualRead'
ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-bus	0.134s

make schemawire-check        -> schemawire-check: OK
make schemawire-conformance  -> schemawire-conformance: OK (5s), 1648 mutants, 0 divergences
```

## 3. Dual-read interop and the writer switch

`TestLayer1EnvelopeDualRead` (pkg/buswire/envelope_test.go:666) reads the old form and the new forms (binary, base64, JSON) identically through the new reader, and proves a writer switched to the typed envelope hands today's readers the entry they read today, field for field (line 688). `TestEnvelopeNativeBinaryField` proves the legacy fields are written beside the native field unchanged. `TestRecvDualRead` (cmd/nova-bus) proves recv reads both forms and counts them. In nova-sprint:

```
go test -count=1 -timeout 600s ./cmd/nova-sprint -run TestEnvelopeDualRead
--- PASS: TestEnvelopeDualRead (0.00s)
ok  	github.com/mas-bandwidth/nova-sprint/cmd/nova-sprint	0.122s
```

All pass. This card changes no code, so it weakens no existing test.

## 4. The full unit suite, nova-tools, tip

`go test -count=1 -timeout 600s ./...` last line: `FAIL` (135 packages ok). Two runs, three distinct reds, each shown below at the pre-aspect reference:

| red at tip | at pre-aspect `ee3ebe146` | verdict |
|---|---|---|
| pkg/typedrec `TestOneTypedParser/tree` (oneparser_test.go:708-710): `pkg/swarm/lintpaths.go:521 spikeOutsideResearch() bare PATHS complit` | red, same line | pre-existing; the line was introduced by `94cbd541f` (2026-10-15), not the aspect |
| hook `TestEachRoundTripHasItsOwnTimeout` (redis_test.go:350, 50ms store timeout) | green | load flake in the whole-suite run; green `-count=3` in isolation at tip and at pre-aspect |
| pkg/cardcontract `TestShimEmptyPathspecInputCannotMoveStagedHEAD/claude/--mixed/-` (shims_test.go:364-365) | green | load flake in the second whole-suite run; green `-count=5` in isolation at tip, and absent from the first whole-suite run |

No red is at a file or test an Aspect 1 piece owns.

## 5. The functional tier, nova-tools, tip

`make test-functional`. The default 100s per-package budget timed out `internal/ci` (TestDeadCode) and `pkg/ntable` (TestTableRowsAreBounded) while two suites shared the bench; rerun alone with `FUNCTIONAL_TIMEOUT=600s`: 42 packages ok, one red:

```
--- FAIL: TestTESTSFirstRunIsWhatTheToolPrints  (cmd/nova-friend/firstrun_test.go:100)
    docs/TESTS.md says "--dir ./bob --width 0"; the tool prints the run directory
FAIL	github.com/mas-bandwidth/nova-tools/cmd/nova-friend
```

Red at pre-aspect `ee3ebe146` too (same test, same line): pre-existing. At pre-aspect the tier also showed `cmd/nova-config TestInventoryTimeoutFlagGovernsTheConnection` and `pkg/bus TestRedisFriendACLSetsOnlyItsOwnTokenRecords` red; both are green at tip.

## 6. The other repository: nova-sprint at its v15 tip

`go test -count=1 -timeout 600s ./...` last line: `FAIL` (23 packages ok). Reds, identical at the pre-aspect `166356a9`:

- cmd/nova-sprint: 17 to 18 tests (the TestReleaseCheck*, TestALaneTake*, TestAFriend*, TestFriend* group), then `panic: index out of range [0] with length 0` at friend_take_test.go:136, which aborts the package at tip and at pre-aspect alike; which tests run before the abort varies (`TestFriendTakeAllUnstartedTakesOnlyTheCardsDealtAndNotTaken` and `TestFriendDownGivesBackEveryCardStartedOrNot` fail alike at both in isolation). Tests after the abort do not run at either commit.
- internal/sprint/refmodel: `TestDecideIsWhatTodaysTickPlansOnAThousandSnapshots`, `TestTheStoreWritesWhatTheReferenceDecides`.
- pkg/friend: `TestNoStringLiteralCallsAFriendHerOrHim`.

All are pre-existing; none is in a file the dual-read piece changed.

## 7. Not done

- The nova-sprint functional tier was not run (the card names the nova-tools tier and the nova-sprint unit suite).
- Tests in cmd/nova-sprint after the friend_take_test.go:136 panic did not run at tip or at the pre-aspect reference.
- No red was fixed here.

## Verdict

Aspect 1 holds on its own evidence: every focused test, the dual-read interop and the writer-switch proof pass, and `schemawire-check` and `schemawire-conformance` pass. The two whole-suite gates are not green, so the whole-suite DONE-WHEN clause is not met as written. Every red is either pre-existing at the pre-aspect reference (TestOneTypedParser, TestTESTSFirstRunIsWhatTheToolPrints, the nova-sprint set) or a load flake green in isolation; none is owned by a piece.
