# nova-sandbox dogfood, 2026-10-06

Stranger's use of `nova-sandbox` from `nova-sandbox -h`, `help`, `<verb> -h` and docs/SPEC-SANDBOX.md,
at nova-tools `sprint/mechanical-2026-10-02` tip 0760eac79, built and run on the Linux bench
(landlock abi 8, `nova-sandbox v1.0.1-0.20261006184847-0760eac79c77 linux/amd64`). The darwin-only
verbs (`run`, `reap`) were only exercised for their linux refusal. Paths are shortened to `$J`/`$T`.
`$T/job` is the scratch `--write`, `HOME=$T/job/home`.

## Findings

1. `nova-sandbox` (no arguments)
   - `SANDBOX REFUSED reason=no_command: no arguments; every path is yours and none is guessed, so a run names at least one --write and a command after --; run: nova-sandbox help` / `rc=2`
   - Expected: exit 125. `nova-sandbox -h` says of the bare wrap "125 nova-sandbox refused before the command ran, a usage error included"; every other bare-form refusal (`--write $T/job` with nothing after `--`, unknown flag, `--json`) gives 125 and this one gives 2.
   - Grade: URGENT (help lies about the exit code a caller branches on).

2. `nova-sandbox --write $T/job /bin/true`
   - `SANDBOX REFUSED reason=no_command: unexpected argument /bin/true; run: nova-sandbox help` / `SANDBOX REFUSED reason=no_command: no --; the command comes after it: nova-sandbox --write <dir> -- <command> <args...>; run: nova-sandbox help` / `rc=125`
   - Expected: one refusal. Two REFUSED lines for one mistake read as two problems; the same doubling happens for `--write $T/job --cwd -- /bin/true`, where the real mistake is the missing value for `--cwd`, and the message blames the command.
   - Grade: NEXT.

3. `nova-sandbox --write $T/job --net-allow localhost:abc -- /bin/true` (and `localhost:99999`)
   - `SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise ...` / `SANDBOX DONE exit=0 cmd=gnutrue` / `rc=0`
   - Expected: `reason=bad_net` and 125, as for `--net-allow 8.8.8.8:53` and `--net-allow localhost`. A non-numeric or out-of-range port is accepted, and the OK line does not say whether the entry did anything on linux.
   - Grade: NEXT.

4. `nova-sandbox --write $T/job --gpu metal -- /bin/true`
   - `SANDBOX OK backend=landlock abi=8 used=6 ... net=nopromise ... gpu=none` (the first line is the abi NOTE) / `SANDBOX DONE exit=0 cmd=gnutrue`
   - Expected: a NOTE that metal is a darwin capability and was not applied, the way `--name`/`--acl` are documented to print "one NOTE line" where ignored. It is silently dropped and the OK line says `gpu=none`; nothing says why.
   - Grade: NEXT.

5. `nova-sandbox --write $T/job --write $T/job -- /bin/true`
   - `SANDBOX OK ... write=2 ...` / `SANDBOX DONE exit=0 cmd=gnutrue` / `rc=0`
   - Expected: either one entry or a refusal; the same directory in `--read` and `--write` is a refusal ("name it once"), but twice in `--write` is counted as two. `--write $T/job/` (trailing slash) is likewise accepted as a distinct spelling.
   - Grade: NEXT.

6. `nova-sandbox --write $T/job --read / -- /bin/true`
   - `SANDBOX OK backend=landlock abi=8 used=6 read=1 ...` / `SANDBOX DONE exit=0 cmd=gnutrue` / `rc=0`
   - Expected: at least a NOTE. A read root of `/` grants read of the whole disk, the thing the wall exists to prevent; spec rule 3 says a caller who adds one back "has done so in its own argv", so this is by design, but the line gives no sign that the wall is now open. (I did not test what the child could then read.)
   - Grade: NEXT.

7. `nova-sandbox --write $T/job -- /bin/true` (any successful run)
   - `SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), ...` / `SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise cwd=... cwdb64=L2hvbWUv...` / (the command's output)
   - Expected: the result of `check` already said this once; repeating a six-line-wide NOTE and a base64 copy of the cwd on every run buries the command's own output on stderr, which is where a caller reads failures. A caller who greps stderr for the command's message must skip two long lines.
   - Grade: NEXT.

8. `nova-sandbox probe --write $T/job --read $T/nope` (HOME not inside `--write`)
   - `PROBE REFUSED reason=check: --read $T/nope does not exist; every path is named by the caller and none is created (bad_read); run: nova-sandbox probe -h` / `PROBE REFUSED reason=check: HOME ... is outside every --write; ... (home_outside); run: mkdir -p ...` / `PROBE REFUSED reason=check: the directory of $J/nova-sandbox is $J, a home directory, and the home directory is never a root: ...` / `rc=2`
   - Expected: the reason code in `reason=` (every other verb puts `bad_read`/`home_outside` there; probe prints `reason=check` and the code in parentheses), and not three refusals including one about where the binary lives, which only appeared because the binary sat under a home directory on this bench. The third line was cut by my 300-column view, so I did not read its remedy.
   - Grade: NEXT.

9. `nova-sandbox reap --dry-run` and `nova-sandbox run --name t1 --size 8g -- /bin/true` (linux)
   - `SANDBOX REFUSED reason=no_sandbox: the disposable place is darwin's APFS volume ... linux has no body here ...` / `run: nova-sandbox --write <dir> -- <command> <args...>, naming the card's own image root as <dir> ...` / `rc=125`
   - Expected: the message and remedy are good, but `help` documents reap as "Exit 0 clean, 3 when anything remained" and says `reap --dry-run` is "a gate a card can end on". On linux it is always 125, so a card that ends on it cross-platform fails; the `(darwin)` tag is on the usage line only, not in the exit-code paragraph.
   - Grade: NEXT.

10. `nova-sandbox egress plan --run r1 --policy $T/pol.txt --model-host api.anthropic.com --resolver 10.0.0.1 --uid 1000 --out $T/plan.nft` (resolver unreachable)
    - `EGRESS STEP name=resolve state=start` / `EGRESS STEP name=resolve state=done ms=40017` / `EGRESS REFUSED reason=resolve_failed: github.com could not be resolved: lookup github.com: i/o timeout; ...`
    - Expected: a fast failure. The four names are resolved one after another at 10 s each (40 s), `state=done` is printed for a step that failed, and there is no flag for the timeout. The same wait comes before a bad `--out` (`/nonexistent/x`) is looked at, so a typo in `--out` costs 40 s to learn.
    - Grade: NEXT.

11. `nova-sandbox egress drop --run r1` (no such table)
    - `EGRESS STEP name=drop state=start` / `EGRESS STEP name=drop state=done ms=44` / `EGRESS REFUSED reason=nft_failed: nft delete table inet nova_egress_r1: exit status 1: Error: Could not process rule: No such file or directory\x0adelete table inet nova_egress_r1\x0a ...` / `rc=1`
    - Expected: dropping a table that is already gone to be `done` (idempotent), or a plain "no such table"; here `state=done` is followed by a failure, and nft's newlines are printed as literal `\x0a`. Exit 1 is documented as "the verb ran and said NO", which does not fit a teardown.
    - Grade: NEXT.

12. `nova-sandbox worktree --repo $T/repo --scratch $T/job --pr 1` (repo made with `git init`, no `origin`)
    - `WORKTREE REFUSED reason=bad_origin: --repo wants an origin remote whose path names <owner>/<name>, and origin reads ""` / `run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>` / `rc=2`
    - Expected: the remedy to say how to fix it (add an `origin` remote); it repeats the usage line instead. The same repeated usage is the remedy for `--pr abc` and for `--prune --pr 1`, where `--pr wants one pull-request number and one mode` does not say which of the two was wrong.
    - Grade: NEXT.

13. `nova-sandbox --write $T/job --read-noexec $T/rx -- $T/rx/p.sh` (an executable shell script in the noexec dir)
    - `SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=1 ...` / `ran` / `SANDBOX DONE exit=0 cmd=p.sh`
    - Expected: execute denied, per the help ("under this flag it can only read it"). The script ran. It is a `#!/bin/sh` script, so the kernel executes `/bin/sh` (a root) and only reads the script, which Landlock allows; a binary under the noexec dir was not tried. So this is likely correct for scripts, but the help and spec say "a program under a --read runs" and the noexec version "can only read it" with no mention that interpreted scripts still run. A stranger reading the help would expect `Permission denied`.
    - Grade: NEXT (help unclear, binary case untested).

## What worked

`check`, `probe` (with and without `--secret`, `--net-deny`, `--json`), `policy`, `version` and the bare wrap all ran with their documented flags. Measured on this bench: writes outside `--write` and into a `--read` dir are denied; `--read` runs a script and `--read-noexec` still ran `p.sh` (see below); `--net-deny` blocked an outbound connect and a loopback bind, and without it both worked; the caller's real home directory was unreadable inside; the child's exit status (7, 137) came back; the refusals for missing, relative, non-directory, both-lists, `--net-deny --net-listen`, remote `--net-allow`, `--cwd` outside, `HOME` outside, and `--secret` inside an allow list all named their reason and gave a remedy.

## Not done

`egress plan` success, `egress check` on a real plan, `egress apply` (needs root and would change the bench's firewall; not run); `worktree --pr` against GitHub (no network use); darwin `run`/`reap`/`sandbox-exec` paths (this card ran on Linux only); `--acl`, `--name`, `--max` on probe, `--tmp` default cleanup; the `--read /` exposure. `egress drop --run r1` was run once against a table that did not exist (finding 11); it changed nothing.

READ 7/10: `-h` and the per-verb pages are thorough and every refusal carried a remedy, but the top-level help is 159 lines of three platforms at once and the exit-code paragraph contradicts the no-argument case.
USE 8/10: the common path (check, probe, bare wrap) worked first time and every wall I tested held; the friction was duplicate refusal lines, silent acceptance of bad `--net-allow`/`--gpu`, and per-run noise.

urgent=1 next=12
