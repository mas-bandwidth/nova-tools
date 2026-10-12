# One member implementation

A sprint has one member loop: beat, queue, push, judge the finish, take to
width, each card a child. **nova-tools owns that loop, in `pkg/member`.** The
fleet's member process, `nova-swarm member`, is the thin command over it: it
builds a `member.Member` and hands it a `Runner`, a `Pusher` and a `Sprint`
(`cmd/nova-swarm/member.go`, `memberattempt.go`, `memberpush.go`), and carries
no loop of its own — `memberLoop` drives `pkg/member`'s `Tick` and `Beat`.

The split of 2026-10-05 copied the shared packages, `pkg/member` among them,
into nova-sprint's own tree, to be imported from nova-tools later
(`docs/SPLIT-NOVA-SPRINT.md`, section b). The two copies drifted, so a member
fix that landed in one repo never reached the fleet; nova-sprint #112 (a read
whose stage failed is run again, never held as a ghost) landed there first and
had to be ported into nova-tools by hand. One copy ends the drift.

## Which repo owns it, and why

nova-tools owns the loop. The choice is decided by the surface, not by habit:

- nova-tools' `pkg/member` is the newer surface. It carries `EndIdleReap`,
  `EndEnvironment` and the `Result.Wall` field, which nova-sprint `v1.2.13`
  does not export. Making nova-tools import nova-sprint's copy would regress
  that surface and break `cmd/nova-swarm`.
- nova-tools' `pkg/member` is already public. The split moved it out of
  `internal/` so a consumer imports it (`c0fae1cab5`); it is already the loop
  the fleet runs, and the ghost-read fix is already in it
  (`47f97d4a0f`, "port of nova-sprint #112").
- The release play already installs the member from nova-tools: `nova-swarm`
  is a nova-tools binary in `fleet/tools.yml`, while nova-sprint, nova-card
  and nova-work come from nova-sprint's own release. The owning repo and the
  installing repo are the same, which is the invariant the card asks for.

The reverse — nova-tools imports nova-sprint's `pkg/member` at a pinned
version — is blocked and is the wrong direction: nova-sprint `v1.2.13` lacks
the newer surface and its shared packages live at other module paths, so the
import would fail to compile without switching every shared type across the
`cmd/nova-swarm` tree. It is also backwards to the split, which is documented
as "copied into nova-sprint, later imported from nova-tools".

## The guard

`TestOneMemberLoop` (`internal/ci/onemember_class_test.go`) walks the module's
package graph and is red when any package other than `pkg/member` declares a
member loop (a `Member` struct with a `Tick` method), so a second copy landed
back into nova-tools is refused on the change that lands it.
`TestFleetCommandConsumesTheOneLoop` pins that `cmd/nova-swarm` imports
`pkg/member` rather than writing its own loop, so the loop the fleet runs is
the one the guard protects.

## Left for the nova-sprint repository

Deleting the second copy in nova-sprint — replacing its `pkg/member` with an
import of `github.com/mas-bandwidth/nova-tools/pkg/member`, and adding the same
guard test there — is a change to the nova-sprint repository and is out of this
checkout's reach. The functional tests in nova-sprint then exercise the member
the fleet runs.
