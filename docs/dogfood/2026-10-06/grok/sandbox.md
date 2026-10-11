# nova-sandbox dogfood, 2026-10-06 (grok)

Tool: nova-sandbox. Build: `nova-sandbox v1.0.1-0.20261007150632-34db18e07dff linux/amd64 go1.26.6 backend=landlock platform=linux`
(the tip of `sprint/mechanical-2026-10-02` at run time). The card's recorded sha
5844884e267c is 30 files behind that tip in `cmd/nova-sandbox` and
`pkg/sandbox` (`git diff --stat` between them), so these findings are against
the branch tip, which is the tree the release is cut from, not the recorded sha.
Run cold, from the binary's own help (`nova-sandbox help`, `nova-sandbox <verb> -h`) and `docs/SPEC-SANDBOX.md` only, with no code read, on a Linux bench (kernel
7.0.0, Landlock ABI 8). Every verb ran for real against one scratch tree:
`check`, `policy`, `probe` (with and without `--secret`, with `--net-deny`), the
bare wall (writes inside and outside the set, reads, both read lists, `--net-deny`
and `--net-allow` checked with curl), `version`, `help`, `run` and `reap` (their
documented Linux refusals), `worktree` (its refusals and the forge path), and all
four `egress` verbs (a real nftables table planned, applied, listed, checked and
dropped). Every command below was typed with

    B=$HOME/zhi-bench/dogfood-grok-sandbox-b.w1~15.g15
    S=$B/scratch

and `HOME=$S/home`, which is inside the `--write`. Transcript paths are
abbreviated to `$B`, `$S` and `/home/<user>`.

## 1. `--read-noexec` is silently defeated when the command lives in the tree — URGENT

**Command:**

    HOME=$S/home nova-sandbox --write $S --read-noexec /tmp/nsdog/tools -- /tmp/nsdog/tools/noexec

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=1 write=1 net=nopromise cwd=$S ... cmd=noexec gpu=none deletes=$S
    noexec-tree-ran

exit 0. The same program run by `/bin/sh` (the resolved command outside the
no-exec tree) is denied, which is the behaviour the flag promises:

    /usr/bin/dash: 1: /tmp/nsdog/tools/noexec: Permission denied
    SANDBOX DONE exit=126 cmd=dash

**Expected:** `--read-noexec` is "readable, recursively, and NOT EXECUTABLE", so
naming the tree should deny the run whichever way the program is reached. The
resolved command's own directory is added as an extra `read=` root the caller
never named, and that root's execute grant wins: `policy` for this argv prints
`read=/tmp/nsdog/tools` and `read-noexec=/tmp/nsdog/tools` side by side, yet the
run proceeds under a `SANDBOX OK` line that still says `read-noexec=1`, with no
`SANDBOX NOTE`. The tool already refuses the exact path given to both
`--read-noexec` and `--write`; this collision of the same path as the command
directory and a no-exec root should be refused or reported too.

**Grade:** URGENT

## 2. A secret beside the resolved command is readable inside the wall — URGENT

**Command:**

    HOME=$S/home nova-sandbox --write $S -- /tmp/nsdog/bin/runme

where `/tmp/nsdog/bin/runme` is a script that cats its sibling `key.txt`; neither
`/tmp/nsdog/bin` nor `key.txt` is named by a `--read` or a `--write`.

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise cwd=$S ... cmd=runme gpu=none deletes=$S
    cmd-dir-secret-LEAKED

exit 0. `nova-sandbox policy --write $S -- /tmp/nsdog/bin/runme` shows the root
that made it readable, listed among the caller's own roots even though the caller
named it nowhere:

    read=/tmp/nsdog/bin
    write=$S
    POLICY OK backend=landlock read=0 read-noexec=0 write=1 bytes=900 gpu=none

**Expected:** rule 6 says the credential file "stays outside every named path, so
the wrapped command cannot read it even if it is told to". The tool grants read on
the whole directory of the resolved command (so the command itself is readable),
so a key placed beside the command is readable inside the wall. The tool knows
this root is dangerous: a probe whose command directory would expose `HOME`
refuses with the exact cause and remedy ("the directory of the resolved command IS
a read root, so wrapping a command that lives there would make the whole
of ... readable inside the wall"). A command directory that holds a secret
deserves the same refusal, or a grant narrowed to the command file and its
traversal directories.

**Grade:** URGENT

## 3. `probe --secret` under the command's directory reports the wall broken — NEXT

**Command:**

    HOME=$S/home nova-sandbox probe --write $S --secret $B/secretdir/key.txt

(the binary is `$B/nova-sandbox`, so `$B` is the resolved command's directory).

**Printed:**

    PROBE STEP name=read_secret expect=deny got=allow path=$B/secretdir/key.txt
    PROBE REFUSED reason=check: read_secret expected deny and got allow at $B/secretdir/key.txt; run: nova-sandbox probe -h

exit 1. With the same secret outside the binary's directory the probe passes:
`PROBE OK backend=landlock abi=8 steps=5 passed=5 net=nopromise gpu=none`.

**Expected:** `--secret` is refused with `secret_inside_allow` when it resolves
inside a `--read` or a `--write`, but the implicit command-directory root is not
covered, so the probe calls a wall that is working for every other path broken and
its remedy only reprints help. It should refuse the `--secret` with the same
reason, or say the probe cannot check a secret under the resolved command's
directory.

**Grade:** NEXT

## 4. `--read-noexec` nested under a `--write` is silently void — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --read-noexec $S/tools -- /bin/sh -c $S/tools/noexec

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=1 write=1 net=nopromise cwd=$S ... cmd=dash gpu=none deletes=$S
    hello-noexec

exit 0. `policy` for the same flags prints `read-noexec=$S/tools` beside
`write=$S` and answers `POLICY OK`.

**Expected:** the exact path given to both `--read-noexec` and `--write` is
refused ("in both --read-noexec and --write; --write carries read AND execute, so
the no-exec grant would buy nothing"). A no-exec subtree inside a write grants
nothing for the same reason, so the run should refuse or NOTE it instead of
accepting a control that cannot hold.

**Grade:** NEXT

## 5. A bare-form flag with a command is refused as `reason=no_command`, and the line loses `; run:` — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --json -- /bin/echo hi

**Printed:**

    SANDBOX REFUSED reason=no_command: --json is not a flag of the bare form; it is check, policy and probe's: run nova-sandbox help

exit 125.

**Expected:** a command was given, so `no_command` names the opposite of what
happened; the reason for a flag a form does not have is `bad_flag` (which
`--no-sandbox` prints), and the refusal grammar is `<why>; run: <remedy>`, not
`<why>: run <remedy>`. `--max` and `--secret` on the bare form print the same
line; `egress check` with no `--plan` also answers `reason=no_command` for a
missing flag. The same run's unknown-flag line lists `--json`, `--max` and
`--secret` among "the flags are ..." of the bare form, which the form refuses.

**Grade:** NEXT

## 6. `version` with an argument reasons `unknown_flag`, outside the documented set — NEXT

**Command:**

    nova-sandbox version extra

**Printed:**

    SANDBOX REFUSED reason=unknown_flag: version takes no flags and no arguments, got 1; run: nova-sandbox version

exit 2.

**Expected:** `version -h` says exit 2 is "an argument was given", and the output
grammar carries `bad_flag` for this shape; `unknown_flag` is a token a parser
switching on the documented reasons was never told about.

**Grade:** NEXT

## 7. `--net-allow` is accepted on Linux and opens nothing, silently — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --net-deny --net-allow localhost:11434 -- /bin/sh -c 'curl -sS -m 3 http://localhost:11434/ >/dev/null 2>&1 && echo REACHED || echo NOT_REACHED'

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=denied cwd=$S ... cmd=dash gpu=none deletes=$S
    NOT_REACHED

Plain `--net-allow localhost:11434` without `--net-deny` still prints
`net=nopromise` and adds no note that the allow was dropped.

**Expected:** the help documents `--net-allow` unconditionally and says it opens
the loopback host:port named; on Linux the wall is built at ABI 6 and enforces no
port rule, so the flag changes nothing. It should be marked darwin-only (as `run`
and `egress apply` are), refused, or NOTEd as ignored the way `--acl` is.

**Grade:** NEXT

## 8. `worktree` calls an unauthenticated `gh` "the forge could not be reached" and says to retry — NEXT

**Command:**

    HOME=$S/home nova-sandbox worktree --repo $B/repo --scratch $S --pr 5366

**Printed:**

    WORKTREE REFUSED reason=no_forge: the forge could not be reached
    run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>; retry once the forge answers

exit 2.

**Expected:** the forge did answer; `gh auth status` on the bench prints "You are
not logged into any GitHub hosts. To log in, run: gh auth login". `no_forge` is
the forge's silence, and the one remedy is to retry, which never clears an
authentication failure. Expected the gh failure's own breadcrumb (`gh auth login`), the way `bad_repo` and `bad_scratch` carry theirs.

**Grade:** NEXT

## 9. `egress plan` refuses the bench's own resolver and the remedy names the value it refused — NEXT

**Command:**

    HOME=$S/home nova-sandbox egress plan --run r1 --policy $B/repo/infra/image/egress.txt \
      --model-host api.deepseek.com --resolver 127.0.0.53 --uid 1001 --out $S/r1.nft

**Printed:**

    EGRESS REFUSED reason=bad_resolver: the resolver 127.0.0.53 is inside 127.0.0.0/8, which this plan denies; the deny rules come first, so DNS would be dropped and every name would fail with nothing saying why — name the bench's own resolver
    run: nova-sandbox egress plan --run <id> --policy infra/image/egress.txt --model-host <host> --resolver <ip> [--bench-cidr <cidr>]... --uid <n> --out <file>

exit 2.

**Expected:** the bench's `/etc/resolv.conf` names `nameserver 127.0.0.53`, so
"name the bench's own resolver" names exactly the value just refused. The same
invocation with `--resolver 8.8.8.8` plans cleanly (`EGRESS PLAN run=r1 allow=8 deny=6 names=github.com,api.github.com,objects.githubusercontent.com,api.deepseek.com`),
so the tool works and the remedy is the defect: it sends a reader to a value the
machine reports and the tool rejects, with no way to learn the upstream it wants.

**Grade:** NEXT

## 10. `egress drop` of a table that was never applied is not a no-op and prints raw nft stderr — NEXT

**Command:**

    nova-sandbox egress drop --run neverapplied

**Printed:**

    EGRESS STEP name=drop state=start
    EGRESS STEP name=drop state=done ms=34
    EGRESS REFUSED reason=nft_failed: nft delete table inet nova_egress_neverapplied: exit status 1: Error: Could not process rule: No such file or directory\x0adelete table inet nova_egress_neverapplied\x0a                  ^^^^^^^^^^^^^^^^^^^^^^^^; run: nova-sandbox egress -h

exit 1.

**Expected:** dropping a run's table when nothing was applied is the benign no-op
a runner hits on a path out; it should read `EGRESS OK verb=drop` with nothing to
delete, or a short "no table for that run". The applied-and-dropped path is clean
(`EGRESS OK verb=drop run=r1`), so only the already-clean case is unreadable, and
its remedy (`egress -h`) does not explain a missing table.

**Grade:** NEXT

## 11. `reap` on Linux refuses with the `run` verb's paragraph — NEXT

**Command:**

    nova-sandbox reap --dry-run

**Printed:**

    SANDBOX REFUSED reason=no_sandbox: the disposable place is darwin's APFS volume, made per run in the boot container and deleted on exit, or windows's Job Object plus per-run scratch; linux has no body here and this tool does not pretend an ordinary directory is one
    run: nova-sandbox --write <dir> -- <command> <args...>, naming the card's own image root as <dir>: on linux a card is already disposable because it runs INSIDE its image, and the image is the container

exit 125.

**Expected:** `reap`'s own help marks it `(darwin)`, and its question ("which
`nova-*` volumes is this machine still holding?") is answered on Linux by "no
volumes exist on this platform", not by the `run` verb's paragraph with a remedy
pointing at the bare wall. A reap-specific refusal is one line and does not make
the reader work out that the paragraph is about another verb.

**Grade:** NEXT

## 12. The `--acl` note cites "rule 22", another tool's spec — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --acl caller --name c -- /bin/echo hi

**Printed:**

    SANDBOX NOTE --acl caller is accepted and ignored on linux; the ACEs of rule 22 are the windows body's
    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows

exit 0.

**Expected:** `docs/SPEC-SANDBOX.md` numbers its rules 1 through 17; there is no
rule 22 in this tool's spec. "rule 22" is nova-update's (`docs/SPEC-UPDATE.md`
names it for `--max`), so the note sends a reader to a rule this tool does not
have. Expected a citation of this tool's own windows section.

**Grade:** NEXT

## 13. `--tmp` outside every `--write` is refused as `reason=bad_write` — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --tmp $B -- /bin/echo hi

**Printed:**

    SANDBOX REFUSED reason=bad_write: --tmp $B is outside every --write; the temp directory is inside the wall; run: nova-sandbox help

exit 125.

**Expected:** the message names the flag at fault, but the reason token says
`bad_write`, so a script keying on the reason reads a `--write` problem. `--cwd`
outside the wall gets its own `bad_cwd`; `--tmp` should get `bad_tmp`.

**Grade:** NEXT

## 14. The per-verb `-h` examples are placeholders that exit 2 — NEXT

**Command (the line `nova-sandbox policy -h` itself prints as its example):**

    HOME=$S/home nova-sandbox policy --write /pool/jobs/j1 --read /usr/local/go --net-deny

**Printed:**

    POLICY REFUSED reason=bad_read: --read /usr/local/go does not exist; every path is named by the caller and none is created; run: nova-sandbox policy -h
    POLICY REFUSED reason=bad_write: --write /pool/jobs/j1 does not exist; every path is named by the caller and none is created; run: nova-sandbox policy -h

exit 2. `probe -h`'s example (`nova-sandbox probe --write /pool/jobs/j1`) answers
`PROBE REFUSED ... --write /pool/jobs/j1 does not exist ... (bad_write)` at exit 2
as well.

**Expected:** the banner's own `example:` block runs as printed (its `/tmp/trial`
lines do), and the onboarding standard says a line that runs answers 0 or 1 while
exit 2 is a broken example. The per-verb examples name `/pool/jobs/j1` and
`/usr/local/go`, which exist nowhere, so a stranger who pastes the help's own line
gets exit 2 and no path to a first run on a verb whose other forms are careful to
name every input the caller must supply.

**Grade:** NEXT

The verbs that carry no state ran cleanly and their refusals named remedies:
`check` (and `--json`), `policy` (and `--json`), `probe` (with and without a
secret, with `--net-deny`), `--read`, `--read-noexec`, `--write`, `--cwd`, `--tmp`,
`--net-listen` with `--net-deny`, `--gpu`, `--name`, `--acl`, `--secret` on the
bare form, a missing command, a relative path, a missing path, a path in two
lists, and the command's own exit status passed through whole. The wall itself
denied a write outside the set and a read of the secret, executed the `--read`
tree, denied the `--read-noexec` tree when the command was elsewhere, enforced
`--net-deny` against `curl`, and set `TMPDIR` and `HOME` inside the write. On the
egress side a real nftables table was planned, applied (listed through `sudo nft list table inet nova_egress_r4`), applied again, and dropped, and `egress check` refused a plan whose metadata deny was widened. `worktree --prune` ran
clean (`WORKTREE OK removed=0 kept=0`), and the `run`, `reap` and `worktree`
refusals were otherwise the documented ones.

READ 7/10 — the banner, the verb helps and the page state the wall's nouns and
bounds precisely, every refusal is one line that names a remedy, and the
`SANDBOX OK` receipt is rich enough to reconstruct a run; minus three for help
that promises `--read-noexec` and `--net-allow` properties the Linux body does not
keep, for the per-verb examples that cannot run as printed, and for a note that
cites another tool's rule.

USE 6/10 — nearly every verb ran first try for real: the wall denied the writes
and reads outside the set, enforced `--net-deny`, the four `egress` verbs moved a
real table, and the wall's receipts and refusal reasons were clear; minus four for
an explicit no-exec control that silently loses when the command lives in the tree
(or inside a write), a secret beside the command readable under a `SANDBOX OK`
line that names no such root, a `probe --secret` that calls that working wall
broken, and a `no_forge` remedy that loops.

urgent=2 next=12
