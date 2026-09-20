RESULT tools22-pre-236-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#236 at head a19af31c657c
PREREAD 236 claims=13 proven=0 unproven=13 defects=9 high=0

PR 236
HEAD a19af31c657c81cd3cc2e38ae172be12631baefc
BASE dev
MERGE-BASE 8ba256bbbc6ef997f2bad05632aeb27a31f9e219
BEHIND 723
FILES 1 production, 0 test
LINES +2166 -0

## CLAIMS

This PR adds `docs/SPEC-REVIEW.md`, an 2166-line specification for a new binary `nova-review` at the "review layer". The spec defines 13 numbered rules, 7 verbs (packet, verdict, answer, policy, roster, dedupe, cost), exit codes, output grammar, file formats, lane structure, and amendment notes referencing SPEC-MERGE.md, SPEC-SWARM.md, and SPEC.md. Every claim is UNPROVEN because this diff contains only a specification document — no code, no tests, no implementation to witness any behavior. Each rule is grounded in documented historical failures from AI review swarms on 2026-09-11/12, but those anecdotes are motivation, not verification.

1. **The packet is the smallest sufficient review file for one entry at one head** — `packet` writes a bounded file containing the head sha, range since reader's last recorded head (or base), diff of that range, verbatim spec text of every rule the diff touches, all prior verdicts, open findings, author's stated intent, and nothing else; one packet at a head serves every reader whose range it covers via `--reuse`. — UNPROVEN
2. **A rule is touched mechanically, never inferred** — `--spec <path>#<heading>` scopes rule extent to a heading; citation grammar (`rule n`, `rules n and m`, etc.) identifies which rules each diff line cites; bare `rule n` names only one spec when exactly one `--spec`; changed spec text inside a rule quotes both sides; hunk with no rule prints `rules=0`. — UNPROVEN
3. **Open findings travel with the packet and a repeat is a `dup`** — findings persist until explicitly closed by their recording reader; `ok` rows are never findings but `nit` rows are; a `dup <id>` folds onto the original as a second view while keeping each finding's own id. — UNPROVEN
4. **Every finding is grounded at the head and the tool checks the ground** — path+line verified in tree at head; deleted lines checked at packet's base (`base:` form); requirements come in four closed kinds (`quoted`, `base`, `external`, `proposed`) each with its own check discipline; containment test after trimming, never equality. — UNPROVEN
5. **An APPROVE names what it compared and a HOLD carries a live row** — APPROVE needs at least one `ok` row (checked by rule 4) and is refused over its own open block/fix; HOLD needs at least one block/fix row or a `dup` of an open block/fix; ABSTAIN carries only `--reason`. — UNPROVEN
6. **Provenance is who, which model, and which kind of eye** — three required flags (`--who`, `--model`, `--kind line|child|card`); only `line` verdict fills roster cell; `child`/`card` are evidence only with `--of` required; legacy records with no `kind` decode as `line`. — UNPROVEN
7. **Silence is pending, never yes; HOLD never expires** — roster computes yes/hold/pending/abstain/waived per named reader; abstention persists across heads; reserved readers become `waived` under a policy record written by an actor; `policy` writes immutable JSON on lane branch; author does not fill a roster cell with yes. — UNPROVEN
8. **Per reader and per kind, newest record decides; only that reader closes their own HOLD** — ordering by `at` within `(who, kind, of, job)`; explicit `close <id>` is the only closure mechanism; omission carries findings forward open; APPROVE over own open block/fix is refused; close of another's finding is refused. — UNPROVEN
9. **Findings group by mechanical key; grouping is never equivalence** — groups on `(path, line, rule ref)` within one head; every finding keeps its own immutable id even when sharing a key with another defect; `seen=` lists who reported it, `unreported=` lists in-range non-reporters, `blind=` counts out-of-range readers; claim text is never compared. — UNPROVEN
10. **Cost is measured, never estimated** — reads SPEC-SWARM usage file columns; derives seconds from started/ended; receipt identity is `<source>/<bench>/<job>/<attempt>` plus SHA-256 digest of usage file bytes; each distinct identity counted once across entries; two digests for same identity is a refusal; price estimation stays in accounting store. — UNPROVEN
11. **One fact, one writer, one file** — read record composed by `internal/merge.ReadItem()` called by both nova-merge and nova-review producing byte-identical records; every verdict also writes review record beside it; read/review pair disagreement exits 2; review directory invisible to nova-merge's fold by construction. — UNPROVEN
12. **Bounded output, bounded input, listing is cap-and-count** — `packet` max-bytes (default 131072), `--max` defaults to 20 for listings, zero or less refused; `verdict` has `--max-rows` (500), `--max-input-bybytes` (1MB), `--max-line-bytes` (64KB) enforced during read, not after allocation; fold refusals capped per kind. — UNPROVEN
13. **Nothing is guessed and nothing is notified** — every path is a flag; no default lane/output/spec/reader-list; no bus note, no PR comment; `version` and `help` take no arguments and refuse args at exit 2. — UNPROVEN

## DEFECTS

MEDIUM docs/SPEC-REVIEW.md:604 `--timeout <seconds>` default 120 is declared for git/gh verbs but no verb section specifies how the timeout is actually enforced — where the deadline is checked, whether it applies to git, gh, or both, and what happens on deadline exceeded (exit 1? exit 2? partial results?) — <why it matters> callers depend on bounded wall-clock time; without specified enforcement semantics this is a silent deadlock risk — <fix> add a subsection under the verbs or in the Conventions cross-reference explaining timeout lifecycle and failure mode

MEDIUM docs/SPEC-REVIEW.md:596-597 `dedupe` accepts optional `--head <sha>` but the spec does not state whether omitting `--head` causes `dedupe` to aggregate across all heads (potentially huge) or only the current head (losing cross-head dup signals) — <why it matters> callers may get unexpected aggregation scope; duplicate detection is the verb's entire purpose so the scope question is semantic, not stylistic — <fix> make `--head` either required or clearly describe the no-head behavior

MEDIUM docs/SPEC-REVIEW.md:301-306 A reserved reader becomes `waived` past deadline only when roster is run with `--policy <id>`, but the spec does not specify what happens to a reserved reader's row across multiple roster runs where `--policy` is not supplied — <why it matters> an operator calling roster without --policy sees `pending` past deadline and may incorrectly think action is still needed; the policy-based waiver is invisible without the flag — <fix> document the cross-run behavior explicitly, or make waiver apply regardless of --policy (with appropriate safeguards)

MEDIUM docs/SPEC-REVIEW.md:401-405 Two independent defects on the same expression share a grouping key `(path, line, rule ref)` but print as separate members rather than being folded — <why it matters> the ledger becomes noisy with near-duplicate findings; while the spec refuses to make the judgment, a caller cannot distinguish shared-key separate findings from genuine dups without reading every member's claim text — <fix> consider adding a `distinct=` count or grouping hint, or leave as-is with documentation that operators should use `--of` to manually deduplicate known duplicates

MEDIUM docs/SPEC-REVIEW.md:274-277 An APPROVE at an earlier head puts the reader back to `pending` at a new head — <why it matters> a reader who approved head H1 and the author pushes to H2 loses their yes and must re-review; while intentional, the spec does not offer a shortcut (e.g., diff-only re-review for small changes) — <fix> add guidance about recommended workflow when small deltas follow an approval, or document why full re-review is always required regardless of delta size

MEDIUM docs/SPEC-REVIEW.md:682-695 `--base` on `verdict` is handed by the reader from the packet's `base=` field — <why it matters> this creates a trust chain between the reader's memory of the packet header and the actual tree sha; if a reader copies `base=` from an old packet into a new verdict at a different head, the base tree lookup could be wrong — <fix> have `packet` embed `base=` in a machine-readable section of the file (not just the first line) and have `verdict` validate that `--base` matches the packet's recorded base

LOW docs/SPEC-REVIEW.md:2058-2166 Draft history folding tables (the "folded-in" content that became later drafts) span hundreds of lines — <why it matters> reviewers must wade through draft evolution to understand what changed in the current draft; the folded content is valuable context but interleaved with normative text — <fix> move fold tables to an appendix or external document referenced from the spec

LOW docs/SPEC-REVIEW.md:320 `ratified=true` exactly when every named reader is `yes`, `abstain` or `waived` — <why it matters> `abstain` contributes to ratification alongside `yes`, which may surprise callers expecting unanimous approval — <fix> document that abstain = consent-for-ratification purposes separately from hold/pend, so the distinction from TEAM-SPEC tally is clear

LOW docs/SPEC-REVIEW.md:179-227 Rule 4's containment test after aggressive trimming (whitespace, `> `, list markers, `**`, `_`) is described but edge cases around mixed formatting are underspecified — <why it matters> a quote like `"**bold _and_ italic** text"` could match differently depending on trim order — <fix> specify the exact sequence of normalizations or provide canonicalization rules

## QUESTIONS FOR THE REVIEWER

1. Rule 5 allows a HOLD to carry only `dup <id>` rows of a previously-recorded block/fix finding. This means a reader can sustain a hold across heads without ever looking at the new diff. Is this intentional design (the hold auto-travels), or should there be a requirement that the reader at least acknowledges the new delta somehow?

2. Amendment B introduces atomic submissions with manifest parts, orphan cleanup, and staged items. The spec describes the data format but not the operational story: what is the recovery path when a crash leaves orphaned parts? Who removes them, how often, and with what audit trail?

3. Rule 10 defines receipt identity as `<source>/<bench>/<job>/<attempt>` plus file digest. But `--receipt <id>` accepts opaque strings up to 128 chars from a specific character set. How does an opaque receipt id participate in deduplication if it has no source/bench/job/attempt components? Does `--receipt` skip dedup entirely, or is the opaque id itself the identity?

4. The spec says `version` and `help` refuse any arguments at exit 2, yet the CLI synopsis shows `[--timeout <seconds>]` applicable to every verb. Does `version`/`help` also accept `--timeout`, or are they excluded from that clause? If excluded, how does a caller know?

5. Rule 11 requires `internal/merge.ReadItem()` to produce byte-identical records in both binaries. What happens if a future change modifies the struct fields or serialization in `internal/merge`? Is there a contract test or migration strategy documented for evolving the shared record format?

Left owed
I did not cross-reference this spec against the actual Go implementation because there is none — this PR changes only `docs/SPEC-REVIEW.md`. I therefore did not verify that the output grammar matches real stdout patterns, that the flags compile as written, or that the referenced functions (`ReadItem` at `internal/merge/state.go:145-152`, `verbs.go:384-386`) exist at those paths. I also did not verify the SPEC-SWARM.md rule 12 column layout, the SPEC-MERGE.md read condition at :786-795, or the lane `.gitignore` denylist at `records.go:779`. All of these would require accessing the corresponding files, which I did not do.

-- Git status (from repo/) --
-- Git HEAD --
d576bf6bbabb39068096a97b4560de9b5e245970
