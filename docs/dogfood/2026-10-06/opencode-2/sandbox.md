# nova-sandbox dogfood — opencode-2, 2026-10-06

Built in the staged checkout on a Linux bench (`<bench>`, kernel 7.0.0-34-generic, x86-64, Landlock ABI 8) with `go build -o $B/bin/nova-sandbox ./cmd/nova-sandbox`, never the installed binary, at the base tip `7acb90e18a76` of `sprint/mechanical-2026-10-02`; it printed `nova-sandbox v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6 backend=landlock platform=linux`, and the pass ran under the made-up actor name `boss`. Read cold, and nothing else: `nova-sandbox -h`, `nova-sandbox help`, `nova-sandbox <verb> -h`, and the tool's page `docs/SPEC-SANDBOX.md`. Every verb then ran for real against one scratch tree: `check` (plain and `--json`, and its bad-flag refusal), `probe` (with and without `--secret`, with `--net-deny`, `--json`, the refusals), `policy` (the roots, `--read`, `--net-deny`, `--net-allow`, `--net-listen`, `--json`, and the refusals), the bare wall (writes and deletes inside and outside the set, `--read` execute and `--read-noexec`, `--cwd`, `--tmp`, `HOME`, `--net-deny`/`--net-allow`/`--net-listen`, `--name`/`--acl`/`--gpu`, the command's status and a signal, the unknown-flag and missing-command refusals), `run` and `reap` (their Linux refusals and `reap -h`), `worktree` (its refusals, the `--prune` clean pass, the forge path), all four `egress` verbs (a real nftables table planned, checked, applied, listed and dropped), `version`, `help`, and the unknown-verb refusal; one suspected intermittent `probe --max 1` false deny was looked for and not seen (800 `--max 1` runs and 200 plain runs all passed). Commands below were typed with

    B=$HOME/zhi-bench/dogfood-opencode-2-sandbox-bb.w1~15
    NB=$B/bin/nova-sandbox
    S=$B/scratch/store

and run with `HOME=$S/home`; in quoted output `$B`, `$S`, `$NB` and `/home/<user>` abbreviate those prefixes, a generated process id is written `<pid>`, and a long machine-readable field is elided with `…`; nothing else in a quoted line is changed.

## Findings

1. `HOME=$S/home $NB --write $S -- $B/bin/runme`
   Printed:
   ```
   SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows
   SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise cwd=$S cwdb64=… ancestors=14 cmd=runme gpu=none deletes=$S
   cmd-dir-script-ran
   ```
   I expected the credential beside the resolved command to be unreadable: rule 6 says the file "stays outside every named path, so the wrapped command cannot read it even if it is told to", and the roots section already refuses a computed command directory that is the caller's home. The `read=0` on the OK line counts no such root, yet the script's next line printed `SECRET-CMD-DIR`.
   Grade: URGENT (a credential beside the resolved command is readable inside a wall whose OK line reports read=0)

2. `HOME=$S/home $NB --write $S --read-noexec $B/noexectree -- $B/noexectree/noexec`
   Printed:
   ```
   SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows
   SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=1 write=1 net=nopromise cwd=$S cwdb64=… ancestors=14 cmd=noexec gpu=none deletes=$S
   noexec-tree-ran
   ```
   I expected `--read-noexec` to hold: it is "readable, recursively, and NOT EXECUTABLE", and the ELF `$B/noexectree/dash` in the same tree is denied when it is invoked through `/bin/sh` (`Permission denied`, exit 126). Here the command itself is the script under the no-exec tree, and it runs, because the computed root "the directory of the resolved command" grants execute back.
   Grade: URGENT (an explicit no-exec grant is silently defeated by the tool's own computed root)

3. `$NB worktree --repo $B/repo --scratch $S/wtscratch --pr 5327`
   Printed:
   ```
   WORKTREE REFUSED reason=no_forge: the forge could not be reached
   run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>; retry once the forge answers
   ```
   I expected the forge's own definite answer to be reported, not silence: the page says `no_forge` "is the forge's own silence and never an input the forge was not asked about", while `gh auth status` answered `You are not logged into any GitHub hosts. To log in, run: gh auth login` (exit 1) and `gh pr view 5327` answered `To get started with GitHub CLI, please run: gh auth login` (exit 4). The remedy — retry once the forge answers — can never succeed.
   Grade: URGENT (a refusal with no remedy: a credential problem is reported as an unreachable forge and sent to a retry loop)

4. `HOME=$S/home $NB probe --write $S --secret $B/bin/binsibling.txt`
   Printed:
   ```
   PROBE STEP name=write_outside_control expect=allow got=allow path=$B/scratch/.nova-sandbox-probe-<pid>
   PROBE STEP name=write_outside expect=deny got=deny path=$B/scratch/.nova-sandbox-probe-<pid>
   PROBE STEP name=read_secret expect=deny got=allow path=$B/bin/binsibling.txt
   ```
   I expected `secret_inside_allow`, because the probe's own executable is the resolved command of its re-exec and `$B/bin` is therefore the computed command-directory root. Instead the run prints `PROBE REFUSED reason=check: read_secret expected deny and got allow at $B/bin/binsibling.txt; run: nova-sandbox probe -h`, exit 1, and calls a working wall broken with only a help reprint as the remedy.
   Grade: NEXT (a false broken-wall report whose remedy is a help reprint)

5. `HOME=$S/home $NB --no-sandbox --write $S -- /bin/echo hi`
   Printed:
   ```
   SANDBOX REFUSED reason=bad_flag: unknown flag --no-sandbox; the flags are --read, --read-noexec, --write, --cwd, --tmp, --name, --acl, --secret, --gpu, --net-deny, --net-listen, --json, --net-allow, --max; run: nova-sandbox help
   (one line printed)
   ```
   I expected the list to name only flags this form accepts, and a flag it does not have to be `bad_flag`: the exit table says "a flag the bare form does not have (`reason=bad_flag`, naming the flags it has)". Three flags in that list — `--secret`, `--json` and `--max` — are refused by the same form with `SANDBOX REFUSED reason=no_command: --<flag> is not a flag of the bare form; it is <verb>'s`.
   Grade: NEXT (the refusal lists flags the bare form itself refuses, under a different reason)

6. `$NB egress plan --run r3 --policy $B/repo/infra/image/egress.txt --model-host api.deepseek.com --resolver 8.8.8.8 --uid 10001`
   Printed:
   ```
   EGRESS REFUSED reason=no_command: --out wants the file the ruleset is written to: --out /run/nova/egress-<run>.nft
   run: nova-sandbox egress plan --run <id> --policy infra/image/egress.txt --model-host <host> --resolver <ip> [--bench-cidr <cidr>]... --uid <n> --out <file>
   ```
   I expected `reason=bad_out`, the word the page's egress refusal set has for a missing `--out`; `no_command` names a missing command in a verb that takes none. The same drift sends a `check` or `apply` reader to the plan remedy: `egress check --plan $S/absent.nft` prints `EGRESS REFUSED reason=bad_plan: …` and then `run: nova-sandbox egress plan … --out <file>`.
   Grade: NEXT (the reason word comes from the exec verb, and each verb's remedy does not name that verb)

7. `$NB reap -h`
   Printed:
   ```
   nova-sandbox reap: clear the disposable volumes a killed run left behind (darwin)

   usage:
   ```
   I expected a Linux reader's first page to state the refusal they will meet: `reap` and `reap --dry-run` both print `SANDBOX REFUSED reason=no_sandbox: the disposable place is darwin's APFS volume …` at exit 125, but this help documents only 0 and 3 and never the platform refusal. Its own unknown-flag path is the same drift: `reap --bogus` prints `SANDBOX REFUSED reason=no_command: unknown flag --bogus; run: nova-sandbox help reap`, where the exit table makes an unknown flag `bad_flag`.
   Grade: NEXT (the verb's own help hides the refusal and exit code this platform prints)

8. `HOME=$S/home $NB --write $S --net-allow localhost:11434 -- /bin/sh -c 'echo hi'`
   Printed:
   ```
   SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows
   SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=0 write=1 net=nopromise cwd=$S cwdb64=… ancestors=14 cmd=dash gpu=none deletes=$S
   hi
   ```
   I expected a flag whose wall this platform does not build to be refused or noted as ignored, as `--acl caller` is (`SANDBOX NOTE --acl caller is accepted and ignored on linux`). On this Linux bench `--net-allow` and `--net-listen` change nothing and print no note: `policy --write $S --net-allow localhost:11434` prints `net=nopromise` and no line for the grant.
   Grade: NEXT (a grant the platform does not build is accepted in silence)

9. `$NB egress drop --run r3`
   Printed:
   ```
   EGRESS STEP name=drop state=start
   EGRESS STEP name=drop state=done ms=43
   EGRESS REFUSED reason=nft_failed: nft delete table inet nova_egress_r3: exit status 1: Error: Could not process rule: No such file or directory\x0adelete table inet nova_egress_r3\x0a                  ^^^^^^^^^^^^^^; run: nova-sandbox egress -h
   ```
   I expected a bench with no table for this run to be reported as clean, not as a failed `nft`: `egress plan`, `egress check` and the first `egress drop` all succeeded (`EGRESS OK verb=drop run=r3 table=nova_egress_r3`, exit 0). A second drop should say the table is not there and exit 0 (nothing to drop) or carry a line a reader can act on, not nft's stderr escaped as `\x0a` with a caret.
   Grade: NEXT (an already-clean bench is reported as `nft_failed`, with raw escaped stderr)

10. `$NB check --max 1`
    Printed:
    ``` CHECK REFUSED reason=bad_flag: unknown flag --max; run: nova-sandbox help check (one line printed) ```
    I expected one surface for a flag the tool owns: `policy --write $S --max 1` answers `POLICY NOTE --max is probe's flag and is ignored here`, the bare form's flag list names `--max`, and `probe -h` — the verb the note points at — lists `--gpu`, `--json`, `--net-deny`, `--net-listen`, `--read`, `--read-noexec`, `--secret`, `--write` and no `--max` with its unit.
    Grade: NEXT (a flag is named by one verb's refusal and note and absent from the help that owns it)

11. `$NB version extra`
    Printed:
    ``` SANDBOX REFUSED reason=unknown_flag: version takes no flags and no arguments, got 1; run: nova-sandbox version (one line printed) ```
    I expected a reason word the page's SANDBOX REFUSED set names; the set has `bad_flag`, `not_found` and `not_executable`, and no `unknown_flag`. The verb's own help documents exit 2 for "an argument was given", so the status is right and only the word is new.
    Grade: NEXT (a refusal reason outside the documented set)

12. `$NB help bogus`
    Printed:
    
    nova-sandbox: run one command inside an OS-enforced wall around the directories you name

    how it works: the wall is built for one run from your flags and kept nowhere:
    
    I expected `help <verb>` to answer for the verb named, and an unknown verb to be refused with the verbs there are (`reason=unknown_verb`), the way bare `nova-sandbox bogus` is: `SANDBOX REFUSED reason=unknown_verb: unknown verb "bogus"; available: check, egress, policy, probe, reap, run, version, worktree; run: nova-sandbox help`. Instead a mistyped verb prints the whole top-level help at exit 0 and names nothing.
    Grade: NEXT (unclear help: a mistyped verb is silently answered with the whole banner, exit 0)

## What held

`check` (plain and `--json`) and `version` answered 0; `help` and every `<verb> -h` answered 0; `probe` passed 4/4 without a secret and 5/5 with one outside the lists, `--net-deny` printed `net=denied`, and a secret inside a `--read` or `--write` was `secret_inside_allow` at exit 2; `policy` printed the Landlock ruleset for the roots, `--read`, `--net-deny` (`net=denied`) and `--json`, and refused a path in both `--read` and `--write`, in both `--read` and `--read-noexec`, and in both `--read-noexec` and `--write`, a missing path, a relative path, a `HOME` outside every `--write`, and `--net-deny` with `--net-listen`; the bare wall wrote and deleted inside the write set, denied a write and a read outside it, ran an ELF under `--read` (`elf-via-read-ran`) while the same ELF under `--read-noexec` was `Permission denied` at exit 126 when invoked through `/bin/sh`, refused a `--cwd` outside the write set, set `TMPDIR` from an existing `--tmp`, printed `net=denied` for `--net-deny`, passed the command's status through (`7`, and `143` for a signal), and refused `HOME` outside every `--write`; `run` and `reap` gave their own Linux `no_sandbox` refusal at 125 for the platform the banner marks `(darwin)`; `worktree --prune` printed `WORKTREE OK removed=0 kept=0` at exit 0 and an absent `--scratch`, an absent `--repo`, a missing `--repo` and `--prune` with `--pr` each refused with one remedy line; `egress plan`, `check`, `apply` and `drop` moved a real nftables table (`allow=8 deny=6`, `EGRESS OK verb=apply`, then `EGRESS OK verb=drop`), and a `--model-host` not in the policy, a plan with no `--uid`/`--veth`, a bad `--resolver` and an absent `--plan` for `apply` all refused. Not run: `run`'s disposable-volume body and `reap`'s real cleanup (both darwin's or windows's, so on Linux the refusals above are the verb's whole body), the `worktree` success, reuse and removal paths (the bench's `gh` has no credential), `--net-allow` and `--net-listen` against a live loopback listener (the card bars starting a server on the bench; the grant was read in the printed policy instead), and `--gpu metal` against a local model.

READ 7/10 — the banner answers what, how and how to use it before the usage lines, every verb's `-h` exits 0 with its flags and own exit codes, the refusals are one line with a pasteable remedy, and the page states its own measurements and gaps; the score is held down by a bare-form flag list that names refused flags, by `--max` present in a note and a refusal but absent from the verb's help, by `reap -h` hiding its own Linux refusal and 125, by `help bogus` answering 0 with the whole banner, and by an `unknown_flag` reason word the page never lists.

USE 6/10 — the wall did the real work on Linux: writes and deletes inside the write set, reads and writes outside it denied, `--read` carried execute, `--read-noexec` denied an ELF when the command was elsewhere, `HOME` outside a write refused, the command's status and a signal passed through, `policy` printed a real ruleset, `probe` proved the wall, and the four `egress` verbs moved a real nftables table on the bench; the score is held down by a credential beside the resolved command readable under a `SANDBOX OK` line that reports no such root, by `--read-noexec` losing to the tool's own command-directory root, by a `probe --secret` that calls that working wall broken, by a `no_forge` that sends a credential problem to a retry loop, and by a second `egress drop` that reports an already-clean bench as `nft_failed`.

urgent=3 next=9
