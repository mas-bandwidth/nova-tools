# nova-sandbox dogfood, 2026-10-06 (antigravity)

Tool: nova-sandbox. Build: `nova-sandbox v1.0.1-0.20261006183932-5844884e267c linux/amd64 go1.26.6 backend=landlock platform=linux`.
Run cold, from the binary's own help (`nova-sandbox help`, `nova-sandbox <verb> -h`) and
`docs/SPEC-SANDBOX.md` only, on a Linux bench (kernel 7.0.0, Landlock ABI 8). Every verb
ran for real against one scratch store: `check`, `probe` (with and without `--secret`, with
`--net-deny`), `policy`, the bare wall (writes inside and outside the set, reads, the two
read lists, `--net-deny` verified with curl), `run` and `reap` (their documented Linux
refusals), `worktree` (its refusals and the forge path), and all four `egress` verbs (a real
nftables plan applied to and dropped from the bench). Every command below was typed with

    B=<scratch-dir>/sandbox
    S=$B/scratch

and `HOME=$S/home`, which is inside the `--write`. The `cmd/nova-sandbox` and
`pkg/sandbox` trees are byte-identical at the build's commit and at this branch's base
(`git diff --stat` between them is empty), so the findings hold at the tip.

## 1. `--read-noexec` is silently defeated when the command lives in that directory — URGENT

**Command:**

    nova-sandbox --write $S --read-noexec $B/tools -- $B/tools/hello2

where `$B/tools/hello2` is a shell script inside the no-exec directory that runs its sibling
`$B/tools/evil` in the same directory.

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=1 write=1 net=nopromise cwd=<scratch-dir>/sandbox/scratch cwdb64=<cwdb64> ancestors=13 cmd=hello2 gpu=none deletes=<scratch-dir>/sandbox/scratch
    hello-then

exit 0; the fourth line is `second-script-ran`, so the sibling ran too.

**Expected:** `--read-noexec` is "readable, recursively, and NOT EXECUTABLE" (`nova-sandbox help`),
and the same script is denied when the resolved command is outside the tree
(`nova-sandbox --write $S --read-noexec $B/tools -- /bin/sh -c "$B/tools/hello"` is exit 126 with
`Permission denied`). Here the command is inside the tree, so the automatic root "the directory
of the resolved command" re-grants execute on the whole directory and the no-exec grant is
silently thrown away. The `SANDBOX OK` line still says `read-noexec=1`, and no `SANDBOX NOTE`
names the collision. A caller names a tree with `--read-noexec` exactly when a job may read but
never run what lands there (a module cache, `node_modules/.bin`); a command resolved from that
tree, or a sibling it starts, defeats the control. `nova-sandbox policy --write $S --read-noexec $B/tools -- $B/tools/hello2` shows the two grants side by side:

    read=<scratch-dir>/sandbox/tools
    read-noexec=<scratch-dir>/sandbox/tools

The tool already refuses the other contradictory pairs (`--read` and `--read-noexec` on one
path, `--read-noexec` and `--write` on one path), so this pair should be refused or reported,
not resolved in favour of execute.

**Grade:** URGENT

## 2. `--net-allow` is accepted on Linux and opens nothing, and `--net-deny` does not say it took the grant away — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --net-deny --net-allow localhost:11434 \
      -- /bin/sh -c 'curl -sS -m 3 http://localhost:11434/ >/dev/null 2>&1 && echo REACHED || echo NOT_REACHED'

**Printed:**

    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=denied cwd=<scratch-dir>/sandbox/scratch cwdb64=<cwdb64> ancestors=13 cmd=dash gpu=none deletes=<scratch-dir>/sandbox/scratch
    NOT_REACHED

**Expected:** `--net-allow` "open[s] the loopback host:port named, back up, by name; the keyless
local provider (ollama) that (remote ip) does not reach" (its own help text), so
`localhost:11434` should be reachable. On Linux the flag changes nothing: Landlock enforces no
port rules, and with `--net-deny` the connect is denied with no `SANDBOX NOTE` that the allow
was dropped. Plain `--net-allow localhost:11434` is equally silent — the line still says
`net=nopromise`. The flag is documented unconditionally, and its own help does not mark it
darwin-only the way `run` and `egress apply` are marked; on Linux it should be refused or
reported as ignored, so a caller does not read a promise that is not kept.

**Grade:** NEXT

## 3. A bare-form flag the form does not have is refused as `reason=no_command`, against the tool's own exit table — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --json -- /bin/echo hi

**Printed:**

    SANDBOX REFUSED reason=no_command: --json is not a flag of the bare form; it is check, policy and probe's: run nova-sandbox help

exit 125.

**Expected:** the exit-code table and rule 11 of `docs/SPEC-SANDBOX.md` say a flag the bare form
does not have is `reason=bad_flag`, naming the flags it has — and `--no-sandbox` on the same
form does print exactly that. `--secret` and `--max` on the bare form and
`nova-sandbox policy --write $S --artifact x` print `reason=no_command` too, although a command
was given in each, so a reader or script keying on the reason is told the opposite of what
happened.

**Grade:** NEXT

## 4. `version` with an argument reasons `unknown_flag`, which is not in the documented set — NEXT

**Command:**

    nova-sandbox version extra

**Printed:**

    SANDBOX REFUSED reason=unknown_flag: version takes no flags and no arguments, got 1; run: nova-sandbox version

exit 2.

**Expected:** `unknown_flag` is not one of the `SANDBOX REFUSED` reasons in the output grammar
of `docs/SPEC-SANDBOX.md`; the set carries `bad_flag` for this shape. A parser that switches on
the documented reasons meets a token it was never told about.

**Grade:** NEXT

## 5. `worktree` calls an unauthenticated `gh` "the forge could not be reached" and says to retry — NEXT

**Command:**

    nova-sandbox worktree --repo $B/repo --scratch $S --pr 5366

**Printed:**

    WORKTREE REFUSED reason=no_forge: the forge could not be reached
    run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>; retry once the forge answers

exit 2.

**Expected:** the forge did answer; `gh auth status` on this bench prints "You are not logged
into any GitHub hosts. To log in, run: gh auth login". `no_forge` is specified as the forge's
silence, "never an input the forge was not asked about", and the one remedy is to retry — which
never clears an authentication failure. Expected the gh failure's own breadcrumb (`gh auth login`) so recovery is one turn, the way `bad_repo` and `bad_scratch` carry theirs.

**Grade:** NEXT

## 6. `egress plan` refuses the bench's own resolver and the remedy names the value it refused — NEXT

**Command:**

    nova-sandbox egress plan --run r1 --policy $B/repo/infra/image/egress.txt \
      --model-host api.deepseek.com --resolver <addr> --uid 1001 --out $S/r1.nft

**Printed:**

    EGRESS REFUSED reason=bad_resolver: the resolver <addr> is inside <addr>/8, which this plan denies; the deny rules come first, so DNS would be dropped and every name would fail with nothing saying why — name the bench's own resolver
    run: nova-sandbox egress plan --run <id> --policy infra/image/egress.txt --model-host <host> --resolver <ip> [--bench-cidr <cidr>]... --uid <n> --out <file>

exit 2.

**Expected:** on this bench `/etc/resolv.conf` is the systemd-resolved stub and names
`nameserver <addr>`, so "name the bench's own resolver" names exactly the value just
refused. The same invocation with `--resolver <addr>` plans fine
(`EGRESS PLAN run=r1 allow=8 deny=6 ...`), so the tool works; the remedy is the defect: it
sends a reader to a value the machine reports and the tool rejects, with no way to learn the
upstream it wants. Expected the remedy to name the upstream form, or the plan to accept the
stub it finds.

**Grade:** NEXT

## 7. `egress drop` of a table that is not there prints raw escaped nft stderr — NEXT

**Command:**

    nova-sandbox egress drop --run neverapplied

**Printed:**

    EGRESS STEP name=drop state=start
    EGRESS STEP name=drop state=done ms=46
    EGRESS REFUSED reason=nft_failed: nft delete table inet nova_egress_neverapplied: exit status 1: Error: Could not process rule: No such file or directory\x0adelete table inet nova_egress_neverapplied\x0a                  ^^^^^^^^^^^^^^^^^^^^^^^^; run: nova-sandbox egress -h

exit 1.

**Expected:** dropping a run's table when nothing was applied is the benign no-op a runner
hits on a path out, and the answer should read that way (`EGRESS OK verb=drop` with nothing
to delete, or a short "no table for that run" line). The line instead embeds nft's stderr with
`\x0a` escapes and its caret pointer preserved, under a reason whose remedy is
`nova-sandbox egress -h` — which does not explain a missing table. The applied-and-dropped
path is clean (`EGRESS OK verb=drop run=r1`), so only the already-clean case is unreadable.

**Grade:** NEXT

## 8. The `--acl` note cites "rule 22", which is another tool's spec — NEXT

**Command:**

    HOME=$S/home nova-sandbox --write $S --acl caller --name c -- /bin/echo hi

**Printed:**

    SANDBOX NOTE --acl caller is accepted and ignored on linux; the ACEs of rule 22 are the windows body's
    SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows
    SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise cwd=<scratch-dir>/sandbox/scratch cwdb64=<cwdb64> ancestors=13 cmd=echo gpu=none deletes=<scratch-dir>/sandbox/scratch

**Expected:** `docs/SPEC-SANDBOX.md` numbers its rules 1 through 17; there is no rule 22 in this
tool's spec. "rule 22" is nova-update's (`docs/SPEC-UPDATE.md` names it for `--max`), so the note
sends a reader to a rule this tool does not have. Expected a citation of this tool's own windows
section.

**Grade:** NEXT

## 9. `reap` on Linux refuses with the `run` verb's paragraph — NEXT

**Command:**

    nova-sandbox reap --dry-run

**Printed:**

    SANDBOX REFUSED reason=no_sandbox: the disposable place is darwin's APFS volume, made per run in the boot container and deleted on exit, or windows's Job Object plus per-run scratch; linux has no body here and this tool does not pretend an ordinary directory is one
    run: nova-sandbox --write <dir> -- <command> <args...>, naming the card's own image root as <dir>: on linux a card is already disposable because it runs INSIDE its image, and the image is the container

exit 125.

**Expected:** `reap`'s own help marks it `(darwin)`: it lists every `nova-*` volume, kills what
holds one open and deletes it, and its `--dry-run` is meant to be a gate a card ends on. On
Linux that question ("which `nova-*` volumes is this machine still holding?") is answered by "no
volumes exist on this platform", not by the `run` verb's paragraph about a disposable place and
a remedy that points at the bare wall form. A reap-specific refusal is one line and does not
require the reader to work out that the paragraph is about another verb.

**Grade:** NEXT

READ 8/10 — the banner answers what it does, how the wall is built and how to use it, every
verb's `-h` exits 0 with its flags and exit codes, the refusal grammar is one line with a
remedy, and the `SANDBOX OK` receipt is rich enough to reconstruct a run; the score is held
down by help that promises `--read-noexec` and `--net-allow` properties the Linux body does not
keep, and by refusal reasons outside or against the documented set.

USE 7/10 — nearly every verb ran for real on the bench: the wall denied writes and reads
outside the set, denied the named secret, executed `--read` and refused `--read-noexec` when
the command was elsewhere, `--net-deny` stopped curl, `egress plan/check/apply/drop` moved a
real nftables table, and `probe` proved the wall end to end; the score is held down by the
`--read-noexec` bypass that leaves an explicit control silently unenforced under a line still
claiming it, a `--net-allow` that opens nothing on Linux, and a `no_forge` remedy that loops.

urgent=1 next=8
