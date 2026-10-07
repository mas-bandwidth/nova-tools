# Friend onboarding

This guide is for an AI joining the Nova Tools team and the person helping it.
A **friend** is a named AI participant, including the coordinator, with a
`nova-config` friend row, a sprint friends-table entry, and a working directory
on the machine where it runs. The coordinator delivers work; a friend works
under its own identity and reports back through its outbox. The contracts are
also described in [FRIENDS.md](FRIENDS.md), [SPEC-CONFIG.md](SPEC-CONFIG.md),
[SPEC-FRIEND.md](SPEC-FRIEND.md), and [SPEC-SPRINT.md](SPEC-SPRINT.md).

## What the friend owes

The friend follows the brief and `JOB.md` before acting. It works only under
`jobs/<job>/`, changes only the named paths, does not force-push or rebase a
shared branch, does not start a server on a shared machine, and reports what it
did not do. It does not guess at a missing flag, branch, result shape, or
permission: it reads the named help or asks the coordinator.

Attribution is evidence, not a persona. Each commit and report names the friend
(`By: <friend>`) and the actual model and harness used for that work. Never
claim another model or harness; a commit must not add a co-author trailer for a
model that did not write it. A person helping the friend supplies access and
answers questions but does not silently become the author.

## Numbered path

Each step gives the command to run and the observable result that confirms it.
Values in angle brackets come from the coordinator or the current machine; do
not paste a placeholder unchanged.

1. **Create the permanent configuration store.** The coordinator supplies a
   password-free PostgreSQL DSN and its password through the configured secret
   environment, never in the command line. Migrate before adding rows:

   ```sh
   nova-config migrate --pg <dsn>
   ```

   **Worked:** output names the migrations applied and exits 0. If the schema
   is already current, the status says so and exits 0.

2. **Add the friend row.** Choose the slots and model tiers with the
   coordinator. `slots` is desired capacity, not work width; `width` is
   concurrent jobs. The initial row can use the defaults for `width` (8),
   `mode` (`batch`), and `token_cap` (6,000,000):

   ```sh
   nova-config friend add <friend> --slots 1 --tiers flash,pro --roles builder --actor <coordinator> --pg <dsn>
   nova-config friend show <friend> --pg <dsn>
   ```

   **Worked:** `CONFIG ADD` and `FRIEND name=...` show the saved values. A
   duplicate name or invalid field is refused without changing the row; use
   the named `nova-config friend ... -h` help rather than inventing a field.

3. **Apply the row and sync the sprint roster.** PostgreSQL is permanent
   configuration; apply copies the selected kind into the runtime Redis store.
   Friend sync brings that applied row into the sprint's friends table:

   ```sh
   nova-config apply --kind friend --actor <coordinator> --pg <dsn> --redis <sprint-redis-host:port>
   nova-sprint friend sync --pg <dsn>
   nova-sprint friend cards <friend> --json
   ```

   **Worked:** apply prints `CONFIG APPLY kind=friend`; sync reports the roster
   change or no work to do; `friend cards` returns the row and any cards held.
   The friend has no sprint assignment until the coordinator deals one.

4. **Choose the harness and start the daemon.** A regular daemon has a
   persistent, named session in the friend's working directory. On macOS,
   `install` writes and loads its launch agent; the default session is the
   harness's newest session in that directory, or pass `--session` to select
   one. On Linux, run `run` under the machine's supervisor. Do not use the
   macOS installer there.

   ```sh
   nova-friend install --as <friend> --harness opencode --dir ~/<name>-working --server <sprint-server-host:port> --redis <bus-redis-host:port>
   nova-friend status --as <friend> --dir ~/<name>-working
   ```

   On Linux, the supervised process uses the corresponding `run` verb:

   ```sh
   nova-friend run --as <friend> --harness opencode --dir ~/<name>-working --server <sprint-server-host:port> --redis <bus-redis-host:port> --deny-self <coordinator-self-directory>
   ```

   **Worked:** install says the agent was written and loaded. Status reports
   the daemon, harness, directory, and evidence; for a session harness, look
   for a recent session pong, not merely a running process. `nova-friend
   install -h` lists the harness settings it writes and the dry-run option.

5. **Select one-shot lanes when work is per-card.** `mode=one-shot` runs up to
   `width` card lanes. OpenCode opens a lane session and delivers one card per
   turn. Claude runs each card as a headless `claude -p` process and has no
   session to push into. A Claude row needs its own absolute `config_dir`; it
   identifies that account and is not shared between friends. Apply and sync
   row changes before expecting the daemon to use them:

   ```sh
   nova-config friend set <friend> --mode one-shot --width 2 --actor <coordinator> --pg <dsn>
   nova-config apply --kind friend --actor <coordinator> --pg <dsn> --redis <sprint-redis-host:port>
   nova-sprint friend sync --pg <dsn>
   nova-friend run --as <friend> --harness opencode --dir ~/<name>-working --server <sprint-server-host:port> --redis <bus-redis-host:port> --deny-self <coordinator-self-directory>
   ```

   For a Claude account, set its directory on the row before apply and sync:

   ```sh
   nova-config friend set <friend> --config_dir <absolute-claude-config-directory> --actor <coordinator> --pg <dsn>
   nova-config apply --kind friend --actor <coordinator> --pg <dsn> --redis <sprint-redis-host:port>
   nova-sprint friend sync --pg <dsn>
   nova-friend run --as <friend> --harness claude --dir ~/<name>-working --server <sprint-server-host:port> --redis <bus-redis-host:port> --deny-self <coordinator-self-directory>
   ```

   **Worked:** the daemon record and `nova-friend status` identify one-shot
   mode and its lane count. A Claude row without `config_dir` records a
   refusal and starts no lane. `token_cap` limits each card; set it to `0`
   only when the coordinator explicitly wants no cap. Batch is the default,
   and is not the same as one-shot.

6. **Understand the friend row before changing it.** `nova-config friend
   show <friend>` prints the fields and their current values:

   ```sh
   nova-config friend show <friend> --pg <dsn>
   nova-config friend set -h
   ```

   - `slots`: desired resource slots, charged against the machine ceiling.
   - `tiers`: comma-separated `flash`, `frontier`, `heavy`, and `pro` work the
     friend may receive.
   - `roles`: `builder`, `may-hold`, and/or `reader`; the coordinator role is
     selected by the sprint row, not this field.
   - `width`: simultaneous jobs for this friend, at least 1; default 8.
   - `mode`: `batch` (default, messages in one session turn) or `one-shot`
     (one card per lane turn).
   - `config_dir`: optional absolute Claude account directory; required for a
     Claude one-shot row.
   - `token_cap`: per-card token ceiling for one-shot lanes; default 6000000,
     and 0 means no cap.

   **Worked:** `friend show` prints the current values and the help output lists
   every generated field flag and its type.

7. **Know what the bus proves.** The bus is Redis streams: a send writes a
   message to each recipient and the log; a receive is pending until acked.
   The daemon acknowledges successful delivery to its session. A normal
   session message can be sent and inspected with:

   ```sh
   nova-bus send --as <friend> --to <coordinator> --subject "hello" --body "I am online"
   nova-bus peek --as <coordinator>
   nova-bus recv --as <coordinator> --max 1
   nova-bus ack --as <coordinator> --id <message-id>
   ```

   **Worked:** send prints a message id; peek distinguishes pending from new;
   recv prints that id and its sender; ack reports `acked=true`. In normal use
   the daemon receives and acknowledges on the friend's behalf. A message in
   the log is not proof the session read it.

8. **Answer a wake ping and read presence correctly.** A PING is a transport
   challenge. The daemon immediately replies `daemon-pong` and acknowledges
   it; that proves delivery, not that the AI session is awake. The session
   answers the nonce with `nova-friend pong`, which is the proof. The
   coordinator can test the round trip:

   ```sh
   nova-friend ping --as <coordinator> --to <friend> --nonce abc123 --wake --redis <bus-redis-host:port>
   nova-friend wait-pong --from <friend> --nonce abc123 --timeout 5m --redis <bus-redis-host:port>
   ```

   The daemon delivers the exact `nova-friend pong` line carried by the wake
   PING into the session. **Worked:** ping prints `PING OK nonce=abc123`; the
   session runs that line; wait-pong prints `WAIT-PONG OK` with the same
   nonce. The session command has this form:

   ```sh
   nova-friend pong --as <friend> --nonce abc123 --to <coordinator> --queue 0 --working 0 --width 1 --redis <bus-redis-host:port>
   ```

   Presence is up after a recent session answer to a wake challenge (within
   ten minutes), or a card finished within thirty minutes. A daemon beat,
   daemon-pong, or open application alone never makes a friend up. One-shot
   Claude has no session pong; its recent card finish is the presence evidence.
   Otherwise status reads down and gives the missing evidence and its age. A
   down friend keeps assigned work until the coordinator takes unstarted work
   back; no status verb silently moves cards.

   **Worked:** `nova-friend status --as <friend> --dir ~/<name>-working`
   prints `STATUS OK` with fresh evidence, or names the stale/missing proof.

   A coordinator can explicitly hold or restore the sprint roster entry; this
   does not replace session evidence and does not automatically move cards:

   ```sh
   nova-sprint friend down <friend> --reason "session unavailable"
   nova-sprint friend up <friend>
   ```

   **Worked:** each command reports the friend row change. A down row remains
   down until the coordinator brings it up; a session still needs fresh proof.

9. **Take a real card from the inbox through to the report.** The coordinator
   sends `inbox/<job>/BRIEF.md`; `inbox/QUEUE.json` is the daemon's queue view.
   The brief's `STATUS` line names the branch and the report destination. Read
   the whole brief and `JOB.md` first. Clone or make a worktree only inside
   `jobs/<job>/`; keep build output there and use the friend's shared
   `.cache/go-build`. Follow `PATHS`, tests, bench and commit instructions
   exactly. Push only the named branch, never force-push or rebase it.

   The usual layout is:

   ```text
   <name>-working/
   ├── inbox/QUEUE.json
   ├── inbox/<job>/BRIEF.md
   ├── jobs/<job>/repo/             # clone or worktree and job-local output
   ├── outbox/<job>/REPORT.md
   ├── outbox/<job>/RESULT.md
   └── .nova-friend/                # daemon state; normally not edited by a card
   ```

    Check the current sprint assignment and its state with:

    ```sh
    nova-sprint friend cards <friend> --json
    ```

   **Worked:** the JSON lists the assignment; after a deal, the matching brief
   is under inbox. After the run, the sprint stops showing that card as working
   only when it has collected the report.

10. **Publish the two different records.** `REPORT.md` is the sprint-facing
    finish. A work card normally uses this exact front matter, then one
    paragraph saying what changed and what the gate did:

    ```text
    Verdict: LAND
    Head: <full 40-character commit sha>

    <one paragraph: change, exact gate result, and any work not done>
    ```

    Use `HOLD` when work needs a coordinator decision and `FAIL` when it could
    not be done; state the precise blocker and omit the head when nothing was
    pushed. Do not call unrun tests green. `RESULT.md` records the execution
    evidence, usually in the card contract's key/value shape:

    ```text
    head: <full commit sha>
    branch: <branch named by the brief>
    verdict: ok | not-done | nothing
    gate: <exact gate command, or ->
    output: <gate output path, or ->
    report: <one line>

    ## Body

    <evidence, including what was not done>
    ```

    The brief or `JOB.md` can require a narrower result schema; that specific
    contract wins. `RESULT.md` is not a replacement for `REPORT.md`. Include
    `By: <friend>` and the actual model and harness in the commit and report. A
    one-shot lane takes the next card only after it sees the result, and after
    two turns without one it sets the card aside and tells the coordinator.

    **Worked:** the report's head is the pushed branch tip and the verdict is
    one of the brief's accepted words. The sprint's friend-card view shows the
    finish or the reason it remains held:

    ```sh
    nova-sprint friend cards <friend> --json
    ```

11. **Try a first card on an isolated twin.** This is a local learning store,
    not the fleet's Redis or a real assignment. It has no machine running
    between commands; the friend runs each tick by hand. In one shell:

    ```sh
    export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=<your-actor>
    nova-sprint init --readers <reader-one>,<reader-two> --members <worker>
    nova-sprint add --stream practice --count 1 --one
    nova-sprint start
    nova-sprint tick
    nova-sprint tick
    nova-sprint queue --as <worker>
    nova-sprint take --as <worker> --epoch 0
    nova-sprint finish --as <worker> <card>@<generation> --epoch 0 --report "practice finish"
    nova-sprint tick
    nova-sprint read --as <reader-one> --begin --epoch 0
    nova-sprint read --as <reader-one> --ok --epoch 0
    nova-sprint tick
    nova-sprint merge --stream practice --batch 1
    nova-sprint tick
    nova-sprint where
    ```

    Copy the card id and generation printed by `queue`/`take` into the finish
    line. The twin's synthetic finish has no pushed Git head and its merge is a
    simulation; it is not how a delivered friend card is reported.

    **Worked:** each verb exits 0 and prints its `MOVED`, `READ`, or `MERGE`
    result; the final `where` shows the card landed. `mem:sprint.twin` is a
    twin file that persists between invocations. Do not point a live fleet at
    a twin; run one command at a time.

12. **Recover without guessing.** For any refusal, run the named help. Check
    the row, the roster, the exact STATUS branch, the harness session, and the
    last output before retrying. After a provider pause has been resolved, a
    person can clear the one-shot lane marker:

    ```sh
    nova-friend resume --as <friend>
    nova-friend status --as <friend> --dir ~/<name>-working
    nova-sprint friend cards <friend> --json
    ```

    If the friend is blocked on a coordinator decision, leave the card
    untouched and send a concise bus message:

    ```sh
    nova-bus send --as <friend> --to <coordinator> --subject "blocked: <job>" --body "<precise blocker and the decision needed>"
    ```

    If work cannot continue, write the required `HOLD` or `FAIL` report with
    the precise reason and say what was not done. **Worked:** resume reports the
    pause it cleared (or `cleared=none`); status gives fresh evidence; the bus
    prints a message id that the coordinator can inspect with `nova-bus log`.

For command flags and usage, `nova-config friend add -h`, `nova-friend help`,
`nova-bus help`, and `nova-sprint help friend` are the source of truth. The
guide's command test checks every fenced `nova-*` line against tool verb and
flag declarations without running a binary.
