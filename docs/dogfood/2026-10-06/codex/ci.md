# nova-ci dogfood — Codex (Stella), 2026-10-07

I read `nova-ci`'s own help and `docs/CLI.md`, then used each listed verb at
least once against this Linux scratch checkout. I built the tool from base
`ea4a285c32a5f58c3b65b85ca2a5ea6c32924d82`; scaffold verbs and the receipt
writer used `--dry-run`. A branch-built macOS binary ran `bench run` from the
Studio against Vision, printed `go version go1.26.6 linux/amd64`, and removed
its scratch run directory. I did not write to a Redis store.

1. command: `nova-ci local --base HEAD --dry-run`
   printed:
   > nova-ci local: base=HEAD merge-base=ea4a285c32a5 packages=2 ./internal/ci ./internal/docs
   > nova-ci local: would run nice -n 15 make test "PKGS=./internal/ci ./internal/docs" GOTEST_P=2 GOTEST_COUNT_FLAG=-count=1 GOTEST_TAGS= (GOMAXPROCS=2)
   > nova-ci local: NOTE --dry-run ran no test; run it again without --dry-run to run them
   expected: With a clean checkout and an empty diff from `HEAD` to itself, report no changed packages, or say why CI always selects these two packages.
   grade: NEXT — the dry run selects and prints tests to run even though `git status` is clean and `git diff HEAD...HEAD` is empty.

2. command: `nova-ci functional ./internal/docs`
   printed:
   > CI FUNCTIONAL OK packages=0 reason=no-functional-tag-in-1-dirs
   expected: State in plain words that the selected directory contains no functional-tagged tests.
   grade: NEXT — the `reason` value is an abbreviated machine token, so the output takes a second read to understand.

READ 9/10 — the banner groups commands by where they work, and each verb's help lists its flags and effect, but the local no-diff behavior is unexplained.
USE 8/10 — the no-store example and dry runs made safe use straightforward, while the local package selection and functional reason need clearer output.
urgent=0 next=2
