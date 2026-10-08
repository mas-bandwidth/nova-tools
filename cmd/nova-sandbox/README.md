# nova-sandbox

## What it is

nova-sandbox: run one command inside an OS-enforced wall around the directories you name

## Why use it

Keep a command away from files it should not touch.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-sandbox@latest
nova-sandbox version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-sandbox).

Fixture: a job directory of yours. Every path below is one you name — this tool has no defaults and guesses nothing — so the transcript is a worked example with `/path/to/pool` standing in for yours, and the lines are what the platform prints with the paths shortened.

```text
$ nova-sandbox check
CHECK OK backend=sandbox-exec abi=- net=enforceable hosts=none note=sandbox-exec is deprecated by Apple and works on macOS 26; the wall is the profile it applies; backend at /usr/bin/sandbox-exec

$ HOME=/path/to/pool/jobs/j1/home nova-sandbox probe --read /path/to/pool/ref --write /path/to/pool/jobs/j1 --secret /path/to/.config/anthropic/env
PROBE STEP name=write_outside_control expect=allow got=allow path=/path/to/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=write_outside expect=deny got=deny path=/path/to/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=read_secret expect=deny got=deny path=/path/to/.config/anthropic/env
PROBE STEP name=write_inside expect=allow got=allow path=/path/to/pool/jobs/j1/.nova-sandbox-probe-inside
PROBE STEP name=read_root expect=allow got=allow path=/path/to/bin/nova-sandbox
PROBE OK backend=sandbox-exec abi=- steps=5 passed=5 net=nopromise gpu=none

$ HOME=/path/to/pool/jobs/j1/home nova-sandbox --read /path/to/pool/ref --write /path/to/pool/jobs/j1 -- /bin/sh -c 'echo hello > report.md; cat /path/to/.config/anthropic/env'
SANDBOX NOTE dropped from the child's environment: GPG_AGENT_INFO SSH_AGENT_PID SSH_AUTH_SOCK; an agent socket speaks for a key the wall denies
SANDBOX OK backend=sandbox-exec abi=- read=1 read-noexec=0 write=1 net=nopromise cwd=/path/to/pool/jobs/j1 cwdb64=L3BhdGgvdG8vcG9vbC9qb2JzL2ox ancestors=11 cmd=sh gpu=none deletes=/path/to/pool/jobs/j1
cat: /path/to/.config/anthropic/env: Operation not permitted
SANDBOX DONE exit=1 cmd=sh
```

## Verbs

The [nova-sandbox section of the command reference](../../docs/CLI.md#nova-sandbox) documents every verb's flags, effect and exit codes.

- `probe`
- `policy`
- `check`
- `run`
- `reap`
- `worktree`
- `egress`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-SANDBOX.md](../../docs/SPEC-SANDBOX.md).
