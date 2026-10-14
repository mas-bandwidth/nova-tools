# 50 Sprint delivery and tracking

Index: [00-index.md](00-index.md). This layer relies on 10 to 40. It lives in nova-sprint, and
consumes the comms of nova-tools. Nothing below it may depend on it.

## Owner

The Muse Code friend (she/her).

## Scope

In:

- One card, one delivery branch: the branch the deal names, `sprint/<card>.g<generation>.e<epoch>`, cut from the base the deal names.
  A card branch names that base at cut time and is judged against it.
- The merge gate, in order: the Linux bench gate on the exact head, then a cold reader's
  verdict, then the coordinator's merge. No step substitutes for another.
- Merge candidates are sprint branches only. A card is a candidate when the delivery branch its deal
  names is pushed and its head is the sha that was gated and read.

Out (later releases):

- The verdict carry across a base merge beyond the clean case. v1.3 carries a recorded MERGE
  only when the base merge is clean and the card's own diff digest is unchanged (guarantee 1
  and machine M7 in layer 60). A carry where the base merge touches the card's own files, or
  where the diff digest changes, is not tested by this layer in v1.3 and goes to a later release.
- Any sprint comms path beside the psychic link. The tick's take-back of taken cards to redeal
  them, daemon polls of the sprint, ping/pong session checks, wake files and the per-minute
  goal nudge as a delivery path are retired (layer 20, Layers 40 and 50 sit on the link).
- Automatic merging. The coordinator merges by hand in v1.3; no merge automation is specified.
- Verdicts on non-sprint branches, and candidates registered for unclaimed cards beyond the
  refusal in the Inputs below, until the server record for claims (layer 10) is ratified with
  that refusal as a guarantee.

## Guarantees

Numbered, each one testable.

1. **Exact head, one fence.** The Linux bench gate runs on the exact head sha that will be
   merged, and the gate, the cold read and the merge all name that same sha. Any move of the
   head — a new commit, a rebase, a base merge that changes the card's own files — invalidates
   the gate and the verdict, and both run again on the new head. There is one fence: nothing
   crosses to merge on a sha that was not itself gated and read.
2. **A verdict is about the diff it names.** A cold reader's MERGE quotes at least one changed
   hunk from the candidate's diff verbatim, and the merge step checks that every quoted path is
   in that diff at the gated sha. A verdict that quotes source not in the diff, or files that do
   not exist at that sha, is not a verdict on this candidate and cannot merge it.
3. **Green means the gated build.** The cold reader's MERGE stands beside a green build of the
   exact head on the Linux bench: the same sha that was read is the sha that built green. A
   green build of any other sha, and a correct sha copied into prose, are not proof of a read
   or of a build.
4. **Race is checked, not assumed.** Before the merge, the head is checked against its base
   once, at the fence: a head that is behind the base its deal names, or whose race with the
   base changed the card's own files, returns to the author instead of merging. The check names
   both shas it compared.
5. **Only the coordinator merges.** Authors push candidates; the cold reader records MERGE,
   FIX or HOLD on the head sha; the coordinator alone performs the merge, and only on a
   candidate sprint branch whose gate and verdict both name the current head. No later note
   restates a recorded verdict as a different word; a moved tip is one note naming the new sha,
   and the gate and read run again under guarantee 1.
6. **One claim per card; no take-back.** One card holds at most one active or
   needs-reconciliation claim. The server refuses a second deal of a card under such a claim,
   and the card returns to the deal only on a finish, a release, or a completed reconciliation
   (layer 40 guarantees 4, 9 and 11), with every release and its reason recorded on the claim
   and replayed to its holder on attach. No tick moves a taken card back to ready. Observation:
   with a fake clock, run the tick a thousand times over a taken card whose holder is deaf, then
   down: the card is dealt once, its claim reads active then needs-reconciliation, and the count
   of working-to-ready moves is zero.
7. **Deal only to a hooked friend.** A card is dealt only to a friend who is hooked into her
   psychic link and has passed the self-test of layer 30 guarantee 6, whose binding is proven
   and unexpired, and whose displayed status is up; never to a deaf or down friend (layer 20
   guarantees 12 and 17). Observation: unit test: with one friend hooked, one deaf and one
   down, a deal of three cards lands only on the hooked friend, and the rest stay with the
   server.
8. **Every sprint message rides the link.** Every deal, take, finish, read, judgment and push
   between the sprint, a friend and the coordinator is a message on the psychic link of layer 20,
   admitted only to hooked recipients (guarantee 16 there); the sprint is a client of Redis,
   the one server, and keeps no lease, roster or delivery copy of its own (layer 10 guarantees
   14 and 15). A sprint message to an unhooked friend is refused to the sprint at once and
   raised as a judgment to the coordinator, never queued. Observation: with nova-sprint
   running against a fake store, every judgment and deal it emits appears as a bus message to a
   hooked recipient, and one addressed to an unhooked friend produces a `friend_not_hooked`
   refusal and a coordinator judgment.

Rationale (round 2): in the three hours before 14:24Z on 2026-10-14 the sprint moved friend
cards from working back to ready by the tick deal 12,709 times, and finished friend reports
were refused as no longer the friend's (the Grok friend 92, the Antigravity friend 21, a
one-shot dsh friend 8). Guarantees 6 to 8 make the take-back and the unheard deal impossible.

## State and transitions

- States: card dealt → taken (one claim; needs-reconciliation keeps it) → candidate pushed (the deal's branch at a named head) →
  bench-gated (exact sha, green) → cold-read (MERGE, FIX or HOLD on that sha) → merged by the
  coordinator. FIX and HOLD return the card to its author; a moved head returns the candidate
  to bench-gated.
- Events: branch cut from the base the deal names, push, head move, bench gate result, cold
  reader verdict, base race check, coordinator merge.
- Who owns each record: the server record owns card state and the recorded verdict on its sha;
  the bench owns the gate result for the sha it ran; the coordinator owns the merge. The
  author's local checkout owns none of these.

## Failure modes

For each: how it is detected, and how it is recovered.

1. **Stale verdict.** The head moved after the cold read. Detected by comparing the verdict's
   sha to the branch's current head at the fence. Recovered by re-gating and re-reading the new
   head; the old verdict stays recorded against its own sha and merges nothing.
2. **Verdict about the wrong source.** A quoted hunk or path is absent from the candidate's
   diff at the gated sha. Detected by the merge step's quote check (guarantee 2). Recovered by
   returning the candidate for a fresh cold read of the actual diff; the merge does not proceed
   on the strength of the author's summary.
3. **Base race.** The base the deal names moved and the head is behind, or the catch-up changed
   the card's own files. Detected by the race check at the fence, naming both shas. Recovered by
   the author merging or rebasing the base, then re-gating and re-reading the new head.
4. **Non-candidate branch.** Work arrives on a branch that is not the delivery branch the
   card's deal names. Detected at registration: only sprint branches are candidates. Recovered by moving
   the work onto the card's sprint branch and gating that head; the other branch merges nothing.

## Interfaces

- Below: the friend lane (40), comms and the psychic link (20), and server records on Redis,
  the one server (10). Every sprint message to or from a friend or the coordinator rides the
  link (guarantee 8); this layer has no delivery path of its own.
- Above: the coordinator, the dashboard and the friends' own view of their cards.

## Open questions

Each question carries its default for v1.3; the default holds unless a ratification objection
changes it.

1. Does a recorded MERGE carry across a clean base merge when the card's own diff digest is
   unchanged? Default for v1.3: yes, only in that clean case, and the fence re-checks the
   digest at merge time (machine M7 in layer 60). Every other carry is out, above.
2. Who may register a candidate for a card? Default for v1.3: only the friend the server shows
   holding the claim for that card; a registration for an unclaimed or another friend's card is
   refused at registration, which also prevents duplicate candidates.
3. What binds a verdict: prose naming a sha, or a structured record? Default for v1.3: a
   structured record bound to repo, head, base and diff digest, with at least one verbatim
   quoted hunk checked at the fence (guarantees 2 and 5). Prose alone merges nothing.
4. Is the behind-base refusal checked before a MERGE is recorded, at the fence, or both?
   Default for v1.3: both. The verdict verb refuses a head behind its base (not counting the
   merge commit whose parent is that head), and the fence checks again, naming both shas
   (guarantee 4).

## Inputs

Design points for the owner to weigh. The decisions are the owner's.

- What friends do in the sprint: take, work, finish and report cards; lanes and width; reads by
  friends; bounce-back, and judgments on friend work; friend rows and config; and a dashboard that
  tells the truth about friends.
- A verdict about source that was not in the PR: a cold read cleared files that did not exist at the
  named sha, and only an unrelated "behind" check stopped the merge. Nothing checks that a verdict is
  about the PR it names. Candidate: a verdict quotes at least one changed hunk, and the merge step
  checks that the quoted paths are in the PR's diff.
- The Grok friend: a verdict is a field on a head sha (MERGE, FIX or HOLD), and no later note can restate it
  as a different word. Before a MERGE is sent, refuse it when the head is behind its base, without
  counting the merge commit whose parent is that head. A tip move is one note that names the new sha.
- The Codex friend: verdicts are structured records bound to repo, head sha, base sha and a diff digest, with
  source anchors. Keep source review, Linux execution, hosted CI and merge as distinct steps. A
  correct sha copied into prose is not proof of a read.
- The Antigravity friend: a verdict verb that checks the head is a full sha matching the PR head, that the quote is
  verbatim in the diff, and that the head is not behind. It records the verdict and notifies both
  the author and the coordinator.
- The Antigravity friend: refuse to register a PR for an unclaimed card, to prevent duplicate PRs. Carry a recorded
  MERGE across a clean base merge when the PR's own files are unchanged.
- The Codex friend: one server-visible owner per card, with its dependency head and acceptance gate, so
  assignments recover after a session change.
- Friends were deaf while an ownership change was sent to them. Candidate: a send that changes
  ownership or assignment to a friend who is not reachable raises a judgment to the coordinator.
- Fix priority is transient. It means a card was worked and broke in this sprint, and it ends with
  the sprint.
- Dealt is not taken: a dealt card belongs to the machine until a lane takes it. One card holds at most one active or needs-reconciliation claim; the server refuses a second deal of a card under such a claim, and the card returns to the deal only on a finish, a release, or a completed reconciliation, with every release and its reason recorded on the claim and replayed to its holder on attach.
