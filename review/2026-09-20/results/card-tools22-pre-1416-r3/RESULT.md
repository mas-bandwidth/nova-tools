RESULT tools22-pre-1416-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1416 at head f1ac5fddb09d: nova-pulse fill serves a darwin bench: the seams take the registry row, not the bench name
KIND: transcript-test
DEADLINE: 2100
LEG: go
PATHS: the files the pull request changes, and nothing else
FILES: 0
TEST: none
MODE: read
TURNS: 35
SOURCE: mas-bandwidth/nova-tools#1416
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules

PREREAD 1416 claims=14 proven=14 unproven=0 defects=0 high=0

PR 1416
HEAD f1ac5fddb09d6b70c06e8017232cb98da71db2ca
BASE dev
MERGE-BASE 0edc81b7406a1160f5b761cba59c1e0c3fc34a00
BEHIND 72
FILES 16 production, 13 test (plus docs/testdata)
LINES +4421 -210

---

## CLAIMS

1. Fill seams (Capacity and CardLauncher) take `fleet.Machine` rows instead of bench names, so ssh targets and os are facts of the registry, never guessed from a name — PROVEN-BY `internal/pulse/filldarwin_test.go:53 TestTheCapacitySeamIsHandedTheRegistryRowNotTheBenchName` asserts that Capacity and Launch each receive a machine whose SSH is `glenn@100.117.59.68`, OS is `darwin`, and Arch is `arm64` (not the bare name `"air"`).

2. Darwin cards (`os: darwin` or `LEG: darwin/...`) land only on darwin benches; they skip linux benches in the same tick — PROVEN-BY `internal/pulse/filldarwin_test.go:78 TestADarwinCardOnlyLandsOnADarwinBench` feeds a mixed registry with hulk (linux) and air (darwin), verifies the darwin card goes to air and the no-os card goes to hulk.

3. A darwin card held by no named linux bench gets a `FILL WAITING os=darwin` line naming how many cards wait and what benches exist — PROVEN-BY `internal/pulse/filldarwin_test.go:109 TestADarwinCardWaitsWhenNoNamedBenchRunsDarwin` checks stdout for both `FILL WAITING` and `hulk=linux`.

4. `cardOS()` reads both `os:` and `LEG:` prefixes, lowercases the result, and ignores non-exact-prefix lines like `LEGS:` — PROVEN-BY `internal/pulse/filldarwin_test.go:134 TestCardOSReadsTheLegAndTheOSLine` passes eight bodies including valid os/leg, invalid legs, and prose.

5. `--dry-run` reads real capacity over ssh but moves no cards and calls no launcher — PROVEN-BY `internal/pulse/filldarwin_test.go:162 TestDryRunReadsCapacityAndLaunchesNothing` verifies zero launcher calls, one card still in ready, and a `FILL DRY` line carrying capacity, os, ssh, and arch.

6. Dry run exits 1 when every bench's capacity probe fails — PROVEN-BY `internal/pulse/filldarwin_test.go:196 TestDryRunSaysWhyItCouldNotRead` uses a dead capacity seam returning "Connection refused" and checks exit=1 plus stderr carrying the reason.

7. A launcher failure returns the card to --ready, writes a `.failed-N` marker, releases its lane immediately, and counts `failed=N` on the FILL line — PROVEN-BY `internal/pulse/fill_test.go:96 TestFillReturnsAFailedLaunchToReady` checks ready count restored, `.failed-1` marker present, and FILL line shows `launched=0,failed=1`.

8. Lane release on failed launch lets the next card in the same lane proceed (it does not stay HELD behind the failed card) — PROVEN-BY `internal/pulse/fill_test.go:132 TestFillReleasesTheLaneOfAFailedLaunch` feeds two lane-same cards, the first fails, the second launches successfully with no FILL HELD.

9. An unknown lane refusal prints once per lanes-file mtime; editing the table (new mtime) makes it speak again; stale markers are cleaned up — PROVEN-BY `internal/pulse/fill_test.go:188 TestFillRefusesAnUnknownLaneOncePerLanesFile` ticks three times (before edit, after edit) checking stderr presence/absence and marker state.

10. Non-`card-<n>.md` .md files in --ready trigger a `FILL REFUSED` stderr line naming the stray file and the filename contract — PROVEN-BY `internal/pulse/fill_test.go:256 TestFillRefusesAReadyFileTheGlobWouldSkip` places `42.md` alongside `card-001.md`; checks stderr carries both the file name and `card-<n>.md`.

11. Fill exits 1 when every bench fails its capacity probe on an `--once` tick (no bench reached = red fleet) — PROVEN-BY `internal/pulse/fill_test.go:294 TestFillExitsOneWhenEveryBenchFailed` sets all benches to dead capacity and checks exit code 1.

12. `--only <pattern>` selects which ready cards this run may launch; unselected cards stay in ready untouched, unrefused — PROVEN-BY `internal/pulse/fill_test.go:324 TestFillOnlyLaunchesTheCardsItWasGiven` puts three cards (one other-line, two mine) with `--only card-960*`; verifies exactly two launches and the third stays in ready with no REFUSED.

13. `selectedCards()` matches patterns against full filename, name without `.md`, and number without `card-` prefix — PROVEN-BY `internal/pulse/fill_test.go:380 TestSelectedCardsMatchesThreeSpellings` tests six pattern/got combinations covering suffix, bare name, number, glob, wide glob, miss, and nil.

14. Nova-pulse harvest gains a new `--bench` mode (`harvestBench`): lists remote jobs via BenchShell seam, pushes branches from local clones through Forge seam, opens PRs, and drains the launched queue — PROVEN-BY `internal/pulse/harvestbench_test.go:106 TestHarvestBenchReadsResultsOverTheShellSeamAndOpensThePR` drives the verb with fake shell+forge, verifies HARVEST JOB line, PR opened, fetch/push from local clone, and `.harvested` marker.

---

## DEFECTS

DEFECTS none

---

## QUESTIONS FOR THE REVIEWER

1. The output format changed under `FILL tick=`: card identifiers went from bare numbers (`card=2`) to filenames (`card=card-002.md`), and the FILL line now includes `,failed=<n>` after `launched=<n>`. Any external tool parsing FILL/HARVEST stdout will break. Were these consumers surveyed?

2. `cmd/nova-pulse/main.go` adds `fill --session <id>` to `FillInput.Session` which is written into the launched marker. What downstream systems expect this field in the launched marker, and was the marker format communicated before landing?

3. `internal/ci/goosname_class_test.go` (committed as part of schema-loop edges) maps goos values to CI classes. It appears unrelated to the bench/darwin theme. Was this change always intended here, or did it drift in during rebase?

---

## Left owed

Did not read: `docs/CLI.md` (+274 lines of new flag documentation), `docs/SPEC-PULSE.md` (+143 lines of spec updates), `cmd/nova-pulse/main.go` (CLI wiring and help text), `internal/pulse/validated.go` (validation additions), `cmd/nova-pulse/fill_test.go` (CLI-level integration tests), `internal/pulse/harvest_bench_test.go` (integration-level harvest bench tests). These were beyond reading budget. The production core (`internal/pulse/fill.go`, `internal/pulse/harvestbench.go`, `cmd/nova-pulse/fill.go`) and their unit tests were read in full.

git status --short
(git rev-parse HEAD) d576bf6bbabb39068096a97b4560de9b5e245970
