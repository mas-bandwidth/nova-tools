# nova-card dogfood — opencode-2 (zhi), 2026-10-06

Read as a stranger: only `nova-card -h`, `nova-card help`, `nova-card <verb> -h`
and its page under `docs/` (`docs/CLI.md`, the `## nova-card` section), nothing
else. Built from the staged checkout at
abb9bfecc72930ef36ce116ac2f652b0e85ad044 and used as
`nova-card v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`.
`template`, `version` and `help`; `generate` from all three sources — the
shipped findings TSV, eight ledgers and the tools' own help — with `--dry-run`,
`--max`, `--minutes`, `--tier`, `--prefix`, `--name`, `--dropped` and both the
`--repo-dir` and the `--repo/--base/--sha` forms; `lint` on its own output and
on eight deliberately red briefs; the refusals too. Commands are quoted as typed
from the checkout root (`$J/repo`, the binary `../bin/nova-card`), with the job
root shortened to `$J` in the quoted output; every `$J` below stands for that
one directory. The shipped findings fixture names invented files, so where a
quoted line carries one its invented name is elided with `…`, and this record
names no path the tree does not hold. No code changed; a finding is recorded,
never fixed here.

## Findings

1. `../bin/nova-card help bogus`

   ```
   nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
   nova-card is pre-alpha: not ready for production use.

   ```

   Expected a refusal naming the unknown help subject and the verbs there are,
   exit 2 — exactly what `../bin/nova-card bogus` prints (`nova-card REFUSED:
   unknown verb "bogus"; one of generate, lint, template, version, help; run:
   nova-card help`). As printed a misspelled verb under `help` gets the whole
   banner on stdout at exit 0 with no note, so a caller (or an AI) cannot tell
   `help bogus` from `help` or from `help generate`; the one door the tool says
   it has silently answers a name it does not have. `help help` returns the
   same banner, not a page for the help verb.
   Grade: URGENT.

2. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo foo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ../scratch/edge/e-badrepo`
   and the same with `--repo-dir . --prefix "a b" --out ../scratch/edge/e-prefix`

   ```
   CARDS OK dir=$J/scratch/edge/e-badrepo cards=2 waves=1 tier=pro
   ...
   RESULT: finding-internal-bus-send sha=0123456789ab tier: pro
   REPO: foo
   ```
   and
   ```
   CARDS OK dir=$J/scratch/edge/e-prefix cards=2 waves=1 tier=pro
   $ ls -b ../scratch/edge/e-prefix
   a\ b-cmd-nova-bus-main.md  a\ b-internal-bus-send.md  manifest.tsv
   ```

   Expected `--repo` to want `owner/name` (its own help says `--repo
   <owner/name>`) and to refuse `foo`, and `--prefix` to want the word its help
   promises ("the word every card id opens with") and to refuse a value with a
   blank in it. As printed both are accepted: the brief carries `REPO: foo`, the
   card id and its file name are `a b-internal-bus-send`, and `../bin/nova-card
   lint` calls the result `LINT OK`. The sprint stages `REPO:` and keys cards by
   id, so the tool writes briefs it promises are admitted and are not.
   Grade: URGENT.

3. `../bin/nova-card generate --from findings --out ../scratch/mp/b --repo-dir . --sha deadbeef`
   and `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/mp/c --tier bogus --max abc --minutes xyz`

   ```
   nova-card generate REFUSED: base sha "deadbeef" is not 40 hex: --sha <40hex>, or --repo-dir a checkout with a HEAD; run: nova-card help generate
   ```
   and
   ```
   nova-card generate REFUSED: invalid value for --max: it wants a whole number (write at most this many cards, in source order; 0 is all); run: nova-card help generate
   ```

   Expected one run to name every independent problem, as the standard
   requires. As printed the first hides the missing `--file` behind the bad
   `--sha` (fix the sha and `--from findings wants --file <tsv>` appears), the
   second names only `--max` and hides `--tier "bogus"` and the bad
   `--minutes`, and `--from ledger --ledger bogus … --sha deadbeef --tier
   bogus` names only `--tier`. Recovery is three turns where the tool already
   knows all three answers.
   Grade: NEXT.

4. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/gen/cards-e --minutes 0`
   (also `--max -1`, `--prefix ""`)

   ```
   CARDS OK dir=$J/scratch/gen/cards-e cards=2 waves=1 tier=pro
   $ grep -h Deadline ../scratch/gen/cards-e/*.md
   Deadline: finish within 60 minutes.
   Deadline: finish within 60 minutes.
   ```

   Expected `--minutes 0` to be honored or refused rather than silently
   replaced by the default, `--max -1` to be refused (the help says `0 is all`),
   and `--prefix ""` to be refused rather than silently replaced by `finding`.
   As printed the supplied `--minutes 0` reads `60`, `--max -1` writes every
   card, and an empty prefix writes `finding-…` ids — three silent fallbacks
   whose only tell is the output.
   Grade: NEXT.

5. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/gen/cards-d --max 1`

   ```
   CARDS OK dir=$J/scratch/gen/cards-d cards=1 waves=1 tier=pro
   $ cat ../scratch/gen/cards-d/manifest.tsv
   id	file	test	wave	deps
   finding-internal-bus-send	internal/bus/…send.go	internal/bus TestReceiptIsFsynced	1	-
   ```

   Expected the cut to keep its total (`cards=1 of 2`, or a `MORE shown=1
   total=2` line), as the output rule says. As printed a directory written with
   `--max 1` is indistinguishable from a source that held one row; the manifest
   is complete-looking and nothing says a second card was planned.
   Grade: NEXT.

6. `../bin/nova-card generate --from help --tool nova-bus --bin-dir ../scratch/fake2/bin --repo-dir . --out ../scratch/fake2/card`
   (the fake `nova-bus` help carries `example:` lines `nova-bus frobnicate
   --widget ./nothing` (exit 2) and `nova-bus ok` (exit 0))

   ```
   CARDS OK dir=$J/scratch/fake2/card cards=1 waves=1 tier=pro
   $ grep "THE TASK" ../scratch/fake2/card/help-nova-bus.md
   THE TASK. Read `nova-bus help` as a stranger who has the binary and nothing else, and make it cold-usable: no line runs over 100 characters. Then: every term the help uses is defined where it first appears or is a word a stranger knows (name each undefined one and define it in place); every example line runs as printed against the built tool (run each; one that does not is corrected or dropped); …
   ```

   Expected the card to name the example that exits 2 and the undefined terms,
   as `docs/CLI.md` says `--from help` writes "the lines over 100 characters,
   the undefined terms and the examples that do not run as printed". As printed
   the tool computes only the long lines (the real `nova-card help` card names
   five and nothing else) and hands the other two claims to its worker as
   instructions, so the brief makes its child find the defect it was cut from.
   Grade: NEXT.

7. `../bin/nova-card generate --from help --tool bogus --bin-dir ../bin --repo-dir . --out ../scratch/ref/x-badtool`

   ```
   nova-card generate REFUSED: the source yields no card; nothing to write; run: nova-card help generate
   ```

   Expected the refusal to name `bogus` and that no `bogus` help was found; the
   mixed call does say it (`--tool nova-card --tool bogus …` prints `CARDS NOTE
   skipped bogus: $J/bin/bogus help printed nothing: fork/exec $J/bin/bogus: no
   such file or directory`). As printed a single misspelled `--tool` is reported
   as an empty source, naming neither the tool nor the reason, so the reader
   looks for a problem in the source rather than the flag.
   Grade: NEXT.

8. `../bin/nova-card version --bogus`, `../bin/nova-card template --json`, `../bin/nova-card version -h`

   ```
   nova-card version REFUSED: takes no flags and no arguments, got 1; run: nova-card version -h
   nova-card template REFUSED: takes no flags and no arguments; run: nova-card help template
   usage: nova-card version [flags]
   ```
   (the `-h` then quotes `exit codes: 0 done; 1 a brief is red, named on its
   LINT DRIFT line, and nothing was written; 2 could not run`)

   Expected the refusal to name the offending flag (`--bogus`, `--json`), the
   two no-flag verbs to phrase the same mistake the same way, and each verb's
   `-h` to quote an exit table it can produce. As printed the flag goes
   unnamed, `version extra` and `version --bogus` both say `got 1` while
   `template` omits it and uses a different remedy door, and both inspections
   quote an exit 1 — "a brief is red" — that verbs which read nothing and write
   nothing can never produce.
   Grade: NEXT.

9. `../bin/nova-card --json`, `../bin/nova-card lint --json --card /nope.md`, `../bin/nova-card generate --json --out /tmp/x`

   ```
   nova-card REFUSED: unknown verb "--json"; one of generate, lint, template, version, help; run: nova-card help
   nova-card lint REFUSED: unknown flag --json; the flags of lint are --card, --dropped, --name; run: nova-card help lint
   nova-card generate REFUSED: unknown flag --json; the flags of generate are --base, --bin-dir, --dropped, --dry-run, --file, --from, --ledger, --max, --minutes, --name, --out, --prefix, --repo, --repo-dir, --sha, --tier and 1 more; run: nova-card help generate
   ```

   Expected the house shape: one result value rendered as lines or as JSON of
   the same value, with every verb accepting `--json`. As printed none of the
   five verbs has it, so a caller that consumes the other tools' JSON has to
   parse these lines by hand.
   Grade: NEXT.

10. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/ref/x-unkflag --bogus 1`

    ```
    nova-card generate REFUSED: unknown flag --bogus; the flags of generate are --base, --bin-dir, --dropped, --dry-run, --file, --from, --ledger, --max, --minutes, --name, --out, --prefix, --repo, --repo-dir, --sha, --tier and 1 more; run: nova-card help generate
    ```

    Expected a cut list to keep its total and name the flag that widens it
    (`shown=16 total=17`, or no cut at all: the seventeen flags fit the width).
    As printed `and 1 more` names neither the omitted `--tool` nor a widening
    flag, so an AI reading the refusal never learns the flag exists.
    Grade: NEXT.

11. `../bin/nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo-dir . --out ../scratch/drop/cards --dropped finding-internal-bus-send`

    ```
    CARDS OK dir=$J/scratch/drop/cards cards=2 waves=1 tier=pro
    $ ls ../scratch/drop/cards
    finding-cmd-nova-bus-main.md  finding-internal-bus-send.md  manifest.tsv
    ```

    Expected `--dropped <id>` to keep the dropped id out of the written
    directory, or its help to say it only refuses *references* to that id. As
    printed the dropped card is still generated and its own brief lints green
    (the check exempts self-reference: a same-content copy named `good.md` is
    red, `LINT DRIFT card=good check=dropped-card line=1: names
    finding-internal-bus-send …`), so a coordinator who dropped a card and
    regenerates from the same source still gets it.
    Grade: NEXT.

12. `../bin/nova-card generate --from findings --file ../scratch/ref2/badpaths.tsv --repo-dir . --out ../scratch/ref2/y-badpaths` (the row names `internal/bus/…nonexistent.go:3`)

    ```
    CARDS OK dir=$J/scratch/ref2/y-badpaths cards=1 waves=1 tier=pro
    $ sed -n '1,6p' ../scratch/ref2/y-badpaths/finding-internal-bus-nonexistent.md
    RESULT: finding-internal-bus-nonexistent sha=abb9bfecc729 tier: pro
    ...
    START: internal/bus/…nonexistent.go, internal/bus
    ```

    Expected a `paths-at-base` red line like the one a missing directory gets
    (`PATHS entry internal/nope/*.go names nothing in .`, exit 1, nothing
    written): the row's own file is gone from the checkout, so the brief sends
    its child to a file that is not there. As printed only the computed globs
    `internal/bus/*.go` are checked, they exist, and the tool writes a brief
    whose START names nothing.
    Grade: NEXT.

Right: all three sources produced lint-green briefs with computed PATHS and
waves — the shipped findings example runs exactly as printed from the checkout
root; `--dry-run` wrote nothing and printed the manifest and its line; `--out`
refused a directory that already held briefs and a path that is a file; a
missing file, a bad `--from`, a missing repository, a non-hex `--sha`, a bad
`--tier`, a bad `--ledger`, a missing `--tool`, a non-checkout `--repo-dir`, an
absolute findings path, an empty source, a duplicate `--tool` and a missing
`--card` were each refused with a remedy and wrote nothing; `--name` and
`--dropped` fired their drift checks on generated briefs; `lint` was green on
all generated shapes and red, one named `LINT DRIFT` line per check, on a
missing tier, an unfilled `<...>`, a personal name and a dropped id; the tier
and wave model held (`serial-tests` flash, 13 cards, 2 waves, `shared-paths=yes`;
`generality-fixtures` one wave and no dependencies).

READ 6/10: the banner answers the three questions and its own `example:` block
runs as printed, but `help <unknown>` returns the banner at exit 0 with no note
and the two inspection verbs quote an exit code they cannot produce, so a cold
reader cannot trust the help's edges.

USE 6/10: every real generation wrote correct cards with computed PATHS and
green lint, dry runs never wrote and refusals never overwrote, but malformed
`--repo`/`--prefix` pass silently, `--max` loses its total, `--minutes 0` is
ignored, and the from-help card leaves its own claimed checks to its worker.

urgent=2 next=10
