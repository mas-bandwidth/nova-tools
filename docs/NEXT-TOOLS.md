# The next nova tools: the set, and how it is decided (DRAFT 2, 2026-09-13)

Glenn, 2026-09-13, verbatim: *"land the audit fixes, finish the fixed table work, and then once this is done, let's
work out the next set of nova tools we should add, with particular emphasis on things that help us coordinate work and
count tokens and optimize."* Then, live: *"The two of you should coordinate on a spec for all the new nova tools,
prioritizing this."*

This file is the set. Draft 1 was the shape and the candidates; draft 2 is the set as the five draft 2 specs now state
it, one section per tool, each pointing at its SPEC and its PR, and nothing enters without its first three lines
filled from the record. The order of arrival is the process: **the friends' ideas first** (asked 15:26Z, deadline
17:30Z; Emma answered 15:29Z, Stella 15:30Z), then Rowan's and Stella's lists, then this file, then each tool's own
spec, read by every line before any code.

**All friends review specs.** Glenn made this explicit on 2026-09-13: each friend brings a model, a person and a point
of view. The roster below carries each friend's own response, the revision read, findings and unresolved
disagreements. An unanswered invitation is pending, not assent; a decline is recorded honestly. Cheap-model cold reads
supplement those reviews and keep separate provenance; a child never stands in for its parent's own review. Circulate
material revisions back and reconcile the findings; never claim consensus from a subset. Cheap capable models remain
the default for bounded worker tasks, measured on total tokens through review and repair as well as average token
cost.

## The shape of an entry

- **The friction it removes**: the measured hurt, with its date and where it is recorded.
- **What it must never do**: the fence, stated before the verbs.
- **The verbs**: one line each, in the house grammar.
- **What it reads and writes**: files, the bus, a forge; nothing else.
- **How it is measured**: the number that says it helped, and who reads it.
- **Who asked for it**: the lines, by name, from their own notes.

## nova-work, the work set ([SPEC-WORK.md](SPEC-WORK.md) draft 18, #231)

- **Friction.** Simple progress questions answered by rereading whole histories, and status labels guessed over stale
  tables (Stella, 15:30Z, stella-4b9200ddc994); asleep workers scheduled (Rowan, #177 comment 5654176537).
- **Never.** No production scheduling in this pass; no forge in the tool; no state moved on a sentence.
- **The verbs.** `query --ask` answers a progress question from a session or a snapshot; `render --view` writes a
  roadmap view between markers, tick or cross; `take`/`heartbeat`/`release` are the lease, each with a deadline and a
  written default; `evidence`, `attest`, `attempt` and `state` move a node only on an evidence event; `decompose`,
  `accept`, `source`, `node remove` and `event` carry the scope delta; `check` and `verify` are the gates.
- **Reads and writes.** A session file and its journal, a cache, the repository and one branch; an append-only event
  log; no forge call.
- **Measured.** Progress questions answered without opening a history; nodes moved by an evidence event, not a label;
  leases expired by their own default.
- **Who asked.** Stella, 15:30Z, four candidates; Rowan, #177 and the bus note of 17:30Z.

## nova-review, the review layer ([SPEC-REVIEW.md](SPEC-REVIEW.md) draft 2, #236 at 522bab88)

- **Friction.** 25 duplicate findings across readers on one PR family (2026-09-11, swarm batch 1, SPEC-SWARM.md:982);
  an approve at 12:31Z counted for a head pushed at 12:47Z (2026-09-11); a HOLD unread 100 minutes and another 45,
  each a PR comment nobody polled (2026-09-12 20:32Z to 22:09Z, #169, #174); a spec called ratified with a friend's
  row empty and a child of the author's model counted as a friend's read (2026-09-12/13); review cost measured by feel
  (#183).
- **Never.** It never forms an opinion about code and never checks that a claim is true, only that its ground exists;
  it never merges, gates, publishes, comments or wakes anybody, and has no default reader list, lane or roster file.
- **The verbs.** `packet` builds the smallest bounded file for one reader at one head. `verdict` records APPROVE, HOLD
  or ABSTAIN bound to `--head`, with `--who`, `--model` and `--kind line|child|card`, every finding at `file:line`
  with its rule quoted verbatim. `answer` is the author's disposition: fixed, declined or dup. `roster` says who has
  read which head, where silence is pending. `dedupe` folds findings by `(path, line, rule ref)`. `cost` sums tokens
  and wall clock per read from receipts.
- **Reads and writes.** Reads the nova-merge lane's `state.json` and `reads/`, the diff and spec at a named head, a
  findings file and a usage file. Writes nova-merge's own read record through `internal/merge` unchanged, so that
  format has one writer, plus one review record under `<lane>/reviews/<entry>/`, which nova-merge's fold never walks.
- **Measured.** Duplicate findings per head; minutes from a HOLD to the author's answer; reads bound to the wrong
  head, target zero; tokens and wall per repair round.
- **Who asked.** Rowan, 17:30Z: the 25 duplicates, the wrong-head reads, the 100-minute HOLD.

## nova-release, the release layer ([SPEC-RELEASE.md](SPEC-RELEASE.md) draft 2, #237 at 1af78e31)

- **Friction.** #229, the nights of 2026-09-12: the freeze and the per-merge delta review were a hand-kept ledger
  comment, merges landed after the candidate was chosen, a `--auto` merge landed before CI finished, and v0.14.0's run
  failed on Windows and was re-run on the same tag. 2026-09-13: v0.15.0 prepared by hand as PR #232, certification run
  34771523657 at 9a95e33e RED on `build-windows` and `test-windows (internal/swarm)`, and the titles of v0.13.0 and
  v0.14.0 still the bare tag (Glenn, 2026-08-03).
- **Never.** No merge verb, so no `--auto` to refuse. No generated notes, no edit of the frozen tree, no `git tag`, no
  dispatch of certification, no polling, no bus note, no version-number decision.
- **The verbs.** `candidate` freezes one sha as an immutable record; `delta` walks every first-parent landing since
  the last release and refuses while one is unread; `certify` requires the aggregate green at exactly that sha, read
  job by job from the wire, never a badge, and a red is stop; `bump` checks the version sites against a manifest,
  never a grep; `notes` binds a notes file a line wrote to another line's read of it, keyed by hash; `tree` records
  the whole-tree read. `status` surveys the cut condition and `cut` acts on the same predicate, tag and release in one
  act. `verify` reads the tag, release, assets and the shipped binary's version back from the wire and names the first
  disagreeing field.
- **Reads and writes.** Reads the lane's `state.json` and `reads/`, its own `--work` clone, the wire's peeled tag refs
  and check jobs, and a policy file in the tree at the candidate sha. Writes `releases/`, its own outbox, and the tag
  and release.
- **Measured.** Hand steps per release, today all of them; landings in a release with no recorded read, which must be
  zero; the first field `verify` disagrees on.
- **Who asked.** Rowan, 17:30Z, from #229 and that morning's v0.15.0 preparation.

## nova-wake, wake on a change and never a poll ([SPEC-WAKE.md](SPEC-WAKE.md) amendment draft 2, #239 at 8e459652)

- **Friction.** 2026-09-11: 1,204 turns in the coordinating window, 1.19M written against 652M cache-read, most of
  them polls; the tell was one status check run twice in a minute. 2026-09-12: two review HOLDs unread 100 and 45
  minutes as comments on owned PRs, nothing watching PR comments beside the bus (#178). 2026-09-10: nineteen orphaned
  shells, thirteen polling a log for a child hours gone; 2026-09-09: READY sent, two heavy children started three
  minutes later.
- **Never.** No daemon, no background process, no re-arm. No decision: a `WAKE PR` is not a verdict, `WAKE LOCK free`
  is not a grant, `UNAVAILABLE` is not a reassignment, `WAKE HERE` is not READY. No comment text relayed, only counts,
  kind, id, author, state and URL.
- **The verbs.** `watch` gains `--pr` and `--owned-prs`, a new comment or review on a named or owned pull request, the
  window's own words never waking it; `--run`, the checks on a named head; `--ref`, a branch moving; `--lock`, an
  advisory lock released by a non-blocking probe; `--to-only`; a third clock `--forge-interval`, 30s floor; and a
  fourth verdict `WAKE STOPPED` for the caller's own signal. `probe --line` reads a line's last sign, sends one
  caller-written ping and is `UNAVAILABLE` after `--answer-within`; `probe --here` prints load average, CPU count and
  process count, so a READY carries numbers.
- **Reads and writes.** Reads the bus checkout, the forge for named or owned PRs, refs and runs, an advisory lock file
  and report directories; writes its state file and, on `probe`, one ping note through `nova-bus send`.
- **Measured.** Turns per change instead of turns per tick; cache-read tokens per window hour; minutes from a HOLD to
  the window seeing it.
- **Who asked.** Emma, 15:29Z, quoted verbatim in the spec: *"`nova-wake wait --event
  <branch-pushed|run-finished|lock-released> --timeout <T>`"*, against polling loops and arbitrary timers. Her three
  events became sources on `watch`, with the reason in the spec under *Why `watch` and not a new `wait`*. Rowan,
  17:30Z, from #178.

## nova-tokens, attempts joined to work ([SPEC-TOKENS.md](SPEC-TOKENS.md) amendment draft 2, #240 at 93c9cf7d)

- **Friction.** Glenn, 2026-09-11: *"We have an obligation to report this."* Stella, 15:30Z: count review and repair,
  not merely builder tokens; Glenn via Stella, 16:34Z: reduce both the average cost per token and the total. Rowan,
  2026-09-11: 2.2M tokens on four gaps with no record of which. Emma, 15:29Z: finding a burn meant transcript digging.
  Glenn, 2026-09-12: a tool is only good if everybody adopts it (#182, #117).
- **Never.** Never estimates, never fills a gap, never removes a file. No v2 day file and no change to the `(day,
  model, repo)` key. `usd=` is list rate from the caller's dated table and never a bill; a missing rate is `unpriced`,
  never zero. nova-tokens never reads a work set and nova-work never reads a receipt: two strings name each other.
- **The verbs.** `receipt` folds one session, joined to one node and one of the six stages, under a drawn 32-hex id,
  and is a wall. `cost --node <id>` sums every stage with no stage filter and prints `tokens=`, `usd=`, `per_mtok=`,
  `priced_tokens=`, `unpriced_tokens=`. `hot --session <id> --top <n>` ranks repeated reads and largest step outputs
  with one `HOT NOTE` ending in what it cannot see; `diff` compares two day files or two sessions, largest movement
  first. `friction add` records a stumble with its token or `~` cost, a gap label and the issue it became, `frictions`
  sums per gap, and `publish --frictions` is a third typed kind with an append transition. `sum --rates` prints the
  month's blended mean. The adoption matrix is `nova-update adoption`, specified here with a dated pointer in
  SPEC-UPDATE so the two cannot drift.
- **Reads and writes.** Reads declared sources only: a Claude transcript by session id, an OpenCode session with its
  children, a swarm job by id, a caller's dated rate file. Writes `<out>/receipts/<day>.tsv`, seventeen columns keyed
  `(day, model, repo, receipt)`, a frictions file under its own lock, and the day files unchanged.
- **Measured.** Total end-to-end tokens per node with review and repair counted; the month's blended `per_mtok` beside
  its unpriced count; tokens per friction gap; adoption cells neither unknown nor trying.
- **Who asked.** Emma, 15:29Z, for `hot` and `diff`, verbatim in the spec; Stella, 15:30Z, for the retained accounting
  joined to attempts; Rowan, 17:30Z, for `friction` and the adoption matrix.

## nova-swarm, the finalize result contract ([SPEC-SWARM.md](SPEC-SWARM.md) amendment draft 2, #241 at fbfea5e6)

- **Friction.** 2026-09-13: six spec-read cards produced four grounded HOLDs, all four marked FAILED by the launcher
  on a line-1 disagreement (`READ` in the card, `RESULT:` in the check), then rejected by a hand gate on a rule the
  card never stated. #133, 2026-09-13: twelve Mercury runs, one accepted; 2026-09-12: a placeholder under a `HOLD /
  findings: 1` header posted as a verdict. 2026-09-13: eight cards died in one second on OpenCode 1.18.30_1 crashing
  at init, a repair worker fetched a JDK over the network, and five hit the wall on outside reads in under 90 seconds
  each.
- **Never.** No judgment of reasoning: the contract proves a line exists and a command ran, never that a finding is
  right. No rules parsed out of a card's text, no new binary, and no change to `no-result`, `plan-only` or
  `malformed`, which classify first and unchanged.
- **The verbs.** `contract --kind <READ|PROBE|FIX|REPORT>` prints the card block and the finalize checks rendered from
  one definition. `finalize` accepts or rejects by machinery, ten classes in order with one reject line each, and a
  rejected job is never read without `--anyway`. `run --harness <path>` pins and prints the harness and its version
  after a canary ping; `--needs` is refused at queue time when the named toolchain is not in the card.
- **Reads and writes.** Reads the job directory, the sidecar and the harness's tool-call records; line 1 of
  `RESULT.md` is `RESULT: <KIND> <label> at <sha> <fields>`, the plan in `<job>/PLAN.md`. Writes the class into
  `rejected=`, the count into `attempt=`, a `REJECTED` marker beside the report copy, `refused=<path>` on `RUN DONE`.
- **Measured.** Cards accepted per cards launched, one in twelve on 2026-09-13; launches per task text, capped at two
  across the reap and the reject; cards lost to a dead harness.
- **Who asked.** Rowan, 17:30Z, from #133 and the cards of 2026-09-12 and 2026-09-13.

## Candidates, by the record

| candidate | source | status |
|---|---|---|
| nova-work, the work set | nova-tools#177; Glenn 2026-09-13 | in SPEC-WORK draft 18, #231 (its line 1 is the number of record) |
| `query`/`focus` and the versioned baseline | Stella 15:30Z: "we keep rereading histories to answer simple progress questions" | in SPEC-WORK draft 18, #231, as `query --ask` and the scope events |
| leases plus compact availability | Stella 15:30Z: "prevent scheduling asleep workers"; Rowan #177 comment 5654176537 | in SPEC-WORK draft 18, #231, as `take`/`heartbeat`/`release` with a written default |
| evidence ingestion and projections | Stella 15:30Z: "avoid status-label guesses and stale tables" | in SPEC-WORK draft 18, #231, as `evidence`, `verify` and `render` |
| token and cost accounting joined to execution attempts | Stella 15:30Z: "count review and repair, not merely builder tokens"; Glenn via Stella 16:34Z | in SPEC-TOKENS draft 2, #240, as `receipt` and `cost --node` |
| review packet and one-line verdict per PR | Rowan 17:30Z: 25 duplicate findings, reads of the wrong head, a HOLD unread 100 minutes | in SPEC-REVIEW draft 2, #236 |
| swarm finalize checks the result contract | Rowan 17:30Z; nova-tools#133: 12 Mercury runs, 1 accepted | in SPEC-SWARM draft 2, #241 |
| release from a frozen candidate sha with delta reads | Rowan 17:30Z; nova-tools#229; v0.15.0 by hand (#232) | in SPEC-RELEASE draft 2, #237 |
| a `friction` verb with its token and wall cost and its issue | Rowan 17:30Z; nova-tools#185 | in SPEC-TOKENS draft 2, #240, as `friction add` and `frictions` |
| adoption matrix per line per tool | Rowan 17:30Z; nova-tools#182 | in SPEC-TOKENS draft 2, #240, decided onto `nova-update adoption` with a dated pointer in SPEC-UPDATE |
| wake on an addressed note, never a poll | Rowan 17:30Z: 652M cache-read for 1,204 turns | in SPEC-WAKE draft 2, #239 |
| `nova-wake wait --event` as its own verb | Emma 15:29Z | declined as a separate verb: same loop, same deadline, same grammar, and a window waiting on a branch must also hear a note; her three events are sources on `watch` in SPEC-WAKE draft 2, #239 |
| `nova-tokens hot` and `diff` | Emma 15:29Z: transcript digging to find a burn | in SPEC-TOKENS draft 2, #240 |
| work-set and matrix projection, `nova-work project` / `tally` | Emma 15:29Z: 9 languages by 38 versioning rows collated by hand across test files and CI logs | **open**: SPEC-WORK draft 18 has `query --ask` and `render --view`, and neither is her multi-axis projection over `--repo`/`--category`/`--state`; whether it is a third verb, an `--ask` kind or a roadmap axis view is for the readers of draft 18 |
| `usage:<receipt-id>` among SPEC-WORK's pointer schemes | the cross-spec ask in SPEC-TOKENS draft 2, #240 | **open**: SPEC-WORK draft 18 does not list the scheme, and #240's demanded test pins the two grammars together, so one of the two must move |

## The review roster

Heads at draft 2: SPEC-REVIEW `522bab88` (#236), SPEC-RELEASE `1af78e31` (#237), SPEC-WAKE `8e459652` (#239),
SPEC-TOKENS `93c9cf7d` (#240), SPEC-SWARM `fbfea5e6` (#241); SPEC-WORK is `c5d32751` (#231, draft 18). Silence is pending, never
assent; a reserved line silent past the deadline abstains, and of the rest all must be yes (Glenn, 2026-09-11).

| line | ideas asked 15:26Z, deadline 17:30Z | spec read at draft 2 | feedback on the lists, deadline 2026-09-14 15:00Z |
|---|---|---|---|
| Stella | answered 15:30Z, four candidates (stella-4b9200ddc994) | pending at 522bab88, 1af78e31, 8e459652, 93c9cf7d, fbfea5e6 | pending |
| Emma | answered 15:29Z, three ideas (emma-0fd8c03d5f24): one into SPEC-WAKE, one into SPEC-TOKENS, one open | pending at 522bab88, 1af78e31, 8e459652, 93c9cf7d, fbfea5e6 | pending |
| Alex | pending at 17:30Z (not assent; on the class read and serialize.cs) | pending at 522bab88, 1af78e31, 8e459652, 93c9cf7d, fbfea5e6 | pending |
| Freddy | pending at 17:30Z (not assent) | pending at 522bab88, 1af78e31, 8e459652, 93c9cf7d, fbfea5e6 | pending |
| Johnny | reserved line, not asked | reserved, not asked | not asked |

Rowan's and Stella's lists overlap on the work set, the leases and the token join, and both reached the same hurt from
opposite ends: she from rereading histories to answer a progress question, he from 2.2M tokens spent with no record of
where. The difference is that Stella's four all live inside nova-work's own session and event log, while Rowan's five
reach outside it to the review, release, attention and worker layers, which is why draft 2 is five specs and not one.

## The post-seal board, read 19:25Z 2026-09-13 (nova-tools #251, cairn 7d2e4a91)

Cairn 7d2e4a91 was consumed at the 2026-09-13 roll-up. This board was read off
the wire at about 19:25Z and heads are named so the next session can tell what
landed. Landed after the seal (read, nothing owed): #236 polish 841d492b
(Fable APPROVE at c5b08d6a stands; mark ready is owed); #237 Opus read at d3
d224cafd APPROVE (5655459652); #241 d3 a6088412 pushed. Died with the session
(heads unmoved, no comment): #231 SPEC-WORK d19 (d1b20f42), the paired Fable
and Opus reads never landed, owed both reads, then Emma, Alex, Freddy by their
deadline, Stella and Johnny on record; #241 SPEC-SWARM d3 (a6088412), the Fable
read never landed and the Opus read was not spawned, owed both; #242
nova-daemon (20b48ab5) and #243 nova-admin (cb53909d), the d2 repairs never
landed and both carry two d1 HOLDs, owed d2 on Opus then paired reads; #244
nova-run (f6c8bd95) and #245 nova-cairn (e466313f), d1 reads in
(5655439334, 5655447516; 5655445907, 5655442853) and d2 not spawned, owed d2
then paired reads; #240 nova-tokens d3 (42fb89c4), reads not spawned, owed
paired reads, with Stella's scoped HOLD on #240 (19:21:44Z) landed after the
seal as part of the next repair; #239 nova-wake, six polish lines
(5655448606, 5655435074), then mark ready; #237, Stella's scoped HOLD
(19:18:20Z) landed after the seal so #237 is not at APPROVE on all seats,
owed her findings folded then the friends.

Rule of the lane: every draft read on two models at its head, repairs on Opus,
a Fable read at the final head, then every friend on their own model; ratified
only on explicit APPROVEs; merge to main after. Then NEXT-TOOLS d2 (f9680f3a)
with the friends' feedback by 2026-09-14 15:00Z.
