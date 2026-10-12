# Nova Tools 1.2.15

DRAFT: this file lists what v1.2.15 contains so far. The cut reviewer fills the
sections marked "to be confirmed at the cut" and resolves every line marked
"(pending: PR not yet merged)" before the tag.

Nova Tools 1.2.15 is a point release of fixes, one concern each, paired with
nova-sprint 1.2.15. It carries what landed on the comfort stream after the
1.2.14 cut. Every entry is in [FIXES.md](../FIXES.md) under v1.2.15.

## Upgrading

To be confirmed at the cut. No ordered upgrade steps are known yet; the
1.2.14 steps (install `nova-hook`, hook each identity in, install the
binaries, check the mirrors, price every enabled route) are already in force.

## The friend daemon

- **Codex friend delivery refuses unproved queue routes.** The implicit Codex
  delivery route is refused at factory, check, install, run and reach until a
  supported session push route is proved, so the daemon refuses before it
  starts rather than queue a prompt or require a Steer click. Notification-only
  delivery does not type into a session or queue a prompt, and the refusal
  preserves the installed launch configuration. This guard prevents queued
  Steer regressions; it does not claim idle wake or permanent native push is
  implemented. (#5628)

## The sandbox

- **The card wall lets a TLC step read the jar and java.** The wall names the
  TLC jar directory and the bench's toolchain root read-only, and sets the
  JVM's temporary directory inside the wall, so a step runs
  `java -cp /opt/tla/tla2tools.jar tlc2.TLC` as `tla/README.md` says and a spec
  that extends the standard modules no longer fails on `/tmp`. (ledger status:
  in-progress)

## Landing in this cut

- **Member lanes kill their process group at lane end.** (pending: PR not yet
  merged)

## What is not in this release

To be confirmed at the cut.
