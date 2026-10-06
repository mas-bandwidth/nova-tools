# nova-sprint dogfood — antigravity (opencode), 2026-10-06

Read as a stranger: only `nova-sprint -h`, `nova-sprint help`, `nova-sprint
<verb> -h` and the page under `docs/` (SPEC-SPRINT.md, read for the verbs I
was about to use, not cover to cover). Built from the staged checkout at
df30ce088344588bf75a86168057f468710ae943 (the v1.1.0 base,
sprint/mechanical-2026-10-02) on a Linux bench and used as
`nova-sprint devel linux/amd64 go1.26.6`: every verb at least once with its
real flags against `mem:<file>` twins and a scratch git repository (an empty
bare origin and a clone, as the help's real-flow example describes), the
refusals too, ~35 minutes of use. No live store, no server, no network.

## Findings

1. The first-run flow `nova-sprint help` prints under "trying it without a
   Redis" refuses at its second line, and the refusal's remedy refuses on a
   twin. With `NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss`
   exported exactly as the banner says, `nova-sprint init` runs and then:

   ```
   nova-sprint add --stream s1 --count 1 --one
   nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   [exit 2]
   ```

   Following the remedy, `nova-sprint seat install --harness opencode
   --target ./seat-target` itself refuses: "the push loop waits on the
   sprint's machine, and the in-memory twin mem:sprint.twin has none: install
   it for a Redis store or the sprint's server". The twin flow is only
   completed by `seat push --harness ... --target ...`, `seat push --sent
   <nonce>` and `seat pong <nonce>` — three verbs no output names — after
   which `add`, `start` and the whole card flow run as printed. The same wall
   stops the git-backed example block at its `add`, `start` and `land` lines.
   The banner's exit table says a refusal is exit 1; this one exits 2.
   I expected the example block's lines to run (an example exiting 2 is a
   broken example), or the refusal to name a remedy that runs on a twin.
   Grade: URGENT.

2. `nova-sprint selftest` — the install gate the help points an installer at —
   fails on a fresh twin:

   ```
   nova-sprint selftest --keep
   SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir> dir=.../nova-sprint-selftest-585233933
   [exit 1]
   ```

   The selftest makes its own twin and its own actor; nothing arms the seat's
   push proof there, so the walkthrough stops at `add`. I expected
   `SELFTEST OK landed=1 ...` on a good build at the release base.
   Grade: URGENT.

3. `nova-sprint selftest land --binary <path>` fails on a command the gate
   itself wrote — the binary under test refuses the gate's own canned line:

   ```
   nova-sprint selftest land --binary ../bin/nova-sprint
   nova-sprint selftest land FAILED: selftest land: command [add --stream selftest --count 1 --brief-file .../canned-brief.md --redis mem:.../selftest.twin --actor boss] failed: nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream selftest --brief-dir <dir>; or say --one for a single card; run: nova-sprint add -h: exit status 2
   ```

   I expected the gate to prove the binary, not be refused by it. (Behind the
   `--one` refusal the push gate of finding 2 waits for the same `add`.)
   Grade: URGENT.

4. An `add` on a RUNNING machine says `ADD OK` with `MOVED` lines, but the
   change waits for the next tick, and on a twin the tick is by hand — so the
   reads that follow say nothing is there:

   ```
   nova-sprint add --stream rel2 --sentinel rel-2 --needs api-4
   MOVED sentinel rel-2 -> waiting stream=rel2 score=26
   ADD OK stream=rel2 cards=1 before=- moved=1 refused=0 notes=0 op=add-t73-1
   nova-sprint sentinels
   SENTINELS OK sentinels=0
   ```

   `card rel-2` answered "no primary rel-2" and `where` counted the stream's
   waiting column 0 until a hand tick moved the queued add in. I expected the
   add to say the change is queued for the next tick (a NOTE), or the reads to
   name it; as printed the verb claims a move the table does not hold.
   Grade: NEXT.

5. On a STOPPED machine `take` blames the member for the machine's state:

   ```
   nova-sprint take --as m1 --epoch 0
   TAKE OK moved=0 refused=0 notes=0
   NOTE m1 took 0 of the 1 asked: it is down, and only a member up takes
   STOPPED
   ```

   The fleet table printed `m1 ... up` in the same minute, and the store held
   one ready card (an empty store still said "the 1 asked"). I expected the
   note to name the blocker (the machine is STOPPED) and to count what was
   actually askable. Grade: NEXT.

6. `version` parses no flags: unknown flags are silent and `--json` changes
   nothing:

   ```
   nova-sprint version --nope
   nova-sprint devel linux/amd64 go1.26.6
   [exit 0]
   ```

   I expected `version REFUSED: unknown flag --nope ...` as `init --nope`
   answers at exit 2. Grade: NEXT.

7. A `take` naming a bare card id reports a want already satisfied rather
   than the missing part:

   ```
   nova-sprint take --as m1 api-99 --epoch 0
   nova-sprint take REFUSED: wants --as <member>, and every card named as <card>@<gen>, the generation from queue --as <member>; run: nova-sprint take -h
   [exit 2]
   ```

   `--as m1` was given; what was missing is the `@<gen>` suffix on `api-99`.
   I expected the refusal to name the card that is not in `<card>@<gen>`
   form. Grade: NEXT.

8. A `read` as a name that is no reader still prints OK at exit 0:

   ```
   nova-sprint read --as nobody --begin --epoch 0
   READ OK moved=0 refused=0 notes=0
   NOTE nobody read nothing: it is no reader of the readers table (readers: reader-a,reader-b,reader-c); run: nova-sprint reader add nobody
   ```

   The NOTE is honest and names the readers there are, but the word OK and
   exit 0 let a typo'd reader name pass as a successful turn. I expected a
   refusal at exit 1 for a name that is no row. Grade: NEXT.

9. Empty results print nothing at all where every other read prints a summary:

   ```
   nova-sprint where --release
   [exit 0]
   ```

   `needs --roots` on a store with no roots is silent the same way, while
   `held` answers `HELD OK cards=0`. I expected one OK line carrying the
   total, so a silent exit 0 cannot read as "the flag did nothing".
   Grade: NEXT.

10. `promote --dry-run` fails with an empty git error and a remedy that is
    the command already run:

    ```
    nova-sprint promote --repo-dir work --base sprint/s1 --every 1h --dry-run
    nova-sprint promote: git rev-parse: ; run: nova-sprint promote --dry-run
    [exit 1]
    ```

    I expected the line to say what `git rev-parse` was asked and what it
    answered (the clone held the base branch), and a remedy I had not just
    run. Grade: NEXT.

11. On Linux, `machinery` and `seat check` print macOS-only remedies:

    ```
    MACHINERY loop DOWN tick_age=15s ticks=5 why="silent past 15s" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-<host>"
    ```

    `launchctl` does not exist on Linux; `internal/units` itself loads
    `systemctl --user` there (the host's own name is elided above). I expected
    the remedy to name the loader the platform has. Grade: NEXT.

12. `backup --out` refuses without a nova-secrets seat, while `backup --file`
    works on the same store:

    ```
    nova-sprint backup --out parts --part-bytes 65536
    nova-sprint backup FAILED: the secret scan did not run: the scan wants the nova-secrets seat: --secrets-store, --secrets-as, --secrets-key and --sops, or a login recorded by nova-sprint seat login
    [exit 1]
    ```

    A scratch or small sprint has no multi-part backup path at all. I expected
    the two forms to agree (the `--file` run printed `secrets=none`), or the
    help to say `--out` needs the seat. Grade: NEXT.

13. `snapshot --restore-drill` refuses the snapshot `snapshot --dir` itself
    wrote on a twin:

    ```
    nova-sprint snapshot --restore-drill snaps/snapshot-20261006T205025Z.rdb
    nova-sprint snapshot REFUSED: snaps/snapshot-20261006T205025Z.rdb does not load into a twin: not an RDB (no REDIS header); run: nova-sprint snapshot -h
    [exit 2]
    ```

    The snapshot line said `verified=checksum+twin`. I expected the tool's
    own snapshot to be drillable by the verb made to drill it, or the snapshot
    line to say its file is not one the drill takes. Grade: NEXT.

## What the tool got right

- The twin's own refusals are immediate and exact: `where --watch`,
  `inbox --wait` and `run` on a twin each refuse with "a mem twin has no
  machine running between commands: run nova-sprint tick to tick it by hand".
- The real card flow lands for real: with a bare repository standing for the
  forge, `land --stream s1 --repo-dir work --base sprint/s1` merged the
  card's head `--no-ff`, pushed, and `git ls-remote origin` showed the
  landing on the sprint branch.
- `--op` replay returned the recorded result and changed nothing
  (`ADD OK ... op=dogfood-op-1-1 replay=yes`).
- Bounded output keeps its totals and its widener: `queue --as m1 --max 2`
  ends with "and 8 more; --max 0 lists all", a tick's move list ends with
  `MORE kind=moved shown=20 total=42`.
- Refusals name what the input wants, not only what was wrong:
  `install server` names `--listen`, a stale `finish` names the live
  generation, `ack` of a judgment prints that judgment's own decisions, and a
  lint-failing brief prints the lint's drift lines with their remedies.
- The exit table in `-h` matched what the verbs did everywhere I could check
  it except the PUSH DOWN wall (finding 1) and `version` (finding 6).
- `where`'s text table and `where --json` told the same story, and the
  machine's own verbs (`machinery`, `seat check`, `units --check`) report
  what is down with a remedy per line rather than hiding it.

READ 6/10 — the banner and every verb's `-h` are unusually complete (usage,
examples, exit tables, remedies), but the first-run flow the banner prints
refuses at its second line, and the first two refusals a stranger meets point
at remedies that refuse on the very store the banner told them to use.

USE 6/10 — past the push wall the sprint moved cards end to end exactly as
documented, with crisp refusals, honest NOTEs and a real landing through git;
what keeps it down is that the machine's own install gates (`selftest`,
`selftest land`) are red at this tip, so a fresh install cannot prove itself.

urgent=3 next=10
