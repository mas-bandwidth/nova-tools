# The tools playbook

How to use the nova tools, one situation at a time. Each workflow gives the situation, the verbs
in the order you run them, what success looks like, and what not to do by hand. It is the
companion of nova-sprint's coordinator playbook, which `nova-sprint help playbook` prints and
which covers the sprint seat; this page covers the tools under it.

Each step quotes the verb's usage line as the tool's own help prints it, then the line to run,
then the spec section it comes from (or says that the help is the only source). Where this page
and a tool's help disagree, the help is right and this page has the defect. A test
(`internal/docs/tools_playbook_test.go`) builds the tools and holds every usage line quoted here to
their help, and every verb and flag named here to `<tool> <verb> -h`, so a renamed or removed verb
fails the build. nova-card, nova-work and nova-sprint are built in nova-sprint, and its own test
holds their lines there.

`<tool> <verb> -h` prints a verb's usage, every flag and its effect, and runs nothing, so it is
always a safe first question ([CLI.md](CLI.md)). Notation: `<me>` is your own name, `<friend>` is
another friend's name, `<seat>` is a nova-secrets seat.

## Before you do anything by hand

The rule: find the verb first. Every tool answers `<tool> help` with its whole banner and
`<tool> <verb> -h` with one verb. The tools already do the work the model is tempted to do by
hand: the friend daemon runs lanes and delivery, nova-hook proves liveness, nova-secrets hands
a secret to one command, nova-doctor names what is missing and the line that fixes it, and the
release verbs cut, build and install.

1. Look for the verb: `<tool> help`, then `<tool> <verb> -h`.
2. If no verb does it, do not keep doing it by hand. Add a roadmap entry in the repository whose
   tool should own it. usage (nova-work, built in nova-sprint):
   `nova-work roadmap add [--repo <dir>] --id <id> (--release <v> | --group <g>) --title <t> [--text <t>] --origin <o> [--status planned|in-progress] [--why <w>]`.
   The roadmap verbs edit the repository's `docs/roadmap.sexp` and write `ROADMAP.md` in the same
   step (`nova-work help`).
3. A refusal is an answer: every tool's REFUSED line names why, and usually the command that
   mends it. Read it, and do not work round it.
4. Never put a secret on a command line, in a file you write, or in a log: nova-secrets exec hands
   it to one command's environment (workflow 4).

## 1. Joining as a friend

**Situation.** An AI is to work sprint cards as a friend: a nova-config row, a hooked-in session,
and a daemon running the lanes.

**The verbs, in order.**

1. Write the row. usage: `nova-config <kind> set <name> --<field> <value> ... --actor <name> [--dry-run] [--json]`
   (and `add` for a new row). Run
   `nova-config friend set <me> --width <n> --tiers <t,...> --pronouns <she/her|he/him|they/them> --actor <me>`.
   A claude friend in one-shot mode also needs `--config_dir <absolute dir>`, its
   `CLAUDE_CONFIG_DIR`; nova-friend run refuses a claude one-shot friend without it
   (`nova-config friend set -h`). Spec: SPEC-CONFIG.md, "The kinds of this cut".
2. Hook the session in. usage: `nova-hook hook --as <me> --role <role> [--once] [--harness <name>] [--redis <addr>] [--dry-run]`.
   Run `nova-hook hook --as <me> --role friend --harness <h>` under the session's Monitor.
   `--harness` installs the harness adapter before arming. Answer each `HOOK CHALLENGE` from
   inside your turn with usage `nova-hook prove --as <me> --nonce <n> [--redis <addr>] [--dry-run]`.
   Spec: SPEC-HOOK.md, "Roles and pushes" and "The heartbeat".
3. Install the daemon. usage: `nova-friend install --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--deny-self <d,...>] [--wall-reads <d,...>] --model <provider/model> [--secrets NAME[,NAME] --seat <seat>] [--launchd-log <file>] [--dry-run]`.
   Run it with `--dry-run` first. It writes the harness's settings and the launchd agent
   `com.nova.friend-<me>.plist`, then runs the delivery check once. `--secrets NAME[,NAME]
   --seat <seat>` wraps the daemon in nova-secrets exec. Spec: SPEC-FRIEND.md, "The loop" and
   "Harness settings".
4. Leave the loop to launchd. usage: `nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--adapter folder --delivery-dir <watched-dir>] [--server <addr>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--model <provider/model>] [--db <opencode.db>] [--lane-tiers <t,...>] [--lane-streams <p,...>] [--token-cap <n>] [--load-max <n>] [--load-width <n>] [--pause-on funds|any] [--refuse-go] [--dry-run]`.
   This is the loop launchd runs (install writes it). Its lanes take work only while you are
   hooked in. Spec: SPEC-FRIEND.md, "One-shot lanes".
5. Check yourself. usage: `nova-friend status --as <me> --dir <d> [--state-dir <d>]`, then usage
   `nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--harness codex --dir <d> --session <id> --adapter folder --delivery-dir <watched-dir>] [--json]`.
   `nova-friend check --settings` compares the harness's settings with what install would write.

**Success looks like.** `nova-friend status` says `daemon=up` with `hook=proven` and `status=up`,
and `nova-friend check` gives the verdict ok.

**Not by hand.** Do not edit the plist; after changing an argument, run install again (a changed
argument shows as "plist drift"). Do not take or finish your cards with shell loops, because the
daemon's lanes do. Do not answer a challenge from a script: only the model answers a challenge,
with nova-hook prove from inside its own turn (`nova-hook help`).

## 2. Talking on the bus

**Situation.** You need to tell a friend something, ask and wait for an answer, or see what is
late.

**The verbs, in order.**

1. Send. usage: `nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--kind ask --deadline <duration>] [--token <t>] [--timeout <duration>] [--redis <addr>] [--dry-run]`.
   `--kind` is one of report, ack, status, request, blocker, ask or judgment, and readers filter
   on it. An ask needs `--deadline <duration>`, its server-side wait for the answer
   (`nova-bus send -h`). Answer a message with `--re <id>`. Spec: SPEC-BUS.md, "bus-message-kinds.w1"
   and "asks: an ask has a server deadline; a timeout wakes the sender".
2. Follow your asks. usage: `nova-bus asks [--as <me>] [--open | --timedout | --all] [--liveness [--window <duration>]] [--max <n>] [--timeout <duration>] [--redis <addr>]`.
   `nova-bus asks --as <me> --timedout` lists the asks past their deadline with no answer, and
   `--liveness` gives each friend's liveness from the asks sent to her.
3. Wake the late. usage: `nova-bus overdue [--as <me>] [--older <duration>] [--dry-run] [--max <n>] [--timeout <duration>] [--redis <addr>]`.
   An ask past its deadline gets its one timeout message, sent to its sender. Spec: SPEC-BUS.md,
   "message-receipts: delivered, read, acted; overdue; a redelivered id is dropped".

**Success looks like.** Every ask has an answer or a timeout, and `asks --timedout` is empty or
acted on.

**Not by hand.** Do not resend the same message hoping it lands: a send under `--token` is one
message, and a retried send is safe (SPEC-BUS.md, "a-lost-send-response-is-safe-to-retry.w1").
Do not wait forever on a reply; give the ask a `--deadline`.

## 3. Configuring the fleet

**Situation.** A friend, a route, a tier, a machine or the sprint row has to change.

**The verbs, in order.**

1. Read first. usage: `nova-config <kind> show <name> [--json]` and usage
   `nova-config <kind> history <name> [--max <n>] [--json]` (who changed it, and when).
2. Write one row. usage: `nova-config <kind> set <name> --<field> <value> ... --actor <name> [--dry-run] [--json]`.
   For example, `nova-config friend set <friend> --width <n> --actor <me>`,
   `nova-config route set <route> --note <why> --actor <me>`,
   `nova-config tier set <tier> --routes <a,b> --actor <me>`,
   `nova-config machine set <m> --width <n> --actor <me>`, or
   `nova-config sprint set --max_attempts <n> --actor <me>` (fleet and sprint are one row each,
   with no name). Run it with `--dry-run` first: it prints `CONFIG DRY-RUN` from the same checks.
   Every write adds a history row naming who made it. Spec: SPEC-CONFIG.md, "Kinds" and "History".
3. Apply it. usage: `nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--actor <name>] [--kind <kind>] [--dry-run] [--json]`.
   `nova-config apply --kind <kind>` copies the rows into Redis, the view the fleet reads. A
   committed route write applies its own copy (`nova-config route set -h`). Spec: SPEC-CONFIG.md,
   "Apply".

**Success looks like.** `show` prints the new value, and `history` names you and your
reason.

**Not by hand.** No edit of the PostgreSQL rows or the Redis copy by hand; a lost Redis comes back
with `nova-config apply` (`nova-config help`). No password on the line: `NOVA_PG_PASSWORD_ENV`
names the variable that holds it (`nova-config help`).

## 4. Using a secret

**Situation.** A command needs a key or a token.

**The verbs, in order.**

1. See which names your seat holds, with no value decrypted. usage: `nova-secrets names  --store <dir> --as <name> [--max <n>] [--json]`.
2. Hand the secret to one command. usage: `nova-secrets exec   --store <dir> --as <name> --key <path> --sops <path> --only <NAME,...|all> [--require <NAME>]... -- <cmd> [args...]`.
   For example, `nova-secrets exec --store <dir> --as <seat> --key <path> --sops <path> --only GH_TOKEN --require GH_TOKEN -- gh api user`.
   exec decrypts only the `--only` names into that command's environment, writes nothing, and
   prints no value. The store must be a git working copy on a named branch whose HEAD equals its
   upstream (`nova-secrets exec -h`). Spec: SPEC-SECRETS.md, "`exec`".

**Success looks like.** The command runs with the secret in its environment, and no value appears
on a line, in a file or in a log.

**Not by hand.** Never print a value, echo it, paste it, or put it on a command line. Do not
export it into your shell; exec hands it to one command. Do not decrypt the store with sops by
hand.

## 5. Is this machine ready?

**Situation.** Before work runs on a machine, or when something on it fails for a reason you
cannot see.

**The verbs, in order.**

1. usage: `nova-doctor [run] [--check <name>]... [--dir <path>] [--local] [--strict] [--json]`.
   Run `nova-doctor run`. Each check prints one `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]`
   line, every check runs, and nothing is changed or fixed for you. `--check <name>` runs one
   (the names are in `nova-doctor help run`), `--local` skips the checks only a fleet needs, and
   `--dir` selects a repository. Spec: SPEC-DOCTOR.md, "The command" and "The checks".
2. Run the fix line each failing check prints, then `nova-doctor run --check <name>` again.

**Success looks like.** Exit 0: every check is ok or warn.

**Not by hand.** Do not guess what is missing; the check names it with evidence. Do not "fix" a
warn by editing the files doctor reads.

## 6. Running cards on a machine

**Situation.** A machine is to work a sprint's cards as a fleet member or a reader.

**The verbs, in order.**

1. usage: `nova-swarm member    --as <name> --server <host:port> --harness <path> --root <dir> [--slots <dir>] [--results-root <dir>] [--model <provider/model>] [--deadline <duration>] [--tokens <n>|unmetered] [--reader] [--every <duration>] [--once | --ticks <n>] [--auth <file>] [--config <file>] [--worker <file>] [--no-wall] [--gh <path>] [--pass <NAME,...>] [--disk-floor <GiB>] [--max-load <load>] [--warn-load <load>] [--gocache-limit <GiB>] [--stage-wall <duration>] [--identity <owner>,<name>,<email>] [--prompt <file>]`.
   `--server` is the address of nova-sprint run --listen, and this machine opens no store. Each
   tick beats, reads the queue, reports ended children and takes cards up to the fleet row's
   width. `--reader` runs reads instead of work cards. `--pass <NAME,...>` names the environment
   secrets a child is handed. Spec: SPEC-SWARM.md, "The living verbs".

**Success looks like.** The member beats, and the sprint's fleet table shows it up and working.

**Not by hand.** Do not run the cards' children yourself: member runs each card as one child
(`nova-swarm help`).

## 7. Writing a card

**Situation.** Work is to be cut into cards a stranger's AI can run.

**The verbs, in order.**

1. Start from the template. usage: `nova-swarm template  --name read-pr|probe-row|fix-card|result|worker|setup|capacity|card|read|fix|text|replay|drift|tone|models.tsv`.
   `nova-swarm template --name card` prints a whole card that passes `lint --child-rules`.
2. Or generate cards from a ledger, a findings file or a tool's help with nova-card (built in
   nova-sprint): usage `nova-card generate --from findings --file <tsv> --out <dir> (--repo-dir <dir> | --repo <owner/name> --base <branch> --sha <40hex>) [--tier flash|pro] [--prefix <p>] [--minutes <n>] [--max <n>] [--name <n>...] [--dropped <id>...] [--dry-run]`.
   It holds every brief to the lint nova-sprint add runs before it writes (`nova-card help`).
3. Lint before you add. usage: `nova-swarm lint      --card <file> (or the bare <file>) [--typed] [--child-rules | --child-rules-file <file>] [--member-injects] [--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]] [--trust <file>] [--lineup <file>] [--decide [--decide-answers <file>] [--decide-record <file>]] [--max <n>] | --fleet <file> [--max <n>] | --rules`.
   `nova-swarm lint --rules` lists every check. Exit 0 means the card is clean. Spec: SPEC-SWARM.md,
   "Card lint".

**Success looks like.** The lint reports no drift, and `nova-sprint add` admits the card.

**Not by hand.** Do not write a card's PATHS by hand when nova-card computes them from its START
line (`nova-card help`). Do not add a card the lint has not passed.

## 8. Making a typed decision

**Situation.** A yes-or-no or a choice should be made by a model with a probability, recorded, and
later calibrated.

**The verbs, in order.**

1. Try it with no backend. usage: `nova-decide example --dir <new-directory>`.
2. Ask. usage: `nova-decide ask --schema <file> --state <file|-> --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]`.
   Exit 0 means recorded, never approved (`nova-decide help`). The ready decisions have their
   own verbs, for example the gate verdict, usage
   `nova-decide gate --output <file|-> --card <file|-> [--diff <file|->] [--base-red <test,...>] [--bars <flaky,pre-existing>] --backend <jev|fixed> [--answers <file>] --record <file> [--op <id>] [--timeout <d>] [--dry-run]`.
   Spec: SPEC-NOVA-DECIDE.md section 2 ("A decision is a named schema over a state") and section
   12 ("The gate decision").
3. Record what happened. usage: `nova-decide outcome --record <file> --id <decision-id> --label <word> [--note <text>] [--dry-run]`.
4. Read the bar the record supports. usage: `nova-decide calibrate --record <file> --decision <name> --question <name[=option]> --positive <label,...> --negative <label,...> [--bars <p,...>]`.
   Spec: SPEC-NOVA-DECIDE.md section 5, "Calibration".

**Success looks like.** Every decision is in the record with its outcome, and the bar you set is
the one calibrate reads.

**Not by hand.** Do not pick a bar by feel; calibrate reads it from outcomes. Do not apply a
decision's verb under its bar.

## 9. Building and testing off the working machine

**Situation.** Go code needs a build or a test run, and the working machine must not carry the
load.

**The verbs, in order.**

1. usage: `nova-ci bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>] [--cache <dir>] [--with-git] -- <go command>`.
   Run, for example, `nova-ci bench run --host <bench> --dir . -- go vet ./cmd/nova-ci/`. It copies
   the tree to a fresh run directory on the Linux bench, runs the command there under nice -n 19,
   streams its output, then removes that directory and nothing else. Spec: SPEC-CI.md, "The bench
   run verb".
2. In a nova-tools checkout on a bench, run the unit tier CI would run for your diff. usage:
   `nova-ci local [--base origin/dev] [--functional] [--dry-run]` (`nova-ci help`). Spec: SPEC-CI.md,
   "`tiers` — unit tests on every change, functional tests as streams merge".

**Success looks like.** The CI BENCH line ends the run with the command's exit, and `nova-ci local`
exits 0.

**Not by hand.** Do not type the ssh, rsync and `rm -rf` recipe into a brief or a shell: bench
run is the one way to run Go on a Linux bench against a local tree, and it removes its own run
directory (SPEC-CI.md, "The bench run verb").

## 10. Releasing the tools

**Situation.** nova-tools work has landed and is to ship.

**The verbs, in order** (usage lines from `nova-update help release`):

1. Cut. usage: `nova-update release cut --repo <owner/name> --from <branch> --version <v> --changelog <path> [--sums <file>] [--waive-ci "<who, when>"] [--security-read <id|url>] [--local-diff <checkout> [--paths-from <file>] | --paths-from <file>] [--cli <file>] [--receipts <dir>] [--no-dogfood-gate --reason <why>] [--journeys <file> | --no-journey-gate --reason <why>] [--spend-store <addr>] [--spend-since <RFC3339>] [--spend-receipts <file>] [--no-spend-gate --reason <why>] [--dry-run] [--timeout <d>]`.
   Run it with `--dry-run` first. The cut runs the dogfood gate, the journey gate and the spend
   gate, and refuses a range that touched the sensitive paths until `--security-read` names the
   security reader's read. Spec: SPEC-RELEASE.md sections 1, 2, 12, 14 and 18.
2. Build. usage: `nova-update release build --version <v> --out <dir> --source <dir> [--sprint-release <dir>] [--platform <goos-goarch>,...] [--incremental] [--cli <file>] [--receipts <dir>] [--no-dogfood-gate --reason <why> | --gate report --reason <why>] [--timeout <d>]`.
3. Install and adopt. usage: `nova-update release install --from <dir> --version <v> --bin <dir> [--retire <dir>] [--platform <goos-goarch>] [--timeout <d>]`
   on the coordinator first, then usage
   `nova-update release adopt [--version <v>] --machines <file> --ssh <path> --from <dir|host:dir> --bin <dir> --dest <dir> [--stage <dir> --repo <owner/name> | --stage <dir> --expect-sums <sha256> | --stage <dir> --expect-sums-from <file>] [--retire <dir>] [--platform <goos-goarch>] (--certify <machines.tsv> --certs <file> --standard <file> | --no-certify) [--dry-run] [--timeout <d>]`.
   Spec: SPEC-RELEASE.md section 8, "Install on the coordinator first, then adopt".
4. Or the whole fix, land and install loop in one command. usage: `nova-update release cycle --version <v> --source <dir> --sprint-release <dir> --out <dir> --inventory <file> --benches <a,b,...> --reason <why> --ansible <path> [--receipts <dir>] [--dry-run] [--timeout <d>]`.
   Spec: SPEC-UPDATE.md, "The release verb".
5. Withdraw a release that should not have shipped. usage: `nova-update release pull --version <v> --out <dir> --changelog <path> [--machines <file> --ssh <path> --dest <dir>] [--reason <text>] [--platform <goos-goarch>] [--dry-run] [--timeout <d>]`.
   The tag stays, the CHANGELOG section is marked PULLED, and the release's artifacts are
   deleted. Spec: SPEC-RELEASE.md section 3, "A leaked release is pulled".

**Success looks like.** The cut prints its receipt with the SUMS digest in the annotated tag, and
adopt reports every machine on the new build.

**Not by hand.** No bare `git tag`: the tag is annotated, and the annotation carries the SUMS
digest (SPEC-RELEASE.md section 2). Do not scp binaries to machines; install and adopt verify them
against their sums. Do not delete a tag to withdraw a release; pull does that.

## 11. Recording work

**Situation.** An idea, a bug or a finished fix has to be recorded on a repository's roadmap.

**The verbs, in order** (nova-work, built in nova-sprint; `nova-work help`):

1. usage: `nova-work roadmap list [--repo <dir>] [--file roadmap|fixes] [--release <v> | --group <g>] [--state <s>] [--text <t>] [--max <n>]`.
   Look before you add.
2. usage: `nova-work roadmap add [--repo <dir>] --id <id> (--release <v> | --group <g>) --title <t> [--text <t>] --origin <o> [--status planned|in-progress] [--why <w>]`.
3. usage: `nova-work roadmap done [--repo <dir>] --id <id>... --evidence <PR #n | commit>` once it lands.
4. usage: `nova-work roadmap check [--repo <dir>]` before you commit.

**Success looks like.** `roadmap check` exits 0, and `ROADMAP.md` was written in the same step.

**Not by hand.** Do not hand-edit `ROADMAP.md`, because the verbs write it from
`docs/roadmap.sexp` in the same step (`nova-work help`).

## 12. A friend's lanes stopped

**Situation.** A friend is silent, her lanes are faulted, or a provider paused her.

**The verbs, in order.**

1. Reach her. usage: `nova-friend reach --as <coordinator> --to <friend> [--step-timeout <duration>] [--from <bus|push|window>] [--harness <h>] [--dir <d>] [--session <id>] [--state-dir <d>] [--redis <addr>] [--dry-run]`.
   It climbs the ladder (a bus message, then a push into the session, then her window) and stops
   at the first proof. Spec: SPEC-FRIEND.md, "Reach".
2. A faulted lane (the second identical refusal marks it). usage: `nova-friend lanes --as <me> [--clear-faults] [--dir <d>] [--state-dir <d>]`.
   Without `--clear-faults` it reads and prints the standing fault. Spec: SPEC-FRIEND.md,
   "One-shot lanes".
3. A provider pause, once a person has dealt with the provider. usage: `nova-friend resume --as <me> [--dir <d>] [--state-dir <d>] [--dry-run]`.
   It removes the PAUSED marker, and a running daemon lifts its hold at its next step. A friend a
   person held with `nova-sprint friend down` stays held until `nova-sprint friend up`
   (`nova-friend resume -h`).

**Success looks like.** `reach` prints `REACH OK`, and `nova-friend status` shows the lanes taking
cards again.

**Not by hand.** Do not type into her window; reach's window step does that only when the
earlier steps found no proof. Do not delete the PAUSED marker or the fault file by hand, because
resume and lanes do it and say what they cleared.
