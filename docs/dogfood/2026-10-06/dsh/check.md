# Dogfood: nova-check — 2026-10-06, dsh

One friend, one tool, cold. I read only `nova-check -h`, `nova-check help`, every
verb's `-h`, and nova-check's page under docs/ (`docs/SPEC.md` §nova-check and
the `## nova-check` section of `docs/CLI.md`), then used every verb at least once
with its real flags against a scratch tree on a Linux bench: the example-self and
example-dogfood fixtures, a broken-link tree, a 25-link tree, a self tree with
scripts and machinery, a scratch git repository for `hygiene` and `nocode
--staged`, an in-memory/fixture store for `dogfood`, a fake `gh` and a fake
git checkout for `convergence`, and a misspelled markdown file for `spelling`.
Refusals included. The binary was built from this checkout at d762f5454
(`nova-check v1.0.1-0.20261006205236-d762f545478f linux/amd64 go1.26.6`) and run
on the bench over ssh, in scratch directories under the job. No server, no real
network; `convergence` read a fake `gh` and the checkout through git, and the
tool wrote nothing outside the scratch trees (`--write`/`--state` were exercised
against copies). No code changed.

## Findings

1. `nova-check dogfood ledger --tools toolsdir --receipts dogfood/receipts` (a
   directory holding the built `nova-check`), then
   `nova-check dogfood gate --tools toolsdir --receipts dogfood/receipts --require-all`

       DOGFOOD tool=nova-check verb=version by=nobody at=- ok=- issue=- open=0
       DOGFOOD tool=nova-check verb=- by=nobody at=- ok=- issue=- open=0
       DOGFOOD tool=nova-check verb=quickstart by=nobody at=- ok=- issue=- open=0
       DOGFOOD OK verbs=16 dogfooded=0 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=2

   and the gate:

       DOGFOOD GATE FAIL tool=nova-check verb=: not dogfooded by a non-author; a tool is done when somebody who did not write it has run it on real work
       DOGFOOD GATE FAIL verbs=16 findings=17 shown=17 unmatched=2

   Expected: one row per verb the binary declares. `nova-check` has no bare
   (verbless) form — the bare command answers `nova-check REFUSED: no verb
   given; quickstart is the first run; ...` — so `verb=-` is not a verb. It is
   read from the help's meta line `nova-check <verb> -h, nova-check help
   <verb>`, which the verb parser takes for a bare-invocation declaration
   because its second token is a placeholder. The same parser reads
   `docs/CLI.md` and declares `verb=-` for nova-self-talk, nova-sandbox and
   nova-config (`verbs=254`), and it also gives `nova-check` both `dogfood` and
   `dogfood ledger/record/gate` as separate verbs. `gate --require-all` then
   demands a receipt for the phantom, and it is only satisfiable by typing
   `dogfood record --verb -`, which the record help documents as "for a tool
   that takes no verb". The authoritative list and the release gate are both
   wrong about what verbs exist. Grade: URGENT.

2. `nova-check quickstart --dir self --json` (also `nova-check dogfood ledger
   --cli dogfood/CLI.md --receipts dogfood/receipts --json`)

       nova-check quickstart REFUSED: unknown flag --json; the flags of quickstart are --dir, --exclude, --fail-max, --max; run: nova-check help
       exit=2

   Expected: `docs/SPEC.md`'s Conventions say the JSON rendering is "`--json`
   (every verb takes it)"; `quickstart` and every `dogfood` verb refuse it. The
   tool's own help admits the carve-out in prose, so this is a known gap, not a
   surprise — but it is the one place nova-check steps outside the suite's
   one-value/two-renderings law, and a dogfood consumer cannot read a
   `quickstart` or a `dogfood gate` as JSON. Grade: NEXT.

3. `nova-check nocode --print-deny-list --deny-ext @nf/list.txt` (the file holds
   `md,txt`; inline `--deny-ext md,txt` splits correctly)

       NOCODE DENY-LIST source=--deny-ext count=1
       .md,txt
       NOCODE NAME-LIST source=floor-list names=14 paths=3

   and with a file holding `.md,.txt`:

       nova-check nocode REFUSED: deny-list file "nf/l2.txt": deny-list entry ".md,.txt" has more than one dot; did you mean @md,.txt?; run: nova-check help
       exit=2

   Expected (help, `nocode -h` and `CLI.md`): `--deny-ext <l|@f>` is "comma
   list, or @file". A file holding the comma list a caller would have typed
   inline is instead read one entry per line: `md,txt` becomes the single
   extension `.md,txt` (count=1), matches nothing, and the finding line still
   claims `deny-list=--deny-ext` is in force; the dotted form refuses, and its
   remedy `@md,.txt` names no file. Inline `--deny-ext md,txt` prints `count=2`.
   Grade: NEXT.

4. `nova-check spelling --path 'sp/*.txt'` (a glob matching nothing; a literal
   missing path refuses)

       SPELLING OK files=0 misspellings=0 excluded=0
       exit=0

   Expected: a selector that matched no file to say so rather than printing the
   same `OK` a clean tree prints. `spelling --file nope.md` and `spelling --dir
   nope` both refuse with the path and `refusing to guess`; only the glob form
   reads an empty selection as a pass, so a mistyped glob is invisible. Grade:
   NEXT.

5. `nova-check spelling --dir sp --file sp/bad.md` (the file exists; the same
   `--file` alone works)

       nova-check spelling REFUSED: file "/…/scratch/sp/sp/bad.md": stat /…/scratch/sp/sp/bad.md: no such file or directory; run: nova-check help
       exit=2

   Expected: `--dir sp` is the walk root and `--file sp/bad.md` narrows it, as
   the reader typed it. The spelling help does not say a relative `--file` is
   joined to `--dir`; the links help does say "`--dir` is still the resolution
   root". The refusal names a doubled path and no line explains the join.
   Grade: NEXT.

6. `nova-check links --dir lk` (a tree with `[root](/README.md)`,
   `[abs](/etc/hosts)` and a fragment link)

       LINKS FAILED README.md:5: /etc/hosts (does not exist)
       LINKS FAILED files=2 links=4 broken=1 shown=1 excluded=0
       exit=1

   Expected: the help and `CLI.md` say "every relative … link resolves"; a
   target beginning `/` is not relative, and `docs/SPEC.md` says it resolves
   against `--dir` (the GitHub convention). Because the tool's own help never
   states that, `/etc/hosts` reads as a broken relative link whose remedy is
   "does not exist" rather than "a root-relative target is checked against the
   tree root". `/README.md` in the same tree passes. Grade: NEXT.

7. `nova-check kernel --file self/docs/SEED-CORE.md --max-bytes 100` against
   `docs/SPEC.md` §nova-check's line grammar

       KERNEL FAILED self/docs/SEED-CORE.md: over budget: 771 bytes, budget 100, over by 671
       exit=1

   Expected: the spec's per-verb grammar lists `ATTEST FAIL`, `LINKS FAIL`,
   `KERNEL FAIL`, `NOCODE FAIL`, `FLOORS FAIL` and `CORPUS FAIL`; the binary
   prints `… FAILED` for every one of them, and the same spec's output-grammar
   section and `CLI.md` write `… FAILED`. A reader grepping the spec's token
   finds nothing. Grade: NEXT.

8. `nova-check version --json`

       {"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-check v1.0.1-0.20261006205236-d762f545478f linux/amd64 go1.26.6"}
       exit=0

   Expected: the one-value envelope is `result`/`facts`/`items`/`more`/`notes`.
   Every other verb here uses `facts` and `items`; `version` adds a `payload`
   key the grammar names nowhere, so a consumer branches per verb. Grade: NEXT.

## What worked (no finding, kept short)

The exit table is exact everywhere run: 0 pass, 1 check failed, 2 could not
run. `quickstart` runs both checks even when the first says NO, passes its own
`--max` down (25 broken links printed two FAILED lines, one `LINKS MORE` line
and the count line), and exits 2 when both children refuse `/nonexistent`.
`--max`/`--fail-max` cap correctly on links and spelling (`--fail-max` prints
`NOTE --fail-max is --max`). Help is never a refusal for a valid verb: bare
`help`, `help <verb>`, `-h`, `--help` and `version --help` all exit 0, and `-h`
after a verb runs nothing. Every refusal names the missing thing, its role and
a remedy, and the missing-flag refusals name every flag at once. `attest` is
exact on empty, non-canonical, escaping, duplicate and symlinked manifest
entries, and its OK line is one pasteable file/bytes/sha. `corpus` names an
absent fragment and its ledger line, refuses a ledger below its floor naming
the row count, and refuses an empty ledger. `floors` passes the repo's own
seed pair (`FLOORS OK floors=8`). `nocode` finds extension, shebang, name and
location machinery, `--allow` is genuinely repeatable, `--staged` walks the
index, and `--print-deny-list` prints both floors. `hygiene` finds identity,
out-of-path, stray-file and secret in one run, prints findings on stdout and
the count on stderr, and names the path when `git` will not answer. `dogfood`
records, lists and gates with the edge/closes rules as documented, and refuses
an undeclared verb with the nearest name. `convergence` printed seven streams,
absent-not-zero with the flag each wanted, took `before` from `--state` on the
second tick, left the same tick read twice at one streak, used the batch log
over a disagreeing body, and counted a PR open before `--since` in `before` and
`now`. `spelling` blanks fenced blocks and inline code, `--write --dry-run`
changes nothing, `--write` fixed exactly the prose and left the fence alone.

The card's own mechanics: the named test `TestDocsTreeIsConsistent` does not
exist in `./internal/docs` at this tip — `go test -run TestDocsTreeIsConsistent
./internal/docs` answers ok with no tests to run. Reported, not fixed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs/... ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.360s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	19.888s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.009s [no tests to run]

Both packages pass on a Linux bench (the working tree synced there with
`GOCACHE=$HOME/zhi-bench/.cache/go-build`, `GOFLAGS=-mod=readonly`,
`NOVA_TEST_NO_HOST=1`; `origin/dev` was fetched to its tip `ca8bb8cc3` so the
ledger-ratchet class test has its merge base). `docs/dogfood` is catalogued at
this tip, so this report's new file needs no map edit. The named test does not
exist (above); the run answers ok with no tests to run.

READ 7/10 — the banner, the verb helps, the refusal grammar and the exit table
answer a cold reader fast and truly, but the help omits the root-relative link
rule, claims `--deny-ext @file` is a comma list, and is silent that a relative
`--file` joins `--dir`, while the page's per-verb line grammar writes a `FAIL`
token the binary never prints.
USE 7/10 — every verb ran for real on scratch trees, a scratch git repository,
fake forge and checkout, and in-memory/fixture receipts, with honest refusals
and one-turn remedies, marred by the phantom `verb=-` the dogfood gate then
demands, an inert `--deny-ext @file` list, and a glob that passes on nothing.

urgent=1 next=7
