# nova-card dogfood, 2026-10-06 (grok)

Read as a stranger: only `nova-card -h`, `nova-card help`, `nova-card <verb> -h` and
its page under `docs/` (`docs/CLI.md`, the `## nova-card` section), nothing else.
Built from the checkout at `e8f70f600ebf10b3beb60862508ce6019bb6ac7b` (the base tip
`sprint/mechanical-2026-10-02`) and used as
`nova-card v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6`. The pass
was run by the dsh harness (deepseek/deepseek-v4) for Zhi, in the grok slot this
card names. `template`, `version` and `help`; `generate` from all three sources
(the `serial-tests`, `dead-code`, `transcripts`, `generality-fixtures` and
`namedpaths` ledgers, the shipped findings TSV, a tool's help) with `--dry-run`,
`--max`, `--minutes`, `--tier`, `--prefix`, `--name`, `--dropped`, the
`--repo`/`--base`/`--sha` form and `--repo-dir`; `lint` on its own output and on
deliberately red briefs; the refusals too. Commands are quoted from the scratch
directory (the tool `../bin/nova-card`, the checkout `../repo`). No code changed;
a finding is recorded, never fixed here.

## Findings

1. `../bin/nova-card help bogus` (and `../bin/nova-card help help`)

   ```
   nova-card: writes a directory of pre-linted briefs from a ledger, a findings file or a tool's help
   nova-card is pre-alpha: not ready for production use.

   ```

   exit 0. Expected the refusal a misspelled verb already gets —
   `nova-card bogus` prints `nova-card REFUSED: unknown verb "bogus"; one of generate, lint, template, version, help; run: nova-card help`
   at exit 2 — or the banner with a NOTE naming the name it does not have. As
   printed the whole banner goes to stdout at exit 0, so `help bogus`, `help help`
   and a real verb are indistinguishable to a caller, and the one door the tool
   advertises silently answers a name it does not have.
   Grade: URGENT.

2. `../bin/nova-card generate --from findings --file ../repo/cmd/nova-card/testdata/findings.tsv --repo-dir ../repo --out ./cards-min0b --minutes 0`
   and the same call with `--minutes -5`

   ```
   CARDS OK dir=./cards-min0b cards=2 waves=1 tier=pro
   ```
   ```
   CARDS OK dir=./cards-minnegb cards=2 waves=1 tier=pro
   ```

   The `--minutes 0` cards carry `Deadline: finish within 60 minutes.` (the pro
   default, silently substituted); the `--minutes -5` cards carry
   `Deadline: finish within -5 minutes.` — a deadline in the past written into
   every brief. Expected `--minutes` to be validated once, with a non-positive
   value refused (`--minutes 0 wants a positive number`) or given one documented
   meaning, never silently replaced by the default and never emitted verbatim.
   As printed the flag is unvalidated in both directions.
   Grade: URGENT.

3. `../bin/nova-card generate --from help --tool nova-card --bin-dir ../bin --repo-dir ../repo --out ./cards-help-nc`

   ```
   CARDS OK dir=./cards-help-nc cards=1 waves=1 tier=pro
   ```

   The card cut from the help names only `5 line(s) run over 100 characters (help line 16 (229 chars), …)`
   and then tells its worker "every example line runs as printed against the
   built tool (run each; one that does not is corrected or dropped)". The page
   under `docs/` promises `--from help` writes "the lines over 100 characters,
   the undefined terms and the examples that do not run as printed". Expected the
   card to name the failing example and the undefined term, as the page promises
   and as the tool can check for itself; as printed the tool's own promised
   checks are delegated to the worker, so the brief does not carry the defect it
   was cut from.
   Grade: NEXT.

4. `../bin/nova-card generate --from help --tool no-such-tool-xyz --repo-dir ../repo --out ./cards-help-missing2`

   ```
   nova-card generate REFUSED: the source yields no card; nothing to write; run: nova-card help generate
   ```

   exit 2, naming neither the tool nor the reason. The same call with one good
   tool beside it prints `CARDS NOTE skipped no-such-tool-xyz: …/no-such-tool-xyz help printed nothing: fork/exec …: no such file or directory`
   and exits 0; with `--bin-dir ./nope-bin` and a real tool name the reason is
   lost the same way. Expected every requested tool named with why it yielded
   nothing, as the mixed call does. As printed a caller cannot tell a typo'd tool
   name or a bad `--bin-dir` from a genuinely empty source.
   Grade: NEXT.

5. `../bin/nova-card generate`; `../bin/nova-card generate --out ./cards-x`; `../bin/nova-card generate --from bogus --out ./cards-x`; `../bin/nova-card generate --from findings --repo-dir ../repo --out ./cards-nofileflag`

   ```
   nova-card generate REFUSED: wants --out <dir>; run: nova-card help generate
   ```
   ```
   nova-card generate REFUSED: no repository: --repo <owner/name>, or --repo-dir a checkout whose origin names one; run: nova-card help generate
   ```
   ```
   nova-card generate REFUSED: no repository: --repo <owner/name>, or --repo-dir a checkout whose origin names one; run: nova-card help generate
   ```

   and `--from findings wants --file <tsv>` once a repository is named. Expected
   one run to name every independent problem (the refusal rule the sibling tools
   follow): the missing `--out`, the missing repository and the bad `--from`. As
   printed each turn reveals one input, and with no repository the invalid
   `--from "bogus"` — which is named correctly as soon as a repository is given —
   waits behind the repository refusal.
   Grade: NEXT.

6. `../bin/nova-card lint --card ./nope1.md --card ./nope2.md` (and `../bin/nova-card lint --card ./cards-findings-rd/finding-internal-bus-send.md --card ./nope2.md`)

   ```
   nova-card lint REFUSED: cannot read ./nope1.md: open ./nope1.md: no such file or directory; run: nova-card help lint
   ```

   Only `nope1.md` is named; `nope2.md` is never mentioned. The mixed call prints
   `LINT OK file=./cards-findings-rd/finding-internal-bus-send.md` first, then
   the refusal for the missing card, exit 2. Expected all unreadable cards named
   in one run and no `LINT OK` line on a run that ends 2; as printed a caller
   that reads the OK line and then the non-zero exit has two contradictory
   signals and only the first of several problems.
   Grade: NEXT.

7. `../bin/nova-card generate --from findings --file ../repo/cmd/nova-card/testdata/findings.tsv --repo-dir ../repo --out ./cards-max1b --max 1` (the source holds 2) and the same call with `--max -1`

   ```
   CARDS OK dir=./cards-max1b cards=1 waves=1 tier=pro
   ```
   ```
   CARDS OK dir=./cards-maxneg cards=2 waves=1 tier=pro
   ```

   Expected a cut run to keep its total, as the house output rule says (`cards=1 of 2`,
   or a `MORE`/`CARDS NOTE` line), and a negative `--max` refused or documented;
   `--max 0` is documented as all, `--max -1` is not. As printed a truncated
   directory is indistinguishable from a complete one and a negative bound
   silently means every card.
   Grade: NEXT.

8. `../bin/nova-card version --json` (and `../bin/nova-card template --json`)

   ```
   nova-card version REFUSED: takes no flags and no arguments, got 1; run: nova-card version -h
   ```
   ```
   nova-card template REFUSED: takes no flags and no arguments; run: nova-card help template
   ```

   Expected the house shape — one result value rendered as lines or as JSON of
   the same value, with every verb accepting `--json` — and at minimum the
   unknown-flag refusal `generate` and `lint` already give, which names `--json`
   and lists the flags there are. As printed neither verb has `--json` and both
   refuse it without naming the flag, so a machine caller that reads the other
   tools' JSON must parse these lines by hand and cannot tell which flag it got
   wrong.
   Grade: NEXT.

9. `../bin/nova-card generate --from ledger --ledger serial-tests --file ../repo/cmd/nova-card/testdata/findings.tsv --repo-dir ../repo --out ./cards-mixed`

   ```
   CARDS OK dir=./cards-mixed cards=14 waves=2 tier=flash shared-paths=yes (add with --allow-shared-paths)
   ```

   Expected a refusal naming that `--file` belongs to `--from findings` alone —
   the tool already refuses a missing `--file` for findings and a bad `--tier` —
   as the flag's own help says "with --from findings". As printed a flag the
   source cannot use is silently ignored, so a caller can believe a TSV was read
   that was not.
   Grade: NEXT.

10. `../bin/nova-card generate --from ledger --ledger namedpaths --repo-dir ../repo --out ./cards-ledger-named`

    ``` LINT DRIFT card=namedpaths-internal-pulse check=paths-at-base line=6: PATHS entry internal/pulse/*.go names nothing in ../repo ```

    12 such `paths-at-base` drifts, then `nova-card generate FAILED: 12 red line(s) above; nothing written to ./cards-ledger-named`,
    exit 1. `--ledger`'s help lists `namedpaths` among the ledgers the build
    knows, and `--repo-dir` checks every PATHS entry against the checkout; this
    ledger's own rows say the files were deleted (2026-09-25, #3969) or removed
    (2026-10-04), so the advertised source can never pass at the base it ships
    in. Expected either the ledger not offered as a card source or its page to
    say it is a record, not a source. The refusal itself is safe and names each
    path.
    Grade: NEXT.

11. `../bin/nova-card template -h` (and `../bin/nova-card version -h`)

    ``` usage: nova-card template [flags] from `nova-card help`: nova-card template ```

    and then `exit codes: 0 done; 1 a brief is red, named on its LINT DRIFT line, and nothing was written` —
    the shared table's exit 1, which `template` and `version` are inspections
    that read nothing and write nothing and can never produce. Expected the
    verb's own table or the unreachable row left out; as printed a reader
    budgeting for the verb's failure modes is told about another verb's.
    Grade: NEXT.

Right: from all three sources the planner computed PATHS and, with `--repo-dir`,
checked every entry against the checkout (the `namedpaths` run above caught
twelve, exit 1, nothing written); the `--repo/--base/--sha` form needs no
checkout and wrote the same two cards; `--dry-run` wrote nothing (the directory
never appeared) and printed the manifest and `CARDS OK … dry-run=yes (nothing written)`;
`--out` refused a directory that already held briefs; `--tier flash`, `--prefix`,
`--name` and `--dropped` were accepted and the tier was carried into the card;
`lint` was green on every generated shape and red, one `LINT DRIFT` line per
check, on a brief missing its rules, carrying an unfilled `<...>` or naming a
test outside its PATHS; a missing ledger, a leading-dash-free bad ledger name, a
bad `--from`, a missing repo, a non-hex `--sha`, a missing checkout, a bad
`--tier`, a non-integer `--minutes`/`--max`, a directory passed to `--card` and
a missing `--out` were all refused safely; two findings on one file merged into
one card carrying both.

READ 7/10: the banner answers what, how and how-to in its first lines, its
`example:` block runs as printed, every verb's `-h` exits 0 and names its effect
class, and the refusals are one line with a remedy; the score is held down by
`help <unknown>` answering with the banner at exit 0, `version` and `template`
quoting an exit code they cannot produce, no verb accepting `--json`, and the
`docs/` page promising from-help checks the tool does not make.

USE 7/10: every real generation wrote correct cards with computed and checked
PATHS, `--dry-run` wrote nothing, refusals never overwrote a directory, and the
shared-paths line names the add flag a ledger needs; but inputs are reported one
at a time, the from-help card leaves its own promised checks to its worker,
`--minutes` and `--max` are unvalidated at their edges, and no verb has `--json`.

urgent=2 next=9
