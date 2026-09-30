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

## Generality Guardrail Extended to All Living Text Files

The `generality` rule (§`generality-text` in [SPEC-CI.md](SPEC-CI.md#generality-text), PR #4877) extends the repository's naming and hygiene invariants beyond Go source code to all living non-Go text files. `TestGeneralityText` continuously sweeps 22 non-Go text file extensions (`.lua`, `.tsv`, `.yml`, `.yaml`, `.j2`, `.md`, `.sh`, `.json`, `.txt`, `.ini`, `.tmpl`, `.tla`, `.lisp`, `.sexp`, `.cfg`, `.card`, `.sql`, `.py`, `.ps1`, `.jsonl`, `.log`, `.notes`), as well as `Makefile` and `Containerfile`, skipping only `deprecated/` and `.git`:
- **Inventory and Pattern Hygiene:** Enforces the machine, host, friend, and person name inventory of `generality_class_test.go`, tailnet address spaces (`100.64.0.0/10` IPv4, tailnet IPv6 prefix, `.ts.net` names), and user home directory paths (`/Users/<name>`, `/home/<name>`, `C:\Users\<name>`). Documentation examples must use generic placeholders (`user`, `username`, `example`, `name`) or documented container/runner users.
- **Repository Link Anchoring:** Public repository links are restricted to the project's own public identities (`mas-bandwidth/nova-tools`, `mas-bandwidth/nova`, `mas-bandwidth/secrets`) anchored against line starts, URL prefixes, or path delimiters, with contact email addresses permitted strictly in `docs/SECURITY.md`.
- **Dynamic Configuration:** Eliminates hardcoded owner defaults in runtime scripts; components such as Lua waiting resolution (`internal/nsprint/fn/lua/waiting_resolve.lua`) dynamically resolve repository owners from card text, card repo fields, or sprint configuration (`cfg:sprint owner`).
- **Shrink-Only Allowlists:** Governed by two shrink-only ledgers: a fixtures list (`generality_text_fixtures_allowlist.txt`) for recorded verbatim data with required per-row justifications, and an exact count-based debt ledger (`generality_text_allowlist.txt`) for existing occurrences. Both lists only shrink; any new finding, unlisted file, or count increase immediately fails CI.

## Multi-Stage Promotion Skip Architecture for Sprint Foundation

Continuous integration introduces a comprehensive multi-stage promotion skip and deletion excusal architecture (§`merges-and-promotions-only-grow` in [SPEC-CI.md](SPEC-CI.md#merges-and-promotions-only-grow), PR #4878) supporting `sprint/foundation -> dev` promotions alongside `dev -> main`. The framework eliminates redundant test runs and false-positive test rejections while safeguarding against unreviewed drops:
- **Pull Request Stage (`pull_request`):** Promotion pull requests from `sprint/foundation` targeting `dev` are identified by `promotionSkip`, logging an explanatory NOTE and skipping redundant full comparisons. An ancestor precondition check (`promotionBaseCheck`) verifies that `dev`'s tip is an ancestor of the head commit with full remote history fetched (`git fetch --no-tags --filter=blob:none --unshallow origin +sprint/foundation:refs/remotes/origin/sprint/foundation`), failing closed on shallow or stale checkouts.
- **Merge Queue Stage (`merge_group`):** Recognizes `refs/heads/gh-readonly-queue/dev/...` queue groups (`devRun`), evaluating landed promotions directly from git parent commits without requiring head branch name metadata.
- **Landed Promotion Excusal (`push` / `devRun` / `landingRun`):** When landed as a two-parent merge commit on `dev` whose second parent is `sprint/foundation`'s tip (verified via `git merge-base --is-ancestor`), intentional file deletions and added allowlist rows are excused if and only if `sprint/foundation`'s history since the previous promotion deleted the path and the second parent's tree lacks it.
- **Lineage Integrity & Squash Protection:** Only true two-parent merge commits preserve verified promotion history; squash merges of the promotion tree are explicitly refused excusal and evaluated as standard commits, flagging all deletions as unexcused findings.

## First read-only `nova-work` layer

`nova-work import` reads the specified GitHub organization or repositories into a local tree file; `--dry-run` performs the read without writing the file. `nova-work verify --tree <tree.lisp>` fetches again and reports differences in the captured issue records. The tree covers the fields listed in [the tree specification](SPEC-WORK-V1.md), including bodies, comments, cross-references and linked pull requests. Reactions, edit history, other timeline events, projects, issue types, sub-issues and pins are outside this first layer. Import and verify do not edit GitHub.

```sh
nova-work help
nova-sprint help
```
