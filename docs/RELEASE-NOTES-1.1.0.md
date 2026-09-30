# Nova Tools 1.1.0

Nova Tools 1.1.0 contains eighteen command-line tools. This release makes a sprint easier to run across several machines and adds the first read-only `nova-work` import. Choose the tools that fit your work; each keeps its own command-line entry point. Start with `nova-sprint help` or `nova-work help` to see the verbs and flags.

## Sprint handoffs

Fleet placement continues its rolling turn across plans and ticks. A member going down or a fleet level move uses that turn rather than restarting at the first name. When a deal lands during queue reads, the up members can take the ready work together instead of leaving part of the fleet for the next tick.

The coordinator can wait for a tick-end notice with `nova-sprint inbox --wait`; `inbox --json` separates judgments, happened notices and the done state for programs reading the inbox. `accept --read-ok` accepts review work with okay reads from two different readers, and a completed sprint stops its machine. A coordinator can pass a multi-paragraph brief with `add --brief-file <path>` and run its own `merge` without an epoch flag. A worker reporting on a card it was handed still names that card's epoch, so a clear cannot turn an old report into a report on new work.

The dirty-driven tick pumps queued work once at the start of a tick, then settles changes to readers, merge and fleet before the tick ends. If those updates do not settle within the bounded turn, the tick reports a failure. Work those later steps queue is left for the next pump. The tick-end notice is one coordinator wake for the tick's addressed work.

## Members and fleet configuration

`nova-swarm member` joins a named fleet member to the Sprint queue: it beats, takes work within its width, runs each card as a child, and reports the result through Sprint's existing verbs. Its help names the required harness, model, work root, deadline and token settings.

`nova-config machine self` identifies this machine; `nova-config machine width <name>` derives its fleet width from machine slots and the friend slots charged there. `nova-sprint fleet sync --check` shows inventory drift before a sync, and `fleet sync` follows the configured members and widths. A member missing from the inventory is held and its unfinished cards are redealt; a later sync can release a hold made by sync without lifting a coordinator's own hold.

## First read-only `nova-work` layer

`nova-work import` reads the specified GitHub organization or repositories into a local tree file; `--dry-run` performs the read without writing the file. `nova-work verify --tree <tree.lisp>` fetches again and reports differences in the captured issue records. The tree covers the fields listed in [the tree specification](SPEC-WORK-V1.md), including bodies, comments, cross-references and linked pull requests. Reactions, edit history, other timeline events, projects, issue types, sub-issues and pins are outside this first layer. Import and verify do not edit GitHub.

```sh
nova-work help
nova-sprint help
```
