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
tools report, and the fix is `nova-update apply --file <manifest> <tool> --version <release>`.

`nova-up --local` ends by running `nova-doctor --local`.

## Jobs

```
nova-doctor [run] --job <local-notes|messaging|friend|worker|coordinator> [--as <name>] [--dir <d>]
            [--harness <h>] [--redis <host:port>] [--strict] [--json]
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

| step | stage | calls | fails when | fix |
| --- | --- | --- | --- | --- |
| redis-reachable | connectivity | a dial of `--redis`, else `NOVA_REDIS_ADDR`, else `NOVA_SPRINT_REDIS` | no address, or no answer | loopback: `nova-redis serve --bind <host> --port <port> --dir ~/nova/stores/redis`; else `tailscale ping <host>` |
| redis-login | authentication | `nova-redis acl check --addr <a>` | exit 2 (the login was refused) | set `NOVA_REDIS_USER` and `NOVA_REDIS_PASSWORD_ENV`, then the doctor again |
| store-login | authentication | `nova-config login --check` | exit not 0 | the tool's own `run:` line |
| config-schema | schema and config revision | `nova-config status` | exit not 0 | the tool's `run:` line, else `nova-config migrate` |
| config-applied | applied Redis state | `nova-config apply --check` | a kind with add, set or remove, or `applied` behind `rev` | `nova-config apply` |
| redis-acl | applied Redis state | `nova-redis acl check --addr <a>` | exit 1 | the tool's `remedy=`, else `nova-redis acl apply --addr <a>` |
| redis-functions | installed binaries and functions | `nova-redis fn check --addr <a>` | exit not 0 | `nova-redis fn load --addr <a>` |
| binaries | installed binaries and functions | PATH | a tool the job needs is not on it | `go install .../cmd/<tool>@latest` |
| self | installed binaries and functions | the `self` check | as `self` | as `self` |
| swarm-binary | installed binaries and functions | `nova-swarm doctor` | exit not 0 (a shadowing binary) | copy `~/.local/bin/nova-swarm` over the PATH one |
| daemon-running | supervisor | `nova-friend check <f> --json` | the daemon's status is not ok | `nova-friend install --as <f> --harness <h> --dir <d>` |
| harness-responsive | session response | the same report | the session is marked broken, or every delivery failed | `nova-friend install ...` (install again clears a broken session) |
| message-delivered | session response | the same report | no delivery in the window | `nova-friend ping --as <coordinator> --to <f>` |
| session-receipt | session response | the same report | no session pong and no message back | `nova-friend check --as <coordinator> <f> --harness <h>` (the delivery check) |
| card-completion | session response | the same report | never: none completed yet is `ok` and says so | - |
| friends | session response | `nova-friend check [--as <c>] --json` | a friend not ok is a `warn` | `nova-doctor --job friend --as <f>` |

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
the **session's receipt** (a pong or a message back), and **card completion** (the outbox).

The output is one line per step, the frame's `DOCTOR <step> ok|warn|fail|blocked <evidence>
[fix: <line>]`, then one summary line:

```
DOCTOR job=<job> ready steps=<n> calls=<n>
DOCTOR job=<job> not-ready first_missing=<step> calls=<n> next: <the fix of that step>
```

`calls` is how many tool calls the run made. `--json` prints
`{"job","exit","ready","first_missing","next","calls","steps":[{check,dependency,status,evidence,fix}]}`.
It is bounded: a step's evidence and fix are cut to 240 bytes (ending `...`), and a job has at
most ten steps. `--check` and `--local` select checks, not steps, and are refused with `--job`;
`--as`, `--dir`, `--harness` and `--redis` are refused without it.

The acceptance is `TestDoctorNamesTheFirstMissingDependencyAndItsFix`
(`internal/doctor/job_test.go`): for each job, a world with one dependency deliberately
missing; the doctor names it as `first_missing`, every step before it ok and every one after
it blocked; a cold reader that runs only the printed `next:` command, exactly as printed,
makes the job ready in one repair and two doctor runs, and the test logs the call counts.
