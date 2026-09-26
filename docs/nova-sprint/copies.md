# Consumer copies on a bench

A stream primary (`task:<id>`) is worked through COPIES (`task:<id>~<n>`)
that `card deal` cuts onto a consumer (a bench or a friend). On a bench the
copy runs under `nova-card copy <copy>`, started by the bench's copy session
after one `card work --fill`: the copy's card is rendered from its record
(`card.RenderCopy`), the wrapper beats it with `card beat`, and it ends with
`card end --id <copy>` (`card.CopyLedger`, internal/nsprint/card/copy_ledger.go).
Its KIND decides the brief:

- **read**: read the PR at its head, end with `card end --score N/10`.
- **fix**: close the read's finding on the PR's branch, end with
  `card end --ok --pr <owner/name>#<n> --head <sha>`.
- **work**: the primary's own task. The model changes only PATHS, commits on
  the copy's branch `nova/copies/<label>-c<n>-a<n>` in the job's `repo/`,
  writes RESULT.md and exits. It never pushes, never opens a PR and never
  runs `card end`; a sandboxed swarm model could not (GitHub is a git remote
  only, the sandbox shims gh).

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

A DONE work copy with nothing committed fails "done without a commit"; a
DONE read or fix copy that never ended itself fails "done without card end".

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
