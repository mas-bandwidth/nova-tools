# nova-card dogfood — opencode-2, 2026-10-06

Read as a stranger, cold: only `nova-card -h`, `nova-card help`, `nova-card <verb> -h`
and the tool's page under `docs/` (`docs/CLI.md`, the `## nova-card` section), on a
Linux bench (`<bench>`), from the staged checkout at
`7acb90e18a764f0e728cd5ed701196a34405a824`; the binary was built in that checkout with
`go build -o $JOB/bin/nova-card ./cmd/nova-card` and never the installed binary, and it
printed `nova-card v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`. Every
verb ran at least once with its real flags against scratch directories under the job:
`generate` from all three sources (the shipped findings TSV, the eight ledger names,
and a tool's own help, plus a fake tool with a long banner line), `lint` on generated
briefs and on deliberately red ones, `template`, `version` and `help`, with the
refusals and exit codes 0, 1 and 2 all hit; no store and no server was started.
Commands are quoted as typed from the checkout root, with the binary
`../bin/nova-card` and every output under `../scratch/`. A made-up actor,
`--actor boss`, stands in for every name a flag takes, and `<bench>` stands in for the
machine.

## Findings

1. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/trav/out --prefix ../evil`

   Printed:
   ```
   CARDS OK dir=../scratch/trav/out cards=2 waves=1 tier=pro
   (one line printed)
   ```
   I expected the two briefs inside `--out`, with `--prefix` the word every card id
   opens with. Instead the run wrote `evil-internal-bus-send.md` and
   `evil-cmd-nova-bus-main.md` into the parent of `--out` (`../scratch/trav/`), left
   only `manifest.tsv` inside `--out` with the ids `../evil-…`, and reported success.

   Grade: URGENT (wrong result: brief files written outside the named directory, and
   a manifest whose ids name no file there).

2. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo foo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ../scratch/g/find-badrepo`

   Printed:
   ```
   CARDS OK dir=../scratch/g/find-badrepo cards=2 waves=1 tier=pro
   (one line printed)
   ```
   I expected `--repo` to want `owner/name`, as its own help says, and to refuse
   `foo`. Instead both briefs carry `REPO: foo` on line 2 and `lint` calls each
   `LINT OK`; the same run with `--repo a/b/c` writes `REPO: a/b/c`.

   Grade: URGENT (wrong result: a REPO line the sprint stages at BASE: that no
   owner/name can match).

3. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/g/find-prefix --prefix 'a b'`

   Printed:
   ```
   CARDS OK dir=../scratch/g/find-prefix cards=2 waves=1 tier=pro
   (one line printed)
   ```
   I expected a word, since the help says `--prefix <word>` is the word every card id
   opens with, and to be refused the blank. Instead the ids and file names are
   `a b-internal-bus-send` and `a b-cmd-nova-bus-main.md`, `lint` calls them
   `LINT OK`, and `--prefix a/b` is accepted into the id only to fail mid-write with
   `cannot write a/b-internal-bus-send.md: … no such file or directory`.

   Grade: URGENT (wrong result: card ids the sprint keys on carry a blank).

4. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/g/find-max1 --max 1`

   Printed:
   ```
   CARDS OK dir=../scratch/g/find-max1 cards=1 waves=1 tier=pro
   (one line printed)
   ```
   I expected a bounded cut to keep its total (`cards=1 of 2`, or a
   `MORE shown=1 total=2` line). Instead the manifest holds one row and the line
   reads `cards=1`, indistinguishable from a source that held one row.

   Grade: NEXT (a cut that does not keep its total).

5. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/g/find-minneg --minutes -5`

   Printed:
   ```
   CARDS OK dir=../scratch/g/find-minneg cards=2 waves=1 tier=pro
   (one line printed)
   ```
   I expected a negative number of minutes to be refused, not written into the
   brief. Instead both briefs read `Deadline: finish within -5 minutes.`, and
   `--max -1` likewise writes every card although the help says `0 is all`.

   Grade: NEXT (a bad value accepted into the brief).

6. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --minutes 0 --out ../scratch/g/find-min0`

   Printed:
   ```
   CARDS OK dir=../scratch/g/find-min0 cards=2 waves=1 tier=pro
   (one line printed)
   ```
   I expected `--minutes 0`, a supplied value, to be honored or refused. Instead
   both briefs say `Deadline: finish within 60 minutes.`, so the default silently
   stands in for the input; `--prefix ''` is silently replaced by `finding` the same
   way.

   Grade: NEXT (a silent fallback whose only tell is the output).

7. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/g/unkflag --bogus 1`

   Printed:
   ```
   nova-card generate REFUSED: unknown flag --bogus; the flags of generate are --base, --bin-dir, --dropped, --dry-run, --file, --from, --ledger, --max, --minutes, --name, --out, --prefix, --repo, --repo-dir, --sha, --tier and 1 more; run: nova-card help generate
   (one line printed)
   ```
   I expected the cut list to name the flag it hid (`--tool`) or at least a total.
   Instead `and 1 more` names neither, so a reader never learns the flag that widens
   the list exists.

   Grade: NEXT (a bounded list that hides its total and its widening flag).

8. `../bin/nova-card generate --from ledger --ledger serial-tests --repo-dir . --out ../scratch/g/led-unk --tool x`

   Printed:
   ```
   CARDS OK dir=../scratch/g/led-unk cards=10 waves=2 tier=flash shared-paths=yes (add with --allow-shared-paths)
   (one line printed)
   ```
   I expected `--tool` to be refused when the source is a ledger, since the help
   scopes it `with --from help`. Instead it was accepted and ignored.

   Grade: NEXT (a flag that does not apply to the chosen source is silently
   ignored).

9. `../bin/nova-card generate --from help --tool bogus --bin-dir ../bin --repo-dir . --out ../scratch/g/help-bogus`

   Printed:
   ```
   nova-card generate REFUSED: the source yields no card; nothing to write; run: nova-card help generate
   (one line printed)
   ```
   I expected the refusal to name `bogus` and that no help was found, as the mixed
   call does (`CARDS NOTE skipped bogus: … fork/exec …: no such file or directory`).
   Instead a single misspelled `--tool` is reported as an empty source, so the reader
   looks for a problem in the source rather than in the flag.

   Grade: NEXT (a refusal that names neither the tool nor the reason).

10. `../bin/nova-card generate --from help --tool nova-widget --bin-dir ../scratch/fake2/bin --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ../scratch/g/help-widget`

    Printed:
    ``` CARDS OK dir=../scratch/g/help-widget cards=1 waves=1 tier=pro (one line printed) ```
    I expected the card to name the lines over 100 characters, the undefined terms
    and the examples that do not run, as `docs/CLI.md` says `--from help` writes. It
    named only the long lines (`THE TASK` reads `1 line(s) run over 100 characters (help line 2 (158 chars)); wrap each`) and handed the other two checks to its
    worker as instructions: `every term the help uses is defined … (name each undefined one …)` and `every example line runs as printed … (run each; one that does not is corrected or dropped)`.

    Grade: NEXT (the page promises a computed finding the card makes its child
    find).

11. `../bin/nova-card help bogus`

    Printed:
    
    nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
    nova-card is pre-alpha: not ready for production use.

     I expected a refusal naming the unknown help subject and the verbs there are, exactly as `../bin/nova-card bogus` prints (`nova-card REFUSED: unknown verb
    "bogus"; one of generate, lint, template, version, help; run: nova-card help`).
    Instead a misspelled verb under `help` gets the whole banner on stdout at exit 0
    with no note, so `help bogus` cannot be told from `help`; `help help` returns the
    same banner rather than a page for the help verb.

    Grade: NEXT (help that answers a name it does not have without saying so).

12. `../bin/nova-card version --bogus`

    Printed:
    ``` nova-card version REFUSED: takes no flags and no arguments, got 1; run: nova-card version -h (one line printed) ```
    I expected the refusal to name the offending flag (`--bogus`), the two no-flag
    verbs to phrase the same mistake the same way (`template --json` says `takes no flags and no arguments` with no `got 1` and a different remedy door), and each
    verb's `-h` to quote only exits it can produce. Instead the flag goes unnamed and
    both inspections quote `1 a brief is red, named on its LINT DRIFT line`, an exit
    1 those verbs can never print.

    Grade: NEXT (an unclear refusal and an exit table that does not fit the verb).

13. `../bin/nova-card lint --json --card ../scratch/g/find-dir/finding-internal-bus-send.md`

    Printed:
    ``` nova-card lint REFUSED: unknown flag --json; the flags of lint are --card, --dropped, --name; run: nova-card help lint (one line printed) ```
    I expected the one result value to have a machine-readable rendering, as the
    shared house shape says every verb takes `--json`. Instead no verb of nova-card
    has one, so a caller that consumes the other tools' JSON must parse these lines
    by hand.

    Grade: NEXT (a missing flag across every verb).

## What held

`generate` ran clean from all three sources and wrote only inside `--out` when the
flags were in range: the shipped findings TSV gave two cards and a `manifest.tsv` that
`lint` then called `LINT OK` file by file; the `serial-tests`, `dead-code`,
`transcripts` and `generality-fixtures` ledgers gave their waves and
`shared-paths=yes`; `--dry-run` planned and printed the manifest with `dry-run=yes` and
wrote nothing; a re-run into a directory that already held briefs was refused; and
`--tier bogus`, `--max abc`, a missing `--file`, a missing repository, an unreadable
`--file`, an uppercase `--sha` and an `--out` that is a file were each refused at exit
2. `lint` ran clean on every generated brief and red on the template, on a brief whose
line 1 carried `tier: turbo`, on a brief that depended on a dropped id, and on a brief
naming a `--name` value, each with a named `check=` and exit 1. `template` printed the
card template, `version` printed the one version line, and `help` answered the banner
and every verb's page at exit 0. The empty `slowwaits`, `sleeps-skips` and `fixed-waits`
ledgers each refused with `the source yields no card; nothing to write`, and the
`namedpaths` ledger refused four `paths-at-base` drifts and wrote nothing. Not done: no
`sprint add` ran on any generated directory (it needs a store and the card forbids
starting a server); no brief was fixed and no tool file was changed; the `--from help`
source was run with an explicit `--bin-dir` except once without it, where it fell back
to an installed `nova-card v1.2.0-rc1` on `PATH`, so the quoted runs used the staged
binary.

READ 6/10 — the banner, the flag table and the exit table answer what the tool does and
how to run it, but the page overstates what `--from help` computes, `--prefix` and
`--repo` accept values their own help forbids, and the two inspection verbs quote an
exit they cannot produce.

USE 5/10 — every verb and all three sources produced linted briefs from scratch
directories, but `--prefix ../evil` writes briefs outside `--out` at exit 0, `--repo foo` writes a REPO line the sprint cannot stage, and a blank or slashed `--prefix`
reaches the card id.

urgent=3 next=10
