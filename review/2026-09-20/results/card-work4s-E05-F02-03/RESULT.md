RESULT work4s-E05-F02-03 sha=5298f6be12ea — nova-work E05-F02: does the contract say it? criterion E05-F02-03: Keep test, job, merged and attested proof distinct
DONE
CRITERION E05-F02-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "Keep test, job, merged and attested proof distinct" docs/SPEC-WORK.md -> NO_MATCH
grep -in "proof distinct\|distinct proof\|test.*job.*proof\|attested proof\|merged proof" docs/SPEC-WORK.md -> 0
grep -n "distinct" docs/SPEC-WORK.md -> 18 hits (none for this criterion beyond the acceptance schema)
grep -n "acceptance.*kind\|:kind.*:test\|:kind.*:job\|:kind.*:merged\|:kind.*:attested" docs/SPEC-WORK.md -> 4 hits
grep -in "attested" docs/SPEC-WORK.md -> 14 hits
grep -in "merged proof\|merge.*proof\|test proof\|job proof\|test.*job" docs/SPEC-WORK.md -> 1 hit (line 932)
grep -n "test job merged attested\|test.*job.*merged.*attested\|proof are distinct\|kinds.*distinct\|distinct kinds" docs/SPEC-WORK.md -> 2 hits (lines 889, 932)
docs/SPEC-WORK.md:930-936 — `:task` — work with `:acceptance`, a list of the criteria that close it, **one schema**: `(:id "c1" :kind :test :subject "test:internal/lockfile/TestLockRule1@<rev>" :predicate :passes)`, where `:kind` is `:test`, `:job`, `:merged` or `:attested`, `:subject` names the exact thing the evidence must be about (a test name, a job name, a PR number, or for `:attested` the criterion text a reviewer signs), and `:predicate` is what must be true of it (`:passes`, `:succeeds`, `:merged-at`, `:attested-by`); `node add --acceptance` takes exactly this form, and an evidence pointer qualifies a criterion only when its kind matches
:by-feature tests for E05-F02 (from nova-work.sexp:93): a-done-need-is-unverified-without-complete-proof (IN TREE replays-785-gate.lisp), verify-stale-evidence-needs-no-fetch (IN TREE slice-13-verifier.lisp), stale-evidence-does-not-unmeet-a-need (IN TREE replays-785-gate.lisp), a-done-need-on-unverified-evidence-admits-nothing (IN TREE replays-785-gate.lisp), an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less (IN TREE replays-785-gate.lisp), merged-is-not-distributed (IN TREE slice-05-durable-journal.lisp) — all 6 in tree, none stale
git status --short (empty)
Noticed: The roadmap row says "proof" (singular) while the spec says "criteria" (acceptance criterion kinds). The four kinds `:test`, `:job`, `:merged`, `:attested` are the spec's equivalent of distinct proof types. Also noted that feature E05-F02 in the sexp has `:verified 3 :total 3` but lists 6 test names — indicating the total was adjusted independently of the test name count.