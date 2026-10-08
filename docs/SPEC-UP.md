# nova-up: one machine, from nothing to a first sprint

`nova-up` sets up nova on one machine. Its first mode, `nova-up --local`, takes one machine with
no config to the point where a coordinator runs a first sprint on it. The tool is `cmd/nova-up`
over `internal/up`; its tests run every step on a fake machine (`internal/up/up_test.go`).

## The root

Everything `--local` writes is under one root, `~/nova` unless `--root <dir>` names another,
but for one unit file in the service manager's own directory (step 6). The layout:

| path | what it is | written by |
| --- | --- | --- |
| `stores/sprint.twin` | the sprint's store, a nova-sprint twin (`mem:<file>`) | sprint |
| `stores/redis/` | the Redis store's directory, and `acl.applied`, the users the last `acl apply` set | redis |
| `keys/coordinator.key` | the seat's age identity; `keys/` is mode 0700 | secrets |
| `secrets/` | a nova-secrets store, a git working copy on `main` | secrets |
| `secrets.git` | the store's upstream, a bare repository | secrets |
| `seat.env` | the coordinator's seat, as variables; mode 0600 | seat |
| `logs/` | the Redis loop's log (Linux; on macOS it is `~/Library/Logs/nova-loop-redis-local.log`) | redis |
| `smoke/` | the smoke's twin, its throwaway repository and its record `landed` | smoke |

## Plan, then apply

A setup is a list of steps. Each step has a plan, which reads and never writes, and an apply,
which brings the machine to what the plan wants. A run plans every step first and prints one
line per step:

```
UP <step> <ok|create|change|missing> <detail>
```

- `ok`: already as the step wants it; nothing is run.
- `create`: absent; apply makes it.
- `change`: present and different; apply brings it to the step's form.
- `missing`: a dependency nova-up cannot provide (a program, the operating system). The detail
  carries the command that provides it.

When any step is missing, the run applies nothing and exits 1: nothing is half-applied. Otherwise
it applies, in order, each step that is not ok, planning each again after the steps before it
have applied, so its line reads the machine they left. A step that fails stops the run at exit
1, naming the step and the command; the steps before it stay applied, and a run again starts
from what they left. `--dry-run` prints every step's plan of the machine as it is and writes
nothing: it reads `PATH`, runs each program's version, and lists the secrets store's names when
the store is there.

The first line says how the run ended: `UP OK root=<dir> steps=<n> changes=<n> applied=<n>`,
`UP UNCHANGED ...` when every step is ok, or `UP FAILED ...: <why>` (exit 1, on stderr, with the
step lines under it). `--json` prints the same value as one JSON object, the step lines its
payload.

## Steps

Each step is one file under `internal/up`, registering itself from an init into the step
registry (`up.Register`) with its order. A step added later adds a file and edits no other.

1. **platform** (10): darwin (launchd) or linux (systemd). Any other system is missing.
2. **dirs** (20): the root and `stores/`, `keys/` (0700), `logs/`, `smoke/`.
3. **binaries** (30): `git`, `redis-server`, `sops`, `age-keygen`, `nova-sprint`,
   `nova-secrets`, `nova-redis` and `nova-bus` are each on `PATH` and answer their version
   (`<tool> version`, or `--version` for the programs not of this repository). nova-up installs
   nothing; a program that is not there is missing, with its install command: `brew install`
   on darwin, `apt-get install` on linux, `go install` for a program built from Go.
4. **sprint** (40): the twin store. nova-sprint is the tool with a twin, so the sprint's store is
   `mem:<root>/stores/sprint.twin`, made by `nova-sprint init` with the coordinator as its actor.
5. **secrets** (50): a nova-secrets store for the seat `coordinator`: the seat's key
   (`nova-secrets keygen`), the store on `main` tracking a bare upstream beside it, and its
   `.sops.yaml` naming the seat's public key ([SPEC-SECRETS.md](SPEC-SECRETS.md)).
6. **redis** (60): the tools with no twin (nova-bus) get one Redis, as a supervised loop record
   of the platform's service manager, the same unit `fleet/loops.yml` installs from
   `fleet/templates`: `com.nova.loop.redis-local.plist` as a launchd agent in the login's gui
   domain on darwin, `nova-loop-redis-local.service` as a systemd user unit on linux. It runs
   `nova-redis serve --bind 127.0.0.1 --port 6390 --dir <root>/stores/redis`, loopback only.
   Its ACL users are the ones `nova-redis acl render` names (coordinator, bench, ns-table,
   ns-friend). Each user's password is drawn by nova-up, handed to `nova-secrets seal --stdin
   --no-pr` on standard input, brought onto the store's `main` and pushed; it is in no argument,
   no file nova-up writes and no line it prints. `nova-redis acl apply` and `nova-redis fn load`
   then run under `nova-secrets exec`, which puts the passwords in their environment.
7. **seat** (70): `<root>/seat.env`, the coordinator's seat as variables: `NOVA_SPRINT_REDIS`,
   `NOVA_SPRINT_ACTOR`, the secrets store, seat and key, and the Redis address, user and the
   name of its password's secret. A shell loads it with `set -a; . <root>/seat.env; set +a`.
8. **smoke** (90): one card's whole flow, the first run of the nova-sprint section of
   [CLI.md](CLI.md), on its own twin beside a throwaway repository made for it under the root,
   ending in the card landed. Its record is when it landed and the sprint's summary line.

## Idempotence

Every plan is read from the machine: a file's content against the form the step writes, a
directory's presence, the secrets store's names (`nova-secrets names`, never a value), the
records `acl.applied` and `smoke/landed`. A second run over an applied machine plans ok on every
line, runs nothing but those reads and the version checks, prints `UP UNCHANGED` with
`changes=0`, and exits 0.

## What --local never touches

- No file outside the root but the one unit file of step 6.
- No other unit: a unit of another name, and the fleet's loop units, are left as they are.
- No store but its own: no Redis it did not start, no secrets store but `<root>/secrets`, no
  nova-config, no remote. The secrets store's one upstream is the bare repository in the root.
- No program install, no `sudo`, and no network beyond what a program it runs does on its own.
- No password on a command line or in its output.

## Tests

`internal/up/up_test.go` runs nova-up on a fake machine: a fake exec standing in for every
program (strict where a step relies on it: `nova-secrets seal` refuses an empty stdin, `exec`
refuses a name not sealed, `keygen` a key directory that is not 0700), a filesystem rooted in
`t.TempDir()` and an injected clock. No test starts a service or opens a socket.
`TestUpLocalPlansAWorkingSingleMachineWithNoConfig` applies a whole setup on darwin and on linux,
checks every file, that no password is printed, passed as an argument or written, and that a
second run changes nothing and says so. The dry run, the missing program and system, and the
refusals each have their own test.
