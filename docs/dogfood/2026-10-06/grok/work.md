# Dogfood: nova-work — 2026-10-06

One friend, one tool, cold. I read only `nova-work -h`, `nova-work help`, every
verb's `-h` (`import`, `verify`, `version`) and the tool's page under `docs/`
(`docs/CLI.md`'s `## nova-work` section), then used every verb with its real
flags: `import` in the recorded-page mode (`--fixture`) with `--dry-run`, a real
`--out`, `--replace`, `--repo`, `--max-calls` at its edge, `--page-size` at both
bounds and a missing `--out` directory; `import` against live GitHub with
`--dry-run`, a real `--out`, `--timeout` and `--max-calls`; `verify` with
`--against` (OK, drift, missing, extra, `--max`, `--max-bytes`, `--json`) and
`verify` against live GitHub; `version`; the refusals too. Built from the staged
checkout at `abb9bfecc729` and used as
`nova-work v1.0.1-0.20261007153756-abb9bfecc729` (linux/amd64 on the Linux bench,
darwin/arm64 for the live `gh` runs). No code changed: a finding is recorded
here and never fixed here.

Commands are shown as typed, with the real organization, repository, scratch
directory, fixture directory and `gh` path replaced by `<org>`, `<repo>`,
`<scratch>`, `<fixture>` and `<gh>`, so the page meets the tree's generality rule
(`docs/SPEC-CI.md#generality-text`).

## Findings

1. A live GitHub failure that is not authentication carries the authentication
   remedy, so the stated next turn cannot clear it. Authenticated machine, a
   budget the caller set:

       $ nova-work verify --tree ./tree.lisp --repo <org>/<repo> --page-size 15 --max-calls 1
       VERIFY REFUSED tree=./tree.lisp calls=1 gh=<gh>: <org>/<repo>: call budget spent: 1 of 1 calls made; run: <gh> auth status
       [exit=2]

   A deadline on the run is the same:

       $ nova-work verify --tree ./tree.lisp --repo <org>/<repo> --page-size 15 --timeout 1ns
       VERIFY REFUSED tree=./tree.lisp calls=1 gh=<gh>: <gh> api graphql: context deadline exceeded: it printed nothing on stderr; run: <gh> auth status
       [exit=2]

   I expected the breadcrumb to name the cause that is on the line: raise
   `--max-calls` for the first, drop the deadline for the second. `import` gets
   the budget right, both at plan time (est_calls) and at run time (`run: nova-work import ... --max-calls 4`), so the two verbs disagree about the same
   failure. On this machine `gh auth status` was already green, so following the
   printed command returns success and leaves the reader with no way to see that
   `--max-calls` (or the clock) was the problem. Grade: URGENT.

2. A failed `verify`'s plain result goes to stderr while an OK `verify`'s and the
   same failed value as `--json` go to stdout.

       $ nova-work verify --tree ./a.lisp --against ./b.lisp > out.txt 2> err.txt
       (out.txt is 0 bytes; err.txt holds:)
       VERIFY FAILED tree=./a.lisp sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a against=./b.lisp against_sha256=95d712b1e362e6a462f6eb4046e80a0b4342d67fd9397f404a12278226c1d0e3 repos=1 issues=0 tree_issues=0 against_issues=0 comments=0 seconds=0.0 differences=1 missing=0 extra=0 drift=1
       VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"
       [exit=1]

   The same comparison as `--json` prints 511 bytes on stdout and nothing on
   stderr, and `verify --against` with no differences prints 304 bytes of
   `VERIFY OK` on stdout (0 on stderr). I expected one delivery for the one
   result value: a reader who keeps the differences with `nova-work verify ... > diffs.txt` gets an empty file and, reading the file rather than the exit code,
   no evidence of the drift. Grade: NEXT.

3. `--max` bounds each difference kind separately instead of the listing: one
   `EXTRA` and one `MISSING` with `--max 1` print two lines and no `MORE`.

       $ nova-work verify --tree ./a.lisp --against ./tree.lisp --max 1
       VERIFY FAILED tree=./a.lisp sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a against=./tree.lisp against_sha256=a66813806e540716ba8aace8131dd64b76d094ad1d99092cb6a87d123c673a81 repos=1 issues=20 tree_issues=0 against_issues=20 comments=74 seconds=0.0 differences=2 missing=1 extra=1 drift=0
       VERIFY EXTRA path=repos/acme/widgets field=repo got="0-issues"
       VERIFY MISSING path=repos/<org>/<repo> field=repo want="20-issues"
       [exit=1]

   I expected one item and one `VERIFY MORE shown=1 total=2 --max <n> raises the ceiling, --max 0 lists all`; the banner says "A verb that lists takes --max
   <n> ... and says MORE for the rest". Two lines with no MORE is output wider
   than the ceiling the flag promises, even though the totals stay right. Grade:
   NEXT.

4. `verify --against` reports OK for two trees that differ in their own top-level
   fields, under a banner that says "verified field for field".

       $ nova-work verify --tree ./tree.lisp --against ./v_org.lisp
       VERIFY OK tree=./tree.lisp sha256=a66813806e540716ba8aace8131dd64b76d094ad1d99092cb6a87d123c673a81 against=./v_org.lisp against_sha256=d7064a4c7c145b45409b7ea4b71fd91024a9e87eb1378641893a761984f81309 repos=1 issues=20 tree_issues=20 against_issues=20 comments=74 seconds=0.0 differences=0 missing=0 extra=0 drift=0
       [exit=0]

   `v_org.lisp` is `tree.lisp` with `:org` changed; `v_source.lisp` (`:source`
   changed) and `v_fetched.lisp` (`:fetched` changed) are OK too. I expected the
   receipt to say which fields it compares: the two trees have different
   `sha256=` values and the line still says `differences=0`, and `verify -h`
   lists `:org`, `:source` and `:fetched` among the tree's keys. Grade: NEXT.

5. An organization-wide `import --dry-run` is refused although the usage, the
   `--repo` help and the command reference describe it.

       $ nova-work import --org <org> --dry-run
       IMPORT REFUSED: --dry-run with no --repo reads every repository of --org <org>, up to --max-calls 1500 calls; name one repository and run: nova-work import --org <org> --repo <org>/<name> --dry-run; run: nova-work help
       [exit=2]

   `--repo` is documented as "(default: every repository of the organization)"
   and `docs/CLI.md` describes the `IMPORT PLAN`/`--max-calls` budget check for
   exactly this run. With no `--org` at all the second line's remedy is built
   from empty values:

       $ nova-work import --dry-run
       IMPORT REFUSED: --org is required; it wants the organization whose repositories are read, as GitHub spells it; refusing to guess; run: nova-work help
       IMPORT REFUSED: --dry-run with no --repo reads every repository of --org , up to --max-calls 1500 calls; name one repository and run: nova-work import --org '' --repo ''/<name> --dry-run; run: nova-work help
       [exit=2]

   I expected the dry run to plan the whole organization and let `--max-calls`
   refuse the plan, and a remedy that does not print `''/<name>`. Grade: NEXT.

6. The refusal for an existing `--out` puts the one command that clears it
   inside the reason and points `run:` at help, and the command it suggests drops
   the run's own flags.

       $ nova-work import --org <org> --repo <org>/<repo> --page-size 15 --fixture <fixture> --out ./tree.lisp
       IMPORT REFUSED: --out ./tree.lisp exists; pass --replace to replace it: nova-work import --org <org> --out ./tree.lisp --replace; run: nova-work help
       [exit=2]

   I expected the `run:` breadcrumb to be the remedy. The suggested command
   carries no `--repo`, `--fixture` or `--page-size`, so pasting it reads the
   whole organization from live GitHub instead of repeating the run that was
   refused. Grade: NEXT.

7. `--out` naming an existing directory is caught only after the whole GitHub
   read, and the line names an internal package.

       $ nova-work import --org <org> --repo <org>/<repo> --page-size 15 --fixture <fixture> --out ./adir
       IMPORT REFUSED org=<org> calls=3 points=3 fixture=<fixture>: write ./adir: atomicfile: target "adir" is a directory; run: nova-work import -h
       [exit=2]

   The exists-without-`--replace` check runs before any call; this one spends
   the run's three calls and three rate-limit points first. I expected the same
   pre-flight check for a target that is a directory, and a line a reader can act
   on without knowing the `atomicfile` package. Grade: NEXT.

8. The documented no-login way to try `import` (`--fixture`) reads only at the
   recording's page size; any other size halves repeatedly and refuses in the
   replay package's vocabulary.

       $ nova-work import --org <org> --repo <org>/<repo> --fixture <fixture> --dry-run
       IMPORT REFUSED org=<org> calls=6 points=1 fixture=<fixture>: <org>/<repo>: workgh: replay: call 2 asked map[n:5 owner:<org> repo:<repo>], the recording asked map[n:15 owner:<org> repo:<repo>]; run: nova-work import -h
       IMPORT NOTE PAGE RETRY repo=<org>/<repo> size=25 reason="workgh: replay: call 2 asked map[n:50 owner:<org> repo:<repo>], the recording asked map[n:15 owner:<org> repo:<repo>]"
       IMPORT NOTE PAGE RETRY repo=<org>/<repo> size=12 reason="workgh: replay: call 2 asked map[n:25 owner:<org> repo:<repo>], the recording asked map[n:15 owner:<org> repo:<repo>]"
       [exit=2]

   `import -h` says `--fixture` reads recorded pages "so import can be tried with
   no login", and the same help's first-run line happens to pass `--page-size 15`
   (the recording's size), but the usage's default is 50 and the refusal does not
   say that the recording was made at another size; it says to run help. I
   expected one line naming the recording's page size, or a reading that ignores
   the requested size. Grade: NEXT.

9. `help --json` is read as an unknown verb under a banner that promises
   `--json` to every verb.

       $ nova-work help --json
       {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-work help","why":["unknown verb \"--json\"; the verbs are import, verify, help, version"]},"facts":{}}
       [exit=2]

   I expected `help` to answer as the other verbs do, with `verb:"help"`. Grade:
   NEXT.

READ 7/10 — the banner answers what, how and how-to in its first lines, every
verb's `-h` exits 0 and names its flags, effect and exit codes, and the refusals
name what an input wants; the score is held down by a usage line that promises an
organization-wide `--dry-run` the tool refuses, `help --json` refused under the
banner's "every verb takes `--json`", and a "field for field" claim that does not
cover the tree's own top-level fields.

USE 6/10 — every verb's real path ran: `import` wrote a 65,206-byte tree from the
recording and from live GitHub, the round trip and `--replace` behaved, `verify`
read the tree back against GitHub with `differences=0`, and the drift, missing,
extra, `--max`, `--max-bytes` and `--json` paths behaved; the score is held down
by a failed `verify` that puts its result on stderr, a `--max` that does not
bound a mixed-kind listing, a `--fixture` trial that reads only at the
recording's page size, and live failures whose breadcrumb always says to check
`gh auth status`.

urgent=1 next=8
