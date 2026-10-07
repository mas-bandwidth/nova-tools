# nova-sprint dogfood, 2026-10-06 (grok)

Tool: nova-sprint. Build: `nova-sprint v1.0.1-0.20261007150632-34db18e07dff linux/amd64 go1.26.6`, from the staged checkout at
`34db18e07dff90aba3d2f8dd4b699e72e969bec8` (BASE `sprint/mechanical-2026-10-02`).
Read as a stranger from the tool's own doors only: `nova-sprint`, `nova-sprint -h`,
`nova-sprint help`, `nova-sprint help <verb>`, `nova-sprint <verb> -h`, the group
doors (`help fleet`, `help friend`, `help reader`, `help goal`, `help stream`,
`help lane`, `help merge-window`) and its page under docs/ (`docs/CLI.md`,
"nova-sprint", and `docs/SPEC-SPRINT.md`). Every store call was the in-memory twin,
`--redis mem:<file>` / `NOVA_SPRINT_REDIS=mem:<file>`; no Redis, no live store, no
server on the working machine. Every top-level verb in `nova-sprint help` was run at
least once against a scratch twin or a temp dir, refusals included; the whole card flow
was then run end to end with git (`add`, `start`, `tick`, `take`, `finish --head`,
`read`, `accept`, `merge`, `land`, `verify-landed`, `where`). No code was changed.
One setup no page names, the seat's push proof, was driven by hand where it was
needed: `seat push --harness claude --target <dir>`, then `seat push --sent <nonce>`,
then `seat pong <nonce>`; with that proof recorded once, the rest of the flow runs.

## Findings

1. The first run the tool's own page promises is refused at its first writing verb,
   and the remedy the refusal names cannot run on a twin either.

   **Command** (`docs/CLI.md` "First run", the banner's "trying it without a Redis";
   run on a fresh twin):
   ```
   export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
   nova-sprint init --readers reader-a,reader-b --members m1
   nova-sprint add --stream s1 --count 1 --one
   ```
   **Printed** (the `add`, on stderr, exit 2):
   ```
   nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   **Expected:** `ADD OK stream=s1 cards=1 ...` and the flow to run as printed.
   Instead the second command stops it. The named remedy is refused on the same
   store:
   ```
   nova-sprint seat install REFUSED: the push loop waits on the sprint's machine, and the in-memory twin mem:seat.twin has none: install it for a Redis store or the sprint's server; nothing was written; run: nova-sprint seat install -h
   ```
   Neither page names the push proof, and there is no way to record one from the
   pages alone. Grade: URGENT.

2. The same gate is on the coordinator's writes, so a twin never leaves STOPPED and
   the card flow above `add` cannot be reached. On a fresh twin the writes my sweep
   ran that hit it are `accept`, `ack`, `add`, `ask`, `brief`, `clear`, `collect`,
   `cost reconcile`, `drop`, `fleet up`, `fleet level`, `friend level`, `funded`,
   `hold`, `land`, `merge-window open`, `move`, `play`, `priority`, `promoted`,
   `quack` (through its own `add`), `rank`, `reader add`, `rebase`, `redo`, `relink`,
   `repair`, `resolve`, `resume`, `return`, `rework`, `sentinel set`, `set`, `start`,
   `stop`, `stream remove`, `teardown`, `unhold`, `unpin` and `wait`; each prints its
   own name and the same remainder:
   ```
   nova-sprint <verb> REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   `nova-sprint start` is among them, so a twin stays `STOPPED` and `take`/`finish`/
   `read`/`merge` are unreachable. Only `init`, `fleet beat`, `remind`, `snapshot`,
   `answer --dry-run`, the hand-run `seat push`/`pong` and the reads moved on a fresh
   twin; with the proof recorded once, the same writes ran. Grade: URGENT.

3. The tool's own self-tests cannot pass at this tip.
   ```
   $ nova-sprint selftest --dir selftest1 --keep
   SELFTEST FAILED step=add why=nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir> dir=selftest1/nova-sprint-selftest-1613125952
   ```
   ```
   $ nova-sprint selftest land
   nova-sprint selftest land FAILED: selftest land: command [add --stream selftest --count 1 --brief-file /tmp/nova-sprint-selftest-3420185969/canned-brief.md --redis mem:/tmp/nova-sprint-selftest-3420185969/selftest.twin --actor boss] failed: nova-sprint add REFUSED: one card at a time is the mistake; put the briefs in a directory and run: nova-sprint add --stream selftest --brief-dir <dir>; or say --one for a single card; run: nova-sprint add -h: exit status 2
   ```
   The first is the push gate; the second is the self-test's own command being refused
   by the tool's own "say `--one`" rule. `selftest land --scratch-dir <an existing empty dir>` fails a third way: `nova-sprint selftest land FAILED: selftest land: write README.md failed: open selftest2/work/README.md: no such file or directory`.
   Expected: `SELFTEST OK`. Grade: URGENT.

4. Seven verbs advertise `--dry-run` in their usage lines (both the top-level help and
   the verb's own `-h`) and refuse it as an unknown flag, so the standard's "a verb
   that writes has a dry run" is help that lies.
   ```
   $ nova-sprint recut s1-1 --tier pro --dry-run
   nova-sprint recut REFUSED: unknown flag --dry-run; the flags of recut are --actor, --brief-file, --epoch, --json, --max, --new, --op, --redis, --repo-dir, --rules, --tier, --widen; run: nova-sprint help recut
   ```
   The same refusal, naming each verb's own flags, comes from
   `brief s1-1 --brief x --dry-run`, `drop s1-1 --reason why --dry-run`,
   `return s1-1 --reason why --dry-run`, `rework s1-1 --dry-run`,
   `rank s1-1 --score 5 --dry-run` and `release s1 --reason why --dry-run`.
   Expected: the plan the flag promises, or a usage line that does not promise it.
   Grade: URGENT.

5. The push gate runs before flag validation and before a dry run, so a plain usage
   mistake is reported as a machine being down, and a dry run that should read the
   store only is refused.
   ```
   $ nova-sprint priority s1-1 --high
   nova-sprint priority REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
   ```
   (`priority` wants `--reason`; the refusal should name that.) `land --stream s1 --dry-run`, `cost reconcile --dry-run` and `rebase --from a --to b --dry-run` print
   the same line on a fresh twin, though the help calls the last a local write and the
   first "reads the store only: no git, no push, no report".
   Grade: NEXT.

6. `play` refuses on a twin with a remedy the twin also refuses.
   ```
   $ nova-sprint play --simulation --ticks 3 --seed 1
   nova-sprint play: no machine is running (machine: STOPPED): the driver plays only the outside actors; run: nova-sprint start, and nova-sprint run
   ```
   `nova-sprint run` answers "a mem twin has no machine running between commands: run
   nova-sprint tick to tick it by hand", and `start` is refused by finding 2. The one
   machine the twin does have, `tick`, is not named. Grade: NEXT.

7. `backup --out <dir>` cannot run on a plain twin although the usage presents the
   secrets flags as optional.
   ```
   $ nova-sprint backup --out /tmp/bk2
   nova-sprint backup FAILED: the secret scan did not run: the scan wants the nova-secrets seat: --secrets-store, --secrets-as, --secrets-key and --sops, or a login recorded by nova-sprint seat login
   ```
   The usage line is `backup (--out <dir> [...] | --file <path> [--dry-run])`; a reader
   is told the scan is optional and then told it is mandatory, with no "run" line.
   Grade: NEXT.

8. `nova-sprint help -h` refuses while every other verb answers `-h`.
   ```
   $ nova-sprint help -h
   nova-sprint help REFUSED: unknown verb -h; run: nova-sprint help
   ```
   `help` is the door, and `<tool> help <verb>` is the standard spelling; a stranger
   asking the door for its own help gets an unknown-verb refusal. Grade: NEXT.

9. Several exit-1 failures do not lead with the `REFUSED`/`FAILED` status word the
   tool's own standard requires (after the verb's name, the first word is the status).
   ```
   $ nova-sprint where                 # fresh store, after clear/teardown
   nova-sprint where: this store: no sprint here yet: init makes its tables; run: nova-sprint init --coordinator <name>
   ```
   ```
   $ nova-sprint stream archive s1
   nova-sprint stream archive: s1: stream s1 holds 3 cards not landed (s1-1 working, s1-2 working, s1-3 working); nothing was changed; a stream is archived when every card of it has landed; run: nova-sprint help stream
   ```
   `stream remove`, `promote`, `card <id> --brief`, `handover`, `view coordinator`
   and `answer --dry-run` on a fresh store print the same shapeless exit-1 line.
   Expected: `VERB REFUSED: <reason>; run: <remedy>` like the rest of the tool.
   Grade: NEXT.

10. `dashboard --listen none` still starts the pull loop and prints a pull route,
    though `none` means "no page" and no `--pull` was given.
    ``` $ nova-sprint dashboard --listen none DASHBOARD pull routes on http://127.0.0.1:7395/ (friend/<name>, machine/<name>, team, api/..., events/...) DASHBOARD STOP interrupted ```
    Run under a timeout on the bench and interrupted; it read a twin, never a live
    store. Expected: nothing listening when the page is `none`. Grade: NEXT.

11. `machinery` and `seat check` print a macOS remedy on Linux.
    ``` $ nova-sprint machinery MACHINERY server DOWN addr=none why="NOVA_SPRINT_SERVER is not set" remedy="export NOVA_SPRINT_SERVER=127.0.0.1:$(nova-config loop show sprint-server-<machine> | sed -n 's/.*--listen [^:]*:\([0-9]*\).*/\1/p')" MACHINERY store OK redis=mem:st.twin dbsize=- machine=stopped epoch=0 MACHINERY loop DOWN tick=never why="no heartbeat: run --listen has not ticked this store" remedy="launchctl kickstart -k gui/$(id -u)/com.nova.loop.sprint-server-<machine>" ```
    The build is `linux/amd64` and its install/units doors write systemd units
    (`~/.config/systemd/user/nova-sprint-server.service`), but the loop remedy is
    `launchctl`. The loop's own name carries the machine and is written `<machine>`
    here. Expected: the platform's own restart command, as `units` already
    names. Grade: NEXT.

READ 5/10: the banner, the noun glossary and every verb's help are unusually complete
and honest about effects, and one refusal (`unknown flag`) lists the flags it does
take, but the usage lines promise a `--dry-run` seven verbs refuse and the first-run
page's flow is not runnable as printed.

USE 2/10: with the seat push proof hand-driven the whole card flow runs and the reads
are fast and clear, but out of the box the documented twin flow cannot add a card,
start the machine, or pass the tool's own selftest, and the remedy every refusal names
cannot run on a twin.

urgent=4 next=7
