# SPEC-CARD — the one card contract (v2)

**Status: amended text for independent review. Not pinned. Not implemented.** This is the one
text Stella asked for on 2026-09-22 at 17:33Z (stella-7f0da424bf91): Stella's nine-clause
proposal for nova-tools #2608 at `stella-tools@2fb4393`, with the amendments Rowan made clause by
clause (rowan-31f0dd184c6f) and Emma accepted as the cutter's owner (emma-f4e876aad36f), written
out as one document. The 59-card sprint-stage HOLD stands until Stella and Emma have each read
this exact revision and the gate in clause 9 is met. Nothing here authorises a recut, a staging
run or a measurement campaign.

**Roles.** Stella: contract review. Emma: cutter implementation coordination. Rowan: adoption.

**What this governs.** A card is the whole of what a worker is handed. For a card that declares
`SCHEMA: v2`, this document is the contract; where `docs/WORKER-CARDS.md`,
`docs/SPEC-TOOLWORK.md` §5 or `docs/spec-pulse/10-the-card-as-cut-writes-it.md` say otherwise
about a v2 card, this document governs. A card that declares no version is a legacy card and
keeps the WORKER-CARDS rules through its explicit adapter (clause 1).

**How to read it.** Each clause gives the amended normative text, then a line saying exactly what
changed from `2fb4393`, then the measured effect on the 59 cut cards
(`~/rowan-working/tmp/session-0919b/sprint-tools/cards/`, measured by Rowan on 2026-09-22 against
`dev` at `af6a9fccf331`). Code locations are on `dev` at `1e1e5fb9` unless a PR is named.
Sentences from `2fb4393` are kept word for word where no amendment touched them.

---

## 1. Version

The coordinator-owned card declares `SCHEMA: v2`. One parser/validator dispatches on the declared
version; unknown versions refuse. Legacy cards retain their explicit adapter and existing STEP
rules. Worker output cannot choose a weaker adapter. Missing version on a newly produced v2 card
is a producer error, not a legacy fallback.

The version is declared at a structural metadata position — inside the card's typed header block
(clause 2) — and a `SCHEMA:` line anywhere else in the card is quoted evidence, retained verbatim,
and declares nothing. A validator that finds the version by scanning the whole card does not
satisfy this clause.

*Changed from 2fb4393:* the second paragraph is added (Rowan, amendment 1).
*Why:* `ValidateCardV2` (#2522 at `9ee8155`, `internal/pulse/cut_template.go:428-448`) sets the
version from any line whose trimmed form is `SCHEMA: v2`, fenced or not. The 59 carry that line
117 times: in the header on all 59 and inside the fenced RESULT template on 58.
*Effect on the 59:* 0 change. The amendment is what keeps clause 2's duplicate rule from refusing
the 58.

## 2. Structure

A v2 card's metadata is the **contiguous run of `KEY: value` lines beginning at line 2**, ending
at the first non-empty line that is not one, blank lines skipped, unknown keys read past. Section
headings, prose and fenced blocks come *after* the block and never inside it. Keys are canonical
uppercase with two permanent exceptions, `base-repo:` and `base-sha:`, which are lowercase because
70 launcher scripts read them case-sensitively from the card's first 40 lines; a card is refused
if it carries one without the other. Duplicate or conflicting keys *inside the block* are refused;
an identically spelled line outside it is quoted evidence and declares nothing. A v2 card does not
inherit the WORKER-CARDS prose rules `clone-step`, `steps-numbered`, `result-last` or
`scratch-absolute`; the list is exhaustive and any rule not on it applies.

The body carries the single `## Run` / `RUN:` fenced region approved at #2522@9ee81556 (clause 6).
Parse only structural metadata positions, not quoted evidence.

*Changed from 2fb4393:* replaced "v2 has named metadata sections … It is not a contiguous legacy
line-2 header and does not inherit numbered-STEP, clone-step or result-last prose rules", "Emit
canonical uppercase keys; reject duplicate/conflicting keys" and "Existing lowercase provenance
fields need a documented migration, not silent truncation of later fields" with Rowan's wording
(the first paragraph above, verbatim). Kept: the `## Run` region and "parse only structural
metadata positions".
*Why:* three live readers take the metadata as a contiguous run from line 2 —
`swarm.cardHeaderBlock` (`internal/swarm/lintheader.go:122`), the launchers
(`rowan-tools/bin/launchers/*-native-{darwin,bench}.sh:30-31`, `sed -n '1,40p'` then a
case-sensitive `base-repo:`/`base-sha:` match), and `bin/sprint-stage:24,29` (`^BASE: `,
`^base-sha: `, and its insertion anchor `^RUN: ` → `^TEST: ` → `^KIND: `). Metadata under a
heading strands every typed key and leaves the stage step's anchor unmatched. `scratch-absolute`
fires on the COMMIT RULE sentence every card carries on purpose: with only three rules exempted,
0/59 pass; with four, 57/59.
*Effect on the 59:* 0 change under this wording; all 59 break under the `2fb4393` wording.

## 3. Kinds

Emit `KIND` explicitly. The v2 set is the existing cutter set: `read`, `fix`, `replay`, `spec`,
`rebase`, `guard`, `recut`, `port`, `docs-guard`, `report`. Use explicit per-kind requirements;
do not equate `fix` with `fix-red` by a hidden alias. `MODE: script` describes execution mode,
not another KIND. Unlisted cell/rule/spec2 producers need an explicit adapter/decision before
recutting.

This set **replaces** `internal/hygiene/kinds.txt`; it does not extend it. The names the current
file holds and this set does not — `fix-red`, `transcript-test`, `sweep`, `mutation-kill`,
`probe`, `text`, `tone` — are kept in the file as an explicit, dated deprecation block, and a card
or branch carrying one is refused with `kind=<old> is retired; use <new>`, not with the generic
"not a kind this toolchain declares". A card whose kind was `script` is `KIND: report` with
`MODE: script` retained: its product is a receipt and it changes no file in any repository.

*Changed from 2fb4393:* the second paragraph is added (Rowan, amendments 3a and 3b).
*Why:* `kinds.txt` on `dev` holds a different ten; three names are in common, and
`cmd/nova-check/hygiene.go` (`--kind`) refuses by that file. One of the 59 (`card-tools-01c`)
carries `KIND: script`.
*Effect on the 59:* `fix` 54 and `report` 4 unchanged; `script` 1 → `report`; `kind-declared`
59 → 0.

## 4. PATHS

Emit comma-separated repository-relative entries or the explicit `none` value. Parse once into a
list and pass the same list to validation, staging checks and harvest. Reuse hygiene
validation/matching. Emit `directory/**` canonically; agree any bare-directory migration
explicitly. Never accept a whole multi-path string as one glob. Do not guess how to split a
filename containing spaces.

An entry containing an unquoted space is refused by name, with the line and a remedy naming the
comma form. It is never split on whitespace, never accepted as one glob, and never guessed at.

Harvest's reader is bound by this clause: `pulse.parsePATHS` (`internal/pulse/harveststale.go:207`)
reads the list the shared parser produces (clause 8), not its own scan of the card text.

*Changed from 2fb4393:* "Space-separated old values need an explicit legacy/migration path"
is replaced by the refusal paragraph (Rowan, amendment 4a); the harvest paragraph is added
(Rowan, amendment 4b).
*Why:* `hygiene.ValidatePaths` (`internal/hygiene/glob.go:39`) has no space rule, so
`a.go b.go` validates as one glob that can never match — silently clean at the lint, refused at
harvest (#2600; the 14 stranded DONE cards). `card-tools-01c`'s PATHS line is an English sentence
and validates today.
*State of harvest since the review was measured:* #2598 (merged 16:24Z, after the review read
`af6a9fcc`) makes `parsePATHS` split on commas **and whitespace** (`splitDeclared`,
`harveststale.go:250`) so the cards as written today harvest. Under this clause that split is the
interim reading of cards cut before the contract, not the permanent parser (Johnny,
johnny-09ab72b5b800): a v2 card is refused at the lint before harvest could split it, and the
split is retired when harvest calls the shared parser.
*Effect on the 59:* all 59 change — 58 space → `a, b[, c…]`, 1 (`01c`) → `PATHS: none`. The
widest card after migration declares 5 entries, so `maxPaths = 8` (`glob.go:22`) binds nothing.

## 5. TEST

For a Go test anchor, use one package and one test name (`TEST: ./internal/pulse TestExample`);
`none` is explicit when no single Go anchor applies. Multiple packages, non-Go checks and flags
belong in the exact executable command, not a pseudo two-field TEST selector. Kind-specific rules
decide when a test anchor/control is required; read/report cards must not acquire an obligation to
execute code merely from shared formatting.

A card declaring `TEST: none` still carries a `DONE-WHEN:` sentence naming the exact command or
artifact that decides it. `none` declares that there is no Go anchor, never that there is no
control.

*Changed from 2fb4393:* the second paragraph is added (Rowan, amendment 5; Johnny's ruling
the-control-is-the-sentence, 2026-09-21).
*Effect on the 59:* 14 change — 7 multi-package Go (`tools-02, 08, 17, 19, 24, 49, 50`) → one
anchor with both packages in the Run command; 7 non-Go (`tools-38`…`44`) → `TEST: none` with the
check in the Run command and the control in DONE-WHEN. `test-named` 14 → 0.

## 6. Commands

A v2 card's one operative authority is the fenced block of its single `## Run` section, opened by
a bare `RUN:` marker line. The typed header MAY carry a `RUN:` key whose value is derived from,
and must equal, the region's command line; it is an echo for the staging scripts and is refused on
disagreement, exactly as `COMMAND:` is. A card carrying a `RUN:` header key and no region, or a
region whose command differs from the key, is refused at both entry points.

If `COMMAND` metadata is retained, derive it from the same structured input and reject
disagreement. Do not create a second independently editable command authority. `MODE: script`
retains the separately reviewed JSON-argv contract; do not reinterpret a model card's
prose/markdown as argv or add an implicit shell.

*Reading note (clauses 2 and 6 together):* the bare `RUN:` marker line inside `## Run` is the
region's marker. It is not a header key, not a duplicate of the header's `RUN:` echo, and not a
stranded key.
*Changed from 2fb4393:* "The v2 operative Run region is the execution declaration" is replaced by
the first paragraph (Rowan's wording, verbatim), which says which spelling exists and makes the
header key a checked echo. The rest is kept.
*Why:* 59/59 carry `RUN: <command>` as a header key; 0/59 carry a `## Run` heading; `OperativeRegion`
(#2522, `cut_template.go:506-517`) requires the heading and a bare `RUN:` line, so the producer
refuses all 59 today. `bin/sprint-stage:29` anchors its DONE-WHEN/NO-SUBAGENTS insertion on
`^RUN: ` (with the space), which the echo matches and the bare marker does not; without an echo it
falls back to `^TEST: ` and `^KIND: `, which a v2 card carries (clauses 3, 5).
*Effect on the 59:* all 59 gain a `## Run` section (mechanical: the `RUN:` value, one line);
`card-tools-01c`'s RUN is prose ("bash, gh (read-only), and the nova-decide first-pass checker…")
and is rewritten by hand.

## 7. Existing invariants

Preserve immutable input identity, expected attempt binding, owned RESULT schema, DONE versus check
outcome, Returned/Verified/Landed distinction, retained evidence, STOP/HOLD/UNKNOWN, and contextual
read scope. SYMBOL/RED-WHEN are declarations, not proof of runtime coverage. A4 text lint is not
actual PATHS enforcement.

A rule that reads prose reads only the card's own instructions to the worker, never a fenced block,
a quoted prior card, an inlined diff or other retained evidence.

*Changed from 2fb4393:* the second paragraph is added (Rowan, addition 7).
*Why:* `card-tools-20` is refused today by `no-sandbox` (`cmd/nova-swarm/lint.go:274-277`, a
bare `strings.Contains(l, "nova-sandbox")` over every line) for naming `.nova-sandbox-tmp/` in
its own task description. The measured case for "A4 text lint is not actual PATHS enforcement":
`matchDeclared` (`internal/pulse/harveststale.go:292-305`) admits every file under a bare
directory entry (#2547; after #2598 that is the intended reading of a bare directory, see Open 2).
*Effect on the 59:* 1 (`tools-20`).

## 8. One implementation

Every reader of a card's structure calls the same versioned parser: the cutter, `nova-swarm lint`,
harvest's PATHS enforcement, batch admission's card-shape check, and the launcher scripts'
base-pin read. Where a reader cannot call the parser (a shell script), the contract states the
exact literal shape it depends on and a class test asserts the parser emits it. A reader not on
this list is a defect in the list, not an exemption. Every reader returns the same structured
diagnostic (rule, field/line, remedy). METHODS/help/spec output derives from its rule definitions
where appropriate. No separate regex repair in each producer/consumer.

| # | reader | where (dev `1e1e5fb9` unless named) | what it reads today |
|---|---|---|---|
| 1 | cutter: `pulse.ValidateCardV2` + `OperativeRegion` | #2522 at `9ee8155`, `internal/pulse/cut_template.go:428` | SCHEMA/SYMBOL/RED-WHEN on any line; the `## Run` region |
| 2 | lint: `swarm.LintCardHeader` + `lintCard` | `internal/swarm/lintheader.go:203`, `cmd/nova-swarm/lint.go` | the header block (`headerKeyRE`, `lintheader.go:95`; #2607 widens it) and 12 prose rules |
| 3 | harvest: `pulse.parsePATHS` | `internal/pulse/harveststale.go:207-230` | the first `PATHS:` **or `PATHS `** line anywhere in the text, at any indentation |
| 4 | admission: `swarm.cardShapeFailure` | `internal/swarm/batch.go:2116` | for `opencode/` and `deepseek/` models: a line beginning `STEP 1` in the **first 15 lines** (`hasStep1`, `:2173`), no all-capitals first line, no "launcher" in lines 1-3 |
| 5 | launchers | `rowan-tools/bin/launchers/*-native-{darwin,bench}.sh:30-31` (70 scripts carry the read at `ff32176`) | lowercase `base-repo:` / `base-sha:` in the **first 40 lines** |

The literal shape reader 5 depends on: `base-repo: <url>` and `base-sha: <40 hex>`, lowercase, in
the first 40 lines, both or neither (clause 2 puts them in the block, which ends well before
line 40 on every card).

*Changed from 2fb4393:* "cutter and swarm lint call the same versioned parser/validator" is
widened to all five readers, in Rowan's wording (the first three sentences above, verbatim); the
diagnostic, METHODS and no-separate-regex sentences are kept.
*Why reader 4 matters most:* it is in no earlier draft of #2608. The 59 write steps as
`## STEP 1.` around line 30, so admission refuses every one of them on every opencode/deepseek
route (the dogfood saw `ADMIT REFUSED … card-shape`), and a v2 card with no numbered STEPs, which
clause 2 permits, is refused there with both linted entry points green.
*A sixth reader, by this clause's own last sentence:* `bin/sprint-stage` (lines 24, 29, 37)
reads `^BASE: `, `^base-sha: ` and the `^RUN: ` → `^TEST: ` → `^KIND: ` anchor. Clause 2's
evidence names it; the accepted list of five does not. It is listed here so reviewers can confirm
or strike it (Open 4).

## 9. Acceptance

Invalid cards for every agreed rule, a legacy compatibility set, duplicate/version/quoted-evidence
cases, multiple-path and multi-package cases, and script/model separation. The same bad card must
fail for the same reason at both entrypoints. Zero differences and 59 green alone are insufficient
if semantic requirements were removed.

The contract is accepted when, in one run at the pinned head:

1. **The migrated 59 exit 0 at both entry points**, and the migration is mechanical and
   reviewable: PATHS commas (59), TEST (14), KIND (1), a `## Run` section (59), and one
   hand-rewritten card (`01c`). Identity and evidence are untouched: line 1, `base-sha`, SYMBOL,
   RED-WHEN, DONE-WHEN and the inlined findings. Measured today under the contract simulated:
   **57/59**; the two that do not pass are `01c` (clause 6: RUN is prose) and `tools-20`
   (clause 7: refused for a string it quotes), named fixes, not migration failures.
2. **The cutter re-emits one of the 59 byte-for-byte from structured input** — one card, chosen
   by Stella.
3. **The two fixtures below pass as a class test in CI**, not only on a bench.

*Changed from 2fb4393:* "replay the exact 59 owned card inputs through cutter and consumer,
preserving their identity/evidence" is replaced by items 1-3 (Rowan's replacement); the rest is
kept.
*Why:* the replay is not runnable. The 59 carry 24 distinct header keys; `RenderCardV2` at
`9ee8155` can emit 8 (`SCHEMA`, `ATTEMPT`, `REPO`, `BASE`, `PATHS`, `TEST`, `SYMBOL`,
`RED-WHEN`), writes `BASE_SHA:` where the launchers read `base-sha:`, and has no input field for
`KIND`, `DEADLINE`, `LEG`, `base-repo`, `base-sha`, `FILES`, `RUN`, `DONE-WHEN`, `NO-SUBAGENTS`,
`UNATTENDED`, `MODE`, `TURNS`, `SOURCE`, `ROUTE`, `PREFLIGHT` or `COMMIT RULE`.

### The fixtures

Both live at `cmd/nova-pulse/testdata/card-contract-2608/` and are called at both entry points:
the producer's validator (`pulse.ValidateCardV2` + `OperativeRegion`) and the consumer's lint
(`swarm.LintCardHeader` + `lintCard`, on the command line `nova-swarm lint --card <file> --typed`).
The class test calls the functions; a file-taking producer command (`nova-pulse cut --validate
<file>` in the review) exists neither on `dev` nor on #2522, which has `--validate-contract` over
its own output, so the CLI spelling for the producer is Emma's to name.

**P — `accept.md`.** `card-tools-03-stale-base-false-positive.md` from the 59, migrated and
otherwise byte-for-byte: PATHS with a comma (line 11, and the RESULT template's `PATHS` line), and
a `## Run` section whose command equals the `RUN:` header key. Both entry points **exit 0 and
print no diagnostic**. One property per line:

- the header block is contiguous from line 2 and nothing is stranded (reader 2);
- `base-repo:`/`base-sha:` are lowercase at column 0 on lines 9-10, inside the first 40 lines
  (reader 5);
- `^BASE: ` and `^RUN: ` exist, so sprint-stage's re-pin and insertion anchor both hit;
- PATHS is comma-separated and both entries validate (clause 4);
- TEST is one package and one Go test name (clause 5);
- exactly one `## Run` region, whose command equals the `RUN:` header key (clause 6);
- `SCHEMA: v2` is read from line 3 and the RESULT template's copy is not a duplicate (clauses 1, 2);
- it is the positive control for N: the same card, one character different.

**N — `refuse-paths-space.md`.** P byte-for-byte with one edit: the comma on line 11 becomes a
space. Both entry points **exit 2** and print the same line, differing only in the `card=`/file
prefix:

```
paths-declared: 11: PATHS: entry "internal/pulse/harveststale.go internal/pulse/harveststale_test.go" contains an unquoted space: entries are comma-separated and a value is never split on whitespace nor accepted as one glob remedy=PATHS: internal/pulse/harveststale.go, internal/pulse/harveststale_test.go
```

The assertion is on the rule token, the line number, the quoted entry and the remedy — clause 8's
fields — compared as one string after the prefix is stripped. Not "both refused": both refused for
the same reason, in the same words, at the same line. This rule is chosen because both sides are
silently clean on it today.

*Baseline, measured on `dev` at `1e1e5fb9` (2026-09-22):* `nova-swarm lint --card <f> --typed`
exits 2 on **both** fixtures with the same eight drifts (`clone-step`, `steps-numbered`,
`scratch-absolute`, `result-last`, PATHS/TEST/SOURCE stranded below a block that ends at the
lowercase `base-repo:` on line 9, `kind-declared` on `fix`). Nothing in the output tells P from N.

---

## Effect on the 59, summarised

| change | cards | what changes |
|---|---|---|
| clause 2 | 0 | the 59 already have the contiguous line-2 block |
| clause 3 | 1 | `KIND: script` → `KIND: report`, `MODE: script` kept |
| clause 4 | 59 | 58 space → comma; `01c` → `PATHS: none` |
| clause 5 | 14 | 7 multi-package Go → one anchor; 7 non-Go → `TEST: none` + DONE-WHEN control |
| clause 6 | 59 | a `## Run` section each; `01c` rewritten by hand |
| clause 7 | 1 | `tools-20` stops being refused for text it quotes |
| **net, measured (simulated)** | **57/59** | `01c` and `tools-20` are the two named fixes |

## Open — not decided by this text

Each is a question the sources leave open or a place they conflict; none is settled by silence.

1. **Retired kind → replacement table.** Clause 3 requires `kind=<old> is retired; use <new>` but
   no source names `<new>` for `transcript-test`, `sweep`, `mutation-kill`, `probe`, `text`, `tone`.
   The table is written in the deprecation block and reviewed with it.
2. **Bare-directory PATHS entries.** `2fb4393` says "agree any bare-directory migration
   explicitly". #2598 (merged) reads a bare directory at harvest as everything under it; the
   review cited that same behaviour as the weakness in #2547. Canonical emission is `directory/**`;
   whether the lint refuses a bare directory on a v2 card is not yet ruled.
3. **Lowercase keys other than the two.** #2607 (open) lets any one-word key in any case continue
   the block; clause 2 makes keys canonical uppercase with two exceptions. Whether another
   lowercase key is refused or read past as unknown is not ruled. Under #2607's grammar
   `COMMIT RULE:` (two words) is not a key and ends the block; on the 59 it is the last such line,
   so nothing is stranded.
4. **sprint-stage as a sixth reader** (clause 8). It also carries a defect clause 2 will surface:
   it tests `^NO SUBAGENTS:` (space) but inserts `NO-SUBAGENTS:` (hyphen), so every re-pin of a
   card that already carries `NO-SUBAGENTS:` adds a second one inside the block, which clause 2
   refuses as a duplicate.
5. **Which card the cutter round-trips** (clause 9 item 2): Stella's choice.

## Lineage

- `mas-bandwidth/stella-tools@2fb4393`, `docs/coordination/card-contract-2608-proposal.md` —
  Stella's nine-clause proposal (bus stella-6212385f64d9, 15:37Z).
- emma-1f618e0c56b9 (16:35Z) — Emma agreed the nine clauses at `2fb4393` verbatim; superseded by
  emma-f4e876aad36f.
- rowan-31f0dd184c6f (16:36Z) — Rowan's clause-by-clause and amendments; full review with
  citations at `rowan-new/reports/card-contract-2608-review-rowan-2026-09-22.md`.
- johnny-09ab72b5b800 (16:36Z) — clause 4 versus #2598's whitespace split.
- emma-f4e876aad36f (17:16Z) — Emma, as the cutter's owner, accepts Rowan's amendments across all
  nine clauses.
- stella-7f0da424bf91 (17:33Z) — Stella asks for one exact amended shared artifact before
  implementation adoption or the 59-card recut; her disposition is pending.
- This PR — the amended text, and the two fixtures of clause 9. No code.
