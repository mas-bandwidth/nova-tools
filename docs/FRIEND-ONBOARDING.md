# Friend onboarding

This numbered walkthrough takes an AI friend and the person helping it from a
new friend row to a finished first sprint card. A friend is an AI participant
with its own identity, a `nova-config` friend row, a row in the sprint friends
table, and a working directory on the machine running its harness. The
coordinator is a friend too.

## 1. Create and inspect the friend row

Start with a local config file to learn the row without changing a fleet. These
commands are `nova-config` commands; this file is not the sprint twin used in
step 10.

```sh
nova-config migrate --file friend-config.json
nova-config friend add friend-claude --slots 2 --tiers flash,pro --roles builder,reader --width 2 --mode one-shot --config_dir "$HOME/.claude-friend" --token_cap 6000000 --as coordinator --file friend-config.json
nova-config friend add friend-opencode --slots 2 --tiers flash,pro --roles builder,reader --width 2 --mode one-shot --token_cap 6000000 --as coordinator --file friend-config.json
nova-config friend show friend-claude --file friend-config.json
nova-config friend show friend-opencode --file friend-config.json
```

Worked when: migrate reports success, the add reports a friend row written,
and show prints `friend-claude` with the values just supplied.

The friend row fields are:

- `--slots`: desired number of card slots, within the ceiling of the machine
  reporting the friend; this is not `--width`.
- `--tiers`: comma-separated card tiers the friend can take: `flash`, `pro`,
  `heavy`, or `frontier`.
- `--roles`: comma-separated roles such as `builder`, `reader`, or
  `may-hold`; the sprint's coordinator is selected separately by the sprint
  row.
- `--width`: number of cards worked at once. It defaults to 8 and is
  independent of slots.
- `--mode`: `batch` for the daemon's persistent session, or `one-shot` for
  per-card lanes.
- `--config_dir`: the absolute account configuration directory used by a
  Claude one-shot lane as `CLAUDE_CONFIG_DIR`. Leave it unset for OpenCode or
  another harness. The CLI field uses an underscore.
- `--token_cap`: one-shot lane token ceiling per card; the default is
  6,000,000 and `0` means no cap.

For a live fleet, the coordinator makes the same row in the Postgres config
store and applies that configuration to its Redis copy. `nova-config --file`
above is only a local practice store; applying it does not create a sprint or
populate a sprint twin.

For these live commands, set `NOVA_SPRINT_REDIS` and `NOVA_SPRINT_ACTOR` to
the sprint store and coordinator seat, and set `NOVA_BUS_REDIS` to the bus
store. The bus and sprint stores may be different Redis instances.

```sh
nova-config friend add friend-a --slots 1 --tiers flash --roles builder --width 1 --mode batch --as coordinator --pg "$NOVA_PG_DSN"
nova-config friend add friend-opencode --slots 2 --tiers flash,pro --roles builder,reader --width 2 --mode one-shot --token_cap 6000000 --as coordinator --pg "$NOVA_PG_DSN"
nova-config friend add friend-claude --slots 2 --tiers flash,pro --roles builder,reader --width 2 --mode one-shot --config_dir "$HOME/.claude-friend" --token_cap 6000000 --as coordinator --pg "$NOVA_PG_DSN"
nova-config apply --kind friend --as coordinator --pg "$NOVA_PG_DSN" --redis "$NOVA_SPRINT_REDIS"
nova-config friend show friend-a --pg "$NOVA_PG_DSN"
nova-sprint friend sync --pg "$NOVA_PG_DSN"
nova-sprint where
```

Worked when: the adds write their rows, apply prints `CONFIG APPLY kind=friend`
with its add/set/remove counts, sync reports `FRIEND-SYNC OK`, and where shows
the configured friends in the sprint table.

## 2. Choose the harness and start it

A daemon friend has one persistent session. The daemon parks on the friend's
bus stream, sends messages into that session as turns, and beats to the sprint
server while the session answers. On macOS, install the agent and harness
settings with `nova-friend install`:

```sh
nova-friend install --as friend-a --harness opencode --dir "$HOME/friend-a-working" --server "$NOVA_SPRINT_SERVER" --redis "$NOVA_BUS_REDIS"
```

Worked when: output includes `INSTALL WROTE harness=opencode`, and the agent's
delivery check reports whether the session answered. `nova-friend check
--settings --as friend-a --harness opencode --dir "$HOME/friend-a-working"`
checks the settings later without delivering a turn.

One-shot mode also uses `nova-friend run`, but the card is a lane's own
one-shot process rather than a turn in a persistent session. The row must have
`--mode one-shot` applied first. OpenCode lanes use the OpenCode harness and
its own session record. Claude lanes run `claude -p` once per card and use the
friend row's `config_dir`; a Claude one-shot friend does not use the daemon
session install path.

```sh
nova-friend run --as friend-opencode --harness opencode --dir "$HOME/friend-opencode-working" --server "$NOVA_SPRINT_SERVER" --redis "$NOVA_BUS_REDIS" --dry-run
nova-friend run --as friend-claude --harness claude --dir "$HOME/friend-claude-working" --server "$NOVA_SPRINT_SERVER" --redis "$NOVA_BUS_REDIS" --config-dir "$HOME/.claude-friend" --dry-run
```

Worked when: each prints `RUN DRY-RUN` with the requested friend, harness,
directory, and bus store; no process starts until `--dry-run` is removed.
Without dry-run, `run` stays up to serve the lanes until stopped. The
one-shot row's width bounds concurrent lanes, and its token cap holds a card
when reached.

## 3. Keep the inbox, work, and outbox separate

The coordinator delivers a job as `inbox/<job>/BRIEF.md`. Read the whole brief,
especially its `STATUS` line, rules, allowed paths, commands, and finish
contract. That line names the branch; use it rather than guessing. Keep the
checkout and every build artifact under `jobs/<job>/`, never beside the brief.
Keep one build cache under `.cache/go-build`.

```text
<friend-working>/
  inbox/<job>/BRIEF.md
  jobs/<job>/repo/       checkout and work for this job
  outbox/<job>/REPORT.md verdict, head, change summary, and gate results
  outbox/<job>/RESULT.md the exact RESULT line requested by this card
  .cache/go-build/       shared build cache
```

Worked when: the brief stays unchanged in inbox, the checkout is under
`jobs/<job>/repo`, and reports for that job go under `outbox/<job>/`.

`REPORT.md` starts with `Verdict: LAND` and `Head: <full 40-hex commit>` when
the work is ready for review, followed by one paragraph describing the change
and exact gates. For `HOLD` or `FAIL`, state the blocker and leave the head
blank as the brief directs. `RESULT.md` carries the card's exact `RESULT:` line
when it asks for one. The report is not a replacement for a commit, a push to
the named branch, or any card-specific result file.

The friend owes the brief's rules: work only in its named job directory and
PATHS, do not force-push or rebase a shared branch, and say what was not done.
Attribute honestly. The commit trailer names the friend (`By: <friend-name>`),
not a model. The report names the friend, actual model, and harness used; do
not claim a model, harness, test, or gate that did not run.

## 4. Send and receive a bus message

The bus is Redis streams, separate from the sprint store unless the fleet
deliberately configures them alike. `send` writes one message to every
recipient's stream and the log. A received message stays pending until acked;
the daemon delivers it to the session and a successful turn acknowledges it.
For a manual receive, `--ack` acknowledges after printing. Use `--body`, not
`--message`.

```sh
nova-bus send --as coordinator --to friend-a --subject "first contact" --body "Reply when ready." --redis "$NOVA_BUS_REDIS"
nova-bus recv --as friend-a --max 1 --ack --redis "$NOVA_BUS_REDIS"
```

Worked when: send prints `SEND OK id=...` and recv prints `RECV OK` with the
same message id, followed by the body. A send or receive can refuse a name
whose inbox push has not been proven; start the friend's daemon or lane first
and inspect `nova-bus names` for its push status.

## 5. Prove the session is answering

Ping has two distinct answers. The daemon immediately sends `daemon-pong`,
proving transport only. With `--wake`, it asks an idle session to run the
nonce-bearing `nova-friend pong` line as a turn. Only that session pong proves
the AI is present; `wait-pong` reports both the session answer and whether a
daemon-pong also arrived.

```sh
nova-friend ping --as coordinator --to friend-a --wake --nonce abc123 --redis "$NOVA_BUS_REDIS"
nova-friend pong --as friend-a --nonce abc123 --to coordinator --queue 0 --working 0 --width 1 --redis "$NOVA_BUS_REDIS"
nova-friend wait-pong --from friend-a --nonce abc123 --timeout 5m --redis "$NOVA_BUS_REDIS"
```

Worked when: ping prints `PING OK nonce=abc123`, the friend session's pong
prints `PONG OK nonce=abc123`, and wait-pong prints `WAIT-PONG OK` for
`friend-a` and that nonce. `daemon=true` is additional transport evidence,
not a substitute for the session answer. The daemon runs the pong line from
the wake turn. The middle line is the command delivered for the friend session
to run; do not run it in the coordinator's shell.

## 6. Read up and down from evidence

Check the local daemon and session evidence, not just whether a process exists.
No session answer means the friend is down even if the daemon continues to
answer transport pings. A recent session wake pong or a recently finished
card is evidence for up. The coordinator can place a friend on hold or release
that hold explicitly:

```sh
nova-friend status --as friend-a --dir "$HOME/friend-a-working"
nova-sprint friend down friend-a --reason "session is not answering" --actor coordinator --redis "$NOVA_SPRINT_REDIS"
nova-sprint friend up friend-a --actor coordinator --redis "$NOVA_SPRINT_REDIS"
```

Worked when: status prints `STATUS OK` and its evidence includes
`presence=up|down`; down and up each report the friend hold transition. A hold
is administrative and is not itself proof of session presence.

## 7. Prepare a real first-card brief

The sprint checks briefs before dealing them. Start with the card template,
replace every placeholder with the repository, base, task, steps, test, rules,
and PATHS for the work, and keep the allowed paths narrow. The RESULT line in
the template becomes the line written to RESULT.md.

```sh
nova-swarm template --name card > first-card.md
```

Worked when: `first-card.md` contains the template's `RESULT:`, `REPO:`,
`BASE:`, task, step, and rule sections. Fill those values before linting.

```sh
nova-swarm lint --card first-card.md --child-rules
```

Worked when: lint exits 0 with `LINT OK` and no NOTE naming an unfilled
placeholder. A refusal means fix the brief before adding it to the sprint.

## 8. Make a local repository for the twin exercise

The sprint twin and the example repository are local files. These Git
commands create a bare local origin and a worker checkout; they contact no
remote service. In a real job, the STATUS branch and the repository in the
brief replace these practice names.

```sh
git init -q --bare origin.git
git clone -q origin.git work
printf '# First card\n' > work/README.md
git -C work add README.md
git -C work commit -q -m base
git -C work push -q origin HEAD:sprint/s1
```

Worked when: `git -C work rev-parse HEAD` prints a full commit id and the
`sprint/s1` branch exists in the local bare origin.

For this practice card, fill `first-card.md` with `REPO: mas-bandwidth/nova-tools`,
`BASE: sprint/s1`, `PATHS: README.md`, and a task to add `First card complete.`
to that file. Set its test to `grep -F 'First card complete.' README.md`, then
run step 7's lint again. In real onboarding, use the actual repository and
base, and do not widen the brief's PATHS to make a change convenient.

## 9. Start the sprint twin and add the brief

`NOVA_SPRINT_REDIS=mem:<file>` selects nova-sprint's in-memory file twin. It
is for learning and tests, not a live fleet; it needs no Redis server. Run one
command at a time in the same shell so the environment and saved twin file
carry between commands. Config setup from step 1 is separate: do not point
`nova-config apply` at this twin.

```sh
export NOVA_SPRINT_REDIS=mem:friend-first.twin NOVA_SPRINT_ACTOR=boss
nova-sprint init --readers reader-a,reader-b --members friend-a
nova-sprint add --stream s1 --count 1 --one --brief-file first-card.md
```

Worked when: init prints `INIT OK tables=work,readers,merge,fleet`, and add
prints `ADD OK stream=s1 cards=1`. The card id in this clean twin is `s1-1`.

## 10. Start, tick, and take the card

A twin does not run a machine loop between commands. Start it, tick once to
record the member's presence, then tick again to deal the ready card.

```sh
nova-sprint start
nova-sprint tick
nova-sprint tick
nova-sprint take --as friend-a --epoch 0
```

Worked when: start prints `START OK`, the second tick prints a `MOVED deal:`
line for `s1-1`, and take prints `TAKE OK moved=1` with attempt `s1-1.w1`,
generation `1`, and branch `sprint/s1-1.w1.g1.e0`. Read and obey the packet's
branch, base, paths, and report instructions.

## 11. Do the work and finish with its pushed head

For the practice task, make the one allowed README change, inspect it, commit
it, and push the attempt branch to the local bare origin. A real card uses the
exact branch from its STATUS line instead.

```sh
printf '\nFirst card complete.\n' >> work/README.md
```

Worked when: diff shows only `README.md`, diff-check is silent, push succeeds,
and rev-parse prints the 40-hex head. Then report that head to the sprint:

```sh
nova-sprint finish --as friend-a s1-1.w1@1 --epoch 0 --head "$(git -C work rev-parse HEAD)" --report "Added the README line; grep -F 'First card complete.' README.md passed"
```

Worked when: the command prints `FINISH OK moved=1`. The complete SHA must
already be on the branch from the packet; do not claim a finish for an
unpushed or different head.

## 12. Read, land, and verify the first card

The next tick delivers the finished card to a reader. The reader begins and
approves it; the next tick accepts the read. `land` merges the pushed commit
into the local base branch, and a final tick updates the view.

```sh
nova-sprint tick
nova-sprint read --as reader-a --begin --epoch 0
nova-sprint read --as reader-a --ok --epoch 0
nova-sprint tick
nova-sprint land --stream s1 --repo-dir work --base sprint/s1
nova-sprint tick
nova-sprint where
```

Worked when: the reader commands print `READ OK`, land prints `LAND OK`, and
where shows `s1-1` landed with the sprint at `1/1` (100%). This exercise uses
a local bare Git origin but the disposable twin store; the `land` command is
the real Git merge. A no-Git `merge` is only a simulation and is not proof
that a commit landed.

## 13. When stuck

Read the refusal's remedy, then inspect that verb's flags with `nova-sprint
help <verb>`, `nova-config <kind> <verb> -h`, or the corresponding tool's
`help`. Check that sprint commands use `NOVA_SPRINT_REDIS=mem:<file>` for the
twin, while bus commands use the separately configured `NOVA_BUS_REDIS`.
Never apply a practice config file to the sprint twin. For a live fleet,
check the relevant store address and friend row, then ask the coordinator on
the bus:

```sh
nova-bus send --as friend-a --to coordinator --subject "help" --body "I am stuck at step 10: <specific refusal>" --redis "$NOVA_BUS_REDIS"
```

Worked when: send prints `SEND OK id=...`; quote the refusal, step, and the
last successful check so the coordinator can act on the actual blocker.
