# nova-sandbox READ and USE rating, nova-tools 1.2.0

Rater: friend on a re-rate card (inception/mercury-2.5)
Build: 1c5a46e10594
READ: 7/10
USE: 7/10

## Reasons

READ. Read `nova-sandbox help` and each verb's `-h` cold, then ran on a throwaway directory on a Linux bench. The help explains the wall, provides working first-run examples, and documents exit codes. Every verb responds to `-h`. Refusal messages are good—they name the reason and the correct command. The first confusion is that the tool's own directory is added as a read+execute root, but help never mentions this; `SANDBOX OK` shows `read=0` for this root. The first doubting a claim is `Every path is yours and none is guessed` at help line 167—the computed command directory root is only in the spec (lines 1332, 1775). The first boredom is the spec's length: 2337 lines when ~200 would suffice for the bare form. Flags like `--json`, probe's `--max`, and run's `--max-procs`, `--max-mem`, `--out`, `--artifact`, `--out-max-bytes` are missing from top help.

USE. Tested on Linux (Landlock ABI 8, kernel 6.x) in a throwaway directory. Ran `check`, `probe`, `policy`, `version`, and ~25 bare-form runs. The wall held: writes outside `--write` denied, reads outside grants denied, `--net-deny` blocked HTTPS, `--read-noexec` prevented execution. Exit status passed through (`exit 7` returned as 7), and `SANDBOX DONE exit=<n>` reported the command's status. About 15 refusals provoked and all were precise: missing `--write`, relative paths (answered with absolute), missing paths, `--read`+`--read-noexec` overlap, HOME outside wall, bad `--net-allow`, unknown flags, and egress issues.

The score is held at 7 by one wall defect: the command directory is added to OptRoots with execute (policy.go:229), but the `--read`/`--read-noexec` overlap check (policy.go:697) ignores it. So `nova-sandbox --write T --read-noexec D -- D/prog` runs D/prog with D readable and executable. `policy` prints both `read=D` and `read-noexec=D` with `read=0` on the summary line.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | pkg/sandbox/policy.go:697 | the read/read-noexec overlap check ignores the computed root (the command's directory added to OptRoots with execute at policy.go:229), allowing `--read-noexec D -- D/prog` to run with D readable/executable | refuse when a --read-noexec path equals or contains the command's directory, or grant that root without execute under --read-noexec | S |
| 2 | cmd/nova-sandbox/main.go:535 | `SANDBOX OK` shows `read=0` while the command directory is granted read+execute; scripts in that directory can read sibling files | count the computed root on the OK line (e.g. `cmdroot=<dir>`) and in POLICY OK | S |
| 3 | cmd/nova-sandbox/main.go:167 | help says "Every path is yours and none is guessed" but never mentions the computed command-directory root; only the spec (lines 1332, 1775) does | add one help line: the command's own directory is a read+execute root | S |
| 4 | cmd/nova-sandbox/main.go:188 | help says 126 is "the command could not be executed", but a command file without execute bit exits 125 reason=not_executable (policy.go:964) | exit 126 for not_executable, or document that not_executable is a 125 refusal | S |
| 5 | cmd/nova-sandbox/main.go:405 | bareFlags lists --secret, --json and --max as bare-form flags in the unknown-flag answer, then `--json` on the bare form is refused as "not a flag of the bare form" | list only the bare form's own flags in that answer | S |
| 6 | cmd/nova-sandbox/main.go:55 | the top help omits flags: --json (check, policy, probe), probe's --max, run's --max-procs, --max-mem, --out, --artifact, --out-max-bytes | add them to usage lines or point to `<verb> -h` for each | S |
| 7 | cmd/nova-sandbox/main.go:523 | the SANDBOX NOTE about the ABI clamp is printed on stderr before every wrapped run, plus by check and policy | print it from check and policy only, or once per run as a field on SANDBOX OK | S |
| 8 | cmd/nova-sandbox/main.go:760 | probe's write_outside_control step writes a file in the parent of the first --write, a directory the caller never named; help doesn't say probe touches it | say so in probe -h, or put the control file in a temp directory | S |
| 9 | cmd/nova-sandbox/egress.go:196 | a missing --out on egress plan is refused with reason=no_command; the bare form's `--json` refusal also uses no_command | use a reason that names the fault, e.g. bad_out and bad_flag | S |
| 10 | cmd/nova-sandbox/egress.go:196 | `egress plan --run t1` with nothing else reports --out, --policy and --resolver missing, but not --model-host or --uid/--veth | check every required flag in the first pass | S |
| 11 | cmd/nova-sandbox/main.go:188 | on linux, `reap --dry-run` exits 125 with reason=no_sandbox, but reap's documented codes are only 0 and 3 | document 125 (or 2) for reap and run on a platform with no disposable place | S |
| 12 | docs/SPEC-SANDBOX.md:1 | 2337 lines of normative spec with no short path for an AI that only needs the bare form | add a one-page quickstart at the top that links to sections | M |

## Good, keep
Refusals that name the reason, the flag and the exact corrected command, with several faults reported in one turn.

`probe` as a self-test with a control step, so a broken wall cannot pass silently, and `--json` on check, policy and probe.

`SANDBOX DONE exit=<n>`, which tells the command's own exit status from the tool's refusal codes.

`policy` printing the exact landlock ruleset without running anything.

egress plan refusing a model host that is not already in the reviewed allowlist.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier nova-sandbox rating under docs/ratings | FIRST RATING | docs/ratings/1.1.0 has none |
