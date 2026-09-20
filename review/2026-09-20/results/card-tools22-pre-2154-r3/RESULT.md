RESULT tools22-pre-2154-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2154 at head 6cc7e810d616: pulse: pin six production changes so reverting them goes red
PREREAD 2154 claims=8 proven=8 unproven=0 defects=0 high=0

PR 2154
HEAD 6cc7e810d616905f8f37aaa04d015ea24970b84f
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 0 production, 4 test
LINES +331 -0

The merge base a7611c81 is the PR head's parent and is OLDER than the card's
BASE 5298f6be12ea (a7611c81 is an ancestor of the card base); dev has moved one
commit past the head. The diff is exactly the four test files; production is
unchanged, as the commit message says.

CLAIMS

1. hygiene reap must not delete through a planted <job>/repo or <slot>/tmp symlink — what it removes has to stay strictly inside the job/slot it is reaping.
   PROVEN-BY internal/pulse/hygiene_guard_test.go:63 TestHygieneReapRefusesAJobRepoSymlinkToAnotherTree — plants <job>/repo -> certify and <slot>/tmp -> victim job, reaps slot 1, and asserts both foreign trees' scratch survives, the job's own scratch is gone, and stderr names the symlink. 1fbb5e20 added the realDirBelow guard (rooted at job/slot, not the hygiene roots); without it RemoveUnderRoots deletes the foreign scratch and the note never prints, so the test goes red.

2. hygiene run leaves a directory without <slot>/jobs alone: it is not a slot and is never deleted.
   PROVEN-BY internal/pulse/hygiene_guard_test.go:113 TestHygieneRunSkipsUnshapedTreesAndKeepsALeasedCache — an old "certify-tree" with no jobs dir must survive; reverting slotShaped (added by 1af36d0e) lets the tree be delete-slot'd and the corpus disappears.

3. hygiene run keeps the go-build cache while any lease is live (cache=kept-lease).
   PROVEN-BY internal/pulse/hygiene_guard_test.go:113 TestHygieneRunSkipsUnshapedTreesAndKeepsALeasedCache — a live lease (own pid/host, fresh heartbeat) plus freeGB=1 must print cache=kept-lease and leave the cache file; reverting anyLeased/cache=kept-lease (1af36d0e) drops the cache and the assertion fails.

4. hygiene run counts an unleased job quiet past MinAgeHours with no RESULT.md as dead= and removes it.
   PROVEN-BY internal/pulse/hygiene_guard_test.go:113 TestHygieneRunSkipsUnshapedTreesAndKeepsALeasedCache — the 8-hour-old leaseless job with no RESULT.md must be gone and the HYGIENE line must carry dead=1; both halves are the 1af36d0e DEAD branch.

5. fill with no --bench fills every certified bench from the machines registry, and refuses a registry that names no certified bench.
   PROVEN-BY internal/pulse/machines_test.go:246 TestFillWithNoBenchNamedUsesTheCertifiedRegistryPool and internal/pulse/machines_test.go:273 TestFillWithNoBenchNamedRefusesAnEmptyCertifiedPool — with hulk certified and raw-bench uncertified, one launch on hulk and none on raw-bench; with none certified, exit 2 and "names no certified bench". Reverting 377916fc's THE POOL block yields zero launches / no refusal, so both go red.

6. pool joins a roadmap locator that is not absolute to the sources file's directory.
   PROVEN-BY internal/pulse/pool_test.go:252 TestPoolRoadmapRelativeLocatorJoinsTheSourcesDir — sources.tsv names "roadmap.sexp" relative and the POOL OK line must read candidates=1 roadmap=1; reverting the 687b02e4 join reads the file from the test's cwd, which has no roadmap.sexp, and Pool refuses with exit 2.

7. an issue row's source column in pool.tsv names the repo locator, not the kind word "issues".
   PROVEN-BY internal/pulse/pool_test.go:274 TestPoolIssueRowsNameTheRepoLocator — the fake gh serves issue 7 and pool.tsv's first field must be owner/repo; f293cf63 changed Source from s.kind to s.locator, so a revert prints "issues" and the assertion fails.

8. triage --decide appends exactly one TRIAGE DECIDE advisory line carrying the typed verdict at or above the floor, "?" below it, and the evidence pointer, and changes nothing else.
   PROVEN-BY internal/pulse/triage_test.go:312 TestTriageDecideAppendsAnAdvisoryLine — below floor=0.90 a conf=0.60 ADMIT prints verdict=? conf=0.60; at 0.95 it prints verdict=ADMIT; reverting the ddce356e DECIDE block drops the second line and both cases fail.

DEFECTS none

QUESTIONS

1. TestFillWithNoBenchNamedUsesTheCertifiedRegistryPool would still pass if the pool reverted to a hardcoded Go literal exactly equal to {hulk}; the sharp pin for 377916fc is really the empty-certified-pool sibling test (machines_test.go:273). Is that division deliberate — "never fills an uncertified bench" plus "empty pool refused" — or would you want the certified test to also certify a second bench and assert it is filled, so a literal reintroduction goes red on its own?

2. The reap test plants BOTH a <job>/repo symlink and a <slot>/tmp symlink (hygiene_guard_test.go:81). Was the <slot>/tmp case part of the original #1921 incident (a card planting repo as a symlink), or is it the author's own extension of the scenario to cover the slot-level path too?

3. The commit message describes a "package sweep" of internal/pulse that reverse-applied each commit's production files and found its tests "stayed green". Is there a written record of that sweep (issue/checklist), so a future reader knows which of the six pins trips on which revert?

4. TestTriageDecideAppendsAnAdvisoryLine pins the DECIDE output line but never asserts the card file written with --decide is byte-identical to the one written without it. Is the SPEC-DECIDE "changes no verdict and writes nothing" property intentionally left to the cmd/nova-pulse/triage_decide_test.go that landed with ddce356e?

Left owed

Read in full: all four new test files (hygiene_guard_test.go, the added machines_test.go, pool_test.go, triage_test.go hunks); production fill.go and hygiene.go; the relevant parts of pool.go (readSources, poolIssues, poolRoadmap, writePool, sourceCountKey, cardName/cellID/balanced), triage.go (decide path 300-424), swarm/lease.go (95-214, 355-434), fleet/registry.go CertifiedBenchNames, and the test helpers (fake_test.go, machines_test.go, fill_test.go, reap_test.go writeCard, gate_decide_test.go fakeDecider, pool_test.go writeTestFile/ghFixture).

Not read: cmd/nova-pulse (the cmd-level twins of these behaviors and the original PRs' tests), pool.go lines 348-484 (poolPRs/poolWork/poolAudits detail), triage.go lines 1-300, the remainder of internal/swarm and internal/fleet, docs/, and the pre-existing test files outside those four. The six referenced integration commits were examined only for the specific production hunks each pin names.

Verification done: `go vet ./internal/pulse/` clean, and `go test -run NONE_NONE ./internal/pulse/` compiles the test package at the PR head (extracted via `git archive`; the clone worktree was on `main`, not the PR head). No test was run, per the card.

git status --short
(empty)

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-2154-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2154-r4	1	2026-09-20T19:56:37Z	2026-09-20T20:09:10Z	0	opencode	deepseek-v4-flash	74363	44021	0	2745856	0	0.0996
