RESULT tools22-guard-0878987f-r2 sha=5298f6be12ea — does a test guard dev commit 0878987f? (integration-16u: #1748 — batch and land refuse a held member, --lane required (T16) — )
ABSTAIN the revert does not compile
COMMIT 0878987f9d44eb8c7a9ad01a2180b949d46476fe
REPLICA 2 of 6
PRODUCTION cmd/nova-merge/batch.go cmd/nova-merge/land.go cmd/nova-merge/queue.go cmd/nova-merge/react.go cmd/nova-merge/verbs.go internal/ci/events_react.go internal/merge/fakehost.go internal/merge/host.go internal/merge/read.go internal/merge/records.go internal/merge/reviewers.go (new) internal/merge/state.go internal/merge/verdict.go (new)
TESTS cmd/nova-merge/batch_test.go cmd/nova-merge/helpers_test.go cmd/nova-merge/hold_test.go cmd/nova-merge/land_test.go internal/merge/gh_verdict_test.go internal/merge/readitem_test.go internal/merge/reviewers_test.go (new) internal/merge/verdict_test.go (new)
PACKAGES ./cmd/nova-merge/ ./internal/ci/ ./internal/merge/

ENVIRONMENT NOTE (what I measured). The sandbox sets TMPDIR to
<JOBDIR>/.nova-sandbox-tmp, which sits INSIDE the job-root git work tree
(<JOBDIR>/.git, HEAD 5298f6be). cmd/nova-merge's fixtures (TestMain + helpers)
assume t.TempDir() is NOT inside a work tree: testReviewerFile then inits its own
repo and sets a local git identity, and its `git commit` succeeds. With TMPDIR
inside the work tree it skips the init, its identity-less `git commit` fails
against TestMain's useConfigOnly environment, and getReviewersSHA refuses the
reviewer file as "uncommitted or untracked" — every hold_test fixture failed red
for that reason alone, with zero code touched. Running the gate with
GIT_CEILING_DIRECTORIES=<JOBDIR> stops git's repo discovery at the job root, the
tests take their designed init path, and the baseline is genuinely green. internal/ci
and internal/merge did not need it. I did not modify any file in repo/ for this.

STEP 3 baseline output (green, with GIT_CEILING_DIRECTORIES=<JOBDIR> as above):
  $ GIT_CEILING_DIRECTORIES=<JOBDIR> GOMAXPROCS=8 go test ./cmd/nova-merge/ -count=1
  ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-merge	121.552s
  $ GOMAXPROCS=8 go test ./internal/ci/ -count=1
  ok  	github.com/mas-bandwidth/nova-tools/internal/ci	42.664s
  $ GOMAXPROCS=8 go test ./internal/merge/ -count=1
  ok  	github.com/mas-bandwidth/nova-tools/internal/merge	12.453s
  (Without the ceiling the cmd/nova-merge run is red purely on the fixture
  artifact above — reviewer file "has uncommitted changes or is untracked" — and
  on one git-version-sensitive error string in TestSimulateInvalidInvocationsAreExitTwo;
  none of it is a product red.)

STEP 4 — the control. Reverted only the commit's production hunks: checked out
0878987f~1 for the 11 pre-existing production files, deleted the 2 that the commit
created (reviewers.go, verdict.go). Test files stayed exactly as at the base.
  git checkout 0878987f~1 -- batch.go land.go queue.go react.go verbs.go \
      internal/ci/events_react.go internal/merge/fakehost.go internal/merge/host.go \
      internal/merge/read.go internal/merge/records.go internal/merge/state.go
  rm internal/merge/reviewers.go internal/merge/verdict.go   # files the commit added
  git diff --stat (working tree vs index):
    internal/merge/reviewers.go | 196 ---------------
    internal/merge/verdict.go   | 588 --------------------------------------------
    2 files changed, 784 deletions(-)
  git diff --cached --stat (index vs HEAD — the reverted production hunks):
    cmd/nova-merge/batch.go     | 231 ++++++++------------------------------------
    cmd/nova-merge/land.go      | 188 +++--------------------------------
    cmd/nova-merge/queue.go     |  14 ---
    cmd/nova-merge/react.go     |  13 ---
    cmd/nova-merge/verbs.go     |  34 +------
    internal/ci/events_react.go |  12 ---
    internal/merge/fakehost.go  |  62 ------------
    internal/merge/host.go      |  28 ------
    internal/merge/read.go      |  85 ++++++----------
    internal/merge/records.go   |  74 +-------------
    internal/merge/state.go     |  14 ++-
    11 files changed, 85 insertions(+), 670 deletions(-)

STEP 5 output verbatim — the touched packages do not COMPILE with the commit's
production code gone, because the commit's own tests reference the reverted
symbols. A compilation failure is not GUARDED (only a failing assertion counts),
so this is ABSTAIN the revert does not compile.
  $ GIT_CEILING_DIRECTORIES=<JOBDIR> GOMAXPROCS=8 go test ./cmd/nova-merge/ -count=1
  # github.com/mas-bandwidth/nova-tools/cmd/nova-merge [github.com/mas-bandwidth/nova-tools/cmd/nova-merge.test]
  cmd/nova-merge/hold_test.go:28:34: undefined: merge.ReviewerSet
  cmd/nova-merge/hold_test.go:29:19: undefined: merge.ParseReviewers
  cmd/nova-merge/hold_test.go:75:9: l.host.SetVerdicts undefined (type *merge.FakeHost has no field or method SetVerdicts)
  cmd/nova-merge/hold_test.go:75:30: undefined: merge.Verdict
  cmd/nova-merge/hold_test.go:100:4: h.SetVerdicts undefined (type *merge.FakeHost has no field or method SetVerdicts)
  cmd/nova-merge/hold_test.go:100:28: undefined: merge.Verdict
  cmd/nova-merge/hold_test.go:130:4: h.SetVerdicts undefined (type *merge.FakeHost has no field or method SetVerdicts)
  cmd/nova-merge/hold_test.go:130:28: undefined: merge.Verdict
  cmd/nova-merge/hold_test.go:157:4: h.SetVerdicts undefined (type *merge.FakeHost has no field or method SetVerdicts)
  cmd/nova-merge/hold_test.go:157:28: undefined: merge.Verdict
  cmd/nova-merge/hold_test.go:157:28: too many errors
  FAIL	github.com/mas-bandwidth/nova-tools/cmd/nova-merge [build failed]
  FAIL
  $ GOMAXPROCS=8 go test ./internal/ci/ -count=1
  ok  	github.com/mas-bandwidth/nova-tools/internal/ci	28.811s
  $ GOMAXPROCS=8 go test ./internal/merge/ -count=1
  # github.com/mas-bandwidth/nova-tools/internal/merge [github.com/mas-bandwidth/nova-tools/internal/merge.test]
  internal/merge/verdict_test.go:8:25: undefined: ReviewerSet
  internal/merge/verdict_test.go:229:45: undefined: ReviewerSet
  internal/merge/gh_verdict_test.go:65:21: undefined: ParseReviewers
  internal/merge/gh_verdict_test.go:73:16: gh.Verdicts undefined (type *GH has no field or method Verdicts)
  internal/merge/gh_verdict_test.go:80:11: undefined: UnreleasedHolds
  internal/merge/readitem_test.go:68:5: unknown field Scope in struct literal of type Read
  internal/merge/readitem_test.go:75:5: unknown field Scope in struct literal of type Read
  internal/merge/readitem_test.go:78:5: unknown field Releases in struct literal of type Read
  internal/merge/readitem_test.go:151:13: undefined: LoadLaneVerdicts
  internal/merge/readitem_test.go:166:12: undefined: LoadLaneVerdicts
  internal/merge/readitem_test.go:166:12: too many errors
  FAIL	github.com/mas-bandwidth/nova-tools/internal/merge [build failed]
  FAIL

STEP 6 receipts — tree put back and proven clean:
  $ git checkout -- . && git checkout HEAD -- .
  $ git status --short
  (empty)
  $ git rev-parse HEAD
  5298f6be12eaa0f7e6622334d2b6a1eb427649e3

Left owed: the guard of this commit could not be measured as GUARDED/UNGUARDED
because the revert does not compile — the commit's tests (hold_test.go,
verdict_test.go, reviewers_test.go, gh_verdict_test.go, readitem_test.go) are
built directly on the very production types the commit introduces (Verdict,
ReviewerSet, UnliftedHolds, LoadLaneVerdicts, Read.Scope/Releases, Host.Verdicts).
Deleting the production code deletes the tests' compile-time ground, so no
failing assertion can be observed either way.===FILE=== card-tools22-guard-0878987f-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-guard-0878987f-r2	1	2026-09-20T20:14:35Z	2026-09-20T20:27:15Z	0	opencode	deepseek-v4-flash	89476	32802	0	1817344	0	0.0726
