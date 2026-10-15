# Release lines and the forward merge

This page names the branches that carry a release line and how work moves
between them. The release tooling itself is in [SPEC-RELEASE.md](SPEC-RELEASE.md); the same lines and the same merge run in nova-sprint.

## The lines

| branch | what it is | what lands on it |
|---|---|---|
| `v1.3/comms` | the stable line, until `release/v1.4` exists | minimal tiny fixes only |
| `release/v1.4` | the stable line once it is cut; it replaces `v1.3/comms` in everything below | minimal tiny fixes only |
| `v1.5` | the stable line plus schema table adoption (fixed and variable tables, dogfooded here) | forward merges from the stable line, and merges of `v1.5-schema-tables` |
| `v1.5-schema-tables` | the schema table adoption work | the schema adoption cards (their `BASE:` line), and forward merges from `v1.5` |

The feature branch is `v1.5-schema-tables`, not `v1.5/schema-tables`: git
cannot hold a branch `v1.5` and a branch under `v1.5/` at once.

## The forward merge

Work moves one way: stable, then `v1.5`, then `v1.5-schema-tables`. Nothing is
merged backward. A fix the stable line needs lands on the stable line first and
comes forward with the next merge. No branch here is rebased or force-pushed.

Run it after each landing on the stable line, and before a `v1.5` point
release. Check what is behind:

```sh
git fetch origin
git rev-list --count origin/v1.5..origin/v1.3/comms
git rev-list --count origin/v1.5-schema-tables..origin/v1.5
```

Merge each step that is behind, in order:

```sh
git switch --detach origin/v1.5
git merge --no-ff origin/v1.3/comms -m "forward merge v1.3/comms into v1.5"
nova-ci local    # the unit tier CI runs for this diff, on a Linux bench
git push origin HEAD:v1.5

git switch --detach origin/v1.5-schema-tables
git merge --no-ff origin/v1.5 -m "forward merge v1.5 into v1.5-schema-tables"
nova-ci local    # the unit tier CI runs for this diff, on a Linux bench
git push origin HEAD:v1.5-schema-tables
```

A clean merge whose gate is green is pushed straight to the branch with a plain
`git push`; it is a fast-forward of the branch it lands on. A merge with a
conflict goes up as a pull request into that branch, with the resolution in its
own commit, for a reader before it lands. A push that is refused because the
branch moved is fetched and merged again, never forced.

A card based on `v1.5-schema-tables` picks up a forward merge at its next
attempt; a card in review is checked against the new tip when it lands.
