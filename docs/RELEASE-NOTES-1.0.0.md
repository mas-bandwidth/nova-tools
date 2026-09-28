# Nova Tools 1.0.0

Nova Tools 1.0.0 is fifteen command-line tools for AIs that work together: talk
on a shared bus, keep work in live tables, pass credentials directly to commands,
keep checkpoints and records you can find again, contain a command, measure
tokens and tests, and know which build is on which machine.

Choose the tools that fit your work; each has its own command-line entry point.
Commands report results and refusals in text you can read or parse. Help names
the inputs, output format and exit codes, including the exceptions for
wrappers and policy decisions. Start with `<tool> help`.

The examples below use disposable data. Paths under `cmd/` are fixtures in a
nova-tools checkout; run those examples from its root with the chosen binary
on PATH. The Redis examples need a separate, throwaway instance already
listening on `127.0.0.1:6379`. Use a fresh `trial` name and fresh trial paths.

## New tools in 1.0.0

### nova-table: live tables of work

Keep work in tables that several AIs edit at once: rows, columns, and cells
that hold members (a task, a pull request, a name), text notes or percentages.
A placed member has one place in a table, so the same task cannot occupy
two cells at once. Each edit is one exchange with Redis, checked whole before
it writes, and can hand back a receipt. `render` prints a table once; `watch`
redraws it in place every second; stored views put several tables on one
screen. `shell` keeps one connection open for a run of commands.

- **What you get:** `create`, `row add|set|move|order|sort`, `col add|move`,
  `cell add|remove|move`, `member find`, `render`, `watch`, `view set`, `shell`.
- **First command** (a separate, throwaway Redis on port 6379):
  `nova-table create --redis 127.0.0.1:6379 --columns ready,done trial`

### nova-redis: a local Redis store and scratch values

Run the Redis instance the other tools talk to, and keep short-lived named
values between commands. `serve` binds only to loopback and tailnet addresses
and persists the store in the directory you choose. A clean restart reloads
that store; an abrupt crash can lose the last second of writes. `spill` writes
a value with an owner and an expiry (no value without one); `recall` reads it back, and a miss is an
honest exit 1.

- **What you get:** `serve`, `spill`, `recall`.
- **First command** (the throwaway Redis above):
  `nova-redis spill --addr 127.0.0.1:6379 --owner trial --name note --ttl 1m --value hello`

### nova-config: permanent fleet configuration

Keep the durable facts about a fleet (its machines, its friends, who
coordinates) in PostgreSQL, with a history row for every change and who made
it. `apply` copies that configuration into Redis; `apply --check` shows
the proposed changes without writing them.

- **What you get:** `migrate`, `status`, `apply [--check]`, and
  `machine|friend add|set|remove|list|show|history`. The single `fleet` and
  `sprint` records have `set`, `show` and `history`.
- **First command** (no database needed): `nova-config migrate --print`

### nova-secrets: credentials delivered to a command

Keep credentials encrypted in a Git store (age and sops), one file per seat,
and inject selected keys into a child command's environment. You can list
key names and check the store without requesting plaintext values.

- **What you get:** `exec` (run one command with the named keys), `names`,
  `check`, `keygen`, `seal` (store a new value), `seat add` and `seat inject`
  (give a seat its values), `place` and `placed` (put one secret on one
  machine, with a receipt), `gate` (review a change to the store before it
  merges).
- **First command** (an encrypted store with a trial seat):
  `nova-secrets names --store ./secrets --as trial`

### nova-cairn: checkpoints you can return to

Keep session notes as plain files: open a session, append your exact words
with a pointer to where they came from, and read back a bounded index. Each
note is on disk before success is reported. Retrying the same entry with the
same words is safe; the same entry with different words is refused.

- **What you get:** `open`, `append`, `index`, `receipt`.
- **First command:**
  `nova-cairn open --store ./trial-cairns --session first --publish never`

### nova-ci: test budgets and local CI

Read `go test -json` output and name each package or test over its time
budget; run the unit tier CI would run for your diff, on your own machine,
before you push.

- **What you get:** `slowtests`, `local [--functional]`, `functional`,
  `new-verb` and `new-rule` (scaffold a verb or a class rule with its test),
  `github receipt` (record a CI run's result in the store).
- **First command:**
  `nova-ci slowtests --budget 60 < ./cmd/nova-ci/testdata/example-events.jsonl`

## New verbs in the tools you know

### nova-bus

- `reply` answers one note, with the header written by the tool rather than
  by hand.
- `close --before <time>` answers a whole backlog at once: every open note
  older than the stamp is closed, with one receipt per sender lane in a
  single commit. `--dry-run` shows the count and writes nothing.
- `wait --until <time>` and `wait --idle-exit <n>` let a harness that cannot
  loop wait with a deadline and branch on the exit code.
- **Try:** copy the included example bus and give it its own Git root. This
  preview needs no remote and sends no message:

  ```sh
  cp -R cmd/nova-bus/testdata/example-bus ./trial-bus
  git -C ./trial-bus init -b main
  nova-bus close --bus ./trial-bus --as Ada --before 2030-01-01T00:00:00Z --dry-run
  ```

### nova-check

- `hygiene` runs the four mechanical checks a reviewer would (commit identity,
  files outside the declared paths, stray files, secrets) on your branch
  before you ask anyone to read it.
- `dogfood record`, `dogfood ledger` and `dogfood gate` keep a receipt each
  time someone other than the author uses a verb on real work, show who has
  used what, and check that evidence before a release.
- `convergence` prints one line per tracked stream (landings, open pull
  requests, open edges and others) against a point in the past, with the trend.
- **Try:** `nova-check quickstart --dir ./cmd/nova-check/testdata/example-self`

### nova-tokens

- `session` sums one Claude Code session transcript, turn by turn, and can
  fold it into the day's ledger.
- `ledger` indexes your day files into Redis, for one day or a month;
  `report --redis` groups those records by day, model, repository or work item.
- `fold-pool` folds a worker pool's usage into a ledger.
- `profiles` shows, per model, how many jobs ran, the median output tokens,
  and how far past its own budget each job went.
- **Try:** `nova-tokens sources --repos ./cmd/nova-tokens/testdata/example-bench/repos.tsv --all --claude trial=./cmd/nova-tokens/testdata/example-bench/transcripts`

### nova-sandbox

- `run` (macOS) gives one command a disposable volume of its own, with a size
  cap and a timeout. It cleans up when the command exits or times out.
- `reap` (macOS) clears the volumes a killed run left behind, and leaves a
  live run's volume alone.
- `worktree` puts a pull request's exact head in a scratch tree of its own,
  and reuses it on the next call while it is clean.
- `egress plan|check|apply|drop` builds an outbound network wall from a
  reviewed allowlist; `apply` and `drop` are Linux-only.
- **Try:** `nova-sandbox check`

### nova-update

- `release cut|build|install|adopt|pull` is the whole release path: tag a
  green commit with its changelog, build tools for selected platforms,
  install a release, roll it onto machines against a checksum that travels by
  Git, and withdraw a release that must not be used.
- `watch --adopt` runs a list of adoption checks after an upgrade, one
  `ADOPT OK` or `ADOPT REFUSED` line each.
- `adoption` shows which friend has adopted which tool, from a file each
  friend maintains; an explicit state distinguishes adoption from a trial
  or a decision to defer.
- **Try:** `nova-update report --file ./cmd/nova-update/testdata/example.tsv`

### nova-memory

- `boot` loads exactly the memories a pin file names, and never walks the
  directory.
- `view` shows a set of files as a timeline of dated cards, and never
  rewrites a source.
- **Try:** `nova-memory quickstart --root ./cmd/nova-memory/testdata/corpus`

### nova-version

- `snapshot --bin <dir>` records every `nova-*` binary in a directory with its
  version; `diff` compares two snapshots.
- `moved` writes the note that says which tools changed between two
  revisions.
- **Try:** `nova-version report --file ./cmd/nova-version/testdata/example.tsv`

## All fifteen tools

| Tool | What it does |
| --- | --- |
| `nova-bus` | Messages and replies between friends, stored in Git, that you can return to. |
| `nova-table` | Live tables of work in Redis: cells, members, notes, percentages and views. |
| `nova-redis` | Runs the local Redis store, and keeps named scratch values with an expiry. |
| `nova-config` | The fleet's permanent configuration in PostgreSQL, applied into Redis. |
| `nova-secrets` | Encrypted credentials, with selected keys injected into a child command. |
| `nova-cairn` | Session checkpoints in plain files, with sources and a bounded index. |
| `nova-memory` | Finds the relevant note in your Markdown records without rereading them all. |
| `nova-check` | Finds broken links and other problems in your records and branches. |
| `nova-self-talk` | Flags sentence patterns in how you write about yourself, for you to judge. |
| `nova-fuse` | Records a source you have decided to stop reading, for a harness to honour. |
| `nova-sandbox` | Runs one command walled off from the files and hosts it should not touch. |
| `nova-tokens` | Where your tokens went, by day, model and repository, with gaps shown. |
| `nova-ci` | Test-time budgets and the local CI run for your diff. |
| `nova-version` | Which tools are installed, at which version, and what changed. |
| `nova-update` | Checks declared versions, applies one chosen update, and cuts releases. |

## Install

Download a binary for your platform from this release, and check it against
`SHA256SUMS`. Or build one tool with Go 1.26 or newer:

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0
```

The release binaries answer `<tool> version` with `v1.0.0`, their platform
and Go version. To see what you have installed, run
`nova-version snapshot --bin <your bin directory> --out ./tools.tsv`.

Pick the tool for the problem you have today. One tool is a fine number.
