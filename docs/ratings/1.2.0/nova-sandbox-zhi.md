# nova-sandbox READ and USE rating, nova-tools 1.2.0

Rater: claude-opus-5-5 (Claude Code)
Build: 7a61cf1d1ff9
READ: 7/10
USE: 7/10

## Reasons
READ. `nova-sandbox help`, `-h` on every verb and on every egress subverb, and docs/SPEC-SANDBOX.md were read cold. The top help is strong. It says in five lines how the wall is built, gives a first run whose four lines work as written, and has a per-verb exit-code table. The refusal texts are the best part of the tool to read. The first place of confusion is the `egress plan -h` example (cmd/nova-sandbox/verbhelp.go:82). It has no `--resolver`, which plan requires, and its `--model-host api.example.com` is not in infra/image/egress.txt. Pasted as written, it is refused, and it would be refused again after the first fix. The second is `probe -h`. Its "from `nova-sandbox help`" block quotes the example's continuation line `--secret /path/to/.config/anthropic/env` as if it were a usage line (pkg/nsprint/verbflag/verbflag.go:313). The first place of doubting a claim is cmd/nova-sandbox/main.go:167, "Every path is yours and none is guessed". The directory of the resolved command is a computed read root that carries execute (pkg/sandbox/policy.go:228). The help never says so. The fields `deletes=`, `cwdb64=` and `ancestors=` on SANDBOX OK are not explained in the help either, and `deletes=<the first --write>` reads like a promise to delete that directory. The run deleted nothing. The first place of boredom is the spec: 2337 lines, where the bare form needs about 200. Usage lines omit flags the verbs accept: `--gpu` on the bare form, probe and policy, probe's `--net-listen`, `--json`, and run's `--max-procs`, `--max-mem`, `--out`, `--artifact` and `--out-max-bytes`. `-h` lists them.

USE. Used for real on a Linux bench (landlock ABI 8, kernel 7.0), with the binary built from this head, in a throwaway directory inside the job directory and a scratch HOME inside the `--write`. No live store, and no server started. I ran `check` and `check --json`, and `probe` bare, with `--secret` and `--net-deny`, and with `--json`. I ran `policy` with and without a command and with `--json`, `version`, `egress plan`, `egress check`, `worktree` refusals, `run` and `reap --dry-run`, and about 25 bare-form runs. The wall held on every path I tested. A write outside `--write` was denied. A file outside every grant could not be read. `--read` was readable and not writable. `--net-deny` stopped an HTTPS connect, and the same run without it got a 200. Under `--read-noexec` a shell could not execute a program from that directory. `exit 7` came back as 7, and `kill -9` came back as 137. `SANDBOX DONE exit=<n>` told each one from the tool's codes. These refusals were each provoked once: no `--write`, a relative path (answered with the absolute form), a missing path, `--read` plus `--read-noexec` on one path, `--net-deny` plus `--net-listen`, `--cwd` outside the wall, no `--`, `--json` on the bare form, an unknown flag, a bad `--net-allow`, a command on no PATH entry, a command without an execute bit, HOME outside the wall, HOME unset (both answered with the exact mkdir and HOME line to paste), an unknown verb, an argument to `version`, and egress plan with its flags missing. Each one was exact and actionable, and the tool reported several faults in one turn where it could.

USE is held at 7 by one wall defect that is still open from the earlier 1.2.0 rating. I reproduced it by hand. `nova-sandbox --write W --read-noexec D -- D/prog` RAN D/prog and exited 0, because the command's directory is added as a read root with execute (pkg/sandbox/policy.go:228). The overlap check (pkg/sandbox/policy.go:698) compares only the caller's own `--read` with `--read-noexec`. `policy` prints `read=D` and `read-noexec=D` on the same run, and its summary still says `read=0`. Smaller costs: the ABI clamp NOTE is printed before every wrapped run. probe writes a control file in the parent of the first `--write`, which is a directory never named. `run` and `reap` exit 125 on Linux, though reap's documented codes are 0 and 3. A command that cannot execute exits 125, while the help says 126. egress plan's first refusal leaves out `--model-host` and `--uid/--veth`, so it takes a second turn. probe with no `--write` answers `reason=check`, a reason the spec's grammar fixes, and the fault itself is only in a trailing parenthesis.

A 10 needs:
- the command directory folded into the read-noexec overlap check, or read-noexec winning over it;
- the computed root counted on SANDBOX OK and POLICY OK, and named in help;
- every -h example runnable as printed;
- the SANDBOX OK fields explained in help, and every accepted flag in the usage lines;
- the exit-code text matching what not_executable and not_found return;
- a one-page quickstart over the spec.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | pkg/sandbox/policy.go:698 | the read/read-noexec overlap check ignores the computed root "the directory of the resolved command" (policy.go:228), which carries execute; `--read-noexec D -- D/prog` ran D/prog with exit 0, and `policy` printed both `read=D` and `read-noexec=D` | refuse when a --read-noexec path equals or contains the command's directory, or grant that root without execute when it lies under a --read-noexec | S |
| 2 | cmd/nova-sandbox/verbhelp.go:82 | the `egress plan -h` example (and `egress -h`, :81) has no --resolver, which plan requires, and its --model-host api.example.com is not in infra/image/egress.txt; pasted as printed it is refused bad_resolver, then again on the host | add `--resolver 10.9.0.53` and use a host the shipped allowlist carries, and test that every -h example parses | S |
| 3 | pkg/nsprint/verbflag/verbflag.go:313 | the "from `nova-sandbox help`" excerpt in probe -h, policy -h and egress -h quotes wrapped continuation lines with their indent stripped, so `--secret /path/to/.config/anthropic/env` (an example line) and `[-- <command> <args...>]` read as separate usage lines | keep the continuation indent, or excerpt only the usage block and not the examples | S |
| 4 | cmd/nova-sandbox/main.go:535 | SANDBOX OK carries `deletes=`, `cwdb64=` and `ancestors=` with no word in help; `deletes=<first --write>` reads as a promise to delete that directory (nothing was deleted) | add one help line per field, and spell deletes= as e.g. `delete-ok-under=` | S |
| 5 | cmd/nova-sandbox/main.go:535 | SANDBOX OK and POLICY OK say `read=0` while the command's directory is granted read and execute | count the computed root on both lines, e.g. `cmdroot=<dir>` | S |
| 6 | cmd/nova-sandbox/main.go:167 | "Every path is yours and none is guessed", but the command's own directory is a read+execute root that only the spec mentions | add one help line: the command's directory is a read+execute root; keep commands in a bin directory of their own | S |
| 7 | cmd/nova-sandbox/main.go:188 | help says 125 comes with SANDBOX REFUSED and 126 is "could not be executed"; a command without an execute bit exits 125 reason=not_executable, and a command on no PATH entry prints SANDBOX REFUSED and exits 127 | document not_executable as 125 and not_found as a REFUSED 127, or make the codes follow the text | S |
| 8 | cmd/nova-sandbox/main.go:55 | the usage lines omit accepted flags: --gpu (bare, probe, policy), --net-listen on probe, --json, and run's --max-procs, --max-mem, --out, --artifact, --out-max-bytes | add them to the usage lines, or end each usage line with "(<verb> -h for all flags)" | S |
| 9 | cmd/nova-sandbox/main.go:404 | bareFlags lists --secret, --json and --max as bare-form flags in the unknown-flag answer, then `--json` on the bare form is refused as "not a flag of the bare form" (reason=no_command) | list only the bare form's own flags, and refuse --json with reason=bad_flag | S |
| 10 | cmd/nova-sandbox/main.go:523 | the landlock ABI clamp SANDBOX NOTE is printed on stderr before every wrapped run, as well as by check and policy | print it from check and policy only, or as one field on SANDBOX OK | S |
| 11 | cmd/nova-sandbox/main.go:760 | probe's write_outside_control step creates a file in the parent of the first --write, a directory the caller never named; probe -h does not say so | say so in probe -h, or put the control file in a temp directory the probe makes | S |
| 12 | cmd/nova-sandbox/main.go:702 | probe with no --write answers `PROBE REFUSED reason=check: ... (bad_write)`: by the spec's fixed six-reason grammar every pre-run fault is reason=check, so a reader keying on reason= cannot tell a missing --write from a bad HOME | add a second field for the fault (e.g. `fault=bad_write`) to the PROBE REFUSED grammar, keeping reason=check | S |
| 13 | cmd/nova-sandbox/egress.go:196 | a missing --out on egress plan is refused with reason=no_command | use a reason that names the fault, e.g. bad_out | S |
| 14 | cmd/nova-sandbox/egress.go:196 | `egress plan --run t1` alone reports --out, --policy and --resolver, but not --model-host or --uid/--veth, so a second turn is needed | check every required flag in the first pass | S |
| 15 | cmd/nova-sandbox/run.go:740 | on linux `reap --dry-run` and `run` exit 125 reason=no_sandbox, but help gives reap only 0 and 3 | document 125 for run and reap on a platform with no disposable place | S |
| 16 | pkg/sandbox/policy.go:753 | the HOME refusals cite "rule 9", which no help text defines | drop the rule number from the refusal, or name the spec section it lives in | S |
| 17 | docs/SPEC-SANDBOX.md:1 | 2337 lines of normative spec with no short path for an AI that only needs the bare form | add a one-page quickstart at the top that links the sections | M |

## Good, keep
Refusals that name the reason, the flag and the exact corrected command, report several faults in one turn, and for HOME print the mkdir and HOME line to paste.
`probe` as a self-test with a control step, so a broken wall cannot pass silently, and `--json` on check, policy and probe with one shape (result, facts, items).
`SANDBOX DONE exit=<n>`, which tells the command's own status (7, 137) from the tool's refusal codes.
`policy` printing the exact landlock ruleset, every root by name, without running anything.
`version extra` and an unknown verb each answered with the one command to run instead.
egress plan resolving the allowlist once, and `egress check` reading the ruleset back against its invariants.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 1.2.0 nova-sandbox rating at build 2d1c36b140e1, READ 7.5 USE 7 | READ 7 USE 7: its 12 findings were re-tested and all still hold at this build; READ is half a point lower for the egress plan example that cannot run as printed and the probe -h excerpt | the other nova-sandbox file in docs/ratings/1.2.0, findings 1-12; this file findings 1-3 |
| no nova-sandbox rating in docs/ratings/1.1.0 | no earlier baseline | docs/ratings/1.1.0 |
