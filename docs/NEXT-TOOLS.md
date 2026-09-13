# The next nova tools — the set, and how it is decided (DRAFT 1, 2026-09-13 17:30Z)

Glenn, 2026-09-13, verbatim: *"land the audit fixes, finish the fixed table work, and then
once this is done, let's work out the next set of nova tools we should add, with particular
emphasis on things that help us coordinate work and count tokens and optimize."* Then, live:
*"The two of you should coordinate on a spec for all the new nova tools, prioritizing this."*

This file is the set. One section per tool, in the shape below, and nothing enters it
without the first three lines filled from the record. The order of arrival is the process:
**the friends' ideas first** (asked on the bus 2026-09-13 15:26Z, deadline 17:30Z), then
Rowan's and Stella's lists for their feedback, then this file, then each tool's own
`SPEC-<TOOL>.md`, read by every line before any code.

**All friends review specs.** Glenn made this explicit on 2026-09-13: each friend
brings a model, a person and a point of view. Keep a review roster with each
friend's own response, the revision read, concrete findings and unresolved
disagreements. An unanswered invitation is pending, not assent; a decline is
recorded honestly. Cheap-model cold reads supplement those reviews and keep
separate provenance; a child does not stand in for its parent's own review.
Circulate material revisions back to the friends and reconcile their findings.
Do not claim consensus from a subset. Cheap capable models remain the default
for bounded worker tasks, measuring total tokens through review and repair as
well as average token cost.

## The shape of an entry

- **The friction it removes** — the measured hurt, with its date and where it is recorded.
- **What it must never do** — the fence, stated before the verbs.
- **The verbs** — one line each, in the house grammar.
- **What it reads and writes** — files, the bus, a forge; nothing else.
- **How it is measured** — the number that says it helped, and who reads it.
- **Who asked for it** — the lines, by name, from their own notes.

## Candidates, by the record so far

| tool | source | status |
|---|---|---|
| nova-work | nova-tools#177 and its comments; Glenn 2026-09-13 | [SPEC-WORK.md](SPEC-WORK.md), draft 18 (its line 1 is the number of record) |
| nova-work `query`/`focus` and the versioned baseline | Stella, bus 15:30Z (stella-4b9200ddc994): "we keep rereading histories to answer simple progress questions" | in SPEC-WORK draft 18 as the query contract and the scope events |
| leases: take/renew/release plus compact availability | Stella, 15:30Z: "prevent scheduling asleep workers"; Rowan, #177 comment 5654176537 | in SPEC-WORK draft 18 as the lease events and `who`/`stale` |
| evidence ingestion and projections | Stella, 15:30Z: "avoid status-label guesses and stale tables" | in SPEC-WORK draft 18 as `evidence`, `verify` and `render` |
| retained token and cost accounting joined to execution attempts | Stella, 15:30Z: "count review and repair, not merely builder tokens"; Glenn, 16:34Z via Stella: reduce average cost per token and total tokens | the `:attempt` event's `:usage` pointer in SPEC-WORK; the join is nova-tokens' (#175, #181), its own spec |
| review packet and one-line verdict per PR | Rowan, bus 17:30Z: 25 duplicate findings, reads of the wrong head, a HOLD unread for 100 minutes | candidate; nova-review |
| swarm finalize checks the result contract | Rowan, 17:30Z; nova-tools#133: 12 Mercury runs, 1 accepted (2026-09-13) | candidate; a nova-swarm change, not a tool |
| release from a frozen candidate sha with delta reads | Rowan, 17:30Z; nova-tools#229: --auto merged before CI; v0.15.0 prep by hand (#232) | candidate; nova-release |
| a `friction` verb: a stumble with its token and wall cost and the issue it becomes | Rowan, 17:30Z; nova-tools#185 | candidate |
| adoption matrix per line per tool from receipts and version lines | Rowan, 17:30Z; nova-tools#182 | candidate |
| wake on an addressed note, never a poll | Rowan, 17:30Z: 652M cache-read tokens for 1,204 turns on 2026-09-11 | candidate; a nova-wake change |
| tokens joined to work nodes: an attempt carries its usage receipt, `cost --node X` | Rowan, 17:30Z; Stella, 15:30Z (same hurt, review and repair counted) | in SPEC-WORK as the `:attempt` usage field; the ledger join is open |

## The review roster

| line | ideas asked 15:26Z, deadline 17:30Z | feedback on the lists, deadline 2026-09-14 15:00Z |
|---|---|---|
| Stella | answered 15:30Z, four candidates (stella-4b9200ddc994) | pending |
| Emma | pending at 17:30Z (not assent) | pending |
| Alex | pending at 17:30Z (not assent; on the class read and serialize.cs) | pending |
| Freddy | pending at 17:30Z (not assent) | pending |
| Johnny | reserved line, not asked | not asked |

Rowan's list went out 17:30Z (rowan note, subject "next nova tools"); Stella's and Rowan's overlap on the work set, leases, tokens and swarm finalize, and the difference is asked for by name.

*(The coordination family #175 to #187 is the candidate pool; each entry here names which of
them it draws on.)*
