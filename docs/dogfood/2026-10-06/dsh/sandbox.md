# nova-sandbox dogfood, 2026-10-06 (dsh)

Tool: nova-sandbox. Build: `nova-sandbox devel darwin/arm64 go1.26.6 backend=sandbox-exec platform=darwin`, built from this card's base tip with `go build ./cmd/nova-sandbox`.
Run cold, from the binary's own help (`-h`, `help`, `<verb> -h`) and the tool's page `docs/SPEC-SANDBOX.md` only, on the machine this card ran on (macOS 26, arm64, `sandbox-exec` at `/usr/bin/sandbox-exec`). Every verb ran for real against a scratch tree inside the job directory: the bare wall (reads, writes, `--read-noexec`, `--net-deny`, a failing command), `probe` (five checks, four without a secret, `--net-deny`, `--net-listen`, `--json`, and its refusals), `policy` (determinism, the command-directory root, the cross-verb flag notes, `--json`), `check`, `run` (a real 64m disposable volume, the `--out` handoff, a `--timeout` kill, and a SIGKILLed run whose leaked volume `reap` cleared), `reap`, `worktree` (a real pull request, the second-call reuse, `--prune`), `egress` (`plan` resolving the shipped allowlist, `check`, and the darwin refusals of `apply` and `drop`), plus `version` and the refusals. In quoted output the job-tree prefix is elided as `…`; nothing else in a quoted line is changed.

Not run: `--net-allow` and `--net-listen` against a live loopback listener (the card bars starting a server on this machine; the grant was read in the printed policy instead), `run --go` (running the Go toolchain on this machine is barred for this card, so its two extra read roots were not observed), `run --container` naming a container other than the boot one, `worktree --base`, and `--gpu metal` (no local model trial).

## 1. `worktree` reports a pull request the forge does not know as an unreachable forge — URGENT

**Command:**

    nova-sandbox worktree --repo …/repo --scratch …/scratch/wtscratch --pr 5174

**Printed:**

    WORKTREE REFUSED reason=no_forge: the forge could not be reached
    run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>; retry once the forge answers

exit 2.

**Expected:** the page separates the two failures — "A pull request the forge does not know is `reason=no_pr` and an unreachable forge is `reason=no_forge`" — and says of `no_forge` that it "is the forge's own silence and never an input the forge was not asked about". The forge was reached: `gh pr view 5174` on this machine answers `Could not resolve to a PullRequest with the number of 5174`, and the same verb invocation with a real pull request of the same repository succeeds seconds later (`WORKTREE OK path=…/wtscratch/0a7082dbdc7befd2ec550a753ec4302b head=ff816afa7eaffe16608ad6a986eb7fcdedab9284`). A mistyped pull-request number is told the network is at fault, and the remedy — "retry once the forge answers" — cannot succeed, so the one-turn recovery a refusal owes its reader is spent on a retry that never lands. The code and the page disagree, and one of them has a bug.

**Grade:** URGENT

## 2. The bare form's unknown-flag refusal lists flags the bare form refuses — NEXT

**Command:**

    nova-sandbox --write …/scratch/job/w --max-procs 4 -- /usr/bin/true

**Printed:**

    SANDBOX REFUSED reason=bad_flag: unknown flag --max-procs; the flags are --read, --read-noexec, --write, --cwd, --tmp, --name, --acl, --secret, --gpu, --net-deny, --net-listen, --json, --net-allow, --max; run: nova-sandbox help

exit 125.

**Expected:** a refusal that names "the flags it has" naming flags this form accepts. Three of those listed — `--secret`, `--json` and `--max` — are refused when passed to the bare form (`--json is not a flag of the bare form; it is check, policy and probe's`), each with `reason=no_command`, where the page's exit table calls a flag the bare form does not have `reason=bad_flag`. A reader who takes the line at its word tries a listed flag and gets a second refusal; and `--max-procs`/`--max-mem`, documented on `run -h` and described by the page's wall-caps section as accepted everywhere, are refused here too — debt the page records as owed, which the line does not say.

**Grade:** NEXT

## 3. `probe` accepts `--max` and its `-h` does not list it — NEXT

**Command:**

    nova-sandbox probe --write …/scratch/job/w --max 3

**Printed:**

    PROBE STEP name=write_outside_control expect=allow got=allow path=…/scratch/job/.nova-sandbox-probe-98683
    PROBE STEP name=write_outside expect=deny got=deny path=…/scratch/job/.nova-sandbox-probe-98683
    PROBE STEP name=write_inside expect=allow got=allow path=…/scratch/job/w/.nova-sandbox-probe-inside

then `PROBE OK … steps=4 passed=4 net=nopromise gpu=none`, exit 0. `probe -h`'s flag list names `--gpu`, `--json`, `--net-deny`, `--net-listen`, `--read`, `--read-noexec`, `--secret`, `--write` — no `--max`.

**Expected:** every flag a verb takes appears in its `-h` with what it wants: the bare form's refusals say `--max` "is probe's", and the page's verb block lists `--max` on probe. What `--max` caps is also not observable from the help or from a passing run — the same four steps print whatever the value.

**Grade:** NEXT

## 4. A failing run's DENIED lines name the wrong writes — NEXT

**Command:**

    nova-sandbox run --name dshdog3 --size 64m --timeout 2m -- /bin/sh -c 'echo x > /etc/nope-dsh.txt'

**Printed** (after the volume steps, the command's own `Operation not permitted` on `/etc/nope-dsh.txt`, and the denials query):

    SANDBOX DENIED path=/dev/dtracehelper op=write remedy="--write /dev"
    SANDBOX DENIED path=/dev/tty op=write remedy="--write /dev"

then the delete steps and `SANDBOX DONE name=dshdog3 exit=1 wall=16.133 freed=32768`, exit 1.

**Expected:** the write that failed the command is a write to `/etc/nope-dsh.txt`, and it is named nowhere; the two denials that are named are writes the shell attempted on `/dev/dtracehelper` and `/dev/tty`, paths the roots table already grants (`/dev` read, `/dev/null` and `/dev/tty` write), and each remedy says `--write /dev` — a widening that hands the job every device file. The page's own drop rule exists so "a remedy naming a flag already in the argv sends a reader to fix what is not broken"; here the reader is sent to widen the wall for denials that did not fail the run, while the one that did stays silent.

**Grade:** NEXT

## 5. The caller's stdout rule is a sharp edge the bare help never states — NEXT

**Command:**

    nova-sandbox --write …/scratch/job/w -- /bin/sh -c 'echo inside > out && cat out' > <a log file outside the write set>

**Printed:**

    SANDBOX NOTE dropped from the child's environment: SSH_AUTH_SOCK; an agent socket speaks for a key the wall denies
    SANDBOX OK backend=sandbox-exec abi=- read=0 read-noexec=0 write=1 net=nopromise cwd=… cwdb64=… ancestors=27 cmd=sh gpu=none deletes=…
    cat: stdout: Operation not permitted

then `SANDBOX DONE exit=1 cmd=sh`, exit 1. With the log inside the write set the same command prints `inside` and exits 0.

**Expected:** the page's caller rule — stdout and stderr are "a pipe the caller drains, or a path inside the write set", and a wrapped `/bin/cat` on an outside file is denied while a shell builtin writing the same descriptor succeeds — held in the help too, not only in the spec. The first wrapped run of a stranger who logs to a file outside the wall dies inside the child with a `cat:` error that names stdout, not the caller's redirect, and the tool adds no NOTE about it; the page's "Commands for a reader" warns of it in a comment, and `-h` does not.

**Grade:** NEXT

## 6. The egress `not_linux` refusal's second line carries no continuation marker — NEXT

**Command:**

    nova-sandbox egress apply --plan …/scratch/dsh1.nft --run dsh1

**Printed:**

    EGRESS REFUSED reason=not_linux: the egress wall is nftables on the bench and nftables is linux's; darwin has no body here and this tool does not pretend a ruleset was applied
    on darwin the card's outbound wall is the seatbelt profile this binary already generates — run the card under `nova-sandbox run` (or the bare form) and use --net-deny when it needs no network at all; `egress plan` and `egress check` still run here, so a plan can be built and read on a Mac and applied on a bench

exit 2.

**Expected:** the set's line grammar — a continuation line opens with `MORE` or `NOTE` — holding on this refusal's second line too, so a consumer that reads continuations by their prefix does not drop the sentence that says where the outbound wall is on this platform. The same bare second line prints from `egress drop`.

**Grade:** NEXT

READ 8/10 — the help answers what, how and how to use it before the usage lines, every verb's `-h` exits 0 with its flags and exit codes, refusals are one line with a pasteable remedy that reports every problem at once, and the spec page is the rare document that states its own measurements and its own gaps; the score is held down by the bad_flag list that names flags the form refuses (finding 2) and by probe's unlisted `--max` (finding 3).

USE 8/10 — every verb ran for real on this machine, and the wall, the probe, the disposable volume (made, handed off through `--out`, timed out, SIGKILLed and reaped back to a clean machine), the worktree and the egress plan all did what the page says they do; the score is held down by the `no_forge` misdiagnosis that sends a mistyped pull request to the network (finding 1) and by DENIED lines that point at `/dev` while the failing write goes unnamed (finding 4).

urgent=1 next=5
