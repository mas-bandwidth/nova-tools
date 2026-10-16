# Aspect 1 (the bus envelope): acceptance record

Aspect 1 of the schema-table adoption, run with every piece landed. Bench: a Linux amd64 bench (go1.26.6, 16 cores, shared with other jobs: the load average at each run is written beside it), `GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1`, shared GOCACHE. Tips: nova-tools `b581ec0d2e50` plus this record (the diff to it is this one document), nova-sprint `4227d3dea683`. Pre-aspect references: nova-tools `ee3ebe146` (parent of the first envelope commit `0e90a5716`), nova-sprint `166356a9` (first parent of the dual-read landing). The pre-aspect and tip runs of one suite ran back to back from separate clean checkouts.

## Verdict first: NOT DONE

The whole-suite clause of the DONE-WHEN is not met: the full unit suite and the functional tier of nova-tools, and the full unit suite of nova-sprint, each end `FAIL` at the tip. The aspect's own evidence (sections 2 and 3) is green. Every whole-suite red is either deterministic and identical at the pre-aspect reference (section 4: none is in a file an Aspect 1 piece owns) or a timing flake that also fails or passes at random at the pre-aspect reference. Acceptance is withheld until the card owner either rules that reds already red before the aspect do not bind this gate (a change to the DONE-WHEN, which this card cannot make) or the owners of those reds fix them and the gates are rerun. This card changes no code and fixes no red.

## 1. The pieces have landed

| piece | repository | landing commit |
|---|---|---|
| l1-defs-bus-envelope-2 | nova-tools | `3144c1fac` |
| l1-nova-bus-native-binary-form-2 | nova-tools | `39d28a6c4` |
| l1-nova-bus-reader-builder-and-show-2 | nova-tools | `b581ec0d2` |
| l1-bus-envelope-dual-read-2 | nova-sprint | `4227d3de` (the nova-sprint tip); go.mod pins nova-tools `3144c1fac` |

## 2. The focused tests of each piece (green, nova-tools at `b581ec0d2e50`, nova-sprint at `4227d3de`)

```
go test -count=1 -timeout 600s ./pkg/buswire -run TestLayer1Envelope
--- PASS: TestLayer1Envelope
ok  	github.com/mas-bandwidth/nova-tools/pkg/buswire	0.194s

go test -count=1 -timeout 600s ./pkg/bus -run TestEnvelopeNativeBinaryField
ok  	github.com/mas-bandwidth/nova-tools/pkg/bus	0.187s

go test -count=1 -timeout 600s ./cmd/nova-bus -run 'TestSendBuilderRefusals|TestShowRendersText|TestRecvDualRead'
ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-bus	0.075s

make schemawire-check        -> schemawire-check: OK
make schemawire-conformance  -> schemawire-conformance: OK (7s), 1648 mutants, 0 divergences
```

## 3. Dual-read interop and the writer switch (green)

`TestLayer1EnvelopeDualRead` (pkg/buswire/envelope_test.go:666) reads the old form and the new forms (binary, base64, JSON) identically through the new reader, and proves a writer switched to the typed envelope hands today's readers the entry they read today, field for field. `TestEnvelopeNativeBinaryField` proves the legacy fields are written beside the native field unchanged. `TestRecvDualRead` (cmd/nova-bus) proves recv reads both forms and counts them. In nova-sprint:

```
go test -count=1 -timeout 600s ./cmd/nova-sprint -run TestEnvelopeDualRead -v
    --- PASS: TestEnvelopeDualRead/legacy_and_typed_entries_read_alike (0.09s)
    --- PASS: TestEnvelopeDualRead/the_writer_switch_keeps_old_readers_reading (0.10s)
ok  	github.com/mas-bandwidth/nova-sprint/cmd/nova-sprint	0.148s
```

This card changes no code, so it weakens no existing test.

## 4. The whole-suite gates, each red at the tip beside the pre-aspect reference

The whole-tree unit command is `go test -count=1 -timeout 600s` over every package (the all-packages pattern; the repository's docs lint, nova-tools#4336, refuses that literal pattern in a document, so it is not spelled out here). The functional tier is `make test-functional FUNCTIONAL_TIMEOUT=600s` (the default 100s per-package budget times out internal/ci and pkg/ntable when the bench is loaded). Each red below was then run alone 10 times at the pre-aspect reference and at the tip (`go test -count=1`, with `-tags functional` for the functional ones). Counts are passes of 10.

### 4.1 nova-tools unit suite

Tip `334f6da2c2b8` (load 8.2 at the start): last line `FAIL`. Reds: `TestOneTypedParser` (pkg/typedrec), `TestEachRoundTripHasItsOwnTimeout` (hook), `TestUsagecardConcurrentHeaderExpansionLosesNoAppend` (pkg/swarm), and `TestEveryNamedRepoPathExists` and `TestNoWholeTreeGoTestInDocs` (internal/ci). The last two were red because the earlier version of this document named a nova-sprint-only path and spelled the whole-tree command; this version does not, and they are green on it (section 5). Pre-aspect `ee3ebe146` (load 76): last line `FAIL`, one red, `TestOneTypedParser`.

| red | pre-aspect alone | tip alone | verdict |
|---|---|---|---|
| pkg/typedrec `TestOneTypedParser`: `pkg/swarm/lintpaths.go:521 spikeOutsideResearch() bare PATHS complit` | 0 of 10 | 0 of 10 | deterministic, the same line at both; introduced by `94cbd541f` (2026-10-15), not by the aspect |
| hook `TestEachRoundTripHasItsOwnTimeout` (50ms store timeout) | 10 of 10 | 10 of 10 | load flake of the whole-tree run |
| pkg/swarm `TestUsagecardConcurrentHeaderExpansionLosesNoAppend` | 10 of 10 | 10 of 10 | load flake of the whole-tree run |
| pkg/cardcontract `TestShimEmptyPathspecInputCannotMoveStagedHEAD` (red in the earlier whole-tree run) | 10 of 10 | 10 of 10 | load flake |

### 4.2 nova-tools functional tier

Tip `334f6da2c2b8` (load 43): ended `FAIL`; pre-aspect `ee3ebe146` (load 21): ended `FAIL`.

| red | pre-aspect alone | tip alone | verdict |
|---|---|---|---|
| cmd/nova-friend `TestTESTSFirstRunIsWhatTheToolPrints` (firstrun_test.go:100, docs/TESTS.md vs the printed run directory) | 0 of 10 | 0 of 10 | deterministic, identical at both |
| pkg/bus `TestRedisFriendACLSetsOnlyItsOwnTokenRecords` | 0 of 10 | 10 of 10 | red before the aspect, green at the tip: the aspect did not make it worse |
| cmd/nova-config `TestInventoryTimeoutFlagGovernsTheConnection` (a 100ms to 250ms store timeout; the printed refusal names the i/o timeout instead of the flag) | 8 of 10 | 4 of 10 | timing flake that fails at both; cmd/nova-config is untouched by the aspect (an empty diff between the two commits). The 8 and 4 were measured at different load averages (66 and 43); they are not a comparison of rates |

### 4.3 nova-sprint unit suite

Tip `4227d3dea683` (load 55): last line `FAIL`; pre-aspect `166356a9` (load 45): last line `FAIL`. All of these are red at both:

| red | pre-aspect alone | tip alone | verdict |
|---|---|---|---|
| cmd/nova-sprint `TestFriendTakeAllUnstartedTakesOnlyTheCardsDealtAndNotTaken` (friend_take_test.go:130, then `panic: index out of range [0] with length 0` at line 136, which aborts the package) | 0 of 10 | 0 of 10 | deterministic, identical |
| cmd/nova-sprint `TestFriendDownGivesBackEveryCardStartedOrNot` | 0 of 10 | 0 of 10 | deterministic, identical |
| cmd/nova-sprint `TestReleaseCheckPassesWhenCardTakenBackInsideDeadline` | 0 of 10 | 0 of 10 | deterministic, identical |
| nova-sprint refmodel `TestDecideIsWhatTodaysTickPlansOnAThousandSnapshots` | 0 of 10 | 0 of 10 | deterministic, identical |
| nova-sprint refmodel `TestTheStoreWritesWhatTheReferenceDecides` | 0 of 10 | 0 of 10 | deterministic, identical |
| nova-sprint pkg/friend `TestNoStringLiteralCallsAFriendHerOrHim` (pronouns_guard_test.go:131) | 0 of 10 | 0 of 10 | deterministic, identical |

In the whole-package run, 22 cmd/nova-sprint tests at the tip and 26 at the pre-aspect reference (the friend, lane-take and release-check group) fail before the panic; which of them run before the abort varies, and tests after the abort do not run at either. These are the cmd/nova-sprint package's own state at the base, not the dual-read piece (its one test, `TestEnvelopeDualRead`, is green).

## 5. This document against the repository's own class tests

At the commit that carries this document: `go test -count=1 ./internal/ci -run 'TestEveryNamedRepoPathExists|TestNoWholeTreeGoTestInDocs'` passes:

```
ok  	github.com/mas-bandwidth/nova-tools/internal/ci	0.369s
```

## 6. Not done

- Whole-suite acceptance: the three suites above are not green, and this card cannot fix a red or change its own DONE-WHEN.
- The nova-sprint functional tier was not run: it needs a live Redis server, and a bench that is a fleet member and CI runner runs none.
- The bench was shared (load averages 8 to 76), not quiet; the loads are quoted so a reader can weigh the flakes.
- Tests in cmd/nova-sprint after the friend_take_test.go panic did not run at either commit.
- No red was fixed here.

## Verdict

NOT DONE. Aspect 1 passes every test it owns (sections 2 and 3). The whole-suite DONE-WHEN clause is unmet at the tip, and the evidence in section 4 says each red was already red, or already flaky, before the aspect, with one (pkg/bus `TestRedisFriendACLSetsOnlyItsOwnTokenRecords`) red before and green after. The card owner decides whether that satisfies the gate or the reds go to their owners first.
