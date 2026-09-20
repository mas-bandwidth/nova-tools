RESULT tools22-pre-2138-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2138 at head 38d47636bdef: tests: fakerunner whole-file write is published by temp+rename (#2026)
PREREAD 2138 claims=3 proven=2 unproven=1 defects=2 high=0
PR 2138
HEAD 38d47636bdefc6f4a12a0e5bb3ee795b65306436
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730 (the commit immediately before the card's stated BASE 5298f6be12ea — older, as expected)
BEHIND 1
FILES 0 production, 1 test
LINES +50 -0

CLAIMS

1. A fakerunner `write` step publishes a whole-file write by temp+rename, so a watcher that waits for RESULT.md to EXIST can never catch it created and still empty — PROVEN-BY internal/swarm/fakerunner_test.go:230 TestFakeRunnerPublishesAWholeFileWriteAtomically: the hard-link check asserts `secret` (the sibling name sharing dest's inode) is untouched after the write, i.e. writeFile replaced the directory entry rather than writing through the existing inode; the source-text guard at :209 additionally pins `os.CreateTemp` and `os.Rename` inside writeFile.

2. Reverting the temp+rename to an in-place create-then-fill now turns a test red — PROVEN-BY internal/swarm/fakerunner_test.go:230-231 TestFakeRunnerPublishesAWholeFileWriteAtomically: an in-place truncate+write through the planted hard link would clobber `secret`, failing the assertion (and the source guard at :210 would fail on its own). This is the test's stated purpose and its mechanism discriminates correctly: on Linux, macOS and Windows NTFS `os.Link` succeeds, so the behavioral half runs on every CI leg.

3. Before this PR, reverting the atomic publish left every existing test green (no existing test witnessed the atomicity) — UNPROVEN: this is an absence claim witnessed only by the commit message's assertion. I verified the batch tests assert the *lines* a produced RESULT.md contains (readTestFile against result files), not the timing property "never exists while empty", but nothing in the tree can prove what other tests did NOT assert.

DEFECTS

DEFECT low internal/swarm/fakerunner_test.go:209 — the test pins the temp+rename *mechanism* by grepping writeFile's source for the literal signature and for `os.CreateTemp`/`os.Rename`, so a correct alternative atomic mechanism or a benign signature refactor fails the test even though the property it claims to guard ("never created-then-filled") still holds — a false red is a maintenance cost, and the test's own comment names the property while the guard is the mechanism; an always-runnable behavioral check (e.g. os.Stat the target before and after and require the inode to change) would pin the property on every platform without reading source.

DEFECT low internal/swarm/fakerunner_test.go:219-221 — when `os.Link` fails (filesystems without hard links) the test logs a line and passes on the source-text grep alone, silently dropping the behavioral half of the guard; on such a filesystem an in-place write through an O_EXCL-ish or unlinking mechanism could pass while the atomicity property is not actually exercised — no current CI leg is affected (Linux/macOS/Windows all support hard links), but the degradation is invisible in the run output.

QUESTIONS FOR THE REVIEWER

1. The incident this guards (Studio line1-mismatch on result-after-deadline) was a race in the *fixture*'s writeFile once the runner got fast enough. Production nova-swarm already publishes RESULT.md atomically (writeAtomic in internal/swarm/pool.go:316; the .tmp+rename protocol the worker.go prompt teaches). Is the whole surface of "RESULT.md appears while empty" confined to the fixture, or does some production path still create-then-fill that this PR deliberately does not touch?

2. Was pinning the temp+rename mechanism (source grep) a deliberate choice over checking only the observable property, given the fixture's own comment frames the requirement as the property and the hard-link check alone would catch the regression on every CI platform?

3. The test bakes in the fixture's newline normalization ("the published body" asserts dest reads "the published body\n") and its arg contract (runner invoked as `card-f 1 m card root`, card file never created). Is coupling this guard to those incidental behaviours acceptable, or should the write step be exercised with a body already ending in "\n" to keep the assertion about atomicity only?

Left owed — nothing: the diff is one test function of 50 lines; I read the full PR-head fakerunner_test.go (236 lines), the fixture main.go in full (writeFile at main.go:283, its temp+rename at :298), the readTestFile helper, and the PR-head commit message. I did not run the test (not required); the "would go red on revert" property is argued from the mechanism, not executed. I did not read the rest of the package's test corpus beyond what was needed to check the claims.

git status --short (in repo/) prints nothing; git rev-parse HEAD prints:
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-2138-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2138-r1	1	2026-09-20T19:28:12Z	2026-09-20T19:36:18Z	0	opencode	deepseek-v4-flash	34358	21942	0	607488	0	0.0280
