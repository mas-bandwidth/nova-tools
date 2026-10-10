# nova-sandbox READ and USE rating, nova-tools 1.2.0

Rater: claude-opus-5-5 (Claude Code)
Build: 2d1c36b140e1
READ: 7.5/10
USE: 7/10

## Reasons
READ. `nova-sandbox help` and every verb's `-h` were read cold, then docs/SPEC-SANDBOX.md. The help opens with how the wall works, gives a first-run sequence that works, and documents exit codes per verb. Every verb answers `-h` with its own flags and codes. The refusal texts are good writing: each names the reason, the flag and the corrected command. The first place of confusion is cmd/nova-sandbox/main.go:188, where 126 is "the command could not be executed": a command file with no execute bit, or a missing absolute path, actually exits 125 with `reason=not_executable` (pkg/sandbox/policy.go:934 and :940). The first place of doubting a claim is cmd/nova-sandbox/main.go:167, "Every path is yours and none is guessed". The directory of the resolved command is a computed read root that grants execute too (spec lines 1332 and 1775). The help never says so, and `SANDBOX OK` still prints `read=0`. The first place of boredom is the spec itself: 2337 lines, where an AI needs about 200 to use the bare form. Flags the tool accepts are missing from the top help (`--json`, probe's `--max`, and run's `--max-procs`, `--max-mem`, `--out`, `--artifact` and `--out-max-bytes`). The bare form's unknown-flag answer lists `--json`, `--secret` and `--max` as flags of the bare form, then refuses `--json` there as "not a flag of the bare form".

USE. Used for real on a Linux bench (landlock ABI 8, kernel 7.0) in a throwaway directory inside the job directory, with a scratch HOME. No live store and no server. `check`, `probe` (with and without `--secret` and `--net-deny`, and with `--json`), `policy`, `version`, `egress plan` and `egress check` were run, plus about 30 bare-form runs. The wall held everywhere I tested it. A write outside `--write` was denied. A read outside every grant was denied. `--read` was readable and not writable. `--net-deny` stopped an HTTPS connect while a run without it reached the network. A child under `--read-noexec` could not execute a binary or a script. Status passed through (`exit 7` came back as 7), and `SANDBOX DONE exit=<n>` told the command's status from the tool's. About 20 refusals were provoked: missing `--write`, a relative path (answered with the absolute form), a missing path, `--read` plus `--read-noexec` on the same path, `--net-deny` plus `--net-listen`, `--cwd` and `--tmp` outside the wall, HOME outside the wall or unset (answered with the exact mkdir and HOME line to run), a bad `--net-allow`, an unknown flag and an unknown verb, a missing `--` command, and on egress a missing selector and a model host not in the allowlist. Every one was precise and actionable, and several errors were reported in one turn.

The score is held at 7 by one wall defect, which I reproduced by hand and read in the code. The command's own directory is added as an optional root with read AND execute (pkg/sandbox/policy.go:229), and the overlap check that refuses `--read` plus `--read-noexec` on one path (pkg/sandbox/policy.go:697) does not look at that computed root. So `nova-sandbox --write T --read-noexec D -- D/prog` runs D/prog with D readable and executable. `policy` prints both `read=D` and `read-noexec=D` with `read=0` on the summary line. A script run as the command read a file beside it that was named under no flag at all. That is documented in the spec, but not in the help or on the OK line. Smaller costs: the `SANDBOX NOTE` about the ABI clamp is printed before every wrapped run, `probe` writes its control file in the parent of the first `--write` without the help saying so, and `run` and `reap` are darwin-only and exit 125 on Linux. reap's documented codes are only 0 and 3.

A 10 needs:
- the command directory folded into the overlap check, or `--read-noexec` winning over it;
- the computed root counted on `SANDBOX OK` and named in `help`;
- the exit-code text matching what a non-executable command returns;
- every accepted flag in the top help;
- a one-page "AI quickstart" over the spec.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | pkg/sandbox/policy.go:697 | the read/read-noexec overlap check ignores the computed root "the directory of the resolved command" (policy.go:229), which carries execute; `--read-noexec D -- D/prog` runs with D readable and executable, and `policy` prints both `read=D` and `read-noexec=D` | refuse when a --read-noexec path equals or contains the command's directory, or grant that root without execute when it lies under a --read-noexec | S |
| 2 | cmd/nova-sandbox/main.go:535 | the SANDBOX OK line says `read=0` while the command's directory is granted read and execute; a script run as the command read a file beside it that no flag named | count the computed root on the OK line (e.g. `cmdroot=<dir>`) and in POLICY OK | S |
| 3 | cmd/nova-sandbox/main.go:167 | help says "Every path is yours and none is guessed" but never mentions the computed command-directory root; only the spec (lines 1332, 1775) does | add one help line: the command's own directory is a read+execute root; keep commands in a bin directory of their own | S |
| 4 | cmd/nova-sandbox/main.go:188 | help says 126 is "the command could not be executed", but a command file without an execute bit, or a missing absolute path, exits 125 reason=not_executable | either exit 126 for not_executable, or document that not_executable is a 125 refusal | S |
| 5 | cmd/nova-sandbox/main.go:405 | bareFlags lists --secret, --json and --max as bare-form flags in the unknown-flag answer, then `--json` on the bare form is refused as "not a flag of the bare form" | list only the bare form's own flags in that answer | S |
| 6 | cmd/nova-sandbox/main.go:55 | the top help omits flags the tool accepts: --json (check, policy, probe), probe's --max, run's --max-procs, --max-mem, --out, --artifact, --out-max-bytes | add them to the usage lines and the flag list, or point to `<verb> -h` for each | S |
| 7 | cmd/nova-sandbox/main.go:523 | the SANDBOX NOTE about the landlock ABI clamp is printed on stderr before every wrapped run, as well as by check and policy | print it from check and policy only, or once per run as a field on SANDBOX OK | S |
| 8 | cmd/nova-sandbox/main.go:760 | probe's write_outside_control step writes a file in the parent of the first --write, a directory the caller never named; the help does not say probe touches it | say so in probe -h, or put the control file in a temp directory the probe makes | S |
| 9 | cmd/nova-sandbox/egress.go:196 | a missing --out on egress plan is refused with reason=no_command; the bare form's `--json` refusal also uses no_command | use a reason that names the fault, e.g. bad_out and bad_flag | S |
| 10 | cmd/nova-sandbox/egress.go:196 | `egress plan --run t1` with nothing else reports --out, --policy and --resolver missing, but not --model-host or --uid/--veth, so a second turn is needed | check every required flag in the first pass | S |
| 11 | cmd/nova-sandbox/main.go:188 | on linux, `reap --dry-run` exits 125 with reason=no_sandbox, but reap's documented codes are only 0 and 3 | document 125 (or 2) for reap and run on a platform with no disposable place | S |
| 12 | docs/SPEC-SANDBOX.md:1 | 2337 lines of normative spec, with no short path for an AI that only needs the bare form | add a one-page quickstart at the top that links the sections | M |

## Good, keep
Refusals that name the reason, the flag and the exact corrected command, with several faults reported in one turn (HOME refusals print the mkdir and HOME line to run).
`probe` as a self-test with a control step, so a broken wall cannot pass silently, and `--json` on check, policy and probe.
`SANDBOX DONE exit=<n>`, which tells the command's own exit status from the tool's refusal codes.
`policy` printing the exact landlock ruleset without running anything.
egress plan refusing a model host that is not already in the reviewed allowlist.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier nova-sandbox rating under docs/ratings (1.1.0 and snapshots have none) | FIRST RATING | docs/ratings/1.1.0 |
