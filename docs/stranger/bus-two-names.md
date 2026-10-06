# A cold run of nova-bus between two names

A stranger's run of the README's `nova-bus` trial: two names, alice and bob, on a throwaway Redis, send each other a message, read it and acknowledge it. The run read only README.md, the tools' own `help` and `<verb> -h` output; where the run could not go on from those, the Stumbles section says what was read instead.

## Setup

- Bench: a Linux x86-64 bench, one throwaway container from the functional image (`infra/functional-image`: Go 1.26.6, Redis 8.10.2), `--network none`, run with `--timeout 14400`; the work directory was a mount, and the container and the directory were removed at the end.
- The `go install ...@v1.0.0` line of the README could not run with no network (stumble 1); the tools were built from a source checkout of the branch under test, with the image's module cache mounted read only.
- Versions printed by `<tool> version`: `nova-bus devel linux/amd64 go1.26.6`, `nova-redis devel linux/amd64 go1.26.6`; `nova-config` and `nova-friend` were built the same way.
- Redis: first `nova-redis serve` on 127.0.0.1:6379 (it needs a password), then a plain `redis-server` on 127.0.0.1:6380 with no users, which is the store `nova-bus` was pointed at.

## Transcript

Every command run, in order, with its exact output. `[exit N]` is the exit status. Trimmed only where marked `[trimmed: ...]`: the Redis start-up log lines and the permission lists of `acl render`.

```text
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0: module lookup disabled by GOPROXY=off
[exit 1]
$ ls /gomodcache/github.com/mas-bandwidth 2>&1 | head -3; cd /work/src && GOFLAGS=-mod=readonly GOCACHE=/work/home/gocache GOBIN=/work/bin go install ./cmd/nova-bus ./cmd/nova-redis 2>&1 | tail -5; ls /work/bin
ls: cannot access '/gomodcache/github.com/mas-bandwidth': No such file or directory
go: downloading github.com/redis/go-redis/v9 v9.22.0
internal/redisconn/classify.go:14:2: module lookup disabled by GOPROXY=off
internal/redisconn/open.go:17:2: module lookup disabled by GOPROXY=off
ls: cannot access '/work/bin': No such file or directory
[exit 2]
$ cd /work/src && GOFLAGS=-mod=readonly GOCACHE=/work/home/gocache GOBIN=/work/bin go install ./cmd/nova-bus ./cmd/nova-redis 2>&1 | tail -5; ls /work/bin
nova-bus
nova-redis
[exit 0]
$ export PATH=/work/bin:$PATH; nova-bus version; nova-redis version; nova-bus help
nova-bus devel linux/amd64 go1.26.6
nova-redis devel linux/amd64 go1.26.6
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

how it works: the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand; send and recv refuse a deaf name (no push proven in 10m).
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS); loopback or tailnet only.

usage:
  nova-bus wait [--as <me>] [--after <id>] [--timeout <duration>] [--skip-subject <prefix,...>] [--wake-file <path>] [--redis <addr>]
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus peek [--as <me>] [--kind <k>[,<k>]] [--redis <addr>]
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--redis <addr>] [--dry-run]
  nova-bus ack [--as <me>] --id <id,...> [--redis <addr>] [--dry-run]
  nova-bus log [--bodies] [--max <n>] [--redis <addr>]
  nova-bus names [--redis <addr>]
  nova-bus version
  nova-bus help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).

example:
  nova-bus wait --as bob --timeout 1s
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
  nova-bus peek --as bob
  nova-bus recv --as bob --exec true
  nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
  nova-bus log --max 5
  nova-bus names
[exit 0]
$ export PATH=/work/bin:$PATH; nova-redis help
nova-redis: run a local Redis store, and keep short-lived named values in it

how it works: serve runs redis-server on loopback or tailnet addresses only, with its data in --dir.
spill writes a value under <owner>:<name> with a required expiry; recall reads it back.
fn load and fn check install and verify the functions nova-table and nova-sprint call.
The password is read from the variable NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD.
first run: the --dry-run line needs no store; spill and recall need a Redis at 127.0.0.1:6379.

usage:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--dry-run]
  nova-redis spill --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
  nova-redis recall --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>
  nova-redis fn load --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis fn check --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl render
  nova-redis acl check --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl apply --addr <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
  nova-redis version
  nova-redis help [<verb>]

Every verb but serve, fn load, fn check, acl render, acl check, acl apply takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).

example:
  nova-redis version
  nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note
[exit 0]
$ export PATH=/work/bin:$PATH; nova-redis serve -h
usage: nova-redis serve [flags]
from `nova-redis help`:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--dry-run]
The store's ACL users live in <store-dir>/users.acl (mode 0600), which nova-redis acl apply
writes through with ACL SAVE, so a restart keeps them; serve writes its default user
(the password's SHA-256, never the password) before each launch. --users names the users
that file must hold: a store whose file lacks one is refused and nothing starts.
--dry-run validates --bind, --port and --dir's absolute shape, checks --users against
the ACL file, and prints the binding, the port, the store directory, the ACL file and
the persistence, auth and eviction rules it would use. It defers every effect: it
creates no directory, writes no file, reads no password or environment, looks up no
redis-server and launches nothing.
flags:
  --bind <string>  comma-separated IP addresses to listen on, loopback (127.0.0.1, ::1) or tailnet (100.64.0.0/10, fd7a:115c:a1e0::/48) only
  --dir <string>  the absolute path of the store directory (AOF, RDB and ACL files), created 0700 when missing
  --dry-run  print what the verb would write and write nothing
  --port <string>  the TCP port to listen on, 1 to 65535 (6379 is Redis's own)
  --users <string>  comma-separated ACL users the store's ACL file must hold (those nova-redis acl apply set); a store missing one is refused
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
[exit 0]
$ export PATH=/work/bin:$PATH; mkdir -p /work/redis; (nohup nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis.log 2>&1 &) ; sleep 2; cat /work/redis.log | head; nova-bus names
SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under `nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve ...` so auth comes from nova-secrets at run time, never an argument; run: nova-redis help
NAMES REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help
[exit 2]
$ export PATH=/work/bin:$PATH NOVA_REDIS_PASSWORD=throwaway; (nohup nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis.log 2>&1 &) ; sleep 2; cat /work/redis.log | head; nova-bus names --redis 127.0.0.1:6379
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 program=/usr/local/bin/redis-server
4940:C 06 Oct 2026 15:15:34.439 * Redis version=8.10.2, bits=64, commit=00000000, modified=0, pid=4940, just started
4940:M 06 Oct 2026 15:15:34.445 * Running mode=standalone, port=6379.
4940:M 06 Oct 2026 15:15:34.449 * Server initialized
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
[exit 2]
$ export PATH=/work/bin:$PATH NOVA_REDIS_PASSWORD=throwaway NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus names -h; nova-bus names
usage: nova-bus names [flags]
from `nova-bus help`:
  nova-bus names [--redis <addr>]
  nova-bus names
Prints NAMES OK count=<n> proven=<n>, then one NAMES NAME name=<name> push=<state> age=<age>
harness=<h> line per known name (nova-config's friend and machine rows). push is proven (its friend
daemon proved and renewed its inbox push under ten minutes ago: send and recv take it), stale (the
daemon stopped renewing), down (its session did not answer the daemon's SESSION CHECK) or none (no
daemon ever recorded one); age is how long ago the daemon wrote it, never when there is none.
flags:
  --json  print the result as one JSON object instead of lines
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else the fleet row's bus from the sprint store)
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
[exit 2]
$ export PATH=/work/bin:$PATH NOVA_REDIS_PASSWORD=throwaway NOVA_BUS_REDIS=127.0.0.1:6379; export NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_PASSWORD; nova-bus names
NAMES OK count=0 proven=0
[exit 0]
$ export PATH=/work/bin:$PATH NOVA_REDIS_PASSWORD=throwaway NOVA_BUS_REDIS=127.0.0.1:6379 NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_PASSWORD; nova-bus send --as alice --to bob --subject hello --body 'hi bob'
SEND REFUSED: --as alice is not the login user default: this connection acts as default; drop --as, or log in as alice (NOVA_SPRINT_REDIS_USER=alice with its password); run: nova-bus help
[exit 2]
$ export PATH=/work/bin:$PATH NOVA_REDIS_PASSWORD=throwaway NOVA_BUS_REDIS=127.0.0.1:6379 NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_PASSWORD; nova-bus send -h | head -40
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
Prints SEND OK id=<ulid> to=<names> cc=<names> [kind=<k>] at=<RFC3339> bytes=<n> sha256=<hex>: the id is the
message's for ever, and the byte count and digest are the body's as the store holds it, so a sender
can check a --stdin or shell-built body arrived whole (a shell's $(cat f) drops the trailing newline).
You are the user the connection logged in as (NOVA_SPRINT_REDIS_USER): --as may name it or be left
out, and another name is refused. With no login (a store with no users) --as is your word for who you
are, and the line says login=none. The sender and every recipient must be heard: a name whose friend
daemon proved its inbox push (a SESSION CHECK carried in by its harness's deliver adapter and answered
by the session) under ten minutes ago; any other is refused with deaf: <name> has no proven push since
<age> and the remedy, and nothing is written (nova-bus names shows each name's push). --dry-run checks
the message as send does (every problem named) and prints the line with no id, writing nothing.
flags:
  --as <string>  your name, the sender: the login user when there is one (then it may be left out)
  --body <string>  the message's text (or --stdin; at most 1 MiB)
  --cc <string>  more recipients, comma-separated names; each gets the message as well
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --kind <string>  the kind of message, one of report, ack, status, request, blocker: what a reader filters on
  --re <string>  the id of the message this one answers
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else the fleet row's bus from the sprint store)
  --stdin  read the message's text from stdin
  --subject <string>  one line saying what the message is (required)
  --to <string>  the recipients, comma-separated names (required)
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: delivery: sends beyond this machine: one entry on every recipient's stream and the log, in one transaction
[exit 0]
$ export PATH=/work/bin:$PATH NOVA_REDIS_PASSWORD=throwaway NOVA_BUS_REDIS=127.0.0.1:6379 NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_PASSWORD; nova-redis acl apply -h; nova-redis acl render | head -30
usage: nova-redis acl apply [flags]
from `nova-redis help`:
  nova-redis acl apply --addr <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
flags:
  --addr <string>  the store's address as <host:port>, such as 127.0.0.1:6379 (no default)
  --dry-run  print what the verb would write and write nothing
  --password-env <string>  the NAME of the variable that holds the password, never the password itself (default: the variable $NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD)
  --password-env-for <value>  <user>=<VARIABLE>, repeatable: the variable holding the password a user apply creates gets; a user the store lacks is created only with one
  --user <string>  the ACL user to log in as (default $NOVA_REDIS_USER; with neither, the store's default user)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
ACL FAMILY name=tables keys=table:*,tables
ACL FAMILY name=views keys=view:*,views
ACL FAMILY name=sprint keys=sprint:*
ACL FAMILY name=machines keys=machine:*,machines
ACL FAMILY name=beats keys=bench:*
ACL FAMILY name=friends keys=friend:*,friends,friends:*
ACL FAMILY name=fleet keys=fleet:*
ACL FAMILY name=loops keys=loops,loop:*
ACL FAMILY name=routes keys=routes,route:*
ACL FAMILY name=config keys=config:decl
ACL FAMILY name=tokens keys=tokens:ledger:*
ACL FAMILY name=events keys=ev:github
ACL SETUSER coordinator [trimmed: 1613-character permission list]
ACL SETUSER bench [trimmed: 1431-character permission list]
ACL SETUSER ns-table [trimmed: 750-character permission list]
ACL SETUSER ns-friend [trimmed: 1433-character permission list]
ACL RENDER OK users=4 functions=41 library=0f9c8ca0574bb69c
[exit 0]
$ export PATH=/work/bin:$PATH NOVA_REDIS_PASSWORD=throwaway NOVA_BUS_REDIS=127.0.0.1:6379 NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_PASSWORD; nova-bus help send; nova-bus send --as alice --to bob --subject hello --body hi --dry-run
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
Prints SEND OK id=<ulid> to=<names> cc=<names> [kind=<k>] at=<RFC3339> bytes=<n> sha256=<hex>: the id is the
message's for ever, and the byte count and digest are the body's as the store holds it, so a sender
can check a --stdin or shell-built body arrived whole (a shell's $(cat f) drops the trailing newline).
You are the user the connection logged in as (NOVA_SPRINT_REDIS_USER): --as may name it or be left
out, and another name is refused. With no login (a store with no users) --as is your word for who you
are, and the line says login=none. The sender and every recipient must be heard: a name whose friend
daemon proved its inbox push (a SESSION CHECK carried in by its harness's deliver adapter and answered
by the session) under ten minutes ago; any other is refused with deaf: <name> has no proven push since
<age> and the remedy, and nothing is written (nova-bus names shows each name's push). --dry-run checks
the message as send does (every problem named) and prints the line with no id, writing nothing.
flags:
  --as <string>  your name, the sender: the login user when there is one (then it may be left out)
  --body <string>  the message's text (or --stdin; at most 1 MiB)
  --cc <string>  more recipients, comma-separated names; each gets the message as well
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --kind <string>  the kind of message, one of report, ack, status, request, blocker: what a reader filters on
  --re <string>  the id of the message this one answers
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else the fleet row's bus from the sprint store)
  --stdin  read the message's text from stdin
  --subject <string>  one line saying what the message is (required)
  --to <string>  the recipients, comma-separated names (required)
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).
effect: delivery: sends beyond this machine: one entry on every recipient's stream and the log, in one transaction
SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
[exit 1]
$ export PATH=/work/bin:$PATH; unset NOVA_REDIS_PASSWORD NOVA_SPRINT_REDIS_USER NOVA_SPRINT_REDIS_PASSWORD_ENV; export NOVA_BUS_REDIS=127.0.0.1:6380; mkdir -p /work/plain; (cd /work/plain; nohup redis-server --port 6380 --bind 127.0.0.1 --save '' --appendonly no > /work/plain.log 2>&1 &); sleep 1; nova-bus names; nova-bus send --as alice --to bob --subject hello --body 'hi bob'
NAMES OK count=0 proven=0
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
[exit 2]
$ cd /work/src && GOFLAGS=-mod=readonly GOCACHE=/work/home/gocache GOBIN=/work/bin go install ./cmd/nova-config 2>&1 | tail -3; export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; nova-config help | head -50
nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

how it works: each kind (machine, fleet, friend, sprint, loop, route, tier) is a
table of rows in PostgreSQL's schema config, which migrate makes; every write
adds a history row naming who made it. apply copies the rows into Redis, the
view the fleet reads; inventory prints that view for Ansible. --file <path>
keeps the rows in a local JSON file instead, to try every verb with no database.
first run: the example: lines need no database and write only ./try.json; the fleet's store is
  export NOVA_PG_DSN=postgres://user@host:5432/db
then migrate.

usage:
  nova-config help [<verb>]
  nova-config version
  nova-config kinds [--json]
  nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]
  nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>]
                    [--kind <kind>] [--dry-run] [--json]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>]
                        [--timeout <duration>]
  nova-config <kind> add <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> set <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> remove <name> --as <name> [--dry-run] [--json]
  nova-config <kind> list [--json]
  nova-config <kind> show <name> [--json]
  nova-config <kind> history <name> [--json]
  nova-config machine width <name> [--json]
  nova-config machine self [--check] [--json]
  nova-config login --store <dir> --as <seat> --key <file> --secret <NAME>
                    --dsn <dsn> --friend <actor> [--sops <path>]
                    records the DSN and where the password is; never the password
  nova-config login --check
                    prints that login and whether the secret resolves
  nova-config logout
                    removes the recorded login
  nova-config fleet set|show|history        one row each, no name:
                                            fleet and sprint have no add, remove or list
  nova-config sprint set|show|history
  nova-config <kind> <verb> -h              the verb's flags (required ones marked),
                                            its effect and a worked example

The store is --pg <dsn> (or NOVA_PG_DSN; the password is never on the line:
NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password,
NOVA_PG_PASSWORD when it is unset, and never the password itself), or --file
<path>, or --seat <name> (or NOVA_SEAT) which supplies the DSN and password
variable name from the seat profile, or the login nova-config login records
(the DSN and friend; the password is read in this process from nova-secrets,
never recorded and never put in an environment). --pg, NOVA_PG_DSN and
NOVA_PG_PASSWORD_ENV still win when given. --redis is host:port
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; nova-config friend add -h | head -30
usage: nova-config friend add [flags]
effect: store write: one row and its history row, in PostgreSQL or the --file; --dry-run prints the change (CONFIG DRY-RUN) and writes nothing
required: --slots --tiers (and --as); every other field takes its default
example: nova-config friend add f1 --slots 4 --tiers flash,pro --roles builder --as a1 --file try.json
flags:
  --as <name>  the name a write is recorded under in the history (env NOVA_FRIEND)
  --config_dir <text>  text: the absolute directory a claude one-shot lane runs with as CLAUDE_CONFIG_DIR, her account's login and settings; unset (the default, or --config_dir '') for any other harness; nova-friend run refuses a claude friend in one-shot mode without it
  --dry-run  print the change the write would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --mode <batch|one-shot>  batch|one-shot: how her daemon hands her work: batch (the default: every waiting message in one turn) or one-shot (width lanes, each its own session, handed one card per turn)
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --roles <list>  list: comma list of builder, may-hold, reader (who coordinates is the sprint row's)
  --seat <seat>  the seat profile in seats.tsv supplying the PostgreSQL DSN and password variable name (env NOVA_SEAT); exclusive with --file
  --slots <number>  required; number: her desired slots, under the ceiling of the machine her beat reports; no machine's width
  --tiers <list>  required; list: which tiers she can do: comma list of flash, frontier, heavy, pro
  --width <number>  number: the jobs she works at once, the width nova-sprint friend sync sets on her friends row; at least 1, 8 by default
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
ready=no, nothing attempted), 2 could not run (usage, or a store that did not answer);
machine self: 2 not a row, 3 unreadable
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; nova-config migrate --file ./fleet.json; nova-config friend add alice --slots 1 --tiers flash --as alice --file ./fleet.json; nova-config friend add bob --slots 1 --tiers flash --as alice --file ./fleet.json
CONFIG MIGRATE file=./fleet.json from=0 to=34 applied=34
CONFIG ADD kind=friend name=alice rev=1
CONFIG ADD kind=friend name=bob rev=2
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; nova-config apply -h | head -12; nova-config apply --file ./fleet.json --redis 127.0.0.1:6380 --as alice; nova-bus names; nova-bus send --as alice --to bob --subject hello --body 'hi bob'
usage: nova-config apply [flags]
from `nova-config help`:
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>]
  [--kind <kind>] [--dry-run] [--json]
effect: external delivery: writes Redis, the copy of the rows the fleet reads, through its own Redis Functions; --dry-run prints the lines and writes nothing
full apply and --kind fleet require explicit redis_port and pg_dsn; set both with nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>
example: nova-config apply --dry-run --redis 127.0.0.1:6379 --file try.json
flags:
  --as <name>  the name a write is recorded under in the history (env NOVA_FRIEND)
  --check  the same as --dry-run
  --dry-run  print the ADD, SET and REMOVE lines (CHECK ...) and write nothing; it still reads the store and Redis
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
NAMES OK count=0 proven=0
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
[exit 2]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; nova-config apply --kind friend --file ./fleet.json --redis 127.0.0.1:6380 --as alice; nova-bus names; nova-bus send --as alice --to bob --subject hello --body 'hi bob'
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
NAMES OK count=0 proven=0
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
[exit 2]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; nova-config machine add -h | head -14; nova-config fleet set -h | head -14
usage: nova-config machine add [flags]
from `nova-config help`:
  nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json
effect: store write: one row and its history row, in PostgreSQL or the --file; --dry-run prints the change (CONFIG DRY-RUN) and writes nothing
required: --user --seat --slots (and --as); every other field takes its default
the note is why a choice was made, and its history says who wrote it and when: show prints it whole, list cuts it to 60 characters
example: nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json
flags:
  --as <name>  the name a write is recorded under in the history (env NOVA_FRIEND)
  --dry-run  print the change the write would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --note <text>  text: why the machine is as it is: a hold, a rest, the load that was measured; one line, empty (the default) when none; --note '' clears it
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
usage: nova-config fleet set [flags]
effect: store write: one row and its history row, in PostgreSQL or the --file; --dry-run prints the change (CONFIG DRY-RUN) and writes nothing
example: nova-config fleet set --coordinator m1 --store m1 --loops_dir ~/nova-bench/loops --as a1 --file try.json
flags:
  --as <name>  the name a write is recorded under in the history (env NOVA_FRIEND)
  --bus <text>  text: the bus store's Redis address, host:port, what nova-bus reads from the applied fleet:bus when NOVA_BUS_REDIS is unset; empty until set
  --coordinator <name>  the name of a machine row: the machine the coordinator's loops run on (a machine row), or empty
  --dry-run  print the change the write would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --loops_dir <text>  text: the directory where loop logs are written; non-empty, seeded to ~/nova-bench/loops
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --pg_dsn <text>  text: the explicit password-free postgres:// URI the configuration store uses; empty until set
  --redis_port <number>  number: the explicit TCP port Redis listens on, from 1 through 65535; unset until declared
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; F="--file ./fleet.json --as alice"; nova-config machine add m1 --user bench --seat s1 --slots 2 --width 2 $F; nova-config fleet set --coordinator m1 --store m1 --redis_port 6380 --pg_dsn postgres://nobody@127.0.0.1:5432/nova --bus 127.0.0.1:6380 $F; nova-config apply --file ./fleet.json --redis 127.0.0.1:6380 --as alice; nova-bus names
CONFIG ADD kind=machine name=m1 rev=3
CONFIG SET kind=fleet name=fleet rev=4 changed=bus,coordinator,pg_dsn,redis_port,store
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=17
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,bus,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=1
APPLY ADD kind=friend name=alice
APPLY ADD kind=friend name=bob
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=2
APPLY SET kind=sprint name=sprint changed=decide_bounce,decide_review
CONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=0 ms=1
CONFIG APPLY kind=loop add=0 set=0 remove=0 rev=0 ms=0
CONFIG APPLY kind=route add=0 set=0 remove=0 rev=0 ms=0
APPLY ADD kind=tier name=flash
APPLY ADD kind=tier name=heavy
APPLY ADD kind=tier name=pro
CONFIG APPLY kind=tier add=3 set=0 remove=0 rev=0 ms=1
NAMES OK count=3 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; nova-bus send --as alice --to bob --subject hello --body 'hi bob'; nova-bus help recv | head -30
SEND REFUSED: deaf: alice has no proven push since never: no daemon has recorded one; the remedy: alice runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
usage: nova-bus recv [flags]
from `nova-bus help`:
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--redis <addr>] [--dry-run]
  nova-bus recv --as bob --exec true
Prints one message: a line RECV OK id=<id> from=<name> to=<names> cc=<names> re=<id> [kind=<k>] at=<RFC3339>
subject=<s> (login=none when the connection has no login user), a blank line, the body; or RECV
NONE at exit 1 when nothing waits. You are the login user, as in send, and must be heard as there: a
recv for a name with no proven push is refused (deaf: <name> ...). The oldest message a
reader lost (delivered, not acked, idle fifteen minutes) comes first, else the oldest new one; the
reader keeps it for fifteen minutes. --exec '<command>' runs the command with that same text on its stdin and
acks the message when it exits 0 (the line adds acked=true exec_exit=0); a non-zero exit leaves
it pending and is RECV FAILED at exit 1. --max <n> takes up to n messages in order and --all every
one waiting, each printed as its own RECV OK (or handed to --exec and acked on exit 0, stopping
at the first command that fails); --ack acks each after a plain recv prints it. --forever loops,
waiting for messages, and needs --exec; it stops on SIGINT or SIGTERM, or at the first command
that fails. --dry-run moves nothing: it prints RECV OK pending=<n> new=<n> next_new=<id>, what
waits (a pending message held past fifteen minutes comes before the oldest new one).
--kind <k>[,<k>] takes only messages of those kinds, in --all, --max, --forever and --dry-run too: a
message of another kind is skipped, neither acked nor held, and the next recv without the filter
gets it. kind=<k> is left off the line of a status (the default), so an absent kind is a status, as for a message sent
before kinds existed.
flags:
  --ack  ack each message after printing it (a plain recv leaves it pending)
  --all  take every message waiting, in order, each its own result
  --as <string>  your name, the recipient: the login user when there is one (then it may be left out)
  --dry-run  print what the verb would write and write nothing
  --exec <string>  a shell command run with each message on its stdin; exit 0 acks the message
  --forever  loop over every message, delivering each with --exec, until a signal
  --json  print the result as one JSON object instead of lines
  --kind <string>  only these kinds, comma-separated, of report, ack, status, request, blocker (default: every kind); others are left for the next reader
[exit 0]
$ cd /work/src && GOFLAGS=-mod=readonly GOCACHE=/work/home/gocache GOBIN=/work/bin go install ./cmd/nova-friend 2>&1 | tail -3; export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; nova-friend help | cut -c1-260 | head -60
nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon

how it works: one launchd agent per friend (install) runs the daemon (run): it parks on the friend's
nova-bus stream and, when the session is free, pushes every waiting message in as one turn (the
harness's deliver command), beats to the sprint server while the session answers, answers the
coordinator PING at once (daemon-pong); presence is the session's word on the bus, never a process.
state: <dir>/.nova-friend/ (--state-dir moves it), the queue: <dir>/inbox/QUEUE.json.

usage:
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--
  nova-friend install --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--model <provider/model>] [--secrets
  nova-friend uninstall --as <me> [--dry-run]
  nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]
  nova-friend host --as <me> --harness <h> --dir <d> [--prompt <regexp>] [--state-dir <d>] [--dry-run] [--json] -- <launch command...>
  nova-friend ping --as <coordinator> (--to <friend> | --wake --to-friends [--every <d>] [--within <d>] [--never-wake <f,...>] [--server <addr>]) [--nonce <n>] [--since <RFC3339>] [--redis <addr>] [--dry-run]
  nova-friend ping-install --as <coordinator> --every <d> [--within <d>] [--never-wake <f,...>] [--server <addr>] [--redis <addr>] [--launchd-log <file>] [--dry-run]
  nova-friend ping-uninstall --as <coordinator> [--dry-run]
  nova-friend pong --as <me> --nonce <n> [--to <coordinator>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--dry-run]
  nova-friend wait-pong --from <friend> --nonce <n> [--timeout <d>] [--redis <addr>]
  nova-friend status --as <me> --dir <d> [--state-dir <d>]
  nova-friend serve --as <coordinator> [--redis <addr>] [--dry-run]
  nova-friend version
  nova-friend help [<verb>]

Every verb but run, serve takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).

example:
  nova-friend install --as bob --harness opencode --dir ./bob --dry-run
  nova-friend uninstall --as bob --dry-run
  nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider
  nova-friend ping --as ada --to bob --nonce abc123
  nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
  nova-friend wait-pong --from bob --nonce abc123 --timeout 2s
  nova-friend status --as bob --dir ./bob
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6380; cd /work/home; mkdir -p alice; nova-friend run -h | grep -i -E 'harness|SESSION CHECK|usage' | cut -c1-300 | head -12; nova-friend run --as alice --harness opencode --dir ./alice --redis 127.0.0.1:6380 2>&1 | head -5
usage: nova-friend run [flags]
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>
The loop launchd runs (install writes it). It starts only on a push proof: a harness with no deliver
CHECK goes in through the harness and its pong must reach the bus within 5m0s, else exit 2 with the remedy
the session (the daemon's own sends never count), a SESSION CHECK <nonce> goes in through the harness
as a turn of its own, once no turn is under way (on the friend's own stream for a harness with no
bus; a restart clears it. A turn whose harness says it is out of credits or at a usage limit (a Claude
Code rate_limit_event rejected, "Insufficient AI Credits ... will refresh 6:52 PM", "usage limit ...
nonce from inside the session before she beats again, and the seat is told she is back. Each harness's own
runs it as a one-shot of the harness (a claude account's model by the read's tier) inside the lane wall, and records read --ok|--broken --finding --usage from the RESULT.md, or read --return --reason --usage when it names no verdict or the provider's usage limit stops it. Every
lane child (the harness's session open and each card's turn) runs inside the wall profile the row names
(row_profile=), else --profile: as nova-friend wall --profile <p> --dir <d> -- <harness>, writes only to
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="opencode session list: exec: \"opencode\": executable file not found in $PATH"; the daemon did not start; run: open the friend's opencode session in ./alice, then prove it answers: nova-friend check --as alice --harness opencode --dir ./alice
[exit 0]
```

The run stopped here: `nova-bus send` refuses a name that has no proven push, and a push is proven only through a friend daemon driving a real harness session (`opencode` and the like), which the container did not have.

## Stumbles

### 1. The README's install line cannot run where the README says to try nova-bus

- read: README.md, "Try one on a small example": `go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0`.
- expected: a `nova-bus` binary at the version the task names.
- happened: `module lookup disabled by GOPROXY=off` in a container with no network. Building from a source checkout then needed the dependency module cache mounted. The README names version 1.0.0 while the tools print `devel`.
card: stranger-readme-offline-install
- PATHS: README.md, docs/USAGE.md
- task: say in the README how to install with no network (build from a checkout, with the module cache present) and state which release the trial commands belong to.

### 2. `<tool> version` prints `devel`

- read: `nova-bus version`, `nova-redis version` after a source build.
- expected: a version number to record in a report.
- happened: `nova-bus devel linux/amd64 go1.26.6`.
card: stranger-version-from-source-build
- PATHS: internal/tool, cmd/nova-bus/main.go
- task: a build from a checkout prints the checkout's release tag or commit rather than `devel`.

### 3. The first-run line sends a stranger to a Redis that the tool then refuses

- read: `nova-bus help`: "first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS)"; `nova-redis help`: "serve runs redis-server on loopback".
- expected: `nova-redis serve` gives a store `nova-bus` can use.
- happened: `serve` refused without `NOVA_REDIS_PASSWORD`; `nova-bus names` against the served store then refused with NOAUTH and named `NOVA_SPRINT_REDIS_USER` and "the variable that holds its password" without naming that variable; the run guessed `NOVA_SPRINT_REDIS_PASSWORD_ENV`. Then `send --as alice` was refused because the login user was `default`, and no alice login exists (`acl render` lists coordinator, bench, ns-table, ns-friend only). The run gave up on the served store and used a plain `redis-server` with no users.
card: stranger-bus-first-store
- PATHS: cmd/nova-bus/main.go, docs/CLI.md
- task: `nova-bus help` names one working throwaway store (the command, the variables) and the refusal that asks for a password names the variable to set.

### 3b. `--dry-run` on send is refused by its own verb

- read: `nova-bus help send` and `nova-bus send -h` both list `--dry-run`.
- expected: the checked message line and no write.
- happened: `SEND FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, exit 1. This text names a code field and misled: it reads as if a message may have been written.
card: stranger-bus-send-dry-run
- PATHS: cmd/nova-bus/send.go
- task: `send --dry-run` prints what it would send and writes nothing, or the flag is removed from the help.

### 4. "A Redis naming ada and bob" does not say how names are made

- read: `nova-bus help` first-run line; then the SEND REFUSED text: "add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply".
- expected: two commands to register two names.
- happened: that remedy is right but not enough. `nova-config friend add` needed `migrate --file` first; `apply` was refused for fleet endpoints, then `--kind friend` was refused for a missing beat/coordinator machine, and the run needed a machine row and `fleet set --coordinator m1 --store m1 --redis_port 6380 --pg_dsn ... --bus ...` before `apply` printed the friends. Each step was found from the previous refusal, which names the next step; no step pointed at the whole path. Names also exist only through a PostgreSQL DSN, which a throwaway trial does not have.
card: stranger-bus-names-recipe
- PATHS: docs/USAGE.md, cmd/nova-config/main.go
- task: one worked, tested recipe (file store, one machine, two friends, apply) from nothing to `nova-bus names` listing two names, linked from the SEND REFUSED line.

### 5. A name must have a proven push before it can send or receive

- read: the SEND REFUSED text for alice and bob: "deaf: ... runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>)".
- expected: a way to try the bus with two plain shell sessions, as the README promises ("try nova-bus against a throwaway Redis").
- happened: `nova-friend install` writes a launchd agent (a macOS service) and `nova-friend run` starts only on a push proof through a real harness: `RUN REFUSED: no push proof: ... opencode session list: exec: "opencode": executable file not found`. With no harness binary the exchange cannot be done. The run ends here.
card: stranger-bus-no-harness-trial
- PATHS: cmd/nova-bus/send.go, docs/SPEC-BUS.md, README.md
- task: give the throwaway trial a way past the deaf check (a documented trial-store flag, or a stand-in harness) or make the README say the trial needs a friend daemon and a harness.

### 6. The container runtime needed an option the image README does not show

- read: `infra/functional-image/README.md` (this was the operator's side, not the stranger's).
- expected: `podman run ...` to start over a non-interactive ssh session.
- happened: `crun: sd-bus call: Access denied ... interactive authentication`; adding `--cgroup-manager=cgroupfs` fixed it. A named volume `nova-gomod` that the image README shows was created empty by the run; the populated volume has another name (`nova-functional-gomod-uid1000`).
card: stranger-functional-image-run-notes
- PATHS: infra/functional-image/README.md
- task: note the cgroup manager for non-login sessions and the name of the populated module-cache volume.

## Verdict

- Could a stranger do it: no. alice and bob never exchanged a message. The run reached `SEND REFUSED: deaf` for both names and could not go on without a harness session.
- Minutes taken: about 10 minutes of container time (start of the container to the stop), after the bench check and the operator-side fixes of stumble 6.
- What worked: `help` and `-h` read well and every refusal named a next step; `nova-config --file` and `nova-redis serve` ran.
