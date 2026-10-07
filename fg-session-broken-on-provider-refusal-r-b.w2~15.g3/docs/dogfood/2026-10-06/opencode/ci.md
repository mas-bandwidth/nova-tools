# Dogfood: nova-ci — 2026-10-06, opencode

One friend, one tool, cold. I read only `nova-ci -h`, `nova-ci help`, every verb's
`-h`, and its page under docs/ (SPEC-CI.md), then used every verb at least once
with its real flags against a scratch module and temp dirs in `nova-ci` built
from this checkout at 8076dfd. Refusals included. `bench run` was driven end to
end through a fake `ssh` on PATH (a script that execs the remote line locally):
no server, no network. The store write was exercised through `--dry-run` and one
dial to a closed local port.

## Findings

1. `nova-ci slowtests --package-budget 0.2 --allowlist allow.ok < events.json`
   (row: `example.com/scratch/mod/beta<TAB>-<TAB>2<TAB>1.7s@run1`)

       nova-ci slowtests REFUSED: --allowlist allow.ok: line 1: package "example.com/scratch/mod/beta" must be the full module-relative path; run: nova-ci slowtests -h
       exit=2

   Expected: the row names the package exactly as the tool's own CI-SLOW line
   prints it (`package=example.com/scratch/mod/beta`), and is accepted. The
   parser (`refuseShortPackage`, internal/ci/slowtests/slowtests.go:406) accepts
   only rows starting with `cmd/`, `internal/` or `tools/` — nova-tools shapes —
   while the banner claims "slowtests and functional work in any Go module" and
   lists `--allowlist` and `--sleeps` under that same heading. The refusal names
   the wrong want (the path given IS the full module-relative path), so a
   stranger following it loops. `--sleeps` refuses identically. Grade: URGENT.

2. `printf '' | nova-ci slowtests`

       CI-SLOW FAILED packages=0 slowest=none: looked at nothing; run: nova-ci slowtests --allow-empty
       CI-LOAD load=unknown cpus=16: measured, not a verdict (the load could not be read: ...)
       exit=1

   Expected (from the tool's docs page): SPEC-CI.md, "The per-package test time
   budget", red test 3, says an empty stream is `CI-SLOW OK packages=0 slowest=none` with exit 0, and "Its one-line output" carries no FAILED shape.
   The binary and the help agree with each other (exit 1 without
   `--allow-empty`) — the spec page contradicts both, and it disagrees about the
   exit code, the one thing a CI path reads. Grade: URGENT.

3. `nova-ci slowtests --package-budget 0.2 --test-budget 0.1 < events.json`

       CI-SLEEPS test=TestSkipLed package=example.com/scratch/mod/beta: skipped for a wall-clock wait and not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given); inject a clock or tag it //go:build functional
       ...
       exit=1

   Expected: plain spaces. The line carries literal `\x20` sequences inside its
   prose — the house grammar says an item's reason "is prose: plain in the
   line". The verdict and remedy are readable, so not a wrong result. Grade: NEXT.

4. Same run with `--json`: the `sleeps` item carries only `test` and `package`
   fields — the reason and remedy prose present in the line rendering are
   dropped, though the one-value-two-renderings contract says prose survives as
   `text` in the JSON. `result.why` carries a terse summary, so a JSON reader
   learns the count but not what to do; the line reader gets the remedy. Grade: NEXT.

5. `nova-ci slowtests -h` (and the same text in the banner): the `--max`
   sentence appears twice, once inside the verb description and again after
   `--json`, with slightly different wording. A cold reader cannot tell whether
   two flags are meant. Grade: NEXT.

6. `nova-ci slowtests --package-budget 0.2 --test-budget 0.1 < events.json`

       CI-SLOW test=TestMiddle package=example.com/scratch/mod/alpha seconds=0.1s budget=0.1s

   Expected: the reported seconds to show why the test is over budget. The real
   elapsed was 0.12 s, printed rounded to one decimal, so the line reads
   "0.1 over 0.1". Package-level sums print the same way. Grade: NEXT.

7. `nova-ci github receipt --from-runner --redis 127.0.0.1:6399 --repo a/b --sha <40hex> --run-id 7 --workflow CI --conclusion success`

       redis: 2026/10/06 19:02:29 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
       redis: ... (three more such lines)
       nova-ci github receipt FAILED: XADD ev:github: dial tcp 127.0.0.1:6399: connect: connection refused; receipt write could not be confirmed: fix the store or the bench seat and rerun ci-ok

   The refusal line itself is right and exits 1. But the client library's own
   dated log leaks to stderr, and "failed to dial after 5 attempts" sits
   awkwardly beside the banner's "A refused write is tried once, not retried".
   Grade: NEXT.

8. `nova-ci local --base sprint/mechanical-2026-10-02 --dry-run`

       nova-ci local REFUSED: no merge base between "sprint/mechanical-2026-10-02" and HEAD (exit 128: fatal: Not a valid object name sprint/mechanical-2026-10-02); fetch the base (git fetch origin dev) or pass --base <ref>; run: nova-ci local -h

   Expected: the checkout's own upstream branch is exactly
   `origin/sprint/mechanical-2026-10-02`; a bare base name that exists only as
   `origin/<name>` should be tried or named back as the fix. Instead the remedy
   hard-names `dev` — the wrong ref for this card. Passing the origin/ form
   works (packages=2). Grade: NEXT.

9. SPEC-CI.md's bench section reads "a `~`, `..`, home or root path, and a
   missing tree are refused before any bench is reached". Those guards hold for
   the bench-side `--root`/`--cache` (verified: a literal `~` and a path with a
   `..` element are refused without any dial), but `--dir` gets only an exists
   check — `--dir /` reached ssh. A stranger reads the sentence as covering
   every path argument. Grade: NEXT.

## What worked (no finding, kept short)

Every verb answers `-h` at 0; unknown verbs and flags are answered with the full
name list and a nearest guess; `github receipt` names every missing field in one
line; `--dry-run` everywhere prints the plan and writes nothing; `new-rule` /
`new-verb` refuse to overwrite, refuse a tool with no `func main` with a remedy,
and every scaffold file compiles (`go build` and an internal/ci compile pass in a
scratch copy); `bench run` copied, ran, streamed the command's status, removed
exactly its run directory, passed over a non-answering host, and fell back —
`removed=yes` on the stderr line as specified; `local`'s PKG/RED/summary lines
are parseable, and it caught a stray directory I left in the tree, red for a
true reason, then went green (`red=0 make-exit=0`, 85.7 s); the cache-replay
claim in the help is true (a cached package trips `--test-budget`, not the
package budget).

The card's own mechanics: the named test `TestDocsTreeIsConsistent` does not
exist in ./internal/docs at this tip, and the report path this card mandates
makes the map guard require a row in internal/docs/catalog.go and `make map` —
both code/generated edits this card forbids. Reported, not fixed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    --- FAIL: TestCommittedMapMatchesTree (0.08s)
        agents_map_guard_test.go:23: uncatalogued directory docs/dogfood; add a row to internal/docs/catalog.go and run: make map
        agents_map_guard_test.go:23: docs/AGENTS.md is stale; run: make map
    FAIL	github.com/mas-bandwidth/nova-tools/internal/docs	1.142s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	79.317s
    FAIL

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.309s [no tests to run]

The only internal/docs failure is the map guard on this report's own new
directory (cause and remedy in the section above); every other test in both
packages passes. The same two reds are what the whole dogfood wave's report
paths will carry until one landing adds the catalog row and regenerates
docs/AGENTS.md — recorded here, not fixed here.

READ 7/10 — the banner, the verb helps and the refusal grammar answer a cold
reader fast and truly, but the page under docs/ contradicts the binary on an exit
code, repeats a paragraph, and one guard sentence reads wrong for `--dir`.
USE 8/10 — every verb ran for real including the refusals, nearest-name answers
and one turn remedies are what a CI-path tool should be, marred by the
allowlist/sleeps package-shape trap that makes two flags dead outside this repo.

urgent=2 next=7
