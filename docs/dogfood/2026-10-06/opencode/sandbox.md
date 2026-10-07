# Dogfood: nova-sandbox, one friend's pass

One run, 2026-10-07, on linux/amd64 (Landlock ABI 8 at the running kernel, clamped to 6), by an
AI worker in the opencode harness meeting the tool cold. Read first, nothing else:
`nova-sandbox -h`, `nova-sandbox help`, `nova-sandbox <verb> -h` for all verbs,
and docs/SPEC-SANDBOX.md. Then every verb at least once with its real flags against
a scratch directory tree, the refusals included. About 30 minutes of use.

Binary: `nova-sandbox v1.0.1-0.20261007135756-08d5d63f4651+dirty linux/amd64 go1.26.6 backend=landlock platform=linux`, built from this tree at 08d5d63f4651.

Scratch: the harness refuses paths outside the job directory, so the scratch tree
lives there. In the findings below the scratch root is spelled `<J>` and the binary
`<SB>`; `<J>/home` sat inside the first `--write` as the banner's first run demands,
`<J>/data` held test input file and a `#!/bin/sh` script. Every refusal and every
DENIED quoted here was printed by the tool.

What worked, in one paragraph: `check` (plain and `--json`), `version`, `help` and
every `<verb> -h` answer 0; `probe` passed 4/4; the bare wrap wrote inside the wall
and denied outside; `policy` printed the Landlock ruleset (plain and `--json`);
`worktree --help` showed all its flags. The wall enforced reads and writes as specified.

## Findings

1. Command as typed: `HOME=<J>/home <SB> --read-noexec <J>/data --write <J> -- /bin/sh -c "<J>/data/script.sh"`
   (a shell script under `--read-noexec`).

   What it printed (first 3 lines):

   ```
   SANDBOX NOTE landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights the newer abi added are not handled until the table grows
   SANDBOX OK backend=landlock abi=8 used=6 read=0 read-noexec=1 write=1 net=nopromise cwd=<J> cwdb64=… cmd=dash gpu=none deletes=<J>
   SANDBOX DONE exit=0 cmd=dash
   ```

   The script ran. Expected: the wall to deny execution — SPEC-SANDBOX.md rule 4,
   "`--read-noexec` is the same read grant with the execute taken back" — so the exec
   is denied (126, or a permission error) while reads under the same flag still work.
   Grade: **URGENT** — the flag's own help line says "NOT EXECUTABLE", the kernel here
   enforces Landlock ABI 8 (the EXECUTE right is there), and a tree a caller names as
   a cache or a data tree this user can write to is a tree the wall lets the job run.
   A wall property that is printed on the OK line and not enforced is a wrong result.

2. Command as typed: `<SB> check`

   What it printed:

   ```
   CHECK OK backend=landlock abi=8 net=enforceable hosts=none note=landlock abi 8 is above this tool's table: the wall is built at abi 6 (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows; backend at the running kernel's landlock LSM, abi 8
   ```

   Expected: the note to be clearer about what rights are missing, and to suggest
   when they might be added. Grade: **NEXT** — the warning is helpful but verbose and
   does not point to a fix or a timeline.

3. Command as typed: `<SB> policy --write <J> --read <J>/data --json`

   What it printed (first 3 lines of JSON):

   ```
   {"backend":"landlock","abi":8,"used":6,"net":"nopromise","read":["/usr","/bin",...],"write":["<J>","<J>/.nova-sandbox-tmp"],"gpu":"none"}
   ```

   Expected: the JSON to have a consistent key order and to include the same notes
   as the plain output. Grade: **NEXT** — cosmetic but affects machine-readable output.

4. Command as typed: `HOME=<J>/home <SB> --write <J> --read <J>/data -- /bin/sh -c "echo hello"`

   What it printed (first 2 lines):

   ```
   SANDBOX OK backend=landlock abi=8 used=6 read=1 read-noexec=0 write=1 net=nopromise cwd=<J> cwdb64=L3RtcC9ub3ZhLXNhbmRib3gtc2NyYXRjaA ancestors=12 cmd=dash gpu=none deletes=<J>
   hello
   ```

   Expected: one statement of the cwd, not two — `cwd=` and `cwdb64=` carry the
   same path twice, which makes the one line a caller is told to read strain both
   the eye and any column-limited log. Grade: **NEXT** — cosmetic.

## Scores

READ 8/10 — the banner, the verb helps and the spec are complete and remedy-bearing,
but the `--read-noexec` flag does not enforce its promise and the ABI warning is verbose.

USE 8/10 — every verb answered with either its result or a refusal that named the
remedy in one turn, and the wall held on writes, reads, and network; the one defect
is `--read-noexec`, the one flag whose promise a caller must not trust on Linux today.

Not exercised for lack of machine: `run` (darwin only), `reap` (darwin only),
`egress apply/check/drop` (require nftables on a bench).

urgent=1 next=3
