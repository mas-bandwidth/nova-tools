# nova-doctor — specification

nova-doctor says what is missing for the nova tools to work on this machine, and for each
thing the one line that fixes it. It changes nothing and runs no fix.

## The frame

A **check** is a name, the dependency it covers, a `Run` over an `Env`, and a result.
The result is `ok`, `warn` or `fail`, with evidence and, when it is not `ok`, the one fix
line: a nova verb when one exists, never a hand script. A result that is not `ok` with no
fix line is turned into a `fail` that says so; a panic is a `fail`; a check never stops
the others.

`Env` is everything a check reaches outside itself: the environment, files, directories,
a child process, a network dial and the clock. The real `Env` is the process's; a test
passes fakes (an exec that answers from a table, files under `t.TempDir()`, a fixed
clock), so no test starts a service or opens a socket.

Checks live one per file, `internal/doctor/check_<dependency>.go`, and register
themselves from an `init` into the default registry. A new dependency adds one file and
edits no other. A duplicate or unnamed registration panics at start-up.

## The command

```
nova-doctor [run] [--check <name>]... [--local] [--strict] [--json]
```

- `--check <name>` runs only that check; repeatable. A name that is no check is refused
  at exit 2, naming the checks there are.
- `--local` skips the checks only a fleet needs and says which, in one line:
  `DOCTOR local skipped=<name,name> (...)`.
- `--strict` makes a `warn` exit 1.
- `--json` prints `{"exit":N,"results":[{check,dependency,status,evidence,fix}],"skipped":[...]}`,
  the same results as the lines.

Checks run in name order. One line each:

```
DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]
```

## The exit

| exit | when |
| --- | --- |
| 0 | every result is `ok`, or a `warn` without `--strict` |
| 1 | a `warn` with `--strict`, and no `fail` |
| 2 | a `fail`, or a usage refusal |

## The checks

### self

Dependency: the nova tools on PATH. Finds every executable `nova-*` on PATH (the first of
a name wins, as the shell resolves it), runs `<tool> version`, and reads the version line
(`internal/buildinfo`). `ok` when every tool reports the same version. `fail` when a
tool does not answer with a version line (named), when none is on PATH, or when the
versions differ: the evidence names every tool that differs from the version most of the
tools report, and the fix is `nova-update apply --file <manifest> --version <release> <tool>`.

`nova-up --local` ends by running `nova-doctor --local`.

## Jobs

```
nova-doctor [run] --job <local-notes|messaging|friend|worker|coordinator> [--as <name>] [--dir <d>]
            [--harness <h>] [--config-dir <d>] [--redis <host:port>] [--since <d>] [--strict] [--json]
```

`--job` asks a different question from the checks: is this machine ready for one job, and if
not, what is the first thing missing. The answer is a chain, not a badge. A job is a list of
**steps**, each one dependency, each at a **stage**, and the steps run in stage order:

1. connectivity
2. authentication
3. schema and config revision
4. applied Redis state
5. installed binaries and functions
6. supervisor
7. session response

The first step that fails stops the chain: every step after it is `blocked` (`not run: <step>
fails first`) and is not called, since what it would read stands on what is missing. A `warn`
does not stop the chain. The exit is the frame's: 2 when a step fails.

Each step calls the check its tool already has, and reads that tool's exit (0 yes, 1 the tool
ran and said no, 2 it could not run) and its words; nothing is checked twice in two places. A
call two steps read (the login and the users are both `nova-redis acl check`) runs once per
doctor run. A tool that is not on PATH at all fails the step that called it, with
`go install <module>/cmd/<tool>@latest` as the fix, the module the doctor was built from (as nova-up's binaries step names it).

**Every command the doctor runs or prints is one the real tool accepts.** The tools read flags
until the first argument that is not a flag (Go's flag package, internal/tool/tool.go:530-558),
so every flag comes before the first argument: `nova-friend check --json <f>`, never
`nova-friend check <f> --json`, which the tool reads as two friends. Each line carries every
flag its verb requires, and the address the doctor checked is passed explicitly (`--redis`,
`--addr`) rather than left to an environment variable the next tool may read differently.
Caller-supplied values in repair commands are shell-quoted, preserving spaces as one argument.
Paths are absolute: `nova-redis serve` refuses a `--dir` that is not, and a line run without a
shell expands no `~`. A value the doctor cannot know is a `<placeholder>` with no spaces in it.
`nova-config apply`, including `--check`, resolves `--actor` before it runs and refuses when none is set (`--actor`, else `NOVA_FRIEND`). The doctor passes `--actor` (the job's `--as`, else `NOVA_FRIEND`) on the check and on the repair. `nova-redis serve` refuses a real run that is not `--dry-run`, names no login, and has an empty `NOVA_REDIS_PASSWORD`. The loopback repair names the five login flags (`--secrets`, `--as`, `--key`, `--sops`, `--secret`) from `NOVA_SEAT` (for a coordinator job with `NOVA_SEAT` unset, the job's `--as`) and the fleet secrets layout (`NOVA_SECRETS_STORE`, `NOVA_SECRETS_KEY`, `NOVA_SECRETS_SOPS`, else `$HOME/nova-bench/secrets`, `$HOME/.config/nova-secrets/<seat>.key`, and `sops` on `PATH`). The secret is `NOVA_REDIS_<USER>_PASSWORD` (`NOVA_SPRINT_REDIS_USER`, else `coordinator`). When that login cannot be named, the line is the dry run, which launches nothing.

| step | stage | calls | fails when | fix |
| --- | --- | --- | --- | --- |
| redis-reachable | connectivity | a dial of `--redis`, else `NOVA_REDIS_ADDR`, else `NOVA_SPRINT_REDIS`, else `NOVA_BUS_REDIS` | no address, or no answer | loopback: `nova-redis serve --bind <host> --port <port> --dir $HOME/nova/stores/redis` with `--secrets`, `--as`, `--key`, `--sops`, `--secret` (the home directory written out); a seat that cannot be named is that line with `--dry-run`, which launches nothing; else `tailscale ping <host>` |
| redis-login | authentication | `nova-redis acl check --addr <a>` | exit 2 (the login was refused) | `export NOVA_REDIS_USER=<user> NOVA_REDIS_PASSWORD_ENV=<NAME>`, then the doctor again |
| store-login | authentication | `nova-config login --check` | exit not 0 | the tool's own `run:` line |
| config-schema | schema and config revision | `nova-config status` | exit not 0 | the tool's `run:` line, else `nova-config migrate` |
| config-applied | applied Redis state | `nova-config apply --check --redis <a> --actor <c>` | a kind with add, set or remove, or `applied` behind `rev`, or no actor (`--actor` or `NOVA_FRIEND`) | no actor: `nova-doctor --job coordinator --as <c>` again; else `nova-config apply --redis <a> --actor <c>` |
| redis-acl | applied Redis state | `nova-redis acl check --addr <a>` | exit 1 (the `ACL CHECK DRIFT` line) | `nova-redis acl apply --addr <a>` (the tool's `remedy=` carries prose after the command); a user the store lacks makes apply name its password step |
| redis-functions | installed binaries and functions | `nova-redis fn check --addr <a>` | exit not 0 | `nova-redis fn load --addr <a>` |
| binaries | installed binaries and functions | PATH | a tool the job needs is not on it | `go install .../cmd/<tool>@latest` |
| self | installed binaries and functions | the `self` check | as `self` | as `self` |
| swarm-binary | installed binaries and functions | `nova-swarm doctor` | exit not 0 (a shadowing binary) | `install -m 0755 $HOME/.local/bin/nova-swarm <the PATH one>`, both paths written out |
| daemon-running | supervisor | `nova-friend check --json --since <d> --redis <a> <f>` | the daemon's status is not ok | `nova-friend install --as <f> --harness <h> --dir <d> --redis <a>`, and for claude `--config-dir` (`--config-dir`, else `CLAUDE_CONFIG_DIR`), which install requires |
| harness-responsive | session response | the same report | the session is marked broken, or every delivery failed | `nova-friend install ...` (install again clears a broken session) |
| message-delivered | session response | the same report | no delivery in the window | `nova-friend ping --as <coordinator> --to <f> --redis <a>` |
| session-receipt | session response | the same report | nova-friend's verdict is `deaf` over `--since` (any other verdict not ok fails with the install line) | `nova-friend check --as <f> --harness <h> --dir <d> --redis <a> --to <coordinator>` (the delivery check: `--as` is the friend itself) |
| card-completion | session response | the same report | never: none completed yet is `ok` and says so | - |
| friends | session response | `nova-friend check --json --since <d> --redis <a> [--as <c>]` | a friend not ok is a `warn` | `nova-doctor --job friend --as <f> --redis <a>` |

A fix taken from a tool's own words is used only when it is a nova command and not a help
page; otherwise the step's own fix is printed. The coordinator in a fix is
`NOVA_SPRINT_ACTOR`, else `NOVA_FRIEND`.

The jobs:

| job | steps |
| --- | --- |
| local-notes | redis-reachable, redis-login, binaries (nova-redis) |
| messaging | redis-reachable, redis-login, binaries (nova-bus), self |
| friend | redis-reachable, redis-login, binaries (nova-bus, nova-friend), self, daemon-running, harness-responsive, message-delivered, session-receipt, card-completion |
| worker | redis-reachable, redis-login, redis-functions, binaries (nova-redis, nova-sprint, nova-swarm), self, swarm-binary |
| coordinator | redis-reachable, redis-login, store-login, config-schema, config-applied, redis-acl, redis-functions, binaries (nova-bus, nova-config, nova-friend, nova-redis, nova-sprint, nova-swarm), self, friends |

The friend job keeps five facts apart, each its own step and line, because each can hold
while the next does not: the **daemon running** (a beat process is not a session), the
**harness responsive** (the session is not broken), a **message delivered** into the session,
the **session's receipt**, and **card completion** (the outbox).

The session's receipt is nova-friend's own verdict (docs/SPEC-FRIEND.md, "The verdicts"), not
a rule of the doctor's own: `deaf` is a delivery in the `--since` window that succeeded with no
session pong aged within the window and no real message back. The doctor passes the window
explicitly (`--since` wants a positive duration, default 24h, nova-friend's own default) and
judges no pong itself:
`pong_age` is the last pong ever recorded, so a pong of any age is never a receipt here. A
pong 72 hours old with nothing back is `deaf` over 24h; one 11 minutes old is `deaf` over
`--since 10m`. The line prints the verdict, its window, its why, the pong's age and the
messages back.

The output is one line per step, the frame's `DOCTOR <step> ok|warn|fail|blocked <evidence>
[fix: <line>]`, then one summary line:

```
DOCTOR job=<job> ready steps=<n> calls=<n>
DOCTOR job=<job> not-ready first_missing=<step> calls=<n> next: <the fix of that step>
```

`calls` is how many tool calls the run made, including every `self` version read. `--json` prints
`{"job","exit","ready","first_missing","next","calls","steps":[{check,dependency,status,evidence,fix}]}`.
It is bounded: a step's evidence is cut to 240 bytes (ending `...`), and a job has at
most ten steps. Repair commands remain complete up to 4096 bytes; a longer repair is refused
with a remedy instead of printing a truncated command. The whole JSON stays under 64 KiB.
`--check` and `--local` select checks, not steps, and are refused with `--job`;
`--as`, `--dir`, `--harness`, `--config-dir`, `--redis` and `--since` are refused without it.

The acceptance is `TestDoctorNamesTheFirstMissingDependencyAndItsFix`
(`internal/doctor/job_test.go`): for each job, a world with one dependency deliberately
missing; the doctor names it as `first_missing`, every step before it ok and every one after
it blocked; a cold reader that runs only the printed `next:` command, exactly as printed,
makes the job ready in one repair and two doctor runs, and the test logs the call counts.
The test's fake tools and fake shell read every nova command through a table of the real
verbs' argv rules (`internal/doctor/argv_test.go`, each rule citing its source): a flag after
an argument, a flag the verb does not define, or a required flag missing fails the test, as
the real tool would refuse it. `TestDoctorFakeShellRefusesWhatTheRealToolRefuses` pins the
table, including the three lines earlier attempts printed.
