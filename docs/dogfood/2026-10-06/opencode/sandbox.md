# Dogfood: nova-sandbox, one friend's pass

One run, 2026-10-06, on linux/amd64 (Landlock ABI 4 at the running kernel), by an
AI worker in the opencode harness meeting the tool cold. Read first, nothing else:
`nova-sandbox -h`, `nova-sandbox help`, `nova-sandbox <verb> -h` for all ten verbs,
and docs/SPEC-SANDBOX.md. Then every verb at least once with its real flags against
a scratch directory tree, the refusals included. About 25 minutes of use.

Binary: `nova-sandbox v1.0.1-0.20261006184440-8076dfdfcd85 linux/amd64 go1.26.6
backend=landlock platform=linux`, built from this tree at 8076dfdfcd85.

Scratch: the harness refuses paths outside the job directory, so the scratch tree
lives there. In the findings below the scratch root is spelled `<J>` and the binary
`<SB>`; `<J>/home` sat inside the first `--write` as the banner's first run demands,
`<J>/data` held an input file, a secret file, a `#!/bin/sh` script and a compiled
Go binary. Every refusal and every DENIED quoted here was printed by the tool.

What worked, in one paragraph: `check` (plain and `--json`), `version`, `help` and
every `<verb> -h` answer 0; `probe` passed 4/4 (and 5/5 with an outside `--secret`);
the bare wrap wrote inside the wall, was denied by the kernel outside it (`Permission
denied`, exit 1), denied a read outside both lists, passed the child's own status
through (2, 143 for a SIGTERM) and enforced `--net-deny` (bind/connect: permission
denied) against a compiled prober; `--net-listen` with `--net-deny`, zero `--write`,
relative paths, absent paths, a path in both lists, `--read`/`--read-noexec` and
`--read-noexec`/`--write` overlaps, a missing `--`, the bare form's `--secret`, an
unknown verb and an unset or outside `HOME` were each refused at 125/2 with a reason
and a paste-able remedy, the relative path naming the absolute form it would have
taken; `policy` printed the Landlock ruleset (plain, `--json`, with the `POLICY NOTE`
for probe's `--secret`); `git status` and `git log` ran inside the wall with `HOME`
inside a `--write`; `worktree` named all three of its input problems at once and
refused `no_forge` at 2; `run`, `reap`, `egress apply` and `egress drop` refused
honestly on this machine (darwin bodies; no `nft` on this bench) with the linux
alternative in the remedy line. git inside the wall, `git -C <J>/repo status` under
`--read /usr/bin --write <J>`, printed `On branch master / nothing to commit`.

## Findings

1. Command as typed: `HOME=<J>/home <SB> --read-noexec <J>/data --write <J> -- <J>/data/tinybin`
   (a compiled Go binary under `--read-noexec`; the same result with a `#!/bin/sh`
   script there).

   What it printed (first 3 lines; the `cwdb64=` value elided, the line is one):

   ```
   SANDBOX OK backend=landlock abi=4 read=0 read-noexec=1 write=1 net=nopromise cwd=<J> cwdb64=… cmd=tinybin gpu=none deletes=<J>
   SANDBOX DONE exit=0 cmd=tinybin
   ```

   The binary ran. Expected: the wall to take execute back — SPEC-SANDBOX.md rule 4,
   "`--read-noexec` is the same read grant with the execute taken back — … on linux
   the read subset minus `fsExecute`" — so the exec is denied (126, or a permission
   error) while reads under the same flag still work (they do: `/bin/cat` of a file
   in the same tree printed the file, exit 0). Grade: **URGENT** — the flag's own
   help line says "NOT EXECUTABLE", the kernel here enforces Landlock ABI 4 (the
   `EXECUTE` right is there from ABI 1), and the one tree a caller names as a cache
   or a data tree this user can write to is a tree the wall lets the job run. A
   wall property that is printed on the OK line and not enforced is a wrong result.

2. Command as typed: `HOME=<J>/home <SB> probe --write <J> --max 2`

   What it printed (first 3 lines):

   ```
   PROBE STEP name=write_outside_control expect=allow got=allow path=<J>/../.nova-sandbox-probe-1264925
   PROBE STEP name=write_outside expect=deny got=deny path=<J>/../.nova-sandbox-probe-1264925
   PROBE STEP name=write_inside expect=allow got=allow path=<J>/.nova-sandbox-probe-inside
   ```

   followed by the fourth `PROBE STEP` and `PROBE OK … steps=4 passed=4`. Expected:
   the listing cut at 2 with a `MORE shown=2 total=4` line — the standard's
   cap-and-count rule — or a refusal that `--max` is not probe's. Under `--max 2
   --json` the object carries `items: 4`, no `more` and no note: the flag is
   accepted and does nothing, and `probe -h` does not list it, while
   SPEC-SANDBOX.md's verbs block (line 467) says probe takes `[--max <n>]`. Grade:
   **NEXT** — a silently ignored flag and a spec/help drift; the standard refuses
   exactly this shape ("a flag that belongs to another verb is never silently
   dropped").

3. Command as typed: `<SB> egress plan --run trial1 --policy <repo>/infra/image/egress.txt
   --model-host api.deepseek.com --resolver 10.10.0.1 --uid 1000 --out <J>/egress.nft`
   (after `--resolver 127.0.0.1` was refused with a correct reason — the plan denies
   the loopback, so DNS would die silently).

   What it printed (first 3 lines of stderr):

   ```
   EGRESS STEP name=resolve state=start
   EGRESS STEP name=resolve state=done ms=40117
   EGRESS REFUSED reason=resolve_failed: github.com could not be resolved: lookup github.com: i/o timeout; a name that cannot be pinned is a name the run cannot reach
   ```

   Expected: the same honest refusal without the 40-second silent gap — this bench
   has no DNS, and three policy names at 40 s each is a minute of nothing before a
   caller learns anything; the help names no flag to bound the resolve wait. Grade:
   **NEXT** — the refusal, its reason and its remedy are right; the stall and the
   missing bound are the friction.

4. Command as typed: `HOME=<J>/home <SB> --read <J>/data --write <J> -- /bin/sh -c 'echo inside > out.txt'`

   What it printed (first line; the `cwdb64=` value elided, the line is one):

   ```
   SANDBOX OK backend=landlock abi=4 read=1 read-noexec=0 write=1 net=nopromise cwd=<J> cwdb64=<base64 of the same cwd> ancestors=15 cmd=dash gpu=none deletes=<J>
   ```

   Expected: one statement of the cwd, not two — `cwd=` and `cwdb64=` carry the
   same path twice, which on this job's paths puts the OK line near 500 characters
   and makes the one line a caller is told to read strain both the eye and any
   column-limited log. Grade: **NEXT** — cosmetic, but it is on every wrapped run's
   first line.

## Scores

READ 8/10 — the banner, the verb helps and the spec are complete, truthful and
remedy-bearing, and the only drift found is probe's `--max` (in the spec, absent
from `probe -h`, ignored by the verb).

USE 8/10 — every verb answered with either its result or a refusal that named the
remedy in one turn, and the wall held on writes, reads, network and signals; the
one defect is `--read-noexec`, the one flag whose promise a caller must not trust
on Linux today.

Not exercised for lack of machine, only refused: `run` and `reap` (darwin bodies),
`egress apply`/`drop` past the refusal (no `nft` on this bench), `egress check`
against a real plan (no plan could be generated without DNS), `worktree`'s happy
path (the forge is unreachable from this bench).

urgent=1 next=3
