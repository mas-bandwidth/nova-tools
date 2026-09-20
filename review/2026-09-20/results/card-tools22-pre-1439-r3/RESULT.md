RESULT tools22-pre-1439-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1439 at head a7881bd73685: certify: the M2 Air is a darwin bench, and a workload body is portable because the bench is not
PREREAD 1439 claims=25 proven=24 unproven=1 defects=3 high=1

PR 1439
HEAD a7881bd736855f45e2d8a5eaa53b609d9ba956db
BASE rowan/fleet-certify-fix
MERGE-BASE 530235875826c370731c6a16a4547710e7f070b7
BEHIND 18
FILES 42 production, 15 test
LINES +8947 -53

The merge base 53023587 is OLDER than the card's base 5298f6be12ea (it is an ancestor of it), so this head is 18 commits behind origin/rowan/fleet-certify-fix. The head matched the card exactly and nothing was refetched. Read in full: every Go file, every workload card, the allowlist, the plist, and the certify/status/adopt doc hunks; built and vetted the four changed packages and ran their tests at the PR head (only a pre-existing join test fails, identically on the merge base).

CLAIMS

1. A machine is certified by DOING a representative piece of each role's work under the card's wall, and one certificate row per class is written.
   PROVEN-BY internal/fleet/certify_test.go:431 TestCertifyRunsEveryWorkloadOfTheMachinesRolesAndWritesARowEach — asserts one OK line and one row per bench class, each carrying the machine's own build and the run's hash.
2. A workload is a file (front matter, blank line, body) whose class is the file's name, and one with no roles, no expect or no body is refused.
   PROVEN-BY internal/fleet/certify_test.go:230 TestAWorkloadIsAFileWithFrontMatter and :270 TestAWorkloadWithoutARoleOrAnExpectIsRefused.
3. The standard hash is taken over the standard file and every workload's bytes, so either moving expires every certificate written under the old pair.
   PROVEN-BY internal/fleet/certify_test.go:299 TestTheStandardHashMovesWithTheStandardAndWithTheWorkloads — mutates the standard and adds a workload, asserting the hash moves both times.
4. A certificate is current only for the exact (machine, class, build, hash); the newest matching row wins, a FAIL is never a certificate and a WARN is.
   PROVEN-BY internal/fleet/certify_test.go:333 TestCertifiedIsTrueOnlyForTheCurrentBuildAndHash and :371 TestTheLastRowWins.
5. go-test, git-push, sbcl and wall-toolchain run inside nova-sandbox with the toolchain named as a read root, and the plain-ssh classes stay outside the wall.
   PROVEN-BY internal/fleet/certify_test.go:510 TestGoTestRunsInsideTheWallWithTheToolchainAsAReadRoot — inspects the composed go-test script for nova-sandbox/--read/sdk and asserts services-reach is not wrapped.
6. A transport failure and a timeout are UNREACHABLE and TIMEOUT, not verdicts: no row, no repair, counted apart, exit 3, and the build column holds a parsed version or `-`, never a sentence.
   PROVEN-BY internal/fleet/transport_test.go:82 TestATransportFailureIsUnreachableAndNeverAVerdict, :124 TestTheBuildColumnOnlyEverHoldsAParsedVersion, :360 TestAWorkloadThatRunsOutOfTimeIsItsOwnTokenAndNeverAFailure.
7. The repair never runs on a machine nobody reached, and an unreachable machine still answers its coordinator classes through the forge.
   PROVEN-BY internal/fleet/transport_test.go:170 TestTheFixNeverRunsOnAnUnreachableMachine and :216 TestAnUnreachableMachineStillAnswersItsCoordinatorClasses.
8. A line in the class's own token (read off a `^`-anchored expect) is the machine answering, so ssh's own words inside a body's answer are a FAIL and never a transport failure.
   PROVEN-BY internal/fleet/spoke_test.go:53 TestAnAnswerInTheClassTokenIsNeverATransportFailure and :36 TestTheClassTokenIsReadOffTheExpect.
9. A forge nobody could ask is UNREACHABLE and writes no row.
   PROVEN-BY internal/fleet/spoke_test.go:107 TestTheForgeNotAnsweringIsNotAVerdictAboutAMachine.
10. The machine that IS this machine is certified through the local runner with no ssh, decided by name, short host or one of this machine's own addresses, and an IPv4 literal is never cut at the first dot.
    PROVEN-BY internal/fleet/localaddr_test.go:57 TestTheAirRunsItsOwnWorkloadsWithNoSSH, :25 TestAMachineNamedByItsAddressIsStillThisMachine, :46 TestAnIPv4LiteralIsNotCutAtTheFirstDot.
11. A `where: coordinator` workload never opens an ssh; the branch is taken before the run.
    PROVEN-BY internal/fleet/transport_test.go:315 TestACoordinatorWorkloadWithABodyRunsHereAndNeverOpensAnSSH.
12. One row per (machine, class) per run carrying the run's final verdict, and a dry run says WOULD and never OK.
    PROVEN-BY internal/fleet/transport_test.go:395 TestOneRunWritesOneRowPerMachineAndClass and :251 TestADryRunSaysWouldAndNeverOK.
13. The fill refuses a card whose workload class has no current certificate on that bench, once per bench and class, and the card stays ready; the gate is off without certs, hash and a build reader.
    PROVEN-BY internal/pulse/fill_certify_test.go:63 TestFillRefusesACardWhoseWorkloadIsUncertifiedOnThatBench, :99 TestFillLaunchesOntoACertifiedBench, :153 TestTheGateIsOffWithoutTheThreeInputs.
14. The shipped `nova-pulse fill` turns that gate on, so "the fill asks before every card" holds of the shipped tool.
    UNPROVEN — cmd/nova-pulse/fill.go:63 constructs FillInput with no Certs, no Hash and no Build; no flag reaches any of the three; and certifyBuildReader, written as "the fill's build seam in production" (cmd/nova-pulse/fleet_certify.go:346), is referenced nowhere in the tree, not even by a test. Nothing in the shipped product ever engages the gate.
15. A FAIL whose class maps to a standard item is repaired once, certified again, and escalated by name (one line, one bus note) when it still fails; a class no item repairs escalates without touching the machine; --no-fix and a dry run waive everything.
    PROVEN-BY internal/fleet/fix_test.go:141 TestAFailedClassIsRepairedAndCertifiedAgain, :236 TestAClassThatStillFailsEscalatesOnceAndStaysUncertified, :294 TestAFailureNoStandardItemRepairsEscalatesWithoutTouchingTheMachine, :204 TestNoFixWaivesTheRepairAndReachesTheStandardNotAtAll, :374 TestADryRunNeverRepairsAndNeverEscalates.
16. The fix mapping is bounded (one round by default) and names only real standard items, nova-stamp being the one apply names and never runs.
    PROVEN-BY internal/fleet/fix_test.go:92 TestTheFixMappingNamesAStandardItemPerRepairableClass and :185 TestTheRepairIsBoundedByMaxFixRounds.
17. `fleet standard --apply` repairs the four 2026-09-18 faults idempotently, moves shadow binaries aside and never deletes, and names the adopt instead of running it.
    PROVEN-BY internal/pulse/fleetapply_test.go:81 TestApplyRepairsTheFourFaultsOfTheHandPassAndSaysWhatItChanged, :146 TestApplyIsIdempotent, :283 TestNoRemedyDeletesAnything, :168 TestApplyNeverRunsTheAdopt.
18. The provisioning standard gained four non-interactive checks, a conforming bench passes all of them one line each, and no probe uses `case`.
    PROVEN-BY internal/pulse/fleetverbs_test.go:194 TestFleetStandardPrintsALinePerCheckAndAVerdict and :553 TestNoStandardProbeUsesCase.
19. A workload body is portable: a one-OS spelling is refused unless the same line names its other half, a one-OS tool is refused unless guarded with `command -v`, and the shrink-only allowlist ships empty.
    PROVEN-BY internal/fleet/portable_test.go:22 TestAGNUOnlySpellingIsRefusedWithItsPortableRemedy, :87 TestAOneOSToolIsAllowedWhenTheBodyGuardsIt, :157 TestEveryEmbeddedWorkloadIsPortable, :136 TestTheAllowlistThatShipsIsEmpty.
20. The darwin bench is a first-class citizen: every embedded body carries the darwin spelling or the Cellar toolchain roots, so the Air can answer every workload it is given.
    PROVEN-BY internal/fleet/portable_test.go:188 TestTheDarwinBenchCanAnswerEveryWorkloadItIsGiven and :209 TestTheWallWorkloadsNameTheDarwinToolchainRootsAsReads.
21. The page carries a certification cell per machine read from the certificates file and nothing else; a machine nobody asked is a dash, and a DOWN bench keeps its record.
    PROVEN-BY internal/pulse/statushtml_certs_test.go:37 TestTheFleetPageCarriesACertificationCellPerMachine, :67 TestNoCertsFileIsADashAndNeverAZero, :81 TestADownBenchStillCarriesWhatTheRecordSays.
22. `release adopt` certifies by default — --certify/--certs/--standard renew under the version just installed — and --no-certify waives it out loud; half a trio or both roads is refused before any ssh.
    PROVEN-BY internal/release/certify_test.go:84 TestAdoptWithCertifyRunsTheWorkloadsOnEachAdoptedMachineUnderTheVersionJustInstalled, :208 TestAdoptWaivedByNoCertifyCertifiesNothingAndSaysSo, :184 TestAdoptRefusesHalfOfTheCertifyFlags, :233 TestAdoptRefusesWhenNeitherCertifiedNorWaived.
23. `--if-stale` skips a machine whose every class is current, costing one file read and one build read per machine.
    PROVEN-BY cmd/nova-pulse/fleet_certify_test.go:161 TestIfStaleSkipsAMachineWhoseEveryClassIsCurrent.
24. `--status` reads the record alone, needs only --certs, and reports NONE/FAIL/STALE with exit 1 when any is stale.
    PROVEN-BY cmd/nova-pulse/fleet_fix_test.go:185 TestStatusRunsFromAnywhereWithOnlyTheCertificatesFile and internal/fleet/transport_test.go:274 TestStatusNeedsOnlyTheCertificatesFile.
25. The loop is mechanized: the launchd agent runs `fleet certify --all --if-stale` every six hours, and the plist is held against the verb it runs.
    PROVEN-BY cmd/nova-pulse/fleet_certify_test.go:316 TestTheLaunchdAgentRunsTheVerbTheLoopNeeds — checks every flag in the argv is one the verb declares.

DEFECTS

DEFECT high cmd/nova-pulse/fill.go:63 — the shipped `nova-pulse fill` cannot turn the certification gate on: cmdFill passes no Certs, no Hash and no Build to FillInput, no flag anywhere reaches those three, and the seam written for exactly this, certifyBuildReader (cmd/nova-pulse/fleet_certify.go:346, "the fill's build seam in production"), is dead code referenced nowhere — so the headline "the fill asks before every card" (docs/CLI.md:1320) is true only of tests, and the shipped loop still spends a card on an uncertified bench, which is the hulk failure this PR exists to prevent — wire --certs/--standard-derived hash and a BuildReader into cmdFill (or refuse to run un-gated), so the gate the tests prove can actually be engaged in production.

DEFECT low cmd/nova-pulse/main.go:44 — the `nova-pulse help` line for `fleet certify` lists none of --fix, --no-fix, --max-fix-rounds, --git-name, --git-email, --bus, --as, --to, --lane, --bus-remote, --bus-branch though docs/CLI.md:1211 documents all of them, so an operator who reads `help` cannot discover that the repair round exists or is on by default — add the flags to the usage string (or point the usage line at docs/CLI.md).

DEFECT low cmd/nova-pulse/fleet_fix_test.go:193 — TestStatusRunsFromAnywhereWithOnlyTheCertificatesFile uses t.Chdir, mutating the whole test process's cwd for every later test in the package, so the plist test's repoRootFromCmd (cmd/nova-pulse/fleet_certify_test.go:387), which walks up from the cwd to find go.mod, breaks the moment ordering shifts (#1927); today it passes only by file/test order — run the verb against a working-directory seam instead of a process-wide chdir, or save and restore the cwd.

QUESTIONS

1. The fill gate: given that certifyBuildReader is dead code and `nova-pulse fill` has no flag that can reach FillInput's Certs/Hash/Build, how is the "fill asks before every card" gate meant to be switched on in production — a follow-up PR, or a wiring I should be able to find and cannot?
2. `release adopt --certify` runs fleet.Certify with Fix at its zero value and no Fixer, so a machine whose FAIL a standard item could repair is neither repaired nor escalated during an adopt — just counted refused. Is that deliberate (the repair round belongs to the loop), or should the adopt wire a Fixer?
3. The four new `fleet standard` checks carry OS:"" (internal/pulse/fleetstandard.go:119-148) and so now run on darwin benches too, but the remedies are .bashrc-shaped (pathNonInteractiveRemedy writes a ~/.bashrc block) and path-noninteractive reads the ssh session's $PATH; on the Air (zsh, macOS default ssh PATH, non-interactive bash reads no .bashrc) can that check ever be met and its remedy ever work, or is the darwin standard expected to drift on it?
4. TestFleetJoinKeepsTheAuthKeyOutOfEveryArgv fails in this environment (FLEET JOIN FAILED, no reason) on both the merge base and the PR head — is that a known ambient dependence of the join fake (#2057), or does the fake want something this shard does not provide?

Left owed — docs/CLI.md, docs/SPEC-PULSE.md, docs/spec-pulse/03-fleet.md, docs/spec-pulse/06-status.md and docs/SPEC-UPDATE.md were read only in the certify/status/adopt hunks and the sections grep-anchored from them, not end to end (they run to hundreds of lines of unchanged prose); and the pre-existing packages this PR leans on (internal/fleet registry.go, internal/log, internal/oneline, internal/friends, internal/bus) were read only by compile — go build ./... and go vet of the four changed packages at a7881bd passed, and every test of the changed packages passed at the head except TestFleetJoinKeepsTheAuthKeyOutOfEveryArgv, which fails identically at the merge base.

git status --short:
HEAD: d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1439-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1439-r4	1	2026-09-20T19:27:25Z	2026-09-20T19:51:16Z	0	opencode	deepseek-v4-flash	192943	50840	0	9425408	0	0.3052
