OBJECT 939b3b324ee68b34d57bbe3163df302e81d36555

Round 2 makes the psychic link mandatory and rebuilds sprint comms on it, and most of the new guarantees are testable and consistent. I object because, read literally, several sentences break the mandatory link or the deal gate: layer 20 guarantee 12 lists `nova-bus status --as` as refusing `not_hooked` while guarantees 15 and 17 and failure mode 5 require it to report the away state and reason; the same refused list names `recv` even though guarantee 13 makes `nova-bus recv --forever --exec <deliver>` the reader that must deliver the first challenge on monitor harnesses; layer 50 guarantee 7 (and layer 60 M6) gate a deal on "displayed status is up", which layer 20 guarantee 17 defines to include deaf; and the Claude Code subsection still says to re-arm "both Monitors" after round 2 reduced the link to one. The numbered changes below are the smallest edits that remove those contradictions and close the missing-flag and mis-attribution gaps.

1. docs/spec/v1.3/20-comms.md — `status --as` cannot both refuse `not_hooked` and report the away state.
   Old sentence: "Every verb that sends, receives, reads or acknowledges for a name (`nova-bus send`, `recv`, `peek`, `ack`, `status --as`, and `nova-hook wait` carrying messages) refuses for an unhooked name with the envelope of guarantee 10, `refusal: not_hooked`, and a remedy naming the exact link recipe for that name's harness (layer 30 guarantee 7)."
   Proposed new sentence: "Every verb that sends, receives, reads or acknowledges a message for a name (`nova-bus send`, `recv`, `peek`, `ack`, and `nova-hook wait` carrying messages) refuses for an unhooked name with the envelope of guarantee 10, `refusal: not_hooked`, and a remedy naming the exact link recipe for that name's harness (layer 30 guarantee 7), while `nova-bus status --as` always runs as a read and reports the away state and its reason."
   Why: Guarantee 15's observation is "the name's next send exits `not_hooked` and status reads away with `lease_expired`", guarantee 17 requires every presence read to return the internal state and the hooked bit, and failure mode 5 and the Antigravity subsection have senders querying presence to detect a down friend. All of those are impossible if `status --as` refuses for the very name whose away state must be shown. Interface 9 also promises `nova-bus status` inspects the lease.

2. docs/spec/v1.3/20-comms.md — the monitor link reader must be exempt from the `not_hooked` gate, not named inside it.
   Old sentence: "Only the verbs that arm and prove the link run unhooked: `nova-hook hook`, the link reader before proof (which carries control events only: the challenge and its refusals, never a message), `nova-hook prove`, `nova-hook away`, help and version."
   Proposed new sentence: "Only the verbs that arm and prove the link run unhooked: `nova-hook hook`, the link reader before proof (`nova-hook wait`, or `nova-bus recv --as <name> --forever --exec <deliver>` on a monitor harness; it carries control events only: the challenge and its refusals, never a message, and it refuses `not_hooked` only when it has no control event to carry), `nova-hook prove`, `nova-hook away`, help and version."
   Why: Guarantee 13 makes `nova-bus recv --as <name> --forever --exec <deliver>` the link on monitor harnesses, and the Claude Code subsection says the challenge arrives on that stream. The refused list in the same guarantee names `recv` unconditionally, so a literal implementation refuses the reader before the first prove, the first challenge is never delivered, and a monitor-harness name can never hook. The parenthetical also resolves the Antigravity subsection's "if `nova-hook wait` exits with code 2 (`refusal: not_hooked`)" against the reader that must deliver the fresh challenge after `nova-hook hook`.

3. docs/spec/v1.3/50-sprint-delivery.md — a deal must not read the displayed status, which includes deaf.
   Old sentence: "A card is dealt only to a friend who is hooked into her psychic link and has passed the self-test of layer 30 guarantee 6, whose binding is proven and unexpired, and whose displayed status is up; never to a deaf or down friend (layer 20 guarantees 12 and 17)."
   Proposed new sentence: "A card is dealt only to a friend who is hooked into her psychic link and has passed the self-test of layer 30 guarantee 6, whose binding is proven and unexpired and whose internal state is up; never to a deaf or down friend (layer 20 guarantees 12 and 17)."
   Why: Layer 20 guarantee 17 defines displayed `up` as `up` for `up` and `deaf`, and says "a deaf friend receives no push and no new send". The clause "whose displayed status is up" therefore admits exactly the deaf friend the same sentence forbids, and the observation (one hooked, one deaf, one down) would deal to the deaf friend. The gate must name the internal state.

4. docs/spec/v1.3/60-state-machines.md — M6's deal action repeats the displayed-up error.
   Old sentence: "Deal only to a friend who is hooked, self-tested, proven and displayed up (50 guarantee 7; Reconciled (a) for which clock the deal reads)."
   Proposed new sentence: "Deal only to a friend who is hooked, self-tested, proven and internally up (50 guarantee 7; Reconciled (a) for which clock the deal reads)."
   Why: A model built from "displayed up" deals to a deaf friend, contradicting M1's own invariant "Deaf displays up and is never hooked: no push and no new send reaches a deaf or down friend (20 guarantees 16 and 17)". M6 must read the same internal state as change 3.

5. docs/spec/v1.3/30-harness-hookin.md — the Claude Code re-hook step still assumes round 1's two Monitors.
   Old sentence: "Re-arm both Monitors before they expire."
   Proposed new sentence: "Re-arm the Monitor before it expires."
   Why: Round 2's fix changed this subsection to "One Monitor is the link" and "no second Monitor on `nova-hook hook` is needed or kept", but the re-hook step still says "both Monitors". A reader cannot tell whether one Monitor or two must be re-armed, which is exactly the round 1 fix being contradicted.

6. docs/spec/v1.3/30-harness-hookin.md — the Muse Code link cannot be "unmeasured" while the same subsection hooks, proves and drains.
   Old sentence: "Delivery is active-session delivery through layer 20, in this order; on this installation the link is unmeasured, no end-to-end prove transcript is recorded, and the sender is told delivery waits for the next turn boundary until that test passes."
   Proposed new sentence: "Delivery is active-session delivery through layer 20, in this order; the foreground link is the measured active-session path, while idle wake is unmeasured, no end-to-end idle-wake transcript is recorded, and the sender is told delivery waits for the next turn boundary until that test passes."
   Why: Layer 30 guarantee 7 says that until the pushed-in-turn test passes "the capability record says the link is unmeasured", and guarantee 6 says the gate opens only when a self-test shows the push landing in the live session, with every bus verb refusing `not_hooked` until then. An unmeasured link therefore forbids the very `nova-hook prove` and `nova-bus recv` the subsection describes. The Offers point of the same subsection already observes active-session delivery, so the foreground link is the measured path and only idle wake is unproven.

7. docs/spec/v1.3/10-server-truth.md — "push delivery and claim acquisition read `last_proof` and the lease deadline alone" drops the mandatory-link and self-test gates.
   Old sentence: "Proof, push delivery and claim acquisition read `last_proof` and the lease deadline alone; a displayed up or down is a projection of both clocks that the layers above define, and it never feeds back into proof."
   Proposed new sentence: "Proof reads `last_proof` and the lease deadline alone; push delivery and claim acquisition also read the name's hooked state (guarantee 14), and a displayed up or down is a projection of both clocks that the layers above define and never feeds back into proof."
   Why: Layer 20 guarantees 12 to 14 define `hooked` as a proof answered through the link, guarantee 16 refuses a send to any unhooked recipient, and layer 30 guarantee 6 adds the self-test before a take. A server built from layer 10's "alone" would gate push and claim on `last_proof` and the deadline only and would miss the round 2 conditions that make the link mandatory. The same "alone" wording in the presence paragraph above the state table should be read with this change.

8. docs/spec/v1.3/40-friend-lane-client.md — a late finish must be attributed to the author, not to the claim's current holder.
   Old sentence: "A finish that arrives after its claim was released or reassigned is recorded as a late finish under that same friend and held for reconciliation; it is never refused without a record, and it merges nothing on its own."
   Proposed new sentence: "A finish that arrives after its claim was released or reassigned is recorded as a late finish under the friend who held the claim when it was released or reassigned and held for reconciliation; it is never refused without a record, and it merges nothing on its own."
   Why: "That same friend" points back to "the friend the server shows holding the claim", which after reassignment is the new holder, while the guarantee's title ("stays its author's") and observation ("from the old holder: one late-finish record exists under her name") put the record under the author. The two readings attribute the finish to different friends, which is the exact 92/21/8 refusal the guarantee exists to remove.

9. docs/spec/v1.3/60-state-machines.md — M5's LateFinish repeats the ambiguous holder.
   Old sentence: "LateFinish: a finish on a released or reassigned claim is recorded under the server's holder (40 guarantee 11)."
   Proposed new sentence: "LateFinish: a finish on a released or reassigned claim is recorded under the friend who held the claim when it was released or reassigned (40 guarantee 11)."
   Why: After reassignment "the server's holder" is the new holder, not the author whose report must stay recorded. The model would put the late finish under the wrong friend and must match change 8.

10. docs/spec/v1.3/20-comms.md — `nova-hook hook`'s signature omits `--harness`, which the recipe and every refusal remedy need.
   Old sentence: "1. `nova-hook hook --as <friend> [--role <r>] [--json]`:"
   Proposed new sentence: "1. `nova-hook hook --as <friend> --harness <h> [--role <r>] [--json]`:"
   Why: The same entry says the verb "prints the link recipe for the harness (`--harness <h>`, layer 30)", and layer 30 guarantee 7 requires `nova-hook hook --as <name> --harness <h>` and makes that per-harness recipe the remedy every `not_hooked` refusal names. Without `--harness` in the signature the server cannot know which recipe to print or refuse with.

11. docs/spec/v1.3/20-comms.md — `nova-bus recv`'s interface omits the `--forever` and `--exec` flags that implement the link.
   Old sentence: "6. `nova-bus recv --as <friend> [--all] [--ack] [--json]`:"
   Proposed new sentence: "6. `nova-bus recv --as <friend> [--all] [--ack] [--forever --exec <deliver>] [--json]`:"
   Why: Guarantee 13 and the Claude Code subsection run `nova-bus recv --as <name> --forever --exec <deliver>` as the link on monitor harnesses, but the interface list specifies no such flags, so the one command the link runs is undefined.

12. docs/spec/v1.3/20-comms.md — guarantee 5 shows `--nonce` as required while the reader starts without one.
   Old sentence: "5. **Zero-polling reactive wake**: `nova-hook wait --as <friend> --nonce <nonce> [--timeout <dur>]` blocks synchronously on the server notification channel."
   Proposed new sentence: "5. **Zero-polling reactive wake**: `nova-hook wait --as <friend> [--nonce <nonce>] [--timeout <dur>]` blocks synchronously on the server notification channel."
   Why: Interface 4 lists `[--nonce <n>]`, and the Grok subsection starts the wait with no nonce and reads the nonce from its output. With `--nonce` shown required, the reader cannot start before proof and the first challenge cannot arrive. The nonce is an optional piggyback on the wait, not a precondition.
