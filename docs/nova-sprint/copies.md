# Consumer copies on a bench

A stream primary (`task:<id>`) is worked through COPIES (`task:<id>~<n>`)
that `card deal` cuts onto a consumer (a bench or a friend). On a bench the
copy runs under `nova-card copy <copy>`, started by the bench's copy session
after one `card work --fill`: the copy's card is rendered from its record
(`card.RenderCopy`), the wrapper beats it with `card beat`, and the wrapper
ends it with `card end --id <copy>` (`card.CopyLedger`,
internal/nsprint/card/copy_ledger.go). No copy's model runs `card end`, pushes
or opens a PR; a sandboxed swarm model could not (GitHub is a git remote only,
the sandbox shims gh, and the boundary says the model never talks to Redis:
#4227, #4270). The model leaves what the wrapper needs and exits; its KIND
decides what that is:

- **read**: read the PR at its head against BASE@base-sha (CI at head, base,
  scope, then a score 1-10; a score under 10 names each gap; a read edits
  nothing inside PATHS) and write RESULT.md in the job dir, outside `repo/`:
  line 1 is line 1 of the card verbatim, line 2 is exactly
  `SCORE N/10 gates=ci:<green|red>,base:<ok|behind>,scope:<ok|over> finding=<one line>`
  (`card.ScoreLine`), or `ABSTAIN <why>` when it could not read.
- **fix**: `repo/` is staged at the PR's head (the card's base-sha is the
  head; its BRANCH line is the PR's branch). The model closes the read's
  finding, commits on top of that head, writes RESULT.md (line 2 DONE) and
  exits.
- **work**: the primary's own task. The model changes only PATHS, commits on
  the copy's branch `nova/copies/<label>-c<n>-a<n>` in the job's `repo/`,
  writes RESULT.md and exits.

## The read copy's end is its SCORE line (#4270)

On a DONE harness of a read copy, `CopyLedger.End` reads RESULT.md line 2:

- a well-formed SCORE line (`card.ParseScore`: N 1-10; gates, when present,
  the three named gates in that order) ends the copy ok with the score, the
  gates and the finding, the way `card end --id <copy> --score N/10 --gates
  --finding` would: the SCORE line goes on `pr:<name>:<n>` at the head, and
  the move file (`TM.finish`) moves the primary `review -> merging` on an 8+
  or cuts one fix copy under 8;
- `ABSTAIN <why>` ends the copy fail with the typed reason `abstain` and the
  why;
- no RESULT.md, no line 2, or a line that is not a SCORE line ends the copy
  fail with the reason `no-score` and the text as the why.

A passing score the move file refuses is handled by its word: `CIPENDING`
(no CI verdict at the head yet) gives the copy back (`card cancel`: it ends
fail "cancel: read N/10 held: ...", the primary stays in review with no
verdict pending, and the deal pass cuts a fresh read when CI is in);
`CIRED` ends the read under 8 with the CI failure as its finding, as the
refusal says, so the author gets a fix copy.

## The fix copy's end is its commit on the PR's branch (#4270)

On a DONE harness of a fix copy with a commit, `CopyLedger.End` runs the same
boundary step as a work copy (below) with two differences: the branch is the
PR's own head branch (the copy carries it as `branch`; else the PR record's),
and the push names the head the fix built on (`harvestcopy.Request.Onto`): the
branch at that head moves forward to the commit, never with force; at any
other sha it is `ErrBranchMoved`. The open PR is found (never a second one),
the PR record moves to the new head (CI pending), and the copy ends
`--ok --pr <owner/name>#<n> --head <new sha>`, which retires the open reads
and cuts fresh ones at the new head. A DONE fix copy with nothing committed
fails "done without a commit".

## The work copy's end is the boundary step (#4227)

On a DONE harness with a commit, `CopyLedger.End` runs the one outward write
the boundary allows, through `internal/nsprint/card/harvestcopy`
(`Harvest(ctx, Request) (Result, error)`):

1. **Push** the commit from the job's checkout to the primary's repository as
   `refs/heads/<branch>`, never with force: a remote branch already at the sha
   is success (`already`), a branch at any other sha is the typed refusal
   `ErrBranchMoved`, and the tip is read back after the push so `pushed` means
   ls-remote showed it (the rules of the card harvest's push script).
2. **Open the PR** with the GitHub REST API against the primary's BASE, with
   its title and a body carrying `STREAM:`, `ORIGIN:` and `DONE-WHEN:` and
   ending with the Claude Code line; an open PR for that head already on the
   repository is success (one GET first, then one POST).
3. **Record and end**: the PR record `pr:<name>:<n>` is written at the head
   (what the move file's ok-with-a-PR check reads), then the copy ends
   `--ok --pr <owner/name>#<n> --head <sha>`, which moves the primary
   `working -> review` and cuts its read copies in that one call.

A harvest that fails ends the copy `fail` with a typed reason and the error
text as its why, the commit and the branch kept on the record for the review:

| reason         | when                                                            |
|----------------|-----------------------------------------------------------------|
| `no-token`     | `GH_PUSH_TOKEN` is empty in the nova-card process's environment |
| `push-refused` | the branch moved, the commit is missing, git refused the push   |
| `pr-refused`   | the REST lookup or the POST was refused                         |

A DONE work copy with nothing committed fails "done without a commit".

## Reads honour room; kinds and tiers (#4270)

The move into review cuts read copies only onto consumers with room (slots
less working less ready), at most that many per consumer, never more than
four in all; with no room anywhere none are cut, and the deal pass cuts them
when room appears (`TM.ensure`: a primary in review with no live copy). A
consumer's copy filters are on its desired hash and set by
`nova-sprint capacity bench|friend --kinds work|read|fix,... --tiers <t>,...
<name> <slots>`: omitted keeps them, an empty value clears; the move file's
`TM.may` refuses a copy of a kind or tier the consumer does not take.

## The push credential

`GH_PUSH_TOKEN` is the bench's push credential, sealed in the secrets store
for the bench seats and present in the nova-card process's environment. It
reaches git only through askpass: git runs the nova-card binary again with
`NOVA_CARD_ASKPASS=1` and reads the token from its stdout, so the token is
never in argv and never on disk. The REST calls send it as a bearer token.
It never reaches the harness or the sandbox: `harnessEnv` (the harness
program or the in-process Go harness) and `runnerEnv` (nova-swarm native)
strip `GH_PUSH_TOKEN` and `NOVA_CARD_ASKPASS` by name, and a test holds them
to it. A path remote (a local bare repository, what a test pushes to) needs
no credential.
