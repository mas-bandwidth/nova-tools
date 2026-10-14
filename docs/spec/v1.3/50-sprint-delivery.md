# 50 Sprint delivery and tracking

Index: [00-index.md](00-index.md). This layer relies on 10 to 40. It lives in nova-sprint, and
consumes the comms of nova-tools. Nothing below it may depend on it.

## Owner

The Muse Code friend (she/her).

## Scope

In:

- One card, one delivery branch: `sprint/<card>.aurora`, cut from the current sprint base.
  Sprint bases are the `sprint/v1.2.x` lines; a card branch names its base at cut time and is
  judged against that base.
- The merge gate, in order: the Linux bench gate on the exact head, then a cold reader's
  verdict, then the coordinator's merge. No step substitutes for another.
- Merge candidates are sprint branches only. A card is a candidate when its `sprint/<card>.aurora`
  branch is pushed and its head is the sha that was gated and read.

Out (later releases):

- (to fill)

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
   once, at the fence: a head that is behind its `sprint/v1.2.x` base, or whose race with the
   base changed the card's own files, returns to the author instead of merging. The check names
   both shas it compared.
5. **Only the coordinator merges.** Authors push candidates; the cold reader records MERGE,
   FIX or HOLD on the head sha; the coordinator alone performs the merge, and only on a
   candidate sprint branch whose gate and verdict both name the current head. No later note
   restates a recorded verdict as a different word; a moved tip is one note naming the new sha,
   and the gate and read run again under guarantee 1.

## State and transitions

- States: card dealt → taken → candidate pushed (`sprint/<card>.aurora` at a named head) →
  bench-gated (exact sha, green) → cold-read (MERGE, FIX or HOLD on that sha) → merged by the
  coordinator. FIX and HOLD return the card to its author; a moved head returns the candidate
  to bench-gated.
- Events: branch cut from a `sprint/v1.2.x` base, push, head move, bench gate result, cold
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
3. **Base race.** The `sprint/v1.2.x` base moved and the head is behind, or the catch-up changed
   the card's own files. Detected by the race check at the fence, naming both shas. Recovered by
   the author merging or rebasing the base, then re-gating and re-reading the new head.
4. **Non-candidate branch.** Work arrives on a branch that is not the card's `sprint/<card>.aurora`
   branch. Detected at registration: only sprint branches are candidates. Recovered by moving
   the work onto the card's sprint branch and gating that head; the other branch merges nothing.

## Interfaces

- Below: the friend lane (40), comms (20) and server records (10).
- Above: the coordinator, the dashboard and the friends' own view of their cards.

## Open questions

1. (to fill)

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
- Dealt is not taken: a dealt card belongs to the machine until a lane takes it.
