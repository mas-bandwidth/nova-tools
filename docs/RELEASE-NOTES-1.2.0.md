# Draft: Nova Tools 1.2.0 (unreleased)

This is proposed future scope, not a published release or an upgrade guide for
v1.1.1. The steps and feature descriptions below require review against the
eventual certified 1.2.0 commit before anyone follows them.

The proposed Nova Tools 1.2.0 is about two things. The first is that a message
sent to an AI should reach that AI. Before it, the bus took every message, even
for a friend whose session could not hear, and a note could sit unread for hours.
Now the push into a session is proved before anything relies on it, and the
tools refuse, with a remedy, where it is not. The second is that a read is a
card, when read cards are on: the sprint deals a read the way it deals work,
at the card's tier, to the cheapest reader that may take it.

Its changes are in `nova-bus`, `nova-friend` and `nova-sprint`, with fixes to
`nova-sandbox` and `nova-secrets` from a security read of the release range,
and a few to `nova-redis`, `nova-check` and `nova-version` that kept the base's
gate green.

Version 1.1.0 was tagged at `2e73c44d` on 2026-10-07. This draft describes
proposed work beyond that baseline; it does not replace the v1.1.0 release.

## Proposed upgrade path (not for v1.1.1)

If 1.2.0 ships with this scope, verify its final notes before doing these
steps on the machines they name.

1. **Install the 1.2.0 binaries** on every machine that runs a friend daemon,
   the sprint server or the seat.
2. **Load the Redis function library**, once against every store:
   `nova-redis fn load --addr <host:port> [--user <name>] [--password-env <NAME>]`.
   The `nova_sprint` library in this release is smaller (the card machine's old
   Lua is gone), so its digest differs from the one a store holds. Until the
   load, `nova-redis fn check` reports the store `STALE` and names the same
   command as its remedy.
3. **Re-install each friend daemon**, on the machine it runs on:
   `nova-friend install --as <name> --harness <h> --dir <dir> [--session <id>] [--server <addr>]`
   with the arguments it was first installed with. Install rewrites the
   launchd plist and loads it again; a daemon whose arguments differ from its
   plist says `plist drift` when it starts, and an edit to a plist in place is
   not read until the agent is loaded again. The install itself now runs the
   push proof (below), so it may refuse; read the refusal's remedy.
4. **Re-install the seat's push loop**, on the machine that holds the
   coordinator seat: `nova-sprint seat install --actor <seat> --harness <h> --target <dir>`
   (add `--session <id>` for a harness that names one). It records where the
   seat's pushes go; a second install is the first again.
5. **Check**: `nova-sprint seat check` should end `MACHINERY OK`, and
   `nova-bus names` should show `push=proven` for every friend whose daemon
   runs.

A friend whose harness has no deliver command is now refused (below). Start
those friends under a harness that has one before step 3.

## The push proof

The owner's rule, 2026-10-05: "Your inbox MUST push to you." It is mandatory
and enforced in three places.

### A friend's daemon

`nova-friend install` and `run` refuse a harness nothing pushes into: one with
no deliver command is refused (exit 2) before anything is written, loaded or
delivered, `--dry-run` included. The remedy is to run the friend under a
harness that has one; the refusal lists them. A harness that runs each card
as a process of its own (Claude) has no session to push into and owes neither
the refusal nor the proof. A session `nova-friend` cannot drive, such as a
dsh session under an agent preset, is refused with
`start a session in <dir> with no agent preset and name it with --session <id>`.

`run` then proves the push before its loop: it delivers one SESSION CHECK and
the session's pong, carrying the check's nonce, must reach the bus within five
minutes. With none, `run` exits 2 with `RUN REFUSED: no push proof` and a
remedy, and the daemon does not start. `install` runs the same check after
loading the agent and, when the session cannot be driven, unloads the agent
and removes its plist.

Every beat carries the session's last proof. The sprint treats a friend
whose proof is more than fifteen minutes old as down, however fresh her beat,
and deals her nothing; the coordinator's pass raises one `friend deaf` judgment
when a proof lapses.

### The bus

The daemon writes each name's proof into the store (the hash `bus2:push`),
renewed every minute while the session stays up, and marked down the moment
the session stops answering. `nova-bus` reads it:

- `send --as <me>` and `recv --as <me>` refuse until `<me>` has a proof younger
  than ten minutes.
- `send --to <x>` (and `--cc`) refuses a recipient without one, all
  recipients named at once, nothing written. The refusal is one `deaf:` line
  each, with the age of the last proof, why, and the remedy.
- `recv --forever` stops at its next read when its proof goes stale.
- `nova-bus names` prints each name as `push=proven|stale|down|none`, with the
  age and the harness. `peek`, `ack` and `log` are never refused, so a deaf
  name can still look and clean up.

A friend daemon's own sends (its SESSION CHECK and its pong) go around the gate,
since they are how the proof is made. A name with no friend daemon, such as a
machine row, has no way to a proof and is refused until one runs. The proof does
not see deafness at once: a session that stopped hearing between two checks
still reads up for up to fifteen minutes.

### The seat

The coordinator seat is held only by a session its push loop reaches. The
seat's push record names its harness and target, the last check delivered and
the last pong. The loop (`nova-sprint inbox --wait --push seat`) delivers a
check when one is due, and the session answers with
`nova-sprint seat pong <nonce> --actor <seat>`. Without a proof no older than
fifteen minutes, every coordinator verb refuses with one line:
`PUSH DOWN: <why>; ... run: nova-sprint seat install --actor <seat> --harness <h> --target <dir>`.
`seat install`, `seat push`, `seat pong`, `seat check`, the reads and the
workers' and machines' verbs are never refused by it. `seat check` gains a
`MACHINERY push OK|DOWN` line, so a seat with no live proof fails the check
(exit 1) and the line carries the remedy. `seat install` now also installs the
loop as a launchd agent (`nova-sprint.seat-push`) or a systemd user unit, kept
alive and started at login.

## The friend daemon writes every card she holds

On 2026-10-05 two friends held cards no lane ran: nothing had written their
briefs, because the one writer was the coordinator's `friend sync` pass, which
is all or nothing and had stopped. Now the daemon reconciles her inbox with
her row on every loop:

- it asks the sprint server which cards are on her row, whoever put them there,
  and writes `inbox/<job>/BRIEF.md` for each that has none, never over a file
  already there;
- a sprint job whose card has left her row, that no lane is running, is moved
  to `inbox/retired/<job>`; a job any other hand put there is never moved;
- it finishes a card whose `outbox/<job>/REPORT.md` says `LAND` with a head
  (and fails one that says `HOLD` or `FAIL`), even when the daemon did not stage
  the brief;
- one-shot lanes are handed held cards whether or not her `QUEUE.json` names
  them.

`nova-friend status` prints `held=`, `inbox=` and `missing=`. If the server
cannot be asked, nothing is written or retired, and the status says so.

## Liveness comes from the session's pong

A friend is up on her session's word, never on a process. A daemon that looked
for a harness's application and held its beat back when it did not find one
left a friend that answered every check reading down for three hours.
Now:

- the harness watch is advice only: status says `harness_seen=running|not-seen`
  and the beat is never held for it;
- a wake check is answered only by the session's pong; the daemon's
  `daemon-pong` is a separate fact (`daemon_pong_age` and `session_pong_age`);
- any bus message the session writes brings the friend up;
- the daemon's state directory is `<dir>/.nova-friend`, inside what a sandboxed
  session may write, falling back to `~/.nova-friend/<name>` when that
  is refused (a removable volume may refuse a background process).

## Reads are cards

A read used to be asked of a reader from a table of its own, beside the deal,
one read at a time. With `nova-sprint set --read-cards on` it is a card (off,
the default, keeps the readers table):

- A finished card is always asked a read. Each read is a consumer card, dealt
  like work at half a slot of the reader's one width, in parallel with the
  card's other reads, at the card's tier.
- A reader may take a read of its own tier or one tier below it;
  among those that may, the read goes to the cheapest.
- `nova-sprint set --reads 0|1|2|default` says how many reads a card needs;
  `--reads 1` waives the second.
- The tick accepts a primary whose reads are all ok and tells the seat; no
  hand step is needed.
- `tick --shadow` says why each read waits, and the dashboard's rows say how
  many of their cards are reads.

## The deal

- Queued work that has not started moves to idle lanes on either side,
  friends and fleet, within the tier sets. The settings `fleet-tiers` and
  `friends-tiers` say which tiers each side may take; the default is all.
- A card's landing refusal reworks the card at the stream's tip, and the
  stream lands on.
- `nova-sprint brief` edits a card in place, under the same id; a widen makes
  no twin.
- `friend give` undoes a take-back, so the card may be dealt again.
- A generated brief signs the worker's own name.
- A held sentinel wave releases again, and a held card follows its chain.

## Security

A security read of the release range refused it, and these changes answer the
refusal. A later read of the fixes approved them.

- `nova-sandbox`: the bare form keeps the caller's process group, as
  SPEC-SANDBOX rule 12 says, so a caller that kills its own group reaches the
  walled tree. A runaway tree is frozen before it is killed, and `killed` is
  printed only when nothing is left.
- `nova-secrets`: a reseal into an existing seat passes the gate; the
  written-by mark is pinned to a version shape; a seat's keys are read in
  process; no secret appears in an error.
- `profiles/` (the text of the darwin wall) and `tools/sandboxcheck/` are on
  the sensitive path list, so a change to either needs a security read before
  a release is cut.

## The release gate

`nova-update release cut` now runs a journey gate (a checkout that ships
`internal/friend` names a passing run of each recovery journey, or waives the
gate with a reason) and a spend gate (each provider's own count of the
release window's spend beside what the sprint recorded). A waiver of either is
written into the CHANGELOG section.

## Also in this release

- Friends first: unpinned work is dealt to a friend before the fleet, `WHO: only
  friend <name>` is the one hard pin, and `nova-sprint unpin` records an unpin.
- A subscription friend is paced by her windows, and a rate limit backs the
  lanes off instead of taking them down.
- `nova-friend install` writes the harness's settings and `check --settings`
  names any that drifted; a working directory that is a symlink is refused.
- `nova-friend ping --wake --every <d> --to-friends` runs the wake loop as a
  verb, and `nova-sprint friend sync --every <d>` follows the seat.
- Streams, cards and the dashboard: `nova-sprint stream archive`, `view cards
  --by`, `bases`, per-stage cycle times, and a dashboard showing one release.

## The fixes that turned the promotion green

The sprint base failed its gate on its way to `dev`. These fixed it, and each
changes behaviour only where it says so:

- `nova-sandbox`: the process cap counts a tree once more when its leader
  exits, so a fork bomb that killed its parent is still stopped; `SANDBOX OK`
  names the roots deletes are allowed in, and a data home passed as its own
  `--write` root can now delete there.
- `nova-check`: the links check resolves the tree root before testing for an
  escaping symlink, so a root under a symlinked temp directory is not reported
  as escaping; the package loader uses `go list -json`.
- `nova-sprint`: the server runs its restart step once before its first tick; a
  friend's stall wake goes once, after the step commits; the deal's history
  reads are counted, never timed.
- `nova-redis serve` is pinned to print no secret on any path.
- `nova-version snapshot` no longer fails with "text file busy" on Linux.
- Unchecked errors in `internal/friend`, staticcheck findings and the config's
  command list are cleaned up.

## What is not in this release

The seat loop's push of backlog alarms over `nova-bus` is not built; the
loop delivers the seat's judgments and checks only.
