# nova-table dogfood, 2026-10-06 (dsh)

Reviewer: an automated dogfood harness.

Read as a stranger: `nova-table -h`, `nova-table help`, `nova-table <verb> -h`, `docs/CLI.md` section nova-table, and `docs/nova-table/README.md`. Built on the Linux bench from `cb5fb8d4c3290f710d22a21a86aa8d229e4db905` with the page's `go build -o ./nova-table ./cmd/nova-table`. The job copy has no `.git`, so the version line is `nova-table devel linux/amd64 go1.26.6`. The mechanical tip then moved to `d762f545478f8c5118ed9342f69fd23d8b2d5042`. `cmd/nova-table`, `docs/nova-table`, `docs/CLI.md`, and `pkg/ntable` are the same at both commits, so this branch starts at the new tip and the runs still describe it. A Redis was already listening on `127.0.0.1:6379` on that bench; it was not opened. Nothing was pointed at the other local Redis ports or at a tailnet address, and no server was started. Every verb ran inside `podman --network=none`, which has no route to that host store. Write verbs ran with `--dry-run`. Read verbs, which have no `--dry-run`, were given `--redis` of an absolute socket path under the job scratch (`/scratch/nostore.sock`) that was never created. `redis-server` was not on that PATH, so a refusal could not start a store.

`nova-table create demo --columns ready,working,done --dry-run` printed the page's line and nothing else, exit 0: `TABLE DRY-RUN verb=create arg1=demo columns=ready,working,done sends="FCALL ns_table_create" redis=- dialled=0 written=0`. `help` and `-h` are the same text. `help row set`, `row set -h`, and `row set --help` match. A bad formula, a `pct` folded `avg`, a width for a column the spec does not name, `--epoch -1`, an empty table name, batch rules (string score, empty `members`, a field set and unset, `--epoch` or `--actor` disagreeing with the manifest), a 65-byte view state, and `row sort --manual` together with `--keep` or `--desc` all refuse before a dial and name a next command. `-- --pending` is taken as a member. Findings are only where a run did not match the help or the page.

## 1. An unknown flag's inventory leaves out `--seat` — URGENT

**Command:**

    nova-table create demo --columns ready --bogus

**Printed:**

    CREATE REFUSED: unknown flag --bogus; the flags of create are --actor, --columns, --dry-run, --epoch, --epoch-field, --epoch-key, --fence, --footer, --idem, --member-prefix, --receipt, --redis, --width; run: nova-table help create

exit 2. That is the only line.

**Expected:** `--seat` in that list. `create -h` documents `--seat <name>` as "dial as this seat: its seats.tsv row, else the nova-secrets seat of that name". `list --bogus` prints `the flags of list are --redis` and `list -h` also documents `--seat`. `list --seat nosuch` is not rejected as an unknown flag, so the flag is real and the inventory is false.

**Grade:** URGENT

## 2. A row key the tool itself rejects exits 1, the store's code — URGENT

**Command:**

    nova-table row add demo $'build\x01' --dry-run --redis /scratch/nostore.sock

**Printed:**

    ROW-ADD REFUSED: table "demo" row "build\x01": row wants a non-empty UTF-8 key with no ASCII control characters; run: nova-table row help

exit 1. That is the only line. The socket was not created.

**Expected:** exit 2. The guide says exit 2 for a usage refusal and exit 1 when the store said no. This check happened before any dial: `--dry-run` was set and the socket does not exist. `create "" --columns ready --dry-run` is the same class of check and exits 2 (`the table name wants letters, digits, _ . and -, got ""`). A caller that branches on 1 will treat a key the tool rejected as the store having said no.

**Grade:** URGENT

## 3. `--columns` given twice keeps the last spec and exits 0 — NEXT

**Command:**

    nova-table create demo --columns ready,working --columns working,done --dry-run --redis /scratch/nostore.sock

**Printed:**

    TABLE DRY-RUN verb=create arg1=demo columns=working,done sends="FCALL ns_table_create" redis=/scratch/nostore.sock dialled=0 written=0

exit 0. That is the only line.

**Expected:** a refusal. `create -h` shows one `--columns`. The first spec is gone and the plan does not say a value was dropped. The same shape with `--footer one --footer two` prints `footer=two` and exits 0.

**Grade:** NEXT

## 4. `--hidden` and `--visible` are both sent — NEXT

**Command:**

    nova-table set demo --hidden --visible --dry-run --redis /scratch/nostore.sock

**Printed:**

    TABLE DRY-RUN verb=set arg1=demo hidden=true visible=true sends="FCALL ns_table_set" redis=/scratch/nostore.sock dialled=0 written=0

exit 0. That is the only line.

**Expected:** a refusal. The usage writes `--hidden | --visible`. `row move demo build --first --last --dry-run` refuses with `wants one place, not 2`, and `row sort demo --manual --keep --dry-run` refuses with `--manual ends a standing sort and takes no --keep or --desc`. `row sort demo --by name --manual --dry-run` does not: it exits 0 and prints `by=name manual=true`.

**Grade:** NEXT

## 5. A repeated member in one `cell add` is a dry-run success — NEXT

**Command:**

    nova-table cell add demo build ready a a --dry-run --redis /scratch/nostore.sock

**Printed:**

    TABLE DRY-RUN verb=cell-add arg1=demo arg2=build arg3=ready arg4=a arg5=a sends="FCALL ns_table_cell_add" redis=/scratch/nostore.sock dialled=0 written=0

exit 0. That is the only line.

**Expected:** a refusal before sending. `docs/CLI.md` says duplicate members or row names within one list are refused, and that a late invalid item leaves the store unchanged. `--dry-run` is the check the banner says runs before a send. `row add demo build build --dry-run` is the same hole: exit 0, `arg2=build arg3=build`. The store was not asked, so this is the client's check, not the store's.

**Grade:** NEXT

## 6. `--keep` accepts a column that is not name or label — NEXT

**Command:**

    nova-table row sort demo --by ready --keep --dry-run --redis /scratch/nostore.sock

**Printed:**

    TABLE DRY-RUN verb=row-sort arg1=demo by=ready keep=true sends="FCALL ns_table_set" redis=/scratch/nostore.sock dialled=0 written=0

exit 0. That is the only line.

**Expected:** a refusal. `row sort -h` says `--keep` is "a standing sort (by name or label)", and the page says the same. `ready` is neither word. The client can see both flags without a store. `row sort demo --by name --keep --dry-run` is the case the help describes, and that one exits 0 with `by=name keep=true`.

**Grade:** NEXT

## 7. `shell` cuts the `run:` off a verb's refusal — NEXT

**Command:**

    printf 'show demo\nquit\n' | nova-table shell --dry-run --keep-going --redis /scratch/nostore.sock

**Printed:**

    SHOW REFUSED: redis at /scratch/nostore.sock as the default user, no password: unreachable: dial unix /scratch/nostore.sock: connect: no such file or directory; next: start the store or correct the address, which was given to this tool
    nova-table shell: line 1 failed (exit 2)

exit 2. Stdout was empty.

**Expected:** the refusal `show` prints on its own, plus the input line. The same `show demo --redis /scratch/nostore.sock` continues after `which was given to this tool` with `; for a first try, no redis-server is on PATH to start a throwaway store (it wants Redis 7 or later), and every verb that writes runs with no store under --dry-run; run: nova-table help`. The shell stops at `this tool` and the `run:` command is gone. `next:` is still there, and the line number is named.

**Grade:** NEXT

## 8. A seat name that resolves to nothing is reported as if `--seat` was omitted — NEXT

**Command:**

    nova-table list --seat nosuch

**Printed:**

    LIST REFUSED: --redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); for a first try, no redis-server is on PATH to start a throwaway store (it wants Redis 7 or later), and every verb that writes runs with no store under --dry-run; run: nova-table help

exit 2. That is the only line. The home directory had no `seats.tsv` and no nova-secrets seat. Nothing was dialled.

**Expected:** the name `nosuch` in the refusal. `list --seat` with no name is refused as `--seat <name>, for example --seat bench-a`. Passing a name and being told that a seat was not given is a different fact. `list` with no flags prints this same line, which is the right line for that case.

**Grade:** NEXT

READ 6/10. The dry-run first run matches `docs/CLI.md` on the byte, and `help` is enough to find every verb, but an unknown flag's own list drops `--seat`, the banner's sample refusal ends `run: d=$(mktemp -d) ...` while the printed line ends `run: nova-table help` when `redis-server` is off PATH, and the guide's verb block never shows `batch`.

USE 6/10. Writes that can be checked with no store refuse a bad spec, a bad manifest, and a contradictory `--manual`, and a missing socket is named and not written, but a second `--columns`, a repeated member, and `--hidden` with `--visible` are planned as success, and a row key rejected before any dial exits 1.

urgent=2 next=6
