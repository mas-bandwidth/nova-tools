# nova-sandbox dogfood, 2026-10-06 (opencode-2)

Tool: nova-sandbox. Build: `nova-sandbox v1.0.1-0.20261007155328-768701114b5b linux/amd64 go1.26.6 backend=landlock platform=linux`,
built with `go build ./cmd/nova-sandbox` from this card's base tip
(`sprint/mechanical-2026-10-02` at `768701114`; the card's recorded sha
`5844884e267c` is 579 commits behind that tip, `git rev-list --count
5844884e267c..HEAD`), so the findings are against the tree the release is cut
from. Run cold, from the binary's own help (`-h`, `help`, `<verb> -h`, `help
<verb>`) and the tool's page `docs/SPEC-SANDBOX.md` only, on a Linux bench
(kernel 7.0.0-34-generic, x86-64, Landlock ABI 8). Every verb ran for real against
one scratch tree inside the job directory with `HOME` inside a `--write`: `check`
(`--json`, a bad flag), `policy` (the caller's lists, the roots, `--json`,
`--net-deny`, the refusals), `probe` (with and without `--secret`, `--net-deny`,
`--json`, its refusals), the bare wall (reads and writes inside and outside the
set, `--read`, `--read-noexec` with a shell script and an ELF, `--net-deny`,
`--net-allow`, `--net-listen`, `--cwd`, `--tmp`, `HOME`, deletes, the signal
status, the command's status), `run` and `reap` (their real Linux refusals),
`worktree` (its refusals, the `--prune` clean pass, the forge path), all four
`egress` verbs (a real nftables table planned, applied, listed, checked, dropped,
and dropped again), `version`, `help` and the unknown-verb refusal. Commands
below were typed with

    B=$HOME/zhi-bench/dogfood-opencode-2-sandbox-b.w1~15.g16
    NB=$B/nova-sandbox
    S=$B/scratch/store

and in quoted output `$B`, `$S` and `/home/<user>` abbreviate those prefixes;
nothing else in a quoted line is changed.

Not run: `run`'s disposable-volume body and `reap`'s real cleanup (both darwin's
or windows's; on Linux the refusals above are the verb's whole behavior), the
`worktree` success/reuse and `no_pr` paths (the bench's `gh` has no credential:
`gh auth status` is `You are not logged into any GitHub hosts`, so every forge
call is the `no_forge` line below), `--gpu metal` against a local model, and
`--net-allow`/`--net-listen` against a live loopback listener (the card bars
starting a server on the bench; the grant was read in the printed policy
instead). One observation is not a numbered finding because it did not reproduce
on demand: two runs of `probe --write $S --max 1` (in 1551) reported
`write_inside` and `read_root` denied against a wall that was up; the same
command passed 1549 times and the plain form passed every time, and the failure
is finding 4 below because a gate that lies even rarely is a wrong result.

## 1. A secret beside the resolved command is readable inside the wall — URGENT

**Command:**

    HOME=$S/home $NB --write $S -- $B/bin/runme

where `$B/bin/runme` cats its sibling `$B/bin/key.txt`; neither `$B/bin` nor
`key.txt` is named by any `--read` or `--write`.

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise cwd=$S cwdb64=… ancestors=14 cmd=runme gpu=none deletes=$S
    cmd-dir-script-ran

then `cmd-dir-secret`, `SANDBOX DONE exit=0 cmd=runme`, exit 0. The root that
made it readable is printed by `policy` in the roots block ("the linux roots
table, then this run's optional roots"), as a path the caller never named:

    read=$B/bin

**Expected:** rule 6 says the credential file "stays outside every named path, so
the wrapped command cannot read it even if it is told to". The roots table
computes "the directory of the resolved command", and the page already guards a
different computed root — a command directory that is the caller's home is a
`bad_read` refusal — so the tool knows a computed root needs a check. A key
placed beside the resolved command should be refused with the same
cause-and-remedy sentence, or the grant narrowed to the command file and its
traversal directories.

**Grade:** URGENT

## 2. `--read-noexec` is silently defeated when the resolved command is under the named tree — URGENT

**Command:**

    HOME=$S/home $NB --write $S --read-noexec $S/tools -- $S/tools/noexec

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=1 write=1 net=nopromise cwd=$S cwdb64=… ancestors=14 cmd=noexec gpu=none deletes=$S
    noexec-tree-ran

`SANDBOX DONE exit=0 cmd=noexec`, exit 0. `policy` for the same argv prints the
one path twice, once as a computed root and once as the caller's no-exec grant:

    read=$S/tools
    # the caller's own sets, exactly as named
    read-noexec=$S/tools

The tool refuses that exact collision when the caller spells it — `policy --write
$S --read $S/tools --read-noexec $S/tools` is `POLICY REFUSED reason=bad_read:
$S/tools is in both --read and --read-noexec; one carries execute and the other
takes it away, so name it once` — and it refuses `--read-noexec` against `--write`
with "the no-exec grant would buy nothing". The flag does work when the command
lives elsewhere: an ELF in the same tree fails to exec through `/bin/sh`
(`Permission denied`, exit 126). So the only hole is the root the tool adds
itself.

**Expected:** rule 4's whole reason for refusing the collision — one flag asks for
execute and the other takes it away — applies unchanged to the computed
command-directory root. The tool should refuse the run or drop that root's execute
grant; a no-exec tree must not contain a program that runs merely by being named
as the command.

**Grade:** URGENT

## 3. A forge that answered "not logged in" is reported as an unreachable forge — URGENT

**Command:**

    $NB worktree --repo $B/repo --scratch $S/wtscratch --pr 5327

**Printed:**

    WORKTREE REFUSED reason=no_forge: the forge could not be reached
    run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>; retry once the forge answers

exit 2. The forge was reached and answered: `gh auth status` is `You are not
logged into any GitHub hosts. To log in, run: gh auth login` (exit 1), and `gh pr
view 5327 --repo mas-bandwidth/nova-tools` prints `To get started with GitHub
CLI, please run: gh auth login` (exit 4). `worktree --prune` on the same tree,
which has no records and so asks the forge nothing, is
`WORKTREE OK removed=0 kept=0` exit 0.

**Expected:** the page separates the failures and says `no_forge` "is the forge's
own silence and never an input the forge was not asked about". Here the input was
asked and gave a definite answer (no credential), so the remedy — retry once the
forge answers — can never succeed, and the one-turn recovery a refusal owes its
reader is spent on a retry loop. The refusal should carry the forge's own message
(or name the missing credential); only silence should be `no_forge`.

**Grade:** URGENT

## 4. `probe --max 1` intermittently reports its own working wall broken — URGENT

**Command:**

    HOME=$S/home $NB probe --write $S --max 1

**Printed (the failing run):**

    PROBE STEP name=write_outside_control expect=allow got=allow path=$B/scratch/.nova-sandbox-probe-2647530
    PROBE STEP name=write_outside expect=deny got=deny path=$B/scratch/.nova-sandbox-probe-2647530
    PROBE STEP name=write_inside expect=allow got=deny path=$S/.nova-sandbox-probe-inside
    PROBE REFUSED reason=check: write_inside expected allow and got deny at $S/.nova-sandbox-probe-inside; run: nova-sandbox probe -h
    PROBE STEP name=read_root expect=allow got=deny path=$B/nova-sandbox
    PROBE REFUSED reason=check: read_root expected allow and got deny at $B/nova-sandbox; run: nova-sandbox probe -h

exit 1. The same command prints `PROBE OK backend=landlock abi=8 steps=4 passed=4
net=nopromise gpu=none` exit 0 in the overwhelming majority of runs. Counted: 2
failures in 1551 `--max 1` runs (one in the first 150, one in a 1000-run
confirmation that found none, plus the first seen in the pass), 0 failures in the
~410 plain-form runs interleaved with them, and `--max` accepts 0, 100 and -1 as
documented (`--max -1` is `PROBE REFUSED reason=check: --max wants a whole
number, 0 for all: --max <n>`).

**Expected:** `--max` bounds output; it must not change the policy the probe
applies. A probe that prints "the wall is broken" on a wall it passed a moment
before makes the one gate a card ends on untrustworthy, and the failure is a
wrong result rather than a missing line. The rate (about one run in 800) is the
claim, not the count; the owner should treat this as "the probe can report a
false deny", whatever the underlying race.

**Grade:** URGENT

## 5. `probe --secret` under the resolved command's directory calls a working wall broken — NEXT

**Command:**

    HOME=$S/home $NB probe --write $S --secret $B/secret/key.txt

(the probe's own binary is `$B/nova-sandbox`, so `$B` is the directory of the
resolved command; `$B/secret/key.txt` is named by no `--read` or `--write`).

**Printed:**

    PROBE STEP name=write_outside_control expect=allow got=allow path=$B/scratch/.nova-sandbox-probe-2509694
    PROBE STEP name=write_outside expect=deny got=deny path=$B/scratch/.nova-sandbox-probe-2509694
    PROBE STEP name=read_secret expect=deny got=allow path=$B/secret/key.txt

then `PROBE REFUSED reason=check: read_secret expected deny and got allow at
$B/secret/key.txt; run: nova-sandbox probe -h`, exit 1. The identical probe with
the secret at `/tmp/nsdog-secret/key.txt` passes 5/5 and exits 0.

**Expected:** `--secret` inside any `--read` or `--write` is refused with
`reason=secret_inside_allow`; the implicit command-directory root is not covered,
so the probe calls a wall that is working for every other path broken, and its
remedy only reprints help. It should refuse the secret with the same reason (the
same hole as finding 1), or state that it cannot check a secret under the
resolved command's directory.

**Grade:** NEXT

## 6. The bare form's unknown-flag refusal lists flags the bare form refuses — NEXT

**Command:**

    HOME=$S/home $NB --no-sandbox --write $S -- /bin/echo hi

**Printed:**

    SANDBOX REFUSED reason=bad_flag: unknown flag --no-sandbox; the flags are --read, --read-noexec, --write, --cwd, --tmp, --name, --acl, --secret, --gpu, --net-deny, --net-listen, --json, --net-allow, --max; run: nova-sandbox help

exit 125. Three of the flags that line names are refused by this form when
passed: `--secret`, `--json` and `--max` all answer `SANDBOX REFUSED
reason=no_command: --secret is not a flag of the bare form; it is probe's` (and
the same for `--json`, `--max`). The page's exit table calls "a flag the bare form
does not have" `reason=bad_flag`, not `no_command`.

**Expected:** the list names the flags this form accepts, and a flag it does not
have is refused with the document's `bad_flag`. A reader who takes the line at its
word tries a listed flag and gets a second refusal with a different reason.

**Grade:** NEXT

## 7. `egress plan` without `--out` uses the exec verb's `no_command`, and `egress check` sends the reader to the plan remedy — NEXT

**Command:**

    $NB egress plan --run r3 --policy $B/repo/infra/image/egress.txt --model-host api.deepseek.com --resolver 8.8.8.8 --uid 10001

**Printed:**

    EGRESS REFUSED reason=no_command: --out wants the file the ruleset is written to: --out /run/nova/egress-<run>.nft
    run: nova-sandbox egress plan --run <id> --policy infra/image/egress.txt --model-host <host> --resolver <ip> [--bench-cidr <cidr>]... --uid <n> --out <file>

exit 2. The page's egress refusal set has no `no_command`; it has `bad_out`. A
missing plan gets the plan remedy too: `egress check --plan $S/absent.nft` prints
`EGRESS REFUSED reason=bad_plan: the plan could not be read: open
$S/absent.nft: no such file or directory` and then the `egress plan … --out
<file>` remedy line, not a `check` remedy.

**Expected:** the reason word comes from the refusal set the page documents for
the verb (`bad_out` here), and each verb's remedy names that verb. An AI reading
`no_command` looks for a missing command in a verb that takes none.

**Grade:** NEXT

## 8. `reap -h` hides its own platform refusal and exit code — NEXT

**Command:**

    $NB reap -h
    $NB reap --dry-run
    $NB reap --bogus

**Printed (the first line of `reap -h`, which documents only 0 and 3):**

    nova-sandbox reap: clear the disposable volumes a killed run left behind (darwin)

**Printed (`reap --dry-run`, and `reap` identically):**

    SANDBOX REFUSED reason=no_sandbox: the disposable place is darwin's APFS volume, made per run in the boot container and deleted on exit, or windows's Job Object plus per-run scratch; linux has no body here and this tool does not pretend an ordinary directory is one
    run: nova-sandbox --write <dir> -- <command> <args...>, naming the card's own image root as <dir>: on linux a card is already disposable because it runs INSIDE its image, and the image is the container

exit 125. `reap --bogus` is `SANDBOX REFUSED reason=no_command: unknown flag
--bogus; run: nova-sandbox help reap` at 125, where the exit table makes an
unknown flag `bad_flag`.

**Expected:** the verb's own help states the refusal and the 125 this platform
prints, the way the bare usage marks `(darwin)`; and an unknown flag is
`bad_flag`. The `-h` a reader reads first is the one that does not mention the
answer they get here.

**Grade:** NEXT

## 9. `egress drop` of a table that is not there is `nft_failed`, with nft's raw stderr — NEXT

**Command:**

    $NB egress drop --run r1        # after apply and one successful drop, exit 0

**Printed:**

    EGRESS STEP name=drop state=start
    EGRESS STEP name=drop state=done ms=34
    EGRESS REFUSED reason=nft_failed: nft delete table inet nova_egress_r1: exit status 1: Error: Could not process rule: No such file or directory\x0adelete table inet nova_egress_r1\x0a                  ^^^^^^^^^^^^^^; run: nova-sandbox egress -h

exit 1. The real path works: `egress plan` resolved the shipped allowlist, wrote
`$S/r1.nft`, `egress check` read it back `table=nova_egress_r1 chains=1 rules=14
allow=8 deny=6`, `egress apply` printed `EGRESS OK verb=apply run=r1
table=nova_egress_r1`, and the first `drop` printed `EGRESS OK verb=drop run=r1
table=nova_egress_r1`, both through `sudo -n nft`.

**Expected:** the page's contract is plan → apply → the run → drop "on every path
out"; a runner that calls `drop` when `apply` never made a table, or after a
drop already ran, is looking at a clean bench, not an `nft_failed` with nft's
stderr escaped as `\x0a` plus a caret. The refusal should say the table is not
there and exit 0 (nothing to drop) or carry a line a reader can act on.

**Grade:** NEXT

## 10. Flags a platform does not implement are accepted silently — NEXT

**Command:**

    HOME=$S/home $NB --write $S --net-allow localhost:11434 -- /bin/echo hi
    HOME=$S/home $NB --write $S --net-listen -- /bin/echo hi
    HOME=$S/home $NB --write $S --name c1 -- /bin/echo hi

**Printed (all three, the same shape):**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise cwd=$S cwdb64=… ancestors=14 cmd=echo gpu=none deletes=$S
    hi

`policy --write $S --net-allow localhost:11434` prints `net=nopromise` and no
line for the grant. By contrast `--acl caller` prints `SANDBOX NOTE --acl caller
is accepted and ignored on linux; the ACEs of rule 22 are the windows body's`.

**Expected:** a flag whose wall this platform does not build is either refused or
noted as ignored, as `--acl` is. On Linux `--net-allow` opens nothing and
`--net-listen` grants nothing; `net=nopromise` is honest about the promise but
does not say the two flags did nothing, so a caller can leave a run believing a
loopback provider was opened for it.

**Grade:** NEXT

## 11. `--max`'s surface is three different answers — NEXT

**Command:**

    $NB check --max 1
    HOME=$S/home $NB policy --write $S --max 1
    $NB probe -h

**Printed:**

    CHECK REFUSED reason=bad_flag: unknown flag --max; run: nova-sandbox help check

exit 2; then

    POLICY NOTE --max is probe's flag and is ignored here: the policy printed is the same without it
    POLICY OK backend=landlock read=0 read-noexec=0 write=1 bytes=904 gpu=none

exit 0; then `probe -h` lists `--gpu`, `--json`, `--net-deny`, `--net-listen`,
`--read`, `--read-noexec`, `--secret`, `--write` and no `--max`. The bare form's
refusal (finding 6) names `--max` among the flags this tool has.

**Expected:** if `--max` is probe's bounded-output flag (as the policy note says),
`probe -h` names it with the unit it wants, the way every other flag is named.
`check`'s refusal, the bare form's list and probe's own help should agree on one
surface; a called-out flag a reader cannot find in the verb's help is the
"missing flag" friction the page's onboarding rule is about.

**Grade:** NEXT

READ 7/10 — the banner answers what, how and how to use it before the usage
lines, every verb's `-h` exits 0 with its flags and its own exit codes, the
refusals are one line with a pasteable remedy that reports every independent
problem at once, and `docs/SPEC-SANDBOX.md` is a rare page that states its own
measurements and its own gaps; the score is held down by the bare-form flag list
that names refused flags, by `--max` present in a note and a refusal but absent
from the verb's help, by `reap -h` that hides its own Linux refusal, and by
`egress` reason words (`no_command`) that are not in the refusal set the page
documents.

USE 6/10 — on Linux the wall did what it says for the real work: writes and
deletes inside the write set, reads outside it denied, `--read` carried execute,
`--read-noexec` denied an ELF when the command was elsewhere, `HOME` outside a
write refused, the signal status passed through, `policy` printed a real
ruleset, `probe` proved the wall, and the four `egress` verbs moved a real
nftables table on the bench; the score is held down by a secret beside the
command readable under a `SANDBOX OK` line that names no such root, by
`--read-noexec` losing to the tool's own command-directory root, by a probe that
called that working wall broken (twice, and once with `--max`), by a `no_forge`
that sends a credential problem to a retry loop, and by a second `drop` that
reports an already-clean bench as `nft_failed`.

urgent=4 next=7
