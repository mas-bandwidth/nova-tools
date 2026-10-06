# nova-table — dogfood report, 2026-10-06

Tool: `nova-table` (branch `sprint/dogfood-opencode-table-b.w1.g1.e15`, tip `4b29da81a`)
Author: opencode (dogfood card)

## How this was run

A stranger's path only: `nova-table -h`, `nova-table help`, `nova-table help <verb>`,
`nova-table <verb> -h`, and `docs/nova-table/README.md` + `docs/SPEC-NOVA-TABLE.md`.
No source was read.

No store could be started here (the sandbox denies executing `redis-server`, and the
`redis-server` on PATH is a symlink to `redis-check-rdb`), and the one reachable store
(`127.0.0.1:6379`) carries an older `nova_sprint` library with no `ns_table_*` function.
So every read verb and every real write could only be exercised through its store-less
behaviour: the write verbs under `--dry-run` (the documented store-free form), and the
read verbs through their no-store refusal. The store wall itself is finding 8. All
commands below were run; the printed lines are the first lines of the real output.

## Findings

1. URGENT — `nova-table help help`
   printed:
   ```
   HELP REFUSED: unknown verb "help"; did you mean help? the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
   ```
   (exit 2; `nova-table help -h` prints the same line with `unknown verb "-h"`, also exit 2.)
   expected: the `help` verb is listed in its own verb list, so `nova-table help help` and
   `nova-table help -h` should print the help page for `help` on stdout and exit 0, as
   every other verb's `-h` does. The tool tells the reader that `help` is a verb and then
   denies it, with the suggestion `did you mean help?`. This is help that lies.

2. NEXT — `nova-table list --seat nosuchseat`
   printed:
   ```
   LIST REFUSED: --redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); for a first try, start a throwaway store and give each verb the --redis this prints; run: d=$(mktemp -d) && '/home/ubuntu/.local/bin/redis-server' --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes && echo "--redis $d/redis.sock"
   ```
   (exit 2.)
   expected: `--seat` is a documented flag of `list` (its own help page lists `--seat`), so
   giving one should resolve that seat or refuse naming the seat as unknown. Instead the
   refusal says a seat is an acceptable address while ignoring the seat that was given and
   demanding `--redis`. A seat user cannot tell whether `--seat` was honoured.

3. NEXT — `nova-table show demo --json`
   printed:
   ```
   SHOW REFUSED: unknown flag --json; the flags of show are --at-epoch, --redis; run: nova-table help show
   ```
   (exit 2; the same for `list --json`, `check --json`, `render --json`,
   `cell members ... --json`, `view list --json`, `member find ... --json`, and
   `version --json`, which says `VERSION REFUSED: takes no arguments`.)
   expected: the standard is one result value with two renderings and `--json` on every
   verb; here only `batch` and `member read` accept `--json`, so an AI consuming a table,
   a member list, a check or a view list must parse drawn prose.

4. NEXT — `nova-table list --bogus`
   printed:
   ```
   LIST REFUSED: unknown flag --bogus; the flags of list are --redis; run: nova-table help list
   ```
   (exit 2; `show --bogus` says `the flags of show are --at-epoch, --redis`, `check --bogus`
   says `the flags of check are --redis`, `create --bogus ...` omits `--seat` too.)
   expected: the refusal claims to name the verb's flags but omits `--seat`, which the verb's
   own help page lists under connection. The listed flags are not the flags.

5. NEXT — `nova-table batch --dry-run '{"schema":1}'`
   printed:
   ```
   BATCH REFUSED: table "" wants letters, digits, _ . and -; checked before sending, so this call changed nothing; it says nothing about an earlier call with the same operation id; code=NAME; changed=no; run: nova-table batch -h
   ```
   (exit 1.)
   expected: the manifest is missing every required key (`table`, `epoch`,
   `expected_table_revision`, `operation_id`, `members`), so the refusal should name them;
   instead it blames an empty `table` value and reports one problem at a time
   (`{"schema":1,"table":"demo"}` reports only `operation_id`, and so on). A caller fixes
   one fault per attempt.

6. NEXT — `nova-table member read demo m1 --redis 127.0.0.1:6379`
   printed:
   ```
   MEMBER-READ REFUSED: table "demo" read set: ns_table_read_set: ERR Function not found: function ns_table_read_set; the store's nova_sprint library does not register it: it is older than this nova-table, or was removed after this process loaded it; run: nova-redis fn load --addr <host:port> (the deployer's load of this build's library); run: nova-table show 'demo'
   ```
   (exit 1.)
   expected: one refusal is one grammar, `VERB REFUSED: <reason>; run: <remedy>`. This line
   carries two `run:` clauses; the second is appended to the library-mismatch remedy, so a
   reader cannot tell which command to run.

7. NEXT — `nova-table view state today <65-byte text> --dry-run`
   printed:
   ```
   VIEW-STATE REFUSED: a state is one line of at most 64 bytes; --clear removes it; run: nova-table help view state
   ```
   (exit 2.)
   expected: an over-bound value is a `LIMIT` refusal that names the bound and the count
   found; here it is a usage refusal at exit 2 that never says the observed length, so the
   caller cannot see how much to cut. Compare `member create ... <257-byte id> --dry-run`,
   which does name `bound 256, observed 257`.

8. NEXT — `nova-table list --redis mem:/tmp/x` (the store-free path)
   printed:
   ```
   LIST REFUSED: redis at "mem:/tmp/x" given to this tool: unreachable: not an address: its port is not a number from 1 to 65535; next: give host:port (a port from 1 to 65535) or the absolute path of a Unix socket; for a first try, start a throwaway store and give each verb the --redis this prints; run: d=$(mktemp -d) && '/home/ubuntu/.local/bin/redis-server' --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes && echo "--redis $d/redis.sock"
   ```
   (exit 2.)
   expected: a way to run the reads with nothing dialled — an embedded store or a `mem:`
   address, as `nova-sprint` has with `--redis mem:<file>`. With no store startable, no read
   verb (`list`, `show`, `render`, `check`, `member find/read`, `cell members`, `view
   show/list`) and no real write could be exercised end to end; the whole dogfood ran on
   `--dry-run` plans and refusals. This is the gap the README's own first run asks the
   reader to fill by hand.

9. NEXT — `nova-table member create demo '' --dry-run`
   printed:
   ```
   TABLE DRY-RUN verb=member-create arg1=demo arg2="" sends="FCALL ns_table_member_create" redis=- dialled=0 written=0
   ```
   (exit 0; `cell add demo r c '' --dry-run` likewise accepts the empty id.)
   expected: a member id is a nonempty string (SPEC-NOVA-TABLE, "an id is a nonempty
   string"), and `--dry-run` "refuses exactly where the real run refuses before sending";
   an empty id should be refused before the call is planned.

10. NEXT — `nova-table create demo --columns 'a,' --dry-run`
    printed:
    ```
    TABLE DRY-RUN verb=create arg1=demo columns=a, sends="FCALL ns_table_create" redis=- dialled=0 written=0
    ```
    (exit 0; `',a'` and `'a,,b'` are accepted too, as is `--width ','`.)
    expected: an empty column name is not a name. The parser validates a duplicate name
    (`a,a`) and a formula reference, but not an empty one, so a spec with a trailing or
    doubled comma is sent and only the store can refuse it.

11. NEXT — `nova-table create demo --columns 'pct(nope)'`
    printed:
    ```
    CREATE REFUSED: --columns: column "pct(nope)" wants a name of letters, digits, _ . and -; run: nova-table help create
    ```
    (exit 2.)
    expected: the reader wrote a formula without the required `name:` prefix; the refusal
    explains the column-name grammar but never says a formula needs a name before it
    (`pctcol:pct(nope)`), so the real mistake is left unnamed.

## Scores

READ 8/10 — help is layered (`help`, `help <verb>`, `help <verb> <subverb>`, `<verb> -h`),
every page states usage, example, flags, connection, exit codes and effect, and the
examples run, but the `help` verb cannot document itself and the reads are not readable as
JSON.

USE 7/10 — every write verb plans under `--dry-run` with the exact call it would send and
refuses exactly where the real run refuses before sending, and the refusals are unusually
specific; but no read or real write can be run without a store a stranger can start, and
the documented `--seat` is silently ignored in the address refusal.

urgent=1 next=10
