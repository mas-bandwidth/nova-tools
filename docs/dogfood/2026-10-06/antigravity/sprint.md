# nova-sprint dogfood — antigravity (zhi), 2026-10-06, re-run at 08d5d63f4

Read as a stranger: only `nova-sprint -h`, `nova-sprint help`,
`nova-sprint <verb> -h`, and the page under `docs/`. Built from the checkout at
08d5d63f4651b134ad1e28184ad352e79a99b71d and used as
`nova-sprint v1.0.1-0.20261007135756-08d5d63f4651 linux/amd64 go1.26.6`:
every verb at least once with its real flags against `mem:<file>` twins (a
bare `origin.git` and a `work` clone for the landing flow), the refusals too.
No server was started. This run replaces the attempt-1 report, whose finding 2
was wrong: it said `relink` lacks `--dry-run`, but `nova-sprint relink -h`
names it and `cmd/nova-sprint/relink.go:20` registers it, printing
`RELINK DRY-RUN ...; nothing was changed` and writing nothing. To reach any
flow past `init` I had to drive the push proof by hand
(`seat push --sent <nonce>` then `seat pong <nonce>`), which the walkthrough
does not name; finding 1 is that the walkthrough as printed cannot run.

## Findings

1. `nova-sprint add --stream s1 --count 1 --one` (the tool's own
   "trying it without a Redis" walkthrough, help lines 523-540, on a fresh
   `mem:fresh.twin` after `nova-sprint init --readers reader-a,reader-b --members m1`)
   Printed:
   ```
   nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   exit 2, one line. I expected the walkthrough to run with no Redis and no
   server, as its own paragraph promises ("trying it without a Redis ... for
   learning and tests"). Instead every coordinator verb is gated by the push
   proof, and on a twin there is no way to show it: the proof comes from
   `inbox --wait --push seat`, which the twin refuses
   (`nova-sprint inbox REFUSED: a mem twin has no machine running between commands: run nova-sprint tick to tick it by hand, then read the sprint; run: nova-sprint inbox -h`), and `seat install` refuses a twin too
   (`nova-sprint seat install REFUSED: the push loop waits on the sprint's machine, and the in-memory twin mem:fresh.twin has none: install it for a Redis store or the sprint's server; nothing was written; run: nova-sprint seat install -h`). So the advertised first run stops at its
   second command, and its remedy names a loop that cannot run.
   Grade: URGENT.

2. `nova-sprint drop s1-2 --reason 'never mind' --dry-run`
   Printed:
   ```
   nova-sprint drop REFUSED: unknown flag --dry-run; the flags of drop are --actor, --answers, --cascade, --col, --epoch, --expect, --group, --json, --limit, --max, --one, --op, --reason, --redis, --repo, --stream; run: nova-sprint help drop
   ```
   exit 2. I expected a plan of the drop: the standard says a verb that writes
   has a dry run, and `hold`, `unhold`, `unpin`, `stats tidy`, `relink`, and
   `rank`, `recut` and `brief` (on their selector form) have one.
   `priority`, `move`, `redo` and `accept` lack one too.
   Grade: NEXT.

3. `nova-sprint card s1-1 --brief`
   Printed:
   ```
   nova-sprint card: s1-1 has no brief; run: nova-sprint brief s1-1 --brief-file <path>
   ```
   exit 1. I expected the one refusal grammar, `nova-sprint card REFUSED: ...; run: ...`, since the standard says the status word leads every line.
   `nova-sprint stream archive s2` (`nova-sprint stream archive: s2: no stream s2 on the work or merge table (streams: s1); nothing was changed; run: nova-sprint help stream`) and `nova-sprint friend cards friend-a`
   (`nova-sprint friend cards: no friend friend-a on the friends table (friends: none); run: nova-sprint friend sync`) print the same
   `verb: reason` shape without the word.
   Grade: NEXT.

4. `nova-sprint start -h`
   Printed:
   ```
   usage: nova-sprint start [--actor <string>] [--epoch <int>] [--json] [--max <int>] [--op <string>] [--redis <string>]
   from `nova-sprint help`:
     nova-sprint start
   ```
   exit 0. I expected `usage: nova-sprint start`, as the main usage and the
   `from help` line show; instead the first line is the generic flag list, as
   if the verb took those flags. `bases`, `check`, `repair`, `machinery`,
   `handover`, `routes` and `rules` print the same generic line.
   Grade: NEXT.

5. `nova-sprint needs --roots`
   Printed nothing at all (exit 0). I expected a bounded result with its
   total, as `needs` prints `NEEDS OK cards=0 dropped-or-absent=0` and
   `needs --roots --json` prints an object; a silent success is not a result.
   Grade: NEXT.

6. `nova-sprint view worker --as m1`
   Printed:
   ```
   VIEW worker member m1: working 0 ready 0, results not landed 0
   cursor=1.
   ```
   exit 0. I expected a cursor with both halves, as `view coordinator --json`
   prints `"cursor":"1.T-3r37m0"`; the worker cursor's second half is
   empty, so the printed cursor is malformed.
   Grade: NEXT.

7. `nova-sprint promoted --sha 0000000000000000000000000000000000000000 --dry-run`
   Printed:
   ```
   PROMOTED DRY-RUN sha=0000000000000000000000000000000000000000; nothing was changed
   ```
   exit 0. I expected the `--dry-run` that "check[s] the sha" (`promoted -h`)
   to refuse a sha that is no commit; it checks only 7 to 40 hex digits, and
   without `--dry-run` the same all-zero sha is recorded
   (`MOVED promoted the sprint branch into dev at ... (0000000000000000000000000000000000000000)`).
   Grade: NEXT.

8. `nova-sprint promote --dry-run` (from a directory that is not a git
   repository)
   Printed:
   ```
   nova-sprint promote: git symbolic-ref: fatal: not a git repository (or any parent up to mount point /) | Stopping at filesystem boundary (GIT_DISCOVERY_ACROSS_FILESYSTEM not set).; run: nova-sprint promote --dry-run
   ```
   exit 1. I expected one refusal line naming what `--repo-dir` wants; the
   line is a raw git error piped with `|`, and its `run:` repeats the command
   instead of a remedy.
   Grade: NEXT.

## What the tool got right

- The one-card flow works end to end on a twin once the proof is forced:
  `init`, `add`, `start`, `tick`, `take`, `finish`, `tick`, `read --begin`,
  `read --ok`, `tick`, `land --repo-dir work --base sprint/s1`, `tick`,
  `where` landed the card, and every `MOVED`/`TABLES`/`TICK OK` line carried
  its totals.
- `relink` has its dry run: `nova-sprint relink <old> <new> --dry-run` prints
  `RELINK DRY-RUN old=... new=...; nothing was changed` and writes nothing
  (`cmd/nova-sprint/relink.go:20`, help line `--dry-run  say what would be relinked and write nothing`). The attempt-1 report's claim that it lacks one
  was wrong; this run corrects it.
- `nova-sprint` bare and `nova-sprint bogus` each refuse in one line and name
  every verb; every verb's `-h` exits 0 and carries the exit table and effect.
- `add`, `move`, `merge` and `drop` refusals name the flag, the state and a
  pasteable `run:` remedy; unknown flags name every flag the verb has.
- `where --json`, `card --all --json` and `view cards --json` render the same
  value as the lines, and `--max` and totals survive.
- `check`, `rules`, `routes`, `bases`, `handover` and `machinery` answered
  cold on an empty twin with a clear `OK` or `DOWN` line and remedy.

READ 7/10 — the first 15 lines answer what the tool does, how it works and
where its state lives, and every verb's `-h` names its flags, effect and exit
codes, but the `-h` banner is 669 lines, the walkthrough it points to cannot
run, and the flagless verbs print a generic flag list as their usage.

USE 4/10 — every coordinator verb is gated by a push proof the in-memory twin
can neither show nor install, so the documented first run stops at `init` and
I had to drive `seat push --sent` and `seat pong` by hand before any card
could be added.

urgent=1 next=7
