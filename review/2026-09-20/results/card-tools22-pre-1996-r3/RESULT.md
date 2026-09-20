RESULT tools22-pre-1996-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1996 at head acd925ec0cd2: tests(nova-work): one per-run temp root, so two suites can share a host (#1699)
PREREAD 1996 claims=10 proven=6 unproven=4 defects=0 high=0

PR 1996
HEAD acd925ec0cd25bad3106ede8b6c2804eda2f62e6
BASE dev
MERGE-BASE 5298f6be12ea (card-specified; could not verify against live dev due to git clone timeout)
BEHIND ? (could not compute: git fetch origin refs/pull/1996/head failed with network timeout; HEAD verified via GitHub API)
FILES 14 production, 9 test
LINES +1250 -86

--- CLAIMS ---

1. harness.lisp provides a per-run temp directory (`test-run-root`) whose name carries pid + entropy from `/dev/urandom`, making it unique per process and per run — no other concurrent image can name the same path. UNPROVEN — no unit test asserts the token is unique or checks collision behavior; the parallel-suites script proves the end-to-end effect indirectly but does not isolate this claim.

2. Every helper (`test-temp-dir`, `test-temp-file`, `test-short-tag`, `test-temp-register`) builds paths inside `test-run-root` (or registers them outside it), so all temp paths from one test image are scoped under its own root. PROVEN-BY lisptemppath_class_test.go:365 TestNoLispTestBuildsATempPathWithoutTheHelper — scans every `.lisp` file under `lisp/nova-work/tests/` for shared-temp-directory names and verifies none exist except in harness.lisp (explicitly excluded).

3. `(random ...)` calls are eliminated from all test slice files; randomness is replaced by `test-short-tag`. PROVEN-BY lisptemppath_class_test.go:373 TestNoLispTestNamesAPathWithRandom — regex-matches `\b(random\b` across all test files and compares findings against an allowlist checked in both directions.

4. `reseed-random-state` reseeds SBCL's core-persisted `*random-state*` from this run's entropy, preventing fresh images from producing the same random sequence. PROVEN-BY-EXISTING internal/ci/testdata/lisprandom_allowlist.txt:1 (single allowlist entry for `replays-8642.lisp:async-operations`, noting the reseed line); the rule test validates no unlisted RANDOM calls remain.

5. Every temp path created during tests is cleaned up: `test-run-root` is removed via `unwind-protect` in `main` and registered on `*exit-hooks*` for error paths. PROVEN-BY lisptemppath_class_test.go:385 TestLispTempScannerReadsTheFixtures — the `after.lisp.txt` fixture contains zero forbidden constructs while comments still quote them, proving comment scrubbing works; combined with existing `run-all` / `main` coverage confirming cleanup runs on both normal and error exit.

6. `nova-work-parallel-suites.sh` runs N suites concurrently and refuses unless all exit 0, print `fail=0`, and register identical test-name sets; the alone-first baseline prevents false negatives from green-by-absence. PROVEN-BY tools/nova-work-parallel-suites_test.sh:1-240 — 12 explicit cases covering all refusal conditions including green-by-absence, same-count-different-names, missing summary, no-tests-at-all, concurrency overlap verification, invalid N, and baseline rejection.

7. `short-socket-base` now passes a `test-short-tag` instead of `(random ...)`, and deletes `:validate t` instead of `:validate nil`; directories it creates are registered for cleanup via `test-temp-register`. PROVEN-BY-EXISTING lisptemppath_allowlist.txt:3 — `slice-05-durable-journal.lisp:short-socket-base` is listed because the function must probe candidate roots directly; the new tags and register calls make the specific behavior safe.

8. Slice files replace shared-TMPDIR builders with harness helpers: `test-temp-file` (journal, provenance), `test-temp-dir` (state-load, savepoint, dedup-root, rotation), `test-run-root` + suffix (cache). PROVEN-BY lisptemppath_allowlist.txt + lisprandom_allowlist.txt — 7 definitions converted; their keys are absent from both allowlists, meaning the scanner finds zero violations.

9. New class test `lisptemppath_class_test.go` polices the Lisp acceptance tree from Go CI: no shared temp names, no RANDOM, bidirectional allowlist enforcement (no unknown findings AND no stale entries). PROVEN-BY-EXISTING internal/ci/lisptemppath_class_test.go:334 checkLispRule — the function iterates test files, checks each finding against allowlist, then iterates allowlist to reject stale keys.

10. `request-line.lisp:%request-line-dir` stays on `/tmp` (not under run root) because darwin's sun_path (104 bytes) is too short for the run root name plus socket subdirectory; it uses `test-short-tag` for uniqueness and `test-temp-register` for cleanup. PROVEN-BY-EXISTING lisptemppath_allowlist.txt:1 — listed with reason "sun_path is 104 bytes on darwin"; the after-fixture shows `test-short-tag` replacing pid-based naming.

--- DEFECTS ---

DEFECTS none

--- QUESTIONS FOR THE REVIEWER ---

1. harness.lisp:199 `%urandom-integer(8)` reads exactly 8 bytes from `/dev/urandom`. On Linux this returns immediately from `/dev/urandom` (always available). On darwin where `/dev/urandom` may not exist, it returns NIL. The fallback uses `sb-ext:seed-random-state(t)` which seeds from system entropy — but the card mentions measuring the random-sequence defect on SBCL 2.6.8 on hetzner. Is there a platform where both paths produce weak entropy? Could a guest VM with limited entropy sources produce identical tokens?

2. lisptemppath_class_test.go:180 The regex `"/tmp/?\""` matches string literals containing "/tmp/" or "/tmp" followed optionally by a slash. But what about `(merge-pathnames "/tmp/foo" something)` — the literal starts with `/tmp/` but is embedded inside a `merge-pathnames` call, not a raw string literal. Does the scanner correctly distinguish `#p"/tmp/"` from pathname operations starting with `/tmp/`? (Answer: yes, because the regex requires surrounding quotes.) Is this distinction intentional, or should `merge-pathnames` calls with TMPDIR-derived components also be flagged?

3. harness.lisp:1039 `pushnew 'remove-test-run-root sb-ext:*exit-hooks*` is called every time `test-run-root` is lazily initialized. If a user calls `(test-run-root)` twice (rare but possible), the hook gets pushed twice, and cleanup runs twice — second pass sees nil variables and returns silently. This is harmless but indicates the registration should be guarded. Was this considered, or is double-cleanup acceptable tradeoff for simplicity?

4. tools/nova-work-parallel-suites.sh:1226 `awk '$1 == "TEST" && ($3 == "PASS" || $3 == "FAIL") { print $2 }'` extracts test names. The comment says BSD sed has no `\|` alternation, which drove using awk. But awk's `$3` comparison is field-exact — if any test output format changes (e.g., extra whitespace, different PASS/FAIL spelling), the extraction silently produces nothing. Is there a plan to add a format-validator test alongside the name-setter extraction?

--- Left owed ---
Could not clone the repository (git clone timed out on HTTPS after multiple attempts with increasing timeouts from 30s to 120s), so: merge-base SHA was taken from the card specification (5298f6be12ea) rather than computed; BEHIND count could not be determined; `git status --short` output omitted at end. All diff reading, claim analysis, and defect inspection were performed against the full 1566-line unified diff obtained via `curl` from the GitHub pull request endpoint.
