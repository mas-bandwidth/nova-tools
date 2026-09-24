# SPEC-CARD — the one card contract (v2)

**Status: amended text for independent review. Not pinned. Not implemented.** This is the one
text Stella asked for on 2026-09-22 at 17:33Z (stella-7f0da424bf91): Stella's nine-clause
proposal for nova-tools #2608 at `stella-tools@2fb4393`, with the amendments Rowan made clause by
clause (rowan-31f0dd184c6f) and Emma accepted as the cutter's owner (emma-f4e876aad36f), written
out as one document. The 59-card sprint-stage HOLD stands until Stella and Emma have each read
this exact revision and the gate in clause 9 is met. Nothing here authorises a recut, a staging
run or a measurement campaign.

**Amended for Stella's scoped HOLD** (#2625 comment 5783178167, at `c05fa17b`): the items she
asked to be decided in this artifact are written into their clauses as normative text, each
marked *Amended for HOLD 5783178167* — (a) and (b) clause 3, (c) clause 6, (d) clause 8, (e)
clause 9, and (f) the three former Open items: bare-directory PATHS (clause 4), lowercase keys
(clause 2) and the COMMIT RULE representation (clause 7). Open 1 is the only item left open.

**Clause 10 added at #2636** (stacked on #2625 at `3ae8e1db`): every card declares `DEPENDS-ON:`,
and the dealer's READY rule is normative. The edits it makes to clauses 2, 8 and 9 and to the
summary are each marked *Amended at #2636*.

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
if it carries one without the other. Any other key in the block that is not canonical uppercase —
lowercase or mixed case, known or unknown (`paths:`, `Paths:`, `source:`) — is refused by name,
with its line and a remedy naming the uppercase spelling. The parser does not end the block at
such a key: it reads on, so every later key is still read and diagnosed and nothing is stranded
silently. Duplicate or conflicting keys *inside the block* are refused;
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
*Amended for HOLD 5783178167 (f, former Open 3):* the lowercase-key sentence is added. *Why:*
#2607 widens what *continues* the block (any one-word key in any case), not what a typed key *is*;
exempting only the two keys the launchers read keeps one canonical spelling per key, and refusing
by name while reading on means a stray casing never truncates the header. On the 59: 0 change
(no other lowercase key in any block).
*Amended at #2636:* `DEPENDS-ON:` (clause 10) is a block key under every rule above: canonical
uppercase, hyphenated as `DONE-WHEN:` and `NO-SUBAGENTS:` are (so #2607's one-word key shape and
`headerKeyRE`, `lintheader.go:95`, already read it), once, inside the block. A `DEPENDS-ON:` line
below the block is stranded and refused by name; one inside a fenced block is quoted evidence and
declares nothing. On the 59: each block grows by one line and still ends by line 26, inside
reader 5's 40-line window.

## 3. Kinds

Emit `KIND` explicitly. The v2 set is the existing cutter set: `read`, `fix`, `replay`, `spec`,
`rebase`, `guard`, `recut`, `port`, `docs-guard`, `report`. Use explicit per-kind requirements;
do not equate `fix` with `fix-red` by a hidden alias. `MODE: script` describes execution mode,
not another KIND. Unlisted cell/rule/spec2 producers need an explicit adapter/decision before
recutting.

All ten cutter kinds have one reviewed worked example in [Card exemplars](EXEMPLARS.md). The
typed cutter links the matching pull request in every task body as `Example to follow:`; that
prose is guidance and never a second command authority. The catalog says which six kinds use
`RenderCardV2` today and which four retain their legacy renderer, so the example cannot be
mistaken for renderer support.

This set is the name set a v2 card declares. It does not globally retire the names in
`internal/hygiene/kinds.txt`, and no legacy consumer is removed by it: old names reach v2 only
through a **versioned legacy-kind adapter**. The adapter is a dated mapping table, versioned by
its date (`legacy-kinds 2026-09-22`), each row `<old kind> → <v2 kind>, removed <YYYY-MM-DD>`.
A legacy (unversioned) card or branch carrying a mapped kind is read as its v2 kind and emits
exactly one diagnostic naming the mapping and its removal date —
`kind=<old> mapped to <new> by legacy-kinds 2026-09-22; mapping removed <date>` — never a silent
alias and never the generic "not a kind this toolchain declares". A row is deleted only by a
reviewed change on or after its removal date; after that the old name is refused by name. A row
without a removal date fails the table's own class test. A kind in neither the v2 set nor the
table is refused by name, as today. The adapter serves legacy input only: a v2 card carrying an
old name is a producer error and is refused (clause 1: worker output cannot choose a weaker
adapter).

`script` has no adapter row. `MODE: script` is an execution mode, so `script` → `report` is not
a kind mapping: it is the one-card migration of `card-tools-01c`, which is receipt-only — its
product is a receipt, it changes no file in any repository, and it declares `PATHS: none` — and
which becomes `KIND: report` with `MODE: script` retained. Any other card carrying `KIND: script`
is refused by name and its kind is decided per card; it is never defaulted to `report`.

*Changed from 2fb4393:* the second and third paragraphs are added.
*Amended for HOLD 5783178167 (a, b):* Rowan's amendment 3a ("this set replaces `kinds.txt`";
old names "refused with `kind=<old> is retired`") is replaced by the versioned adapter, which
keeps the adapter clause 1 promises (stella-tools `f6e35e9`); amendment 3b's
"a card whose kind was `script` is `KIND: report`" is restricted to receipt-only `01c`.
*Why:* `kinds.txt` on `dev` holds a different ten; three names are in common, and
`cmd/nova-check/hygiene.go` (`--kind`) refuses by that file, so a global replacement would
refuse every open legacy branch at once. `script` is not in `kinds.txt`; one of the 59
(`card-tools-01c`) carries `KIND: script`.
*Effect on the 59:* `fix` 54 and `report` 4 unchanged; `script` 1 → `report`; `kind-declared`
59 → 0.

## 4. PATHS

Emit comma-separated repository-relative entries or the explicit `none` value. Parse once into a
list and pass the same list to validation, staging checks and harvest. Reuse hygiene
validation/matching. Emit `directory/**` canonically. Never accept a whole multi-path string as one glob. Do not guess how to split a
filename containing spaces.

An entry containing an unquoted space is refused by name, with the line and a remedy naming the
comma form. It is never split on whitespace, never accepted as one glob, and never guessed at.

A bare-directory entry — one that names a directory rather than a file or a glob, with or without
a trailing `/` — is refused by name in the same way, with the line and a remedy naming
`directory/**`. No entry point of a v2 card reads a bare directory as "everything under it". A
trailing `/` is refused on its text alone; any other entry is a directory when it names a tree at
the card's `base-sha`, so every entry point decides it against that tree, and how the lint's
command line is handed the tree is Emma's to name, as clause 9 leaves the producer's spelling.

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
*Amended for HOLD 5783178167 (f, former Open 2):* the bare-directory paragraph is added and
"agree any bare-directory migration explicitly" is removed. *Why:* #2598 reads a bare directory
as everything under it only at harvest, as the interim reading of cards cut before the contract,
and the review cited that reading as the weakness in #2547; the contract must not depend on it,
and refusing it is the one rule that needs no identical bare-directory matching across lint,
staging and harvest.
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
a bare `RUN:` marker line. The typed header MAY carry a `RUN:` key, and only when the region holds
exactly one command line: its value is derived from that line and must equal it exactly — string
equality, no normalisation — and disagreement is refused at both entry points, exactly as
`COMMAND:` is. The echo is optional; a card without it is complete. A multiline region has **no**
echo: a `RUN:` or `COMMAND:` header key on a card whose region holds more than one line is refused,
and no entry point joins, quotes or flattens the lines into one. The region is the sole authority,
so there is nothing to make lossless. A card carrying a `RUN:` header key and no region is refused
at both entry points.

Staging without the echo is part of this clause: `bin/sprint-stage` (clause 8, reader 6) then
anchors on `^TEST: `, else `^KIND: `, both of which every v2 card carries (clauses 3, 5), and a
class test stages a card with no echo and asserts the insertion lands inside the block.

If `COMMAND` metadata is retained, derive it from the same structured input and reject
disagreement. Do not create a second independently editable command authority. `MODE: script`
retains the separately reviewed JSON-argv contract; do not reinterpret a model card's
prose/markdown as argv or add an implicit shell.

*Reading note (clauses 2 and 6 together):* the bare `RUN:` marker line inside `## Run` is the
region's marker. It is not a header key, not a duplicate of the header's `RUN:` echo, and not a
stranded key.
*Changed from 2fb4393:* "The v2 operative Run region is the execution declaration" is replaced by
the first paragraph, which says which spelling exists and makes the header key a checked echo.
The rest is kept.
*Amended for HOLD 5783178167 (c):* Rowan's resolved rule (rowan-fa780df52148) replaces his
earlier wording: the echo is optional, single-line only, exact equality; a multiline region has
no echo and no lossy shell joining; the staging fallback without the echo is tested.
*Why:* 59/59 carry `RUN: <command>` as a header key; 0/59 carry a `## Run` heading; `OperativeRegion`
(#2522, `cut_template.go:506-517`) requires the heading and a bare `RUN:` line, so the producer
refuses all 59 today. `bin/sprint-stage:29` anchors its DONE-WHEN/NO-SUBAGENTS insertion on
`^RUN: ` (with the space), which the echo matches and the bare marker does not; without an echo it
falls back to `^TEST: ` and `^KIND: `, which a v2 card carries (clauses 3, 5).
*Effect on the 59:* all 59 gain a `## Run` section (mechanical: the `RUN:` value, one line, so
the header echo may stay and must equal it); `card-tools-01c`'s RUN is prose ("bash, gh
(read-only), and the nova-decide first-pass checker…") and is rewritten by hand; if its region
is multiline, its header `RUN:` is removed.

## 7. Existing invariants

Preserve immutable input identity, expected attempt binding, owned RESULT schema, DONE versus check
outcome, Returned/Verified/Landed distinction, retained evidence, STOP/HOLD/UNKNOWN, and contextual
read scope. See the [Typed records](SPEC-SWARM.md#typed-records) section in `docs/SPEC-SWARM.md` for the
canonical versioned contract of RESULT v2 and DISPOSITION v1. SYMBOL/RED-WHEN are declarations, not
proof of runtime coverage. A4 text lint is not actual PATHS enforcement.

A rule that reads prose reads only the card's own instructions to the worker, never a fenced block,
a quoted prior card, an inlined diff or other retained evidence.

**The commit rule stays implicit, and its structured input is PATHS.** A v2 card carries no
commit-rule key. The PATHS-only commit rule (A4) has one value for every v2 card — no card may
opt out of it — and nothing A4 enforces reads a key: the cutter lint refuses `git add -A`,
`git add --all` and `git add .` inside the `## Run` region (`ValidateCardV2`, #2522 at
`9ee8155`, `internal/pulse/cut_template.go:462-465`), and what a worker actually staged is
checked by harvest's staged-diff/commit boundary against the clause 4 list. The required
structured input is therefore PATHS itself (clause 4; `none` explicit), which the rule is a
function of. The worker's commit instruction — the `COMMIT RULE:` sentence on the 59, A4's
fixed `Commit rule:` line in `RenderCardV2` (`cut_template.go:316`) — is prose rendered by the
cutter from the clause 4 list and the branch the card names. It is not a key, not a second
command authority and not a second PATHS list: no reader parses it, and harvest checks against
PATHS, never against it. Under #2607's grammar a two-word `COMMIT RULE:` is not a key and ends
the block; it is the first prose line, and a typed key after it is stranded and refused by name
(clause 2), never silently dropped.

*Changed from 2fb4393:* the second paragraph is added (Rowan, addition 7).
*Amended for HOLD 5783178167 (f, COMMIT RULE):* the third paragraph is added. *Why implicit
rather than a `COMMIT: paths-only` key:* A4's landed text renders the rule as a fixed prose
line, not a key, and says its lint "does not enforce PATHS-only staging"; a key with one legal
value would echo nothing harvest reads and add a missing-key failure that guards nothing, while
PATHS is already required and is what the enforcement reads. On the 59: 0 change (each carries
the sentence as the line after the block; nothing follows it).
*Why:* `card-tools-20` is refused today by `no-sandbox` (`cmd/nova-swarm/lint.go:274-277`, a
bare `strings.Contains(l, "nova-sandbox")` over every line) for naming `.nova-sandbox-tmp/` in
its own task description. The measured case for "A4 text lint is not actual PATHS enforcement":
`matchDeclared` (`internal/pulse/harveststale.go:292-305`) admits every file under a bare
directory entry (#2547; #2598 made that the interim harvest reading, and a v2 card refuses a bare
directory, clause 4).
*Effect on the 59:* 1 (`tools-20`).

## 8. One implementation

Every reader of a card's structure calls the same versioned parser: the cutter, `nova-swarm lint`,
harvest's PATHS enforcement, batch admission's card-shape check, the launcher scripts'
base-pin read, `bin/sprint-stage`'s re-pin and insertion, and the dealer's READY test
(clause 10). Where a reader cannot call the parser (a shell script), the contract states the
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
| 6 | stage: `bin/sprint-stage` | lines 24, 29, 37 | `^BASE: `, `^base-sha: `; inserts DONE-WHEN/NO-SUBAGENTS at the anchor `^RUN: ` → `^TEST: ` → `^KIND: ` |
| 7 | dealer: `sprint.Ready` + `deal.Plan` | #2624 at `1e5e0227`, `internal/sprint/refill.go:75`, `internal/deal/deal.go:290` | a task's `depends_on` and `paths`, comma lists in the Redis task hash (`internal/sprint/redis.go:168-169`), written from the `--depends-on`/`--paths` arguments (`cmd/nova-pulse/sprint.go:210`), not from a card |

Clause 10's key has three of these readers. The cutter (reader 1) writes it from the lineup's
`depends-on` column or the nova-work sexp edges; the lint (reader 2) refuses a card without it,
naming the key, and refuses a self or unknown entry; the dealer (reader 7) reads it, with PATHS,
as the parser returns them from the card. Readers 3-6 do not read it.

The literal shape reader 5 depends on: `base-repo: <url>` and `base-sha: <40 hex>`, lowercase, in
the first 40 lines, both or neither (clause 2 puts them in the block, which ends well before
line 40 on every card).

**`bin/sprint-stage` is the sixth normative reader.** The literal shapes it depends on:
`BASE: ` and `base-sha: ` at column 0 in the block, and an insertion anchor inside the block —
`RUN: ` when the optional echo is present (clause 6), else `TEST: `, else `KIND: `. Its re-pin is
**idempotent**: re-pinning a card that is already pinned changes at most the `base-sha:` value and
inserts nothing. A key it inserts is inserted only when the block does not already carry that
key under its canonical spelling — the presence test matches the spelling it inserts
(`NO-SUBAGENTS:`, hyphen; today it tests `^NO SUBAGENTS:` with a space) — so a second re-pin
produces a byte-identical card and never a duplicate `NO-SUBAGENTS:` or `DONE-WHEN:`, which
clause 2 would refuse. Before adoption, a required class test asserts every literal window and
anchor readers 5 and 6 depend on against a card the parser emits (the 40-line window, the two
lowercase keys, each of the three anchors including the no-echo `TEST:`/`KIND:` fallback), and
re-pins one card twice and asserts the second re-pin is a no-op.

*Changed from 2fb4393:* "cutter and swarm lint call the same versioned parser/validator" is
widened to all six readers (the first three sentences above); the diagnostic, METHODS and
no-separate-regex sentences are kept.
*Amended for HOLD 5783178167 (d, former Open 4):* `bin/sprint-stage` is promoted from a listed
candidate to the sixth normative reader, with idempotent re-pin and the literal-shape class
checks required before adoption.
*Amended at #2636:* the dealer is the seventh reader, and the paragraph after the table names
which readers write, refuse and read `DEPENDS-ON:`.
*Why reader 4 matters most:* it is in no earlier draft of #2608. The 59 write steps as
`## STEP 1.` around line 30, so admission refuses every one of them on every opencode/deepseek
route (the dogfood saw `ADMIT REFUSED … card-shape`), and a v2 card with no numbered STEPs, which
clause 2 permits, is refused there with both linted entry points green.
*Why reader 6:* clause 2's evidence names it, and by this clause's own sentence a reader not on
the list is a defect in the list. Its re-pin defect is measured, not predicted: it tests
`^NO SUBAGENTS:` (space) and inserts `NO-SUBAGENTS:` (hyphen), so every re-pin of a card already
carrying `NO-SUBAGENTS:` adds a second one inside the block.

## 9. Acceptance

Invalid cards for every agreed rule, a legacy compatibility set, duplicate/version/quoted-evidence
cases, multiple-path and multi-package cases, and script/model separation. The same bad card must
fail for the same reason at both entrypoints. Zero differences and 59 green alone are insufficient
if semantic requirements were removed.

The contract is accepted when, in one run at the pinned head:

1. **The migrated 59 exit 0 at both entry points**, and the migration is mechanical and
   reviewable: PATHS commas (59), TEST (14), KIND (1), a `## Run` section (59), `DEPENDS-ON`
   (59, clause 10), and one hand-rewritten card (`01c`). Identity and evidence are untouched: line 1, `base-sha`, SYMBOL,
   RED-WHEN, DONE-WHEN and the inlined findings. Measured today under the contract simulated:
   **57/59**; the two that do not pass are `01c` (clause 6: RUN is prose) and `tools-20`
   (clause 7: refused for a string it quotes), named fixes, not migration failures.
   *Amended at #2636:* the 57/59 was measured before clause 10. Under it every one of the 35
   named dependencies is written as a card id or a reference (clause 10), so the expected count
   stays 57/59, provided the lineup carries the dogfood conform card; without it the ten cards
   that name it are refused as unknown and the count is 47/59 (arithmetic, not measured).
2. **The cutter re-emits `card-tools-03-stale-base-false-positive` byte-for-byte from structured
   input** — Stella's choice (5783178167). It is a positive roundtrip: the expected bytes are
   fixture P, `accept.md`, which is that card migrated under this contract, so the one file is
   both what the cutter must render and what both entry points must accept; any byte of
   difference fails it.
3. **The three fixtures below pass as a class test in CI**, not only on a bench.

*Changed from 2fb4393:* "replay the exact 59 owned card inputs through cutter and consumer,
preserving their identity/evidence" is replaced by items 1-3 (Rowan's replacement); the rest is
kept.
*Amended for HOLD 5783178167 (e, former Open 5):* item 2 names the card and binds it to
`accept.md`.
*Amended at #2636:* item 1 gains `DEPENDS-ON` and the count it moves; item 3 binds the third
fixture, D.
*Why:* the replay is not runnable. The 59 carry 24 distinct header keys; `RenderCardV2` at
`9ee8155` can emit 8 (`SCHEMA`, `ATTEMPT`, `REPO`, `BASE`, `PATHS`, `TEST`, `SYMBOL`,
`RED-WHEN`), writes `BASE_SHA:` where the launchers read `base-sha:`, and has no input field for
`KIND`, `DEADLINE`, `LEG`, `base-repo`, `base-sha`, `FILES`, `RUN`, `DONE-WHEN`, `NO-SUBAGENTS`,
`UNATTENDED`, `MODE`, `TURNS`, `SOURCE`, `ROUTE`, `PREFLIGHT` or `COMMIT RULE`.

### The fixtures

All three live at `cmd/nova-pulse/testdata/card-contract-2608/` and are called at both entry points:
the producer's validator (`pulse.ValidateCardV2` + `OperativeRegion`) and the consumer's lint
(`swarm.LintCardHeader` + `lintCard`, on the command line `nova-swarm lint --card <file> --typed`).
The class test calls the functions; a file-taking producer command (`nova-pulse cut --validate
<file>` in the review) exists neither on `dev` nor on #2522, which has `--validate-contract` over
its own output, so the CLI spelling for the producer is Emma's to name.

**P — `accept.md`** (also the clause 9 item 2 roundtrip target).
`card-tools-03-stale-base-false-positive.md` from the 59, migrated and
otherwise byte-for-byte: PATHS with a comma (line 11, and the RESULT template's `PATHS` line),
`DEPENDS-ON: -` on line 12 (ORDER.tsv gives `tools-03` `-`), and a `## Run` section whose
command equals the `RUN:` header key. Both entry points **exit 0 and
print no diagnostic**. One property per line:

- the header block is contiguous from line 2 and nothing is stranded (reader 2);
- `base-repo:`/`base-sha:` are lowercase at column 0 on lines 9-10, inside the first 40 lines
  (reader 5);
- `^BASE: ` and `^RUN: ` exist, so sprint-stage's re-pin and insertion anchor both hit;
- PATHS is comma-separated and both entries validate (clause 4);
- TEST is one package and one Go test name (clause 5);
- exactly one `## Run` region, whose command equals the `RUN:` header key (clause 6);
- `SCHEMA: v2` is read from line 3 and the RESULT template's copy is not a duplicate (clauses 1, 2);
- `DEPENDS-ON: -` is on line 12, inside the block, standing alone (clause 10);
- it is the positive control for N and D: the same card, one character or one line different.

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

**D — `refuse-depends-on-missing.md`.** P byte-for-byte with one edit: line 12, `DEPENDS-ON: -`,
is removed (so D is byte-for-byte the P of #2625 at `3ae8e1db`). Both entry points **exit 2** and
print the same line, differing only in the `card=`/file prefix:

```
depends-on-declared: 1: no DEPENDS-ON: line under the contract line: a v2 card declares what it waits for, or that it waits for nothing remedy=DEPENDS-ON: <card-id or owner/repo#n>[, ...], or DEPENDS-ON: - when the card waits for nothing
```

Line 1 follows the lint's convention for an absent key (`kind-declared` names line 1 for a card
with no `KIND:`, `lintheader.go:245-246`). The assertion is N's: rule token, line, text and
remedy, compared as one string after the prefix is stripped. This rule is chosen because both
sides are silently clean on it today: neither the lint nor the producer knows the key.

*Baseline, measured on `dev` at `1e1e5fb9` (2026-09-22):* `nova-swarm lint --card <f> --typed`
exits 2 on **all three** fixtures with the same eight drifts (`clone-step`, `steps-numbered`,
`scratch-absolute`, `result-last`, PATHS/TEST/SOURCE stranded below a block that ends at the
lowercase `base-repo:` on line 9, `kind-declared` on `fix`); D's line numbers after line 11 are
one lower. No line names `DEPENDS-ON`. Nothing in the output tells P from N, or P from D.

## 10. DEPENDS-ON

Every v2 card declares `DEPENDS-ON:` in its header block (clause 2). Its value is a
comma-separated list of entries, or the single token `-`, which declares that the card waits for
nothing. An entry is one of two forms:

- a **card id** — the id the lineup names the card by, which is the id on the card's own line 1
  (`tools-01-harvest-commit-core`; never the file name `card-tools-01-…` and never a short form
  such as `tools-01`);
- a **reference** — `<owner>/<repo>#<n>`, an issue or a PR on GitHub
  (`mas-bandwidth/nova-tools#2550`; never `nova-tools #2550`, never a bare `#2550`).

Entries are separated as clause 4 separates PATHS: by commas, never by whitespace. `-` stands
alone: `-` beside an entry, an empty entry between commas and an entry listed twice are refused
by name.

A card without the key is refused by name, with a remedy naming the forms; absence never means
`-`. A card naming its own id is refused. A card id that is not the id of a card in the lineup
the card was cut from is refused as unknown, with the entry quoted; how the lint's command line
is handed the lineup is Emma's to name, as clause 4 leaves the tree. The lint checks a
reference's shape; the cutter resolves it and refuses one that names neither an issue nor a PR.
Anything else — a policy gate such as `dogfood CONFORMS <verb>`, prose, a URL — is not an entry
and is refused by name. A gate that work must wait for is cut as a card, and the waiting cards
name that card's id. A cycle cannot be seen from one card: the cutter, which holds the whole
lineup, refuses a lineup whose DEPENDS-ON edges form one, naming the cycle.

The cutter writes the key from the lineup's `depends-on` column, or, for a card cut from the
nova-work graph, from its edges in `docs/roadmaps/nova-work.sexp`, expanding a short form to the
one lineup id it names and a `<repo> #<n>` note to its reference. A model never writes it, and
the cutter never supplies `-` for a source row that names nothing: that is a cut error.

**The dealer's rule is normative here.** A card is **READY** when (a) every DEPENDS-ON entry is
**LANDED** — a card when it is merged into the card's target base (`REPO`/`BASE`), clause 7's
Landed; a PR reference when the PR is merged; an issue reference when the issue is closed. OK,
reviewed, verified or an open PR is not LANDED — and (b) its PATHS (clause 4) are disjoint from
the PATHS of every card in flight, where in flight is handed out and neither LANDED nor closed.
Disjointness is decided by the hygiene matching clause 4 reuses; `PATHS: none` is disjoint from
every list. At every tick the dealer hands out the **highest-priority READY card**. Priority is
the lineup's leverage tier, ties broken by the lineup's row order. A card that is not READY is
passed over, not waited on: it keeps its place, is tested again at the next tick, and the dealer
hands out the next READY card in priority order, so a serial head never blocks the queue. An
entry that can no longer land — a PR closed without merging, a card closed unlanded — is
reported by name at every tick, never silently passed over forever. The
dealer reads DEPENDS-ON and PATHS as the shared parser returns them from the card (clause 8,
reader 7); a task record's `depends_on` and `paths` are written from that parse, never retyped.

The dealer's control: three cards with disjoint PATHS in priority order A, B, C, where B declares
`DEPENDS-ON: A` and C declares `DEPENDS-ON: -`. The dealer hands out A, then C while A is in
flight, then B only after A lands; while A is OK but unmerged, B is not handed out.

*New at #2636:* not in `2fb4393`; clauses 2, 8 and 9 and the summary are amended to carry it.
*Why:* Glenn, 2026-09-22 4:26 PM: "We will work on them in priority order *except* when we are
going serial due to dependencies, then we will pick unrelated things that are next in priority
order." Measured the same afternoon: `ORDER.tsv` (sprint-tools) carries depends-on for 35/59;
0/59 card files carry a dependency key; the 598 schema cells of 2026-09-21 carried none, and the
python/lua/squirrel legs duplicated work. The dealer at #2624 (`1e5e0227`) counts how many open
tasks each one unblocks (`sprint.Ready`, `internal/sprint/refill.go:75-93`) but still puts a task
whose dependency is open into the ready set, and nothing on its route tests PATHS against work in
flight. LANDED and not OK because an OK card's branch is not in the base a dependent card is cut
from: the dependent is cut against code that is not there, and its PR cannot merge. Disjoint
PATHS because two cards editing one file in flight together make two PRs, and the second is a
recut.
*Amended at #2636, the coordinator's ruling (20:28Z):* references are an entry form, and the
`dogfood CONFORMS` gates become one card, so no named dependency of the 59 is left undecided.
*Why a closed PR is reported:* `rowan-tools #132`, named by 7 of the 59, was closed as superseded
by #134-#138; #137 and #138 were then closed unmerged and replaced by #139 (merged 20:22Z). A
card naming #138 would wait forever.
*Effect on the 59:* all 59 gain the line, from `ORDER.tsv`: 24 `-` and 35 named, every one
expressible:

| source in `ORDER.tsv` | cards | written as |
|---|---|---|
| lineup cards only, by short form (16 distinct, `tools-01` … `tools-53a`) | 16 | the full lineup id, each resolving to exactly one card |
| `dogfood CONFORMS <verb>` (`tools-10`, `26`-`31`, `33`-`35`; `tools-26` also `tools-01`) | 10 | `tools-54-dogfood-conform-matrix`, a card the lineup gains (the 60th; it fills the CONFORMS lines of `reports/dogfood-matrix-2026-09-22.md`, and must not name `tools-35`, whose card depends on it, or the cutter refuses the cycle) |
| `rowan-tools #132` (`tools-38`-`44`; `38` also `tools-37`, `42` also `tools-17`) | 7 | `mas-bandwidth/rowan-tools#139`, the PR that merged the tree #132 described |
| `nova-tools #2550` (`tools-50`, also `tools-24`, `tools-25`) | 1 | `mas-bandwidth/nova-tools#2550` (an issue: LANDED when closed) |
| `nova-tools #2522` (`tools-52`) | 1 | `mas-bandwidth/nova-tools#2522` (a PR: LANDED when merged) |

Expected (arithmetic, not measured): 57/59 accepted at both entry points, as before clause 10,
once the lineup carries `tools-54`. At the first tick, on dependencies alone (PATHS not yet
applied), **29/59** can be READY: the 24 `-` cards and the 5 whose only entry is
`mas-bandwidth/rowan-tools#139`, merged (`tools-39`, `40`, `41`, `43`, `44`); `nova-tools#2550`
is open and `nova-tools#2522` is unmerged.

---

## Effect on the 59, summarised

| change | cards | what changes |
|---|---|---|
| clause 2 | 0 | the 59 already have the contiguous line-2 block |
| clause 3 | 1 | `01c` only: `KIND: script` → `KIND: report`, `MODE: script` kept; no adapter row for `script` |
| clause 4 | 59 | 58 space → comma; `01c` → `PATHS: none` |
| clause 5 | 14 | 7 multi-package Go → one anchor; 7 non-Go → `TEST: none` + DONE-WHEN control |
| clause 6 | 59 | a `## Run` section each; `01c` rewritten by hand |
| clause 7 | 1 | `tools-20` stops being refused for text it quotes |
| **net, measured (simulated)** | **57/59** | `01c` and `tools-20` are the two named fixes |
| clause 10 | 59 | each gains `DEPENDS-ON:` from `ORDER.tsv`: 24 `-`, 35 named; 16 cards only, 10 the dogfood conform card `tools-54`, 7 `mas-bandwidth/rowan-tools#139`, 2 `mas-bandwidth/nova-tools#…` references |
| **net with clause 10 (arithmetic)** | **57/59** | accepted, once the lineup carries `tools-54` (47/59 without it); not measured |
| **READY at the first tick (arithmetic)** | **29/59** | the 24 `-` cards and the 5 waiting only on the merged `rowan-tools#139`; before PATHS disjointness |

## Open — not decided by this text

Each is a question the sources leave open or a place they conflict; none is settled by silence.

1. **The rows of the legacy-kind adapter** (clause 3). The adapter, its diagnostic and its dated
   form are decided; no source yet names the v2 kind or the removal date for `fix-red`,
   `transcript-test`, `sweep`, `mutation-kill`, `probe`, `text`, `tone`. The rows are written in
   the table and reviewed with it.

Decided at the HOLD 5783178167 revision, and no longer open: 2 bare-directory PATHS (clause 4),
3 lowercase keys (clause 2), 4 sprint-stage as the sixth reader (clause 8), 5 the round-tripped
card (clause 9 item 2), and the COMMIT RULE representation (clause 7). Decided at #2636: a
dependency that is not a lineup card is a `<owner>/<repo>#<n>` reference or is cut as a card
(clause 10).

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
- This PR, mas-bandwidth/nova-tools #2625 — the amended text, and the two fixtures of clause 9. No
  code.
- Stella's scoped HOLD, #2625 comment 5783178167 (at `c05fa17b`) — agreement with the direction,
  and the items to decide in this artifact before pinning; she selects card-tools-03 for the
  roundtrip.
- rowan-fa780df52148 — Rowan's resolved rules on the bus (the adapter, `01c` only, the optional
  single-line echo, sprint-stage as reader 6), agreeing with Stella's comment; written into
  clauses 3, 6 and 8 at the revision after `c05fa17b`.
- mas-bandwidth/nova-tools #2636 — clause 10, DEPENDS-ON on every card and the dealer's READY
  rule, with fixture D; stacked on #2625 at `3ae8e1db`. Glenn, 2026-09-22 4:26 PM: "We will work
  on them in priority order *except* when we are going serial due to dependencies, then we will
  pick unrelated things that are next in priority order." Then: "Do all cards have dependency
  information?" Measured: no (clause 10, *Why*). The coordinator's ruling at 20:28Z: references
  are an entry form; the `dogfood CONFORMS` gates become one card.
