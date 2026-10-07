# nova-work dogfood, 2026-10-06 (opencode-2)

Friend: opencode-2. Base: `sprint/mechanical-2026-10-02`. The tool was read cold only
from its own help (`nova-work -h`, `nova-work help`, `nova-work help import`,
`nova-work help verify`, `nova-work help version`, every verb's `-h`) and from its
pages under `docs/` (`docs/CLI.md#nova-work`, `docs/SPEC-WORK-V1.md`,
`docs/TESTS.md#nova-work`), then used for real on a small bench (go1.26.6
linux/amd64, `GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1`). `gh` was present but not
logged in, so the live GitHub verbs were exercised through their real refusals and
the `--fixture` path was exercised end to end; every verb was invoked at least once
with its real flags, and the refusals were invoked too.

`$SB` is a scratch directory made for this run; `$FIX` is
`internal/workgh/testdata/reliable`, the recorded public repository (twenty issues,
read at fifteen a page). Tool output below elides the two paths. `a.lisp` is the
minimal tree `verify -h` prints; `b.lisp` is `a.lisp` with `:archived true`; `c.lisp`,
`d.lisp`, `e.lisp` are `a.lisp` with `:org "other"`, a different `:fetched`, and
`:source "gitlab"`; `tree2.lisp` is the fixture import.

## Findings

1. `nova-work import --org $ORG --repo $ORG/$REPO --fixture $FIX --page-size 15 --out tree2.lisp` (tree2.lisp exists)
   ```
   IMPORT REFUSED: --out tree2.lisp exists; pass --replace to replace it: nova-work import --org $ORG --out tree2.lisp --replace; run: nova-work help
   ```
   Expected: the remedy is the same call plus `--replace`, keeping `--repo
   $ORG/$REPO`, `--fixture $FIX` and `--page-size 15`; the spec says a
   remedy carries every value the caller gave, one shell word each. What it does: the
   remedy keeps only `--org` and `--out`, so pasting it runs a live whole-organization
   import with `--replace` and would replace the single-repository fixture tree with an
   organization-wide read. Grade: URGENT — following the tool's own prescription gives a
   different scope and a different result.

2. `nova-work verify --tree a.lisp --against c.lisp` (c.lisp differs only in `:org "other"`)
   ```
   VERIFY OK tree=a.lisp sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a against=c.lisp against_sha256=d1af6695d78d08a6247e22a0f667cf9b76f85763d918d40fb43aa74e1ee42d9e repos=1 issues=0 tree_issues=0 against_issues=0 comments=0 seconds=0.0 differences=0 missing=0 extra=0 drift=0
   ```
   Expected: two tree records that name different organizations should not report
   `differences=0`; the header is part of the tree the reader parses, so either the
   difference is a line or `--against` refuses two organizations. What it does: the
   tree's own `:org` is not compared at all (the same OK comes for `:source` and
   `:fetched` differences, and the OK line never prints the orgs). `differences=0` is
   the receipt the destructive mode requires, so a false OK is a wrong result. Grade:
   URGENT. (If provenance is out of scope by design, the help must say so.)

3. `nova-work import --org $ORG --repo $ORG/$REPO --fixture $FIX --dry-run` (the flag's default page size 50; the recording is at 15)
   ```
   IMPORT REFUSED org=$ORG calls=6 points=1 fixture=$FIX: $ORG/$REPO: workgh: replay: call 2 asked map[n:5 owner:$ORG repo:reliable], the recording asked map[n:15 owner:$ORG repo:reliable]; run: nova-work import -h
   IMPORT NOTE PAGE RETRY repo=$ORG/$REPO size=25 reason="workgh: replay: call 2 asked map[n:50 owner:$ORG repo:reliable], the recording asked map[n:15 owner:$ORG repo:reliable]"
   IMPORT NOTE PAGE RETRY repo=$ORG/$REPO size=12 reason="workgh: replay: call 2 asked map[n:25 owner:$ORG repo:reliable], the recording asked map[n:15 owner:$ORG repo:reliable]"
   ```
   Expected: `--fixture` is documented as "reads recorded GraphQL pages ... so no login
   is needed", and the recording's own `vars` carry its page size, so the import can use
   the recorded size; at least the refusal should name it. What it does: the retry ladder
   halves 50 -> 25 -> 12 -> 6 -> 5 and never tries the recorded 15, the reason prints a Go
   map literal to the caller, and the only remedy is `import -h`. Grade: NEXT — friction
   and an opaque refusal, not a wrong result.

4. `nova-work import --dry-run`
   ```
   IMPORT REFUSED: --org is required; it wants the organization whose repositories are read, as GitHub spells it; refusing to guess; run: nova-work help
   IMPORT REFUSED: --dry-run with no --repo reads every repository of --org , up to --max-calls 1500 calls; name one repository and run: nova-work import --org '' --repo ''/<name> --dry-run; run: nova-work help
   ```
   Expected: one refusal for the missing `--org`, and any scope remedy runnable. What it
   does: the dry-run scope guard fires second with `--org ''` and a literal `<name>`, so
   the remedy cannot run; with `--org acme` the same line prints `--repo acme/<name>`,
   still a literal placeholder. Grade: NEXT — a remedy that will not run.

5. `nova-work import --org acme --repo acme/widgets --dry-run` (gh present, not logged in)
   ```
   IMPORT REFUSED org=acme calls=1 points=0 gh=/usr/local/bin/gh: /usr/local/bin/gh api graphql: exit status 4: To get started with GitHub CLI, please run:  gh auth login\x0aAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.; run: /usr/local/bin/gh auth status
   ```
   Expected: a one-line refusal whose remedy is the next command for the failure. What it
   does: the remedy is `gh auth status`, which reports the same state and cannot fix it,
   while the gh text inside the reason names `gh auth login`; the reason also carries a
   literal `\x0a` where gh's error had a newline. Grade: NEXT — friction in a refusal a
   reader must act on.

6. `nova-work help import verify`
   ```
   IMPORT REFUSED: --org is required; it wants the organization whose repositories are read, as GitHub spells it; refusing to guess; run: nova-work help
   IMPORT REFUSED: --out is required unless --dry-run; it wants the tree file to write; run: nova-work help
   IMPORT REFUSED: takes no positional arguments, got "verify" (flags come before arguments); run: nova-work help
   ```
   Expected: `help` is help; `nova-work help import verify` prints `import`'s help or
   refuses the extra argument as a help call. What it does: the extra positional is
   parsed by the `import` verb, so `help` runs the verb's flag path
   (`help import --out ... --org ...` does print help, so the routing changes with the
   shape of the second argument). Grade: NEXT — inconsistent help routing.

7. `nova-work help --json`
   ```
   {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-work help","why":["unknown verb \"--json\"; the verbs are import, verify, help, version"]},"facts":{}}
   ```
   Expected: every verb accepts `--json` and the one result names the verb it answers.
   What it does: `help` accepts no `--json`, and the refusal reports `"verb":""` (the
   bare `nova-work --json` does the same). Grade: NEXT — a missing flag and an empty
   verb in the one result shape.

8. `nova-work verify --tree a.lisp --against b.lisp`
   ```
   VERIFY FAILED tree=a.lisp sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a against=b.lisp against_sha256=95d712b1e362e6a462f6eb4046e80a0b4342d67fd9397f404a12278226c1d0e3 repos=1 issues=0 tree_issues=0 against_issues=0 comments=0 seconds=0.0 differences=1 missing=0 extra=0 drift=1
   VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"
   ```
   Expected: the status word the command reference and the spec name. What it does:
   `docs/CLI.md#nova-work` writes `VERIFY FAIL` (and "exit 1") and
   `docs/SPEC-WORK-V1.md` section 1.6 writes "under `VERIFY FAIL ... differences=`", but
   the tool prints `VERIFY FAILED`. Grade: NEXT — docs that disagree with the output.

9. `nova-work verify --tree tree2.lisp --against a.lisp` (two organizations)
   ```
   VERIFY FAILED tree=tree2.lisp sha256=1203baf71515dafc1c92b8c5d549b9f1e8fa411c152f9bdfda1c2fb083a19647 against=a.lisp against_sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a repos=1 issues=0 tree_issues=20 against_issues=0 comments=0 seconds=0.0 differences=2 missing=1 extra=1 drift=0
   VERIFY MISSING path=repos/acme/widgets field=repo want="0-issues"
   VERIFY EXTRA path=repos/$ORG/$REPO field=repo got="20-issues"
   ```
   Expected: a repository-level MISSING/EXTRA line in a shape the help documents (the
   spec names "a repository" as a thing that can be missing or extra but never the field
   or its value). What it does: the line uses `field=repo` with the synthesized value
   `"0-issues"` / `"20-issues"` — the issue count dressed as a field value; no tree field
   is named `repo` under a repository path, so a reader cannot tell what `want=0-issues`
   means. Grade: NEXT — an undocumented output shape.

10. `nova-work version -h`
    ```
    usage: nova-work version [flags]
    nova-work is pre-alpha: not ready for production use.
    from `nova-work help`:
    ...
    exit codes: 0 done, or verify found no difference; 1 verify found differences, or an import's encoded tree did not read back equal; 2 could not run (a flag, the budget, gh, GitHub, a file)
    effect: inspection: reads, writes nothing
    ```
    Expected: `version`'s own exit table and effect. What it does: the read verbs' text
    (`verify`, `import`, `gh`, `GitHub`) is copied whole; `version` neither reads GitHub
    nor verifies nor imports. Grade: NEXT — help noise.

11. `nova-work import --org $ORG --repo $ORG/$REPO --fixture $FIX --page-size 15 --timeout 1ns --dry-run`
    ```
    IMPORT OK org=$ORG out=- repos=1 issues=20 comments=74 references=4 linked_prs=2 bytes=65206 sha256=6ca77a532384f3743f63763203e056d1344d4ff489b9d652f7637cce5e410edb calls=3 points=3 rest=0 seconds=0.0 fixture=$FIX dry_run=true
    IMPORT PLAN repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15
    IMPORT REPO repo=$ORG/$REPO issues=20 comments=74 references=4 linked_prs=2 calls=2
    ```
    Expected: `--timeout` is "the whole run's deadline"; a run given 1ns should not finish
    with `IMPORT OK`, exit 0. What it does: the deadline is not enforced on the `--fixture`
    path, so the flag that bounds a run does nothing there. Grade: NEXT — a bound that is
    documented and not felt on the no-login path.

READ 7/10 -- The banner, every `-h`, `docs/CLI.md#nova-work` and
`docs/SPEC-WORK-V1.md` agree on the nouns and the two offline first runs work as
printed; docked for `version -h`'s copied exit table and effect, the `VERIFY FAIL` vs
`VERIFY FAILED` drift between the command reference and the output, and the `--fixture`
description that hides that the recording fixes the page size.

USE 6/10 -- Exit codes are honest, refusals name the flag and what it wants, and the
`--max-calls` and `--max-bytes` remedies keep every value and one-paste; docked for the
`--out exists` remedy that drops the caller's scope (a live whole-org write), the
dry-run no-repo remedy that cannot run, the gh-auth remedy that cannot remedy, and
`verify --against` printing `differences=0` for two trees of different organizations.

urgent=2 next=9
