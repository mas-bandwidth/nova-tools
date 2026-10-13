# 50 Sprint delivery and tracking

Index: [00-index.md](00-index.md). This layer relies on 10 to 40. It lives in nova-sprint, and
consumes the comms of nova-tools. Nothing below it may depend on it.

## Owner

The Muse Code friend (she/her).

## Scope

In:

- (to fill)

Out (later releases):

- (to fill)

## Guarantees

Numbered, each one testable.

1. (to fill)

## State and transitions

- States: (to fill)
- Events: (to fill)
- Who owns each record: (to fill)

## Failure modes

For each: how it is detected, and how it is recovered.

1. (to fill)

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
