RESULT tools22-pre-1867-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1867 at head b342bc91a6e0: docs/CLI.md: the land section a shipped verb never had, held against the flags it registers (#1
PREREAD 1867 claims=9 proven=2 unproven=2 defects=2 high=0

PR 1867
HEAD b342bc91a6e09a310c31ea012464dad8598f78ef
BASE dev
MERGE-BASE 0f0e46c0e26bfbab7fc84c0dfead4f551fb1c503
BEHIND 21
FILES 1 production, 1 test
LINES +101 -0

CLAIMS

1. docs/CLI.md gains a `### land` section under `## nova-merge`, giving the shipped `nova-merge land` verb the command-reference entry it never had.
   PROVEN-BY internal/docs/cli_merge_land_flags_test.go:66-71 TestTheCLIReferenceNamesEveryMergeLandFlag — `found == 0` is a fatal: the reference must document this verb.

2. The reference's one `nova-merge land` usage line names every flag the verb registers directly in cmdLand, so a flag the reference does not name is a flag nobody can find.
   PROVEN-BY internal/docs/cli_merge_land_flags_test.go:73-77 TestTheCLIReferenceNamesEveryMergeLandFlag — for each of the 11 flags scanned out of cmdLand's body, an Errorf if the usage line lacks `--<flag>`.

3. `land` is the one entrance to the merge queue and enqueues the pull request at the front, unless `--no-jump` sends it to the back.
   PROVEN-BY-EXISTING cmd/nova-merge/land_test.go:98 TestLandEnqueuesAGreenBatchAtTheFront (jumps[0]==true, one enqueue) and :213 TestLandWithoutJumpQueuesBehind (jumps[0]==false).

4. Exactly one of `--reviewers <file>` and `--no-require-holds` is required.
   PROVEN-BY-EXISTING cmd/nova-merge/hold_test.go:785,794 (asserts the "exactly one of --reviewers <file> or --no-require-holds --reason <text> is required" line for both the neither case and the both case).

5. A head that is not a batch's — a branch named `rowan/integration-*`, or one a `BATCH OK` receipt names this very commit — is refused.
   PROVEN-BY-EXISTING cmd/nova-merge/land_test.go:117 TestLandRefusesAPullRequestThatIsNotABatch (a non-batch head is refused at exit 1 and the queue is untouched) and :198 TestLandRefusesAReceiptForAnotherHead (a receipt for another head is refused).

6. `--receipt-file` reads the receipt from a file.
   PROVEN-BY-EXISTING cmd/nova-merge/land_test.go:175 TestLandTakesABatchOKReceiptFromAFile (a single-line receipt file opens the door and the enqueue happens).

7. `--receipt-file` takes the LAST line, so a caller may hand it the gate's whole output.
   UNPROVEN — no test feeds `land` a multi-line receipt file; lastBatchLine (cmd/nova-merge/land.go:304) is asserted by no test. (batch's `--receipt-file`, dogfood_test.go:434, is a different parser that reads every BATCH OK line, batch.go:658.)

8. A pull request whose own CI checks are not green is refused.
   PROVEN-BY-EXISTING cmd/nova-merge/land_test.go:138 TestLandRefusesAPullRequestWhoseChecksAreNotGreen (red, pending and no-checks cases all refused at exit 1 with the queue untouched).

9. Its first refusal is the one line `nova-merge land: --repo is required; refusing to guess: the repository whose merge queue this batch enters, as <owner>/<name>`.
   UNPROVEN — the exact refusal text is asserted nowhere; TestLandRefusesAnInvocationThatGuesses (land_test.go:227) checks only exit 2 and that the forge was not reached. (I read the text straight out of `f.require` at land.go:58 + `done` at main.go:333-338; it matches, but nothing holds it.)

DEFECTS

DEFECT medium docs/CLI.md:1292-1297 — the reference marks `--lane` and `--reason <text>` optional while `cmdLand` requires `--reason` whenever `--no-require-holds` or `--untyped-comments=ignore` is given and requires `--lane` whenever `--reviewers` is given (land.go:71-82) — a person following the reference alone runs a refused invocation (exit 2) with the coupling the reference never states, which is the very class of "flag nobody can find" gap this PR exists to close — encode the couplings in the usage line and prose, e.g. `[--reviewers <file> --lane <name>] | [--no-require-holds --reason <text>]` and `--untyped-comments=ignore` requires `--reason`.

DEFECT low internal/docs/cli_merge_land_flags_test.go:36-53 — the scan covers only flags registered directly in cmdLand with the exact `f.fs.` receiver, and unlike its sibling cli_dogfood_gate_flags_test.go:14-15 it never says so — a future move of `--lane`/`--timeout` into a shared helper (laneSet) silently takes them out of the guard, a false green rather than a false red — add the sibling's one-line note documenting the limit.

QUESTIONS

1. `nova-merge help`'s banner (cmd/nova-merge/main.go:65) lists `land` with only `--repo`, `--pr`, `--receipt`, `--receipt-file`, `--no-jump`, `--timeout`, omitting `--lane`, `--reviewers`, `--no-require-holds`, `--reason`, `--untyped-comments`. Is the banner deliberately a short form (so out of scope for the "a flag the reference does not name is a flag nobody can find" standard), or should it be held or updated by this or a follow-up?

2. The new test holds the docs usage line to the flags registered in cmdLand but not the reverse — a flag named in the usage line but never registered would pass. Is one-directional coverage deliberate (the reference is the superset a person reads first), or is set-equality intended?

3. `--receipt-file` semantics differ between `land` (last non-empty line, land.go:304) and `batch` (every BATCH OK line, batch.go:658). For a gate output whose verdict is not the file's last non-empty line, `land` would take the wrong line while `batch` would not. Is the last-line contract deliberate, and why not reuse batch's all-lines parser?

Left owed — the diff is two files and I read both in full, plus the whole of cmd/nova-merge/land.go and land_test.go at the PR head tree, the relevant parts of internal/merge/enqueue.go, hold_test.go, dogfood_test.go, batch.go and main.go. I did not read the other ~1100 lines of docs/CLI.md beyond the nova-merge section and surroundings (the diff touches only the `### land` insertion point). I did not run go build/go vet or the test: the working tree is on `main`, not the PR head, and I chose not to checkout the PR head so the clone stays exactly as fetched; the test was verified statically (body-cut is sound — the only column-0 `}` in lines 42-114 of land.go is the function's own — and all 11 flags appear in the usage line).

git status --short (in repo/):
(nothing printed)

git rev-parse HEAD (in repo/):
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1867-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1867-r2	1	2026-09-20T19:24:17Z	2026-09-20T19:34:15Z	0	opencode	deepseek-v4-flash	50639	41689	0	2025728	0	0.0755
