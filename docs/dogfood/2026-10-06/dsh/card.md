# nova-card dogfood — dsh (zhi), 2026-10-06

Read as a stranger: only `nova-card -h`, `nova-card help`, `nova-card <verb> -h` and
its page under `docs/` (`docs/CLI.md`, the `## nova-card` section), nothing else.
Built from the staged checkout at
cb5fb8d4c3290f710d22a21a86aa8d229e4db905 and used as
`nova-card v1.0.1-0.20261006204015-cb5fb8d4c329 linux/amd64 go1.26.6`.
`template`, `version` and `help`; `generate` from all three sources (a ledger, the
shipped findings TSV, a tool's help) with `--dry-run`, `--max`, `--minutes`,
`--tier`, `--prefix`, `--name` and `--dropped`, both the `--repo-dir` and the
`--repo/--base/--sha` forms; `lint` on its own output and on six deliberately red
briefs; the refusals too. Commands are quoted as typed from the scratch directory
(the tool `../bin/nova-card`, the checkout `../../repo`). No code changed; a
finding is recorded, never fixed here.

## Findings

1. `../bin/nova-card help bogus`

   ```
   nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
   nova-card is pre-alpha: not ready for production use.

   ```

   Expected a refusal naming the unknown verb and the verbs there are, exit 2 —
   which is exactly what `../bin/nova-card bogus` prints
   (`nova-card REFUSED: unknown verb "bogus"; one of generate, lint, template, version, help; run: nova-card help`). As printed, a misspelled verb gets the
   whole banner on stdout at exit 0 with no note, so a caller (or an AI) cannot
   tell `bogus` from `generate`; the one door the tool says it has silently
   answers a name it does not have.
   Grade: URGENT.

2. `../bin/nova-card generate` and `../bin/nova-card generate --from bogus`

   ```
   nova-card generate REFUSED: wants --out <dir>; run: nova-card help generate
   ```

   Both commands print that one line. Expected one run to name every independent
   problem: the missing `--out` and the missing or invalid `--from` (once `--out`
   is given, `--from bogus` is refused with `--from "bogus"; want ledger, findings or help`). As printed, the bad `--from` is hidden behind `--out`, so
   recovery is two turns and a script cannot see the bad kind until it invents a
   directory.
   Grade: NEXT.

3. `../bin/nova-card generate --from help --tool nova-card --bin-dir ./fakebin --repo-dir ../../repo --out ./cards-fakeb`
   (`./fakebin/nova-card` is a marker script whose `help` prints an example line
   that exits 2, `nova-card frobnicate --widget ./nothing`)

   ```
   CARDS OK dir=./cards-fakeb cards=1 waves=1 tier=pro
   ```

   The marker recorded exactly `CALL: help`: the example line was never run. The
   help says `--from help` writes "the lines over 100 characters, the undefined
   terms and the examples that do not run as printed", and the card names only
   `1 line(s) run over 100 characters (help line 1 (145 chars))`; it names no
   failing example and no undefined term, only the instruction "run each; one
   that does not is corrected or dropped". Expected the card to name the example
   that exits 2 (and the terms a stranger does not know), so the brief carries the
   defect it was cut from instead of making its worker find it.
   Grade: NEXT.

4. `../bin/nova-card lint --card ./cards-findings/finding-internal-bus-send.md --json`
   (`version --json`, `template --json` and `generate … --json` refuse the same way)

   ```
   nova-card lint REFUSED: unknown flag --json; the flags of lint are --card, --dropped, --name; run: nova-card help lint
   ```

   Expected the house shape: one result value rendered as lines or as JSON of the
   same value, with every verb accepting `--json`. As printed, none of the four
   verbs has it, so a caller that consumes the other tools' JSON has to parse
   these lines by hand, and `version`/`template` cannot even separate a viewer
   from a machine.
   Grade: NEXT.

5. `../bin/nova-card version --bogus` and `../bin/nova-card template --json`

   ```
   nova-card version REFUSED: takes no flags and no arguments, got 1; run: nova-card version -h
   nova-card template REFUSED: takes no flags and no arguments; run: nova-card help template
   ```

   Expected the refusal to name the offending flag (`--bogus`) and the two
   no-flag verbs to refuse the same mistake the same way, with the same remedy
   door (`nova-card help <verb>`, which every other verb uses). As printed the
   flag goes unnamed, `got 1` counts an argument the reader did not consciously
   pass, and the same shape of mistake gets `version -h` from one verb and the
   help from the other.
   Grade: NEXT.

6. `../bin/nova-card generate --from findings --file ../../repo/cmd/nova-card/testdata/findings.tsv --repo-dir ../../repo --out ./cards-max1b --max 1`
   (the source holds 2 cards) and the same call with `--minutes 0`

   ```
   CARDS OK dir=./cards-max1b cards=1 waves=1 tier=pro
   CARDS OK dir=./cards-min0b cards=2 waves=1 tier=pro
   ```

   Expected a cut run to keep its total, as the house output rule says (`cards=1 of 2`, or a `MORE`/`CARDS NOTE` line), and a supplied `--minutes 0` to be
   honored or refused rather than silently replaced by the default (the card then
   reads `Deadline: finish within 60 minutes.`). `--max -1` also wrote every card
   (`cards=2`), a third silent fallback. As printed, a truncated directory looks
   exactly like a complete one and a wrong `--minutes` reads as if it took.
   Grade: NEXT.

7. `../bin/nova-card template -h` (and `../bin/nova-card version -h`)

   ```
   usage: nova-card template [flags]
   from `nova-card help`:
     nova-card template
   ```

   Expected an exit table the verb can produce. As printed the shared table's
   exit 1 is "a brief is red, named on its LINT DRIFT line, and nothing was
   written", which `template` and `version` — inspections that read nothing and
   write nothing — can never do, so a reader budgeting for the verb's failure
   modes is told about another verb's.
   Grade: NEXT.

Right: from all three sources the planner computed PATHS from the START line and
checked each against the checkout (`paths-at-base` caught a findings row naming a
file that is not there, exit 1, nothing written); `--out` never overwrote a
directory that already held briefs; `--dry-run` wrote nothing (the directory did
not appear), printed the manifest and the `CARDS OK … dry-run=yes` line; `lint`
was green on all three generated shapes and red, one named `LINT DRIFT` line
each, on a missing tier, an unfilled `<...>`, a name outside quoted words and a
dropped id; a missing ledger, a bad `--from`, a missing repo, a non-hex `--sha`,
a bad `--tier`, a non-checkout `--repo-dir` and one good tool beside one missing
tool (`CARDS NOTE skipped …`) were all handled safely; two findings on one file
merged into one card carrying both findings.

READ 7/10: the banner is complete, its own `example:` block runs and the docs
page matches it, but `help <unknown>` returns the banner at exit 0 with no note
and the two inspections quote an exit code they cannot produce, so a cold reader
cannot trust the help's edges.

USE 7/10: every real generation wrote correct cards with computed PATHS and green
lint, refusals were safe and never overwrote, but no verb has `--json`, a missing
input is reported one at a time, and the from-help card leaves its own claimed
checks to its worker.

urgent=1 next=6
