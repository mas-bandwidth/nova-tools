## The learned admission checklist

Ikrima's synthesis for Glenn, "Let tomorrow's questions shape today's memory" (2026-09-16),
reviewed by Rowan. The organizing question: what may Rowan forget while still able to learn,
decide and act correctly tomorrow? The answer from today's record is the **abstain reason** —
the recurring failure class the machinery actually produced. The checklist below is cut from
that history, not from imagination, and every rule names the hurt that made it and the red
test that holds it. `nova-pulse cut --probe` runs it against each new card before launch.

1. **The checklist is cut from the abstain history, never invented.** The 2026-09-15 baseline
   over 60 batches: first-attempt 0.58, and the classes that recur are `no-result` (26),
   `wall refusal`, `idle kill`, `admission` and `line1-mismatch`. **Hurt:** a rule written
   after each fault did not transfer to the next unfamiliar card, so the same class abstained
   again; a checklist with no history behind it is a new opinion, not memory. **Red test:**
   `probe-reads-the-abstain-history` — a check whose class is absent from the history is
   skipped, and the same check refuses the card when the class is present.

2. **`cut --probe` runs the checklist against each new card before launch.** **Hurt:** the
   classes were discovered only after a card was cut, launched and abstained, at the price of
   the whole run; the probe pays a read instead. **Red test:** `probe-refuses-before-launch` —
   a card carrying a known class is refused with no card written and exit 1.

3. **The five checks, each named for the class it was learned from:** paths outside the job;
   quoted forbidden words; a missing red-test step; the budget against the card's size; the
   result location. **Hurt:** `admission` and `wall refusal` came from cards that left their
   job, `line1-mismatch` from a card with no red-test step, `idle kill` from a card over its
   file budget, `no-result` from a card that never named where `RESULT.md` goes. **Red test:**
   `probe-names-the-five-checks` — each class names its one check.

4. **A probe refusal names the known class and its remedy.** **Hurt:** an abstain reason that
   arrived without its class was triaged as new and cost a fresh rule; the probe says
   `class=` so the per-class count stays comparable to the history. **Red test:**
   `probe-refusal-names-the-class`.

5. **Measure repeated abstains of a known class on unfamiliar cards (retained transfer).**
   The measure is abstains per class per day, and total cost with reads and probes included;
   the adoption round and Glenn's judgment stay the external standard. **Hurt:** counting only
   total abstains hid that the classes had moved to new cards. **Red test:**
   `probe-counts-by-class-per-day`.

6. **A compression is legal only if the next decision survives it.** Apply it to the manager's
   `SHIFT END` and `HANDOFF` record: a successor must be able to make the same next decision
   from the record alone. **Hurt:** a handoff that carried what the shift produced, not what
   the successor needs, sent a fresh window to a different first move. **Red test:**
   `handoff-record-supports-next-decision`.

7. **A cairn compaction carries what the next decision needs, not what the last produced.**
   **Hurt:** a session summary that preserved the last turns lost the one fact the next window
   acted on. **Red test:** `cairn-preserves-next-decision`.

8. **A read RESULT states its scope: `not checked: <list>`.** **Hurt** (his PR 300 point): a
   "review passed" line lost the kind and scope of the evidence it stood on, so a reader took
   a pass over one axis for a pass over all — "review passed" is not a verdict. The template
   change lands in `nova-pulse cut` and in SPEC-REVIEW. **Red test:**
   `read-result-states-not-checked`.

9. **The objective is future capability per unit of total cost, verification included.** The
   ledger adds the read and probe cost to each card's row, so the probe is never free by
   construction and a cheaper route that cannot hold the card is named. **Hurt:** a route
   chosen on card price alone bought abstains that cost more than the card. **Red test:**
   `ledger-counts-read-and-probe`.
