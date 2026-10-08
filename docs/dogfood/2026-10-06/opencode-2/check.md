# nova-check — a cold dogfood pass (opencode-2)

Read as a stranger would: only `nova-check -h`, `nova-check help`, `nova-check <verb> -h`, `docs/CLI.md` §nova-check and `docs/SPEC.md` §nova-check (with
`docs/SPEC-CHECK.md` for convergence). Nothing else was read before the runs
below. The binary was built at `abb9bfecc729` (`nova-check v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`) and every verb was run for real against scratch trees, a
scratch git repository, fixture ledgers, a fake `gh` and a fake `git`, the
refusals included. No code was changed.

## Findings

1. **`dogfood gate` drops the `-` of a bare invocation; the spec and its own ledger keep it.** URGENT.

   Command:
   ```
   nova-check dogfood gate --cli docs/CLI.md --receipts ./receipts --require-all
   ```
   Printed (first 3 lines):
   ```
   DOGFOOD GATE FAIL tool=nova-self-talk verb=: not dogfooded by a non-author; a tool is done when somebody who did not write it has run it on real work
   DOGFOOD GATE FAIL tool=nova-self-talk verb=scan: not dogfooded by a non-author; a tool is done when somebody who did not write it has run it on real work
   DOGFOOD GATE FAIL tool=nova-self-talk verb=shapes: not dogfooded by a non-author; a tool is done when somebody who did not write it has run it on real work
   ```
   Expected: the first line reads `verb=-`. `docs/SPEC.md`'s reference rules say a
   synopsis with no verb declares the bare invocation and it "prints as `verb=-`",
   and `nova-check dogfood ledger` against the same reference prints
   `DOGFOOD tool=nova-self-talk verb=- by=nobody at=- ok=- issue=- open=0`. The
   gate emits an empty value, `verb=:`, so the field parses as nothing.

   Grade: **URGENT** — the tool's own page states the rendering and the sibling
   verb gets it right; a release gate line for a bare-invocation tool ships
   malformed.

2. **`spelling` prints the run's facts line last, and its findings share the verdict's status word.** NEXT.

   Command:
   ```
   nova-check spelling --file sp/doc.md
   ```
   Printed (first 3 of 5 lines):
   ```
   SPELLING FAILED sp/doc.md:3:2: recieve -> receive
   SPELLING FAILED sp/doc.md:3:10: teh -> the
   SPELLING FAILED sp/doc.md:3:26: seperate -> separate
   ```
   the fifth and last line is the facts line,
   `SPELLING FAILED files=1 misspellings=4 shown=4`.

   Expected: `docs/CLI.md`'s First run says "The first line of a check is
   `<CHECK> OK`, `<CHECK> FAILED` or `<CHECK> REFUSED` and the run's facts as
   `key=value`; the findings of a failing run are typed lines under it". `links`,
   `kernel`, `nocode`, `attest`, `corpus` and `hygiene` all do this. `spelling`
   is the one check whose facts line is last, and its finding lines carry the
   same `SPELLING FAILED` prefix as the summary, so the verdict line cannot be
   told from a finding by its first word.

   Grade: **NEXT** — `docs/SPEC.md`'s spelling section documents FIX-then-verdict
   for write mode, so the tool's two pages disagree; the page a stranger reads
   first is the one that is wrong.

3. **`spelling` scans indented code blocks as prose.** NEXT.

   Command:
   ```
   nova-check spelling --file sp3.md
   ```
   `sp3.md` holds one prose misspelling, an indented code block (four leading
   columns) and a tab-indented one, a fenced block and an inline span, each with
   `recieve`/`seperate`. Printed:
   ```
   SPELLING FAILED sp3.md:5:4: recieve -> receive
   SPELLING FAILED sp3.md:6:1: seperate -> separate
   SPELLING FAILED sp3.md:14:12: recieve -> receive
   ```
   Expected: only line 14 (the prose). The fenced block and inline span are
   correctly blanked, and `docs/SPEC.md`'s "Deliberately does not check" says
   "code blocks or identifiers … are skipped" and "code is not prose". A
   block indented by four columns or a tab is CommonMark's other code block and
   is scanned; the help's narrower wording ("fenced code blocks and inline code
   spans are blanked") is the accurate one.

   Grade: **NEXT** — false positives on ordinary wrapped examples, with
   `--ignore` as the only remedy.

4. **`convergence --retired` reads only four-column table rows; a dated bullet list reads as zero rows silently.** NEXT.

   Command:
   ```
   nova-check convergence --repo example/project --ledger ./ledger.md --receipts ./receipts --retired ./retired/t3.md --since 2026-09-01T00:00:00Z --bin ./bin --gh ./gh --git ./git --now 2026-09-20T00:00:00Z
   ```
   Printed (first 3 lines):
   ```
   CONVERGENCE LANDING now=3 before=6 ratio=0.50 trend=contracting measure=rounds-per-batch batches=2 per-hour=0.00 prev-batches=1 prev-per-hour=0.00 rounds-read=2
   CONVERGENCE CLASSES now=- before=- ratio=- trend=absent measure=class-test-index-entries source=--repo-dir
   CONVERGENCE SCRIPTS now=3 before=3 ratio=1 trend=flat measure=scripts-left-in-bin retired-in-window=0 retired-rows=0 undated=0
   ```
   Expected: `retired/t3.md` is `## 2026-09-01` followed by `- old-one.sh` and
   `- old-two.py`; those dated rows should feed `retired-in-window=`. The same
   two scripts written as `|`-table rows give
   `retired-in-window=2 retired-rows=2 undated=0`. Instead both `retired-rows=` and
   `undated=` are 0, so SCRIPTS reports `flat` on no evidence and nothing says the
   file was not parsed.

   Grade: **NEXT** — the help says only "whose dated rows", never that a row must
   be a markdown table row; a natural retired-scripts README is invisible.

5. **`convergence --by` silently treats a comma list as one name and reads an empty EDGES.** NEXT.

   Command:
   ```
   nova-check convergence --repo example/project --ledger ./ledger.md --receipts ./receipts2 --retired ./retired/t3.md --since 2026-09-01T00:00:00Z --gh ./gh --git ./git --now 2026-09-20T00:00:00Z --by Ada,Bob
   ```
   Printed:
   ```
   CONVERGENCE EDGES now=0 before=0 ratio=1 trend=flat measure=open-edges receipts=0 rounds=0 not-ok=0 not-ok-per-round=- unreadable=0
   CONVERGENCE OK streams=4 contracting=2 widening=- absent=CLASSES,SCRIPTS,FLEET
   ```
   Expected: `--by Ada --by Bob` over the same two edge receipts prints
   `CONVERGENCE EDGES now=2 before=0 ratio=- trend=widening measure=open-edges receipts=2 rounds=2 not-ok=2 not-ok-per-round=1 unreadable=0`. The comma
   spelling is accepted as one unmatchable name and reads as a clean, empty
   stream instead of a refusal. The help says "repeatable", but a comma list is
   the shape `hygiene --identity` and `spelling --ignore` use, so the empty
   reading is easy to take as real.

   Grade: **NEXT**.

6. **An unreadable `--certs` header is silently ignored when `--versions` is absent.** NEXT.

   Command (no `--versions`; `certsbad.tsv` has the header `wrong<TAB>cols`):
   ```
   nova-check convergence --repo example/project --ledger ./ledger.md --receipts ./receipts --retired ./retired/t1.md --since 2026-09-01T00:00:00Z --certs ./certsbad.tsv --gh ./gh --git ./git --now 2026-09-20T00:00:00Z
   ```
   Printed:
   ```
   CONVERGENCE FLEET now=- before=- ratio=- trend=absent measure=units-off-the-one-build source=--versions
   CONVERGENCE OK streams=4 contracting=2 widening=- absent=CLASSES,SCRIPTS,FLEET
   ```
   exit 0. Expected: exit 2 naming `./certsbad.tsv` and `name<TAB>status`.
   `docs/SPEC-CHECK.md` §14 says "a `--versions` or `--certs` with a header this
   spec does not list names the file and the header it wanted". The same file IS
   refused when `--versions` is also given:
   `CONVERGENCE REFUSED: ./certsbad.tsv has a header this verb does not read (wrong<TAB>cols); it reads name<TAB>status; run: nova-check help`.

   Grade: **NEXT** — a malformed named input fails silently.

7. **`kernel` and `attest` print a wrong file mode in "not a regular file" findings.** NEXT.

   Commands:
   ```
   nova-check kernel --file ./self/docs --max-bytes 100
   ```
   Printed:
   ```
   KERNEL FAILED file=./self/docs bytes=0 budget=100 findings=1
   KERNEL FINDING subject=./self/docs reason="not a regular file (d---------); symlinks are never followed"
   ```
   `stat -c %A self/docs` prints `drwxrwxr-x` for that same directory. A symlink
   (`lrwxrwxrwx`) gives `not a regular file (L---------)` — the type is
   uppercased and every permission bit is a dash. `attest` repeats it for a
   directory named in a manifest:
   ```
   ATTEST FAILED home=./home manifest=./manifest_dir.txt files=0 bytes=0 sha256=- findings=1
   ATTEST FINDING subject=docs reason="not a regular file (d---------)"
   ```
   Expected: the real mode, or the type alone; `d---------`/`L---------` reads as
   a zero-permission file and is wrong.

   Grade: **NEXT**.

8. **`dogfood`'s own lines drift from the shape its page states.** NEXT.

   Command:
   ```
   nova-check dogfood gate --cli docs/CLI.md --receipts ./emptyrec
   ```
   Printed (all of it):
   ```
   DOGFOOD-GATE FAILED: no receipts were read from ./emptyrec, so the gate has nothing to pass on; add receipts, or pass --allow-empty to say that is deliberate
   ```
   exit 1. The unreadable-`--cli` case prints `DOGFOOD-GATE REFUSED: cli: open …`. Expected: the family's `DOGFOOD GATE …` spelling that `docs/SPEC.md`'s
   dogfood section uses (`DOGFOOD GATE FAIL …`, `DOGFOOD GATE OK …`,
   `DOGFOOD NOTE …`); the hyphenated `DOGFOOD-GATE` appears on these two lines
   and nowhere else. The same drift: a receipt that cannot be parsed prints
   `DOGFOOD FAILED <file>:1: not a receipt: …`, where the spec states
   `DOGFOOD FAIL <file>:<line>: <reason>`.

   Grade: **NEXT** — a parser keyed on the verb's spelling sees two verbs for
   one.

READ 8/10 — the banner answers what/how/use, every `-h` names its flags, effect and exit codes,
and a missing flag is one line naming every absent flag with what each one wants, which is the
best refusal grammar read in the tree; the deductions are places where the tool's own pages and
the tool disagree (spelling's verdict line, the gate's bare `verb=`) and the retired-file row
shape is left to inference.

USE 7/10 — every verb did its job on a scratch tree: `links` caught a broken target, `kernel`
measured and refused both-budget calls, `nocode` classified the index, `corpus` caught a lost
fragment and a ledger below its floor, `hygiene` caught identity, out-of-path, stray-file and
five secret shapes, `convergence` read seven streams through fakes and went red on the second
widening tick, and every `--dry-run` wrote nothing; the deductions are the silent zero on a
bulleted retired README, the `--by` comma trap that reads as an empty green EDGES, and a bad
`--certs` header vanishing.

urgent=1 next=7
