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
nova-doctor [run] [--check <name>]... [--dir <path>] [--local] [--strict] [--json]
```

`run` is the one verb and the default: `nova-doctor --local` is `nova-doctor run --local`.
A bare `nova-doctor`, with no verb and no flag, runs no check: it is refused at exit 2 on
stderr, naming the verbs and `run: nova-doctor help`, as every tool's bare command is
(docs/ONBOARDING.md point 1).

- `--check <name>` runs only that check; repeatable. A name that is no check is refused
  at exit 2, naming the checks there are. With `--json`, refusals use the standard
  `pkg/tool` JSON envelope on stdout; plain refusals stay on stderr.
- `--dir <path>` selects the repository for repository-scoped checks. It does not
  change the caller's working directory. A missing or unreadable directory is refused
  at exit 2 before any check runs. Checks that do not inspect a repository ignore it.
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
(`pkg/buildinfo`). `ok` when every tool reports the same version. `fail` when a
tool does not answer with a version line (named), when none is on PATH, or when the
versions differ: the evidence names every tool that differs from the version most of the
tools report, and the fix is `nova-update apply --file <manifest> <tool> --version <release>`.

`nova-up --local` ends by running `nova-doctor --local`.

### The friend checks

The friend checks each enumerate the friend daemons this machine loads
(`~/Library/LaunchAgents/com.nova.friend-*.plist` on darwin, the systemd user
units `nova-friend-*.service` on linux), read each daemon's argv from its plist
(`friend.PlistArgs`) or unit, and name the daemon in every evidence line. A
machine with no such daemon gets `ok no friend daemons here` from each. None is
a Fleet check, so `--local` runs all of them.

**friend-seat.** Dependency: each daemon's `--coordinator` against the seat
holder. Runs `nova-sprint where --json` and reads its `coordinator` field; a
daemon whose `--coordinator` differs is `fail` `plist says <x>, seat is <y>`.
Fix: `nova-friend install ... --coordinator <seat>`, or drop the flag so the
daemon reads the seat from the server.

**friend-row.** Dependency: each daemon's nova-config row. Runs `nova-config friend show <name> --json`; `fail` when the row's `mode` is not `one-shot`, when
its `roles` lack `builder` while the daemon runs cards (`--harness` given), or
when its `width` differs from the daemon's `--width`. Fix: `nova-config friend set <name> --mode one-shot --roles builder,reader --width <n>`, then
`nova-sprint friend sync`.

**friend-live.** Dependency: each daemon's status. Runs `nova-friend status --as <name> --json`; `fail` when `beat_error` or `inbox_error` contain `refused` (the
hook gate; the evidence quotes the refusal) or when the last beat is older than
15 s while the agent is loaded (`launchctl print gui/<uid>/<label>`, or
`systemctl --user is-active`). Fix: read `~/Library/Logs/nova-friend-<name>.log`,
`launchctl kickstart -k` the label, and for a hook refusal hook the seat with
`nova-sprint hook`.

**friend-wall.** Dependency: each daemon's `--dir` against the lane wall's
deny-self. The deny-self is the daemon's `--deny-self`, else
`NOVA_FRIEND_DENY_SELF`, else `<home>/ai/<coordinator>/working`; `fail` when `--dir`
equals, contains, or is contained by it, naming both paths, with both paths
resolved through symlinks where they exist. Fix: move `--dir`
beside the self (a sibling directory), never inside it.

**friend-login.** Dependency: a claude daemon's sign-in. For `--harness claude`
the `--config-dir` (argv, else the row's `config_dir`) is proven by running the
harness itself, `CLAUDE_CONFIG_DIR=<dir> claude -p ok --output-format json --max-turns 1`, and reading `terminal_reason`; an `api_error` with
`duration_api_ms` 0 is `fail config dir <dir> is not signed in`. Fix, the
owner's hand: `CLAUDE_CONFIG_DIR=<dir> claude login`. The check reads only the
harness's answer and never prints, copies or moves a credential.

**plists.** Dependency: the launchd plists and systemd units this machine loads.
Runs `plutil -lint` on every `com.nova.*.plist` under `~/Library/LaunchAgents`
and `/Library/LaunchDaemons` (`systemd-analyze verify` for `nova-*.service` on
linux) and quotes the refusing line. Fix: rewrite the agent with the tool that
owns it (`nova-friend install`, `nova-config loop`) and remove a hand-written
stopgap.
