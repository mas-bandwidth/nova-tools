# Nova Tools 1.1.0

Nova Tools 1.1.0 contains eighteen command-line tools. This release makes a sprint easier to run across several machines and adds the first read-only `nova-work` import. Choose the tools that fit your work; each keeps its own command-line entry point. Start with `nova-sprint help` or `nova-work help` to see the verbs and flags.

## Sprint handoffs

Fleet placement continues its rolling turn across plans and ticks. A member going down or a fleet level move uses that turn rather than restarting at the first name. When a deal lands during queue reads, the up members can take the ready work together instead of leaving part of the fleet for the next tick.

The coordinator can wait for a tick-end notice with `nova-sprint inbox --wait` (bounded optionally by `--timeout`); `inbox --json` separates judgments, happened notices and the done state for programs reading the inbox, and on timeout emits strictly clean JSON without text banners. `accept --read-ok` accepts review work with okay reads from two different readers, and a completed sprint stops its machine. A coordinator can pass a multi-paragraph brief with `add --brief-file <path>` and run its own `merge` without an epoch flag. A worker reporting on a card it was handed still names that card's epoch, so a clear cannot turn an old report into a report on new work.

The dirty-driven tick executes updates across the four tables in strict order: `1. work streams, 2. readers, 3. merge, 4. fleet`. The pump mutates the work table once at the start of a tick, while subsequent steps queue their modifications; table queues act as dirty bits drained immediately before tick end to settle dependent updates. If those updates do not settle within the bounded turn, the tick reports a failure. Work those later steps queue is left for the next pump. The tick-end notice is a single coordinator wake for the tick's addressed work.

## Members and fleet configuration

`nova-swarm member` joins a named fleet member to the Sprint queue: it beats, takes work within its width, runs each card as a child, and reports the result through Sprint's existing verbs. Its help names the required harness, model, work root, deadline and token settings. Loop bounds are hardened with `--once` vs positive `--ticks` (requiring positive counts and disallowing `--once` alongside `--ticks`). On member restart, recovery of in-flight (`working` or `reading`) cards is clamped to member width (`m.Width`), preventing slot oversubscription when unmanaged children are recovered; excess cards remain queued and resume on subsequent passes as capacity opens.

`nova-config machine self` identifies this machine; `nova-config machine width <name>` derives its fleet width from machine slots and the friend slots charged there. `nova-sprint fleet sync --check` shows inventory drift before a sync, and `fleet sync` follows the configured members and widths. A member missing from the inventory is held and its unfinished cards are redealt; a later sync can release a hold made by sync without lifting a coordinator's own hold.

## Cards with complete child instructions

`nova-swarm lint --child-rules` checks the standing instructions a coordinator gives a child. `nova-swarm template --name card` supplies a card template, and `nova-sprint add` checks supplied briefs before writing a card. Refusals identify the missing rule so the coordinator can repair the brief.

## A smaller maintained codebase

The release excises dead code at scale (-198k lines across 921 files, 66 packages) from unused or superseded implementations, along with command implementations replaced by the eighteen living tools. Continuous multi-platform verification across Linux, Darwin (macOS), and Windows for all 18 living tools eliminates duplicate maintenance paths and keeps the reference focused on the commands the release actually builds.

Promotion guards in CI (`isPromotion`) recognize branch promotions from `sprint/foundation -> dev` alongside `dev -> main`. The deletion guard excuses intentional removals that already landed through the source branch's queue, preventing false-positive test rejections while safeguarding against unreviewed drops during integration promotions.

## First read-only `nova-work` layer

`nova-work import` reads the specified GitHub organization or repositories into a local tree file; `--dry-run` performs the read without writing the file. `nova-work verify --tree <tree.lisp>` fetches again and reports differences in the captured issue records. The tree covers the fields listed in [the tree specification](SPEC-WORK-V1.md), including bodies, comments, cross-references and linked pull requests. Reactions, edit history, other timeline events, projects, issue types, sub-issues and pins are outside this first layer. Import and verify do not edit GitHub.

```sh
nova-work help
nova-sprint help
```
