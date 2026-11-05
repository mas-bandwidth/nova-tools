# nova-config

Describe your machines and the commands they should run. Save them in
PostgreSQL, then give the generated inventory to Ansible. Every edit leaves
a history row.

A machine can keep a program running or run it every N seconds. Config calls
that program a **service**. Its row holds the program, arguments, environment,
log path and choice of continuous or scheduled running.

Start with [the setup and command reference](../docs/SPEC-CONFIG.md).
Run `nova-config help` for the command summary.

## Set up the database

```sh
export NOVA_PG_DSN=postgres://nova@db-host:5432/nova
nova-config schema | psql "$NOVA_PG_DSN"
```

Apply the schema before using a fresh or older store. It preserves old columns;
the binary does not migrate the database on startup.

Use `NOVA_PG_PASSWORD` for the tool's password, or name another variable with
`NOVA_PG_PASSWORD_ENV`. Configure psql's login separately.

## Add a machine and a program

```sh
nova-config machine add bench-a --user nova --roles bench --actor ops
nova-config service add hello --machine bench-a --argv '["/usr/bin/printf","hello\n"]' --every 60 --env '{"LANG":"C"}' --actor ops
nova-config history service hello --since 7d
```

These commands save settings. The [fleet plays](../docs/FLEET.md#running-programs)
apply them to machines. Use `--log` to choose a log path.

## Build and test

From the repository root, on a build machine:

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-config" ./config
"$build_dir/nova-config" help
go test ./config
go test -tags functional ./config
```

The first test command uses an in-memory store. The functional tests start a
disposable PostgreSQL instance and skip when `initdb` is absent. They check
that a row and its history are written in one transaction. There is no TLA+
model for this tool. See [TESTING.md](../docs/TESTING.md) for the shared checks.
