# nova-friend READ and USE rating, nova-tools 1.2.0

Rater: a cold rater, mercurys-2.5 under opencode
Build: 17ec8d256a04943b921987ebd9c17b19e9
READ: 8/10
USE: 7.5/10

nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon. The tool has verbs: install, uninstall, ping, pong, wait-pong, status, version, help.

## Reasons

READ. The README is clear about what the tool does. The first-run transcript is complete and shows installation, pinging, and status checking. The spec is in docs/SPEC-FRIEND.md. The verbs are well documented. What holds the score: the harness argument is not well explained in the README.

USE. The install/uninstall dry-run works. The ping/pong cycle works as shown. The status command shows daemon status. What holds the score: there is no way to verify the friend is truly reachable without going through nova-sprint.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-friend/main.go:help | install --help does not explain what harness values are valid | list valid harness names in install help | S |
| 2 | cmd/nova-friend/main.go:status | status output does not show the harness being used | add harness to status output | S |
| 3 | cmd/nova-friend/main.go:install | no --verify flag after install | add --verify that checks daemon status after install | M |

## Good, keep

- Clear first-run transcript with concrete examples.
- Dry-run mode for install/uninstall.
- Ping/pong mechanism provides proof of life.
- Status command shows daemon state.

## Compared with earlier ratings

This tool was not rated in 1.1.0 as it did not exist. The first rating shows a well-designed friend daemon with clear operational patterns.
