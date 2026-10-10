# nova-sprint dogfood — opencode-2, 2026-10-06

Read cold as a stranger: only `nova-sprint -h`, `nova-sprint help`, `nova-sprint <verb> -h`
and the tool's page under `docs/` (`docs/CLI.md`'s nova-sprint section and its `### First run`
transcript, and `docs/SPEC-SPRINT.md`'s "The push proof"). The binary was built on a Linux
bench from the staged checkout with `go build -o $BENCH/bin/nova-sprint ./cmd/nova-sprint`
at commit `7acb90e18a764f0e728cd5ed701196a34405a824`, and it printed
`nova-sprint v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`. Every top-level verb
in `nova-sprint help`'s usage list was exercised or its help read, with real flags against
`mem:<file>` twins (the help's no-Redis path) and temp directories, in the order a
coordinator would use them, the refusals too; the verbs that were read as `-h` only, and the
verbs that were not run, are named with their reasons under `## What held`. The host is
written `<bench>` below, the job directory `$BENCH`, a temporary directory `$TMP`, and the
commands and their output are quoted as typed and printed.

## Findings

1. `nova-sprint add --stream s1 --count 1 --one`
   Printed:
   ```
   nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   (one line printed)
   ```
   I expected the documented no-Redis first run to work: `docs/CLI.md`'s `### First run` and
   `nova-sprint help`'s "trying it without a Redis" both show this exact command after
   `init --readers reader-a,reader-b --members m1` answering `MOVED s1-1 -> ready stream=s1`
   and `ADD OK stream=s1 cards=1`, and the help says the twin "runs every verb". Instead the
   push proof is enforced on the twin, and its one named remedy is refused on the same twin:
   `nova-sprint seat install --harness claude --target $BENCH/session` answers `nova-sprint seat install REFUSED: the push loop waits on the sprint's machine, and the in-memory twin mem:sprint.twin has none: install it for a Redis store or the sprint's server; nothing was written; run: nova-sprint seat install -h`, so a stranger following the only documented
   first run stops at its second command with no remedy the twin accepts.
   Grade: URGENT (a refusal with no remedy)

2. `nova-sprint selftest`
   Printed:
   ```
   SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir> dir=$TMP/nova-sprint-selftest-3264827069
   (one line printed)
   ```
   I expected `SELFTEST OK`. `selftest -h` says it "makes a fresh directory, a bare origin and
   a clone whose base holds a go module, runs the card's flow of the walkthrough on a twin
   file in it and lands one card through the tree gate"; it is the tool's own proof that the
   twin path works, and it fails at the same push gate as finding 1 before it runs the flow.
   Grade: URGENT (a refusal with no remedy)

3. `nova-sprint selftest land`
   Printed:
   ```
   nova-sprint selftest land FAILED: selftest land: command [add --stream selftest --count 1 --brief-file $BENCH/scratch/H/land/canned-brief.md --redis mem:$BENCH/scratch/H/land/selftest.twin --actor boss] failed: nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream selftest --brief-dir <dir>; or say --one for a single card; run: nova-sprint add -h: exit status 2
   (one line printed)
   ```
   I expected `selftest land` to land its canned card. The command it builds for itself,
   `add --stream selftest --count 1 --brief-file <path>`, is refused by add's own one-card
   lint for the very omission `--one` repairs, so this shipped sub-verb cannot run at all.
   Grade: URGENT (wrong result)

4. `nova-sprint adopt 1.1.0 --source . --inventory i --reason x --dry-run`
   Printed:
   ```
   nova-sprint adopt REFUSED: <version|path>: the version "1.1.0" is not v-prefixed (pass --version vX.Y.Z); run: nova-sprint adopt -h
   (one line printed)
   ```
   I expected the remedy to name the form the usage takes: `adopt -h` shows
   `nova-sprint adopt <version|path> ...` with the example `adopt v1.2.0-dev.0123abc`, so it
   should read `adopt v1.1.0`. Following the printed remedy,
   `nova-sprint adopt --version v1.1.0 --source . --inventory i --reason x --dry-run` answers
   `nova-sprint adopt REFUSED: unknown flag --version; the flags of adopt are --actor, --ansible, --dry-run, --epoch, --inventory, --json, --limit, --max, --op, --reason, --receipts, --redis, --source; did you mean --reason?`. A refusal whose one named remedy is
   itself refused.
   Grade: URGENT (help that lies)

5. `nova-sprint seat check`
   Printed:
   ```
   MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set" remedy="export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-<bench> | sed -n 's/.*--listen [^:]*:\([0-9]*\).*/\1/p')"
   MACHINERY store OK redis=mem:sprint.twin dbsize=- machine=stopped epoch=0
   MACHINERY loop DOWN tick=never why="no heartbeat: run --listen has not ticked this store" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-<bench>"
   ```
   I expected a remedy this host can run. On this Linux bench the loop is a systemd user unit
   (`nova-sprint units --check` reads `$HOME/.config/systemd/user/nova-sprint-server.service`),
   so the third line should be the `systemctl --user` form; `launchctl` does not exist here.
   `nova-sprint machinery` prints the same block.
   Grade: NEXT (friction)

6. `nova-sprint backup --file $BENCH/b1 --dry-run`
   Printed:
   ```
   nova-sprint backup FAILED: $BENCH/b1 exists and is never overwritten; run: nova-sprint backup --file <a path that does not exist>
   (one line printed)
   ```
   I expected a preview. `backup -h` says `--dry-run  verify the backup in memory without writing --file; writes nothing`, and the effect line repeats that `--dry-run` writes
   nothing, so asking whether a backup would work at a path that already holds one should
   verify in memory and report, not fail. The file was written by the same command a moment
   before without `--dry-run`.
   Grade: NEXT (friction)

7. `nova-sprint remind --in 0s --note soon`
   Printed:
   ```
   nova-sprint remind REFUSED: give one of --in <duration> (from now) or --at <time> (an absolute time), not both and not neither; run: nova-sprint remind -h
   (one line printed)
   ```
   I expected exactly one of the two to be seen: `--in 0s` was given, and
   `nova-sprint remind --in 5m --note hello` succeeds on the same store. The parser reads a
   zero duration as "neither" and then reports a fault that is not there; the line should
   either accept 0 or name a duration above zero.
   Grade: NEXT (unclear help)

8. `nova-sprint promote --dry-run`
   Printed:
   ```
   nova-sprint promote: git symbolic-ref: fatal: not a git repository (or any of the parent directories): .git: exit status 128; run: nova-sprint promote --dry-run
   (one line printed)
   ```
   I expected the tool's own grammar. `promote -h` documents `--repo-dir <clone>  the clone the branch is cut in (default: the current directory)`, so outside a clone it should
   refuse with `promote REFUSED: --repo-dir <clone> wants ...` and a remedy that is not the
   identical command; the line is a raw git error whose `run:` repeats the call that just
   failed. (It was run in a scratch directory, not a clone.)
   Grade: NEXT (friction)

9. `nova-sprint help version`
   Printed:
   ```
   nova-sprint help REFUSED: unknown verb version; run: nova-sprint help
   (one line printed)
   ```
   I expected the help of a verb the tool has: `nova-sprint version` prints the version line
   and exits 0, and the bare command's own refusal lists `help, version` among the available
   verbs. `nova-sprint help help` answers the same "unknown verb help".
   Grade: NEXT (unclear help)

10. `nova-sprint card nope`
   Printed:
   ```
   nova-sprint card: no primary nope; run: nova-sprint where
   (one line printed)
   ```
   I expected the one refusal grammar, `nova-sprint card REFUSED: <reason>; run: <remedy>`,
   since the standard says the status word leads every line. `nova-sprint friend beat friend-a` (`nova-sprint friend beat: no friend friend-a on the friends table (friends: none): its row is nova-config's friend row; run: nova-sprint friend sync`),
   `nova-sprint stream remove s1` and `nova-sprint reader remove reader-a` print the same
   `verb: reason` shape without the word.
   Grade: NEXT (unclear help)

11. `nova-sprint needs --roots`
   Printed:
   ```
   (nothing printed)
   ```
   I expected a result line with its total. The same store answers `nova-sprint needs` with
   `NEEDS OK cards=0 dropped-or-absent=0`, and `needs --roots` with no roots prints not one
   byte at exit 0, so a caller cannot tell an empty result from a broken one.
   Grade: NEXT (friction)

## What held

The reads and local writes answered clean with their totals: `init`, `where` (text and
`--json`), `check`, `repair`, `stats`, `stats tidy --dry-run`, `routes`, `rules`, `bases`,
`needs`, `held`, `sentinels`, `streams` and `streams --cards`, `log` and `log --card`,
`card`, `card --all --json`, `inbox --read`, `queue`, `view coordinator`, `view cards`,
`view worker`, `handover`, `machinery`, `units --check`, `live`, `gc --dry-run`, `friend level`,
`fleet beat`, `fleet level`, `release check`, `preflight`, `promoted --dry-run`, `lane list`, `lane take`,
`lane give`, `answer --dry-run`, `snapshot`, `remind --in 5m`, `remind --list`,
`cost reprice --dry-run`, `reader add`/`set`/`away`/`up`/`retire`/`remove`, `stream set`/`archive`/`unarchive`, `fleet up`/`down`, `hold --dry-run`, `unhold --dry-run`,
`merge-window open`, `quack`, `set`, `stop`, `play --simulation --ticks 2`, `priority`,
`rank`, `brief`, `resolve`, `accept`, `ci`, `verify-landed`, `teardown --confirm sprint`,
and the whole one-card walk (`init`, `seat push --sent`, `seat pong`, `add --count 1 --one`,
`start`, `tick`, `take`, `finish`, `read --begin`, `read --ok`, `land`, `where`), which
landed a card on a twin once the seat proof was driven by hand with `seat push --sent` and
`seat pong`. `landed` and `ack` were run and refused by the state they found.

Read as `-h` only and never run, because this card starts no server and touches no host:
`run`, `dashboard`, `watch`, `server switch`, `demo`, `install`, `uninstall`, and the real
effect of `promote` (`promote --dry-run` alone was run).

Not run at all, because they need a config store (`--pg`) this card does not have:
`friend sync`, `fleet sync`, `friend clean`, `collect`, `fsck seat`.

READ 6/10 — the first fifteen lines answer what the tool does, how it works and where its state lives, and most verbs' `-h` carry usage, example, flags, effect and exit codes, but the first `-h` is a 680-line wall, the documented first run cannot run, `help version` and `help help` refuse verbs the tool has, and `seat push -h`/`seat pong -h` print the seat group's page whose effect line says it writes nothing.

USE 3/10 — the reads and the whole one-card flow land clean once the seat proof is forced by hand with `seat push --sent` and `seat pong`, a sequence the walkthrough does not name, but the advertised no-Redis learning path stops at its second command, `add`'s one named remedy `seat install` is refused on the same twin, and both `selftest` and `selftest land` fail.

urgent=4 next=7
