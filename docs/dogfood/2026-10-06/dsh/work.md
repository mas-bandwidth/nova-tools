# Dogfood: nova-work — 2026-10-06, dsh (zhi)

One friend, one tool, cold. I read only `nova-work -h`, `nova-work help`, every verb's
`-h`, and the tool's page under `docs/` (`docs/CLI.md`, the `## nova-work` section), then
used every verb at least once with its real flags against a scratch directory and a real
GitHub organization: `import` with `--dry-run`, a real `--out`, `--replace`, `--max-calls`,
`--page-size` at its bounds, a multi-page repository and an empty repository; `verify`
against GitHub and `verify --against` offline (drift, missing, extra, `--json`, `--max`,
`--max-bytes`); `version`; the refusals too. Built from the staged checkout at
e8f70f600ebf and used as
`nova-work v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6` on the Linux bench
(no go command runs on the working machine). 15–40 minutes of use; no code changed, a
finding is recorded here and never fixed here.

Commands are shown as typed, with the real organization, repository and absolute scratch
paths replaced by `<org>`, `<repo>` and `<scratch>` so the page meets the tree's generality
rule (`docs/SPEC-CI.md#generality-text`); `<gh>` stands for the resolved `gh` path.

## Findings

1. A `gh` failure that is not an authentication failure carries the authentication remedy,
   so the one stated next turn cannot clear it. A deadline set on the run:

       $ nova-work import --org <org> --repo <org>/<repo> --dry-run --timeout 1ns
       IMPORT REFUSED org=<org> calls=1 points=0 gh=<gh>: <gh> api graphql: context deadline exceeded: it printed nothing on stderr; run: <gh> auth status
       [exit=2]

   `verify` does the same on the same class of failure:

       $ nova-work verify --tree <scratch>/delta.lisp --repo <org>/<repo> --timeout 1ns
       VERIFY REFUSED tree=<scratch>/delta.lisp calls=1 gh=<gh>: <gh> api graphql: context deadline exceeded: it printed nothing on stderr; run: <gh> auth status
       [exit=2]

   I expected the breadcrumb to name the cause that is on the line — the run's deadline,
   so `run: nova-work import --org <org> --repo <org>/<repo> --dry-run --timeout 30m`, or
   the network — because a reader who follows `run: <gh> auth status`, already logged in,
   gets a passing status and no way to tell that `--timeout` was the problem. The
   authentication remedy is correct only when the failure is authentication (it was, on
   this bench, before the login token was supplied). Grade: URGENT.

2. A failed `verify`'s plain result goes to stderr while an OK `verify`'s goes to stdout,
   and the same failed value as `--json` goes to stdout.

       $ nova-work verify --tree <scratch>/a.lisp --against <scratch>/b.lisp > out.txt 2> err.txt
       (out.txt is 0 bytes; err.txt:)
       VERIFY FAILED tree=<scratch>/a.lisp sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a against=<scratch>/b.lisp against_sha256=95d712b1e362e6a462f6eb4046e80a0b4342d67fd9397f404a12278226c1d0e3 repos=1 issues=0 comments=0 seconds=0.0 differences=1 missing=0 extra=0 drift=1
       VERIFY DRIFT path=repos/acme/widgets field=archived want="true" got="false"
       [exit=1]

   The same run with `--json` prints the one object on stdout and nothing on stderr, and a
   successful `verify --against` prints `VERIFY OK` on stdout. I expected one delivery for
   the one result value: a reader who keeps the differences with `nova-work verify ... >
   diffs.txt` gets an empty file and, if they read the file rather than the exit code, no
   evidence of the drift. Grade: NEXT.

3. `--max` bounds each difference kind separately instead of the listing: with five
   differences and `--max 2` the run prints three difference lines and its single MORE
   line accounts only for `missing`.

       $ nova-work verify --tree <scratch>/a.lisp --against <scratch>/d.lisp --max 2
       VERIFY FAILED tree=<scratch>/a.lisp sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a against=<scratch>/d.lisp against_sha256=991f093a336301ee1de7adaa01823814fde2c72dd1cc7b8b190749428c2acb8b repos=4 issues=0 comments=0 seconds=0.0 differences=5 missing=4 extra=1 drift=0
       VERIFY MISSING path=repos/acme/r1 field=repo want="0-issues"
       VERIFY MISSING path=repos/acme/r2 field=repo want="0-issues"
       (then) VERIFY EXTRA path=repos/acme/widgets field=repo got="0-issues"
              VERIFY MORE kind=missing shown=2 total=4 --max <n> raises the ceiling, --max 0 lists all

   The help says "A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE
   for the rest" and "--max <n> items listed before one MORE line stands for the rest". I
   expected at most `--max` difference lines in total and one MORE standing for all the
   rest; as printed, a caller who reads two lines still misses three differences, and
   `differences=5` in the status line is the only count that tells the truth. Grade: NEXT.

4. A scoped offline check that names a repository in neither tree reports the all-clear
   after examining nothing.

       $ nova-work verify --tree <scratch>/tree.lisp --against <scratch>/tree.lisp --repo <org>/<other>
       VERIFY OK tree=<scratch>/tree.lisp sha256=ee8bf87e168e4699733418d783f29d9e67c1187aab2086c16a40c0e5b4a1c272 against=<scratch>/tree.lisp against_sha256=ee8bf87e168e4699733418d783f29d9e67c1187aab2086c16a40c0e5b4a1c272 repos=0 issues=0 comments=0 seconds=0.0 differences=0 missing=0 extra=0 drift=0
       [exit=0]

   I expected a refusal (`<org>/<other> is not in the tree`) or a NOTE naming the empty
   scope. The network form of the same mistake is honest — `verify --tree <scratch>/zhi.lisp
   --repo <org>/<other>` reports `VERIFY MISSING path=repos/<org>/<other> field=repo
   want="0-issues"` at exit 1 — so only `--against` returns a success for a repository it
   never looked at. Grade: NEXT.

5. `import --dry-run` is refused without `--repo`, though the usage line shows `--dry-run`
   as a form that needs no `--repo`, and the refusal line carries two `run:` clauses.

       $ nova-work import --org <org> --dry-run
       IMPORT REFUSED: --dry-run with no --repo reads every repository of --org <org>, up to --max-calls 1500 calls; name one repository and run: nova-work import --org <org> --repo <org>/<name> --dry-run; run: nova-work help
       [exit=2]

   The banner's first usage line is `nova-work import --org <org> (--out <tree.lisp>
   [--replace] | --dry-run) [--repo <owner/name>]...`, which reads as if `--dry-run` alone
   is runnable; it is not. The line also closes with a second `run:` after carrying a
   `run:`-shaped remedy of its own, where the grammar is one `<reason>; run: <remedy>`. I
   expected the usage line to mark `--repo` required for `--dry-run`, or the dry run to
   read every repository as `--out` does, and one `run:` on the line. Grade: NEXT.

6. The tool's page calls the exit-1 line `VERIFY FAIL`; the tool prints `VERIFY FAILED`.

       docs/CLI.md, `## nova-work`:
       **Output.** One result per run: `IMPORT OK`, `VERIFY OK`, `VERIFY FAIL` (exit 1, ...
       and "one `VERIFY DRIFT ... field=archived` line under `VERIFY FAIL`, exit 1".
       The run:
       VERIFY FAILED tree=<scratch>/a.lisp sha256=f9e09930397ec10d9ec7b2f021aa1a6f9119dd98b9f8f29af57e6658ef9bab3a against=<scratch>/b.lisp against_sha256=95d712b1e362e6a462f6eb4046e80a0b4342d67fd9397f404a12278226c1d0e3 repos=1 issues=0 comments=0 seconds=0.0 differences=1 missing=0 extra=0 drift=1

   I expected the page and the binary to use the same status word; the no-login try-it
   instructions send a reader to look for a word the tool never prints. Grade: NEXT.

7. A missing `--against` file is refused with `import`'s help as the next step.

       $ nova-work verify --tree <scratch>/a.lisp --against <scratch>/missing.lisp
       VERIFY REFUSED against=<scratch>/missing.lisp: stat <scratch>/missing.lisp: no such file or directory; import writes a tree file with --out; run: nova-work import -h
       [exit=2]

   I expected the remedy of the verb that was run and the flag that was wrong, e.g. "the
   --against file must exist; run: nova-work verify -h"; sending a reader to `import -h`
   for a mistyped `--against` path is a turn in the wrong direction. Grade: NEXT.

8. `--out` naming an existing directory is not checked before the GitHub read, and the
   refusal names an internal package.

       $ nova-work import --org <org> --repo <org>/<repo> --out <scratch>
       IMPORT REFUSED org=<org> calls=2 points=4 gh=<gh>: write <scratch>: atomicfile: target "<scratch>" is a directory; run: nova-work import -h
       [exit=2]

   The call and point counts show the read happened first. `import -h` already says the
   `--out` directory must exist, so I expected the target to be vetted at flag time, with a
   plain reason; a slow or metered import should not spend its budget to find out the path
   is a directory. Grade: NEXT.

## What worked

The banner and every verb's `-h` answer a cold reader and exit 0 before reading anything;
unknown verbs and flags are answered with the names there are and the nearest (`--tre` gives
`--tree`, `improt` gives `import`); every refusal names the want and a runnable next command;
the import round trip holds — an import of a multi-page repository verified back to GitHub
with zero differences at `--page-size 1` and `--page-size 100`, the reported `sha256=`
matches the bytes on disk, an empty repository writes a valid empty tree and verifies OK;
`--max-calls`, `--max-bytes`, `--page-size`, `--timeout` and `--repo` are validated, and a
below-estimate `--max-calls` refuses before any issue is read with a runnable raised budget;
the reader refuses an unknown key, a repeated or out-of-order issue, and a malformed or
oversized file by name; `--against` reports `comments-order`, a long or multi-line body as
`bytes:100:sha256:...`, and `MISSING`/`EXTRA` at repository, issue and comment level;
`verify --against` ignores the `:fetched` stamp, so two imports of one state agree.

READ 7/10 — the banner, the example block and every verb help made the tool runnable in the
first few minutes, but the `import` usage line shows a `--dry-run` form the tool refuses,
the page names a status word the tool does not print, and two refusals carry a remedy for
the wrong thing.

USE 7/10 — every verb ran for real against a live organization and a scratch directory with
the round trip holding, but a failed verify's differences land on stderr while the JSON
lands on stdout, and a scoped offline check can return OK after examining nothing.

urgent=1 next=7
