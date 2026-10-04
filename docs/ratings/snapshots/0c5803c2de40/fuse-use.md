# nova-fuse USE rating, current baseline 0c5803c2de40

Rater: qwen3.8-flash, a cold automated rater with no memory of this tool's code, its authors' reasoning or its earlier ratings
Build: 0c5803c2de40
Score: 8.5/10

## Reasons

Every command below ran in a job-local scratch box directory, with the box path written as `./name.json`; the rated source is 0c5803c2de406c1b0b2b0841f579c9bf73406b1c, built by `go build -o bin/ ./cmd/...` from a checkout whose HEAD is that SHA, and the binary's own `nova-fuse version` prints `nova-fuse v1.0.1-0.20261003234940-0c5803c2de40 linux/amd64 go1.26.6`, so the rated source is named by the thing rated. Nothing else was touched: no store on a network, no remote, no code-hosting tool, no database, no model, no key. Every verb is local, so no part of this tool went untried and there is no infrastructure-required remainder; nothing is judged from help alone.

What earns the 8.5:

- The advertised first run is the six lines under `example:` in `nova-fuse help`, run in order into a path with no box: exits 0, 0, 0, 0, 1, 0, each line reporting what the help said it would report, with no guess in the sitting.
- Two further small jobs ran in the same subset: five quarantines with `--max` ceilings and the `STATUS MORE` line, and a lockdown sitting whose `check` answers, `status` report and refused `lift lockdown` all matched the help.
- The four refusals each name the problem and the next command: missing flag (`nova-fuse check` alone explains why there is no default box path), unknown flag (`nova-fuse status --box ./box.json --colour red` lists the verb's flags), unknown verb (`nova-fuse defenestrate --box ./box.json` lists every verb), bad value (`nova-fuse status --box ./box.json --max -3` states the ceiling rule and what 0 means). They do not name every problem at once (finding 4).
- It fails closed wherever I could manufacture a state: no box, a box of not-JSON, a JSON scalar, a directory, a file with mode 0000, a blank surface, a missing `--box`, a `--box` value beginning with a dash, flags after positionals, a flag twice, one surface too many, `-h` after a verb: all exit 2, all say why, all point at a command.
- The gate is quotable: a blown `check` prints the exact `lift` command with the surface shell-quoted, correct even for a name holding a single quote (`'we'\''ird'`), and `--` genuinely delivers a surface named `-h`.
- Writes are temp-file plus rename and verified by re-read; in an eight-way and a twenty-four-way parallel batch of quarantines, the losers exit 1 instead of claiming success, a lockdown written among them survived, and a plain retry of a loser exits 0.

What keeps it below 9.5: `status` prints a surface name with its blank escaped and that printed name reads CLEAR through `check` (finding 1); a re-blow drops the recorded time and reason without a word (finding 2); and the normalisation that decides whether a blow covers the name you are about to check is stated nowhere a caller would look (finding 3).

A 10 needs: a reported name that is the callable name, a second blow that refuses or carries the record it replaces, the normalisation rule in `help check` and `help status` with the canonical spelling echoed, every bad invocation's problems listed in one pass, and a machine-readable read of the box for callers that parse instead of branching on exit codes. `--dry-run` is offered nowhere and a local write into a file named on the command line does not obviously need it; `--json` is the flag whose absence is felt (finding 5).

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse status --box ./b.json` then `nova-fuse check --box ./b.json -- 'spaced\x20name'` | status lists `quarantine=spaced\x20name`, and that listed name back to `check` exits 0 with `FUSE OK … surface=spaced\x20name`, a CLEAR for a blown surface, while `check … -- 'spaced name'` exits 1: the display escaping is not the stored spelling and nothing says so, so the natural copy from the report into the gate answers the wrong question | print the stored spelling (quoted) in status lines, and state in help that a listed name is the name to pass; better still, have `check` refuse a surface whose name needs escaping rather than read it as a new one | M |
| 2 | `nova-fuse quarantine --box ./box.json a-forum "a second, different reason"` | exits 0 quoting only the new line, and the box diff shows the earlier `at` and reason gone: a tool whose whole point is a recorded decision rewrites the record of an earlier decision and never says a fuse was already standing there | refuse the second blow with the standing line, or keep the superseded time and reason in the record and print `REPLACED` beside the new state | M |
| 3 | `nova-fuse check --box ./box.json a_forum` | exits 0 CLEAR while `a-forum` in the same box is blown: `help quarantine` says "Surface spellings match after normalization" and never says what normalization is (trim and case-fold, while a blank and an underscore stay significant), and `help check`, where the spelling decides the answer, does not mention it at all | state the rule in `help check` and `help status`, and have `check` echo the canonical spelling it looked up, not only the one it was handed | S |
| 4 | `nova-fuse status --box ./n.json --nope --max abc --max 9` | reports only `unknown flag --nope`: the unparseable `--max` and the doubled `--max` surface one per round trip, so an agent fixes and re-runs three times to learn one line was wrong in three ways | collect every flag and value problem in one invocation and print them as a list before the `run:` pointer | S |
| 5 | `nova-fuse status --box ./b.json --json` | `unknown flag --json; flags: --box, --max`: no verb offers `--json`, so a caller parsing status gets key runs plus free text after a colon, where a reason can hold anything and names arrive escaped as in finding 1, leaving exit codes as the only dependable machine surface | add `--json` to `status` and `check`: the box's stored names, each fuse's time and reason, the canonical lookup for the surface asked about, and the exit meaning; keep the exit code the contract | M |
| 6 | `nova-fuse quarantine --box ./c.json "surf-1" "trial 1"` run in an eight-way parallel batch on one box | `QUARANTINE FAILED surf-1: written but unverifiable (<nil>): do not trust it; stop reading that surface by hand and tell your person` prints a Go nil where the reason belongs, gives no `run:` pointer unlike every exit-2 line, and never says the remedy is a retry, which does work: the same command run alone exits 0 | print the real cause (the box changed under this write), end the line with `run: re-run this command on its own`, and take a lock on the box so parallel writers queue instead of racing | S |
| 7 | `nova-fuse quarantine --box ./n2.json a-forum $'first line\nsecond line'` | the box stores `"first line second line"`: the newline is flattened out of the record before it is written, and neither help nor the command's own line says the reason was edited | store the text as handed (JSON escapes it) and flatten only when printing one line | S |
| 8 | `nova-fuse check --box` and `nova-fuse lockdown --box ./ro/b.json "cannot write"` on a read-only directory | `flag needs an argument: -box` names a single-dash flag this tool does not accept, and the write failure prints `atomicfile: create temporary file for "ro/b.json"`, an internal package name, with no `run:` pointer at the end where every refusal at exit 2 has one | call the flag `--box` in these lines and end write-failure lines with the next command, as the invocation refusals do | S |

## Good, keep

- The fail-closed contract, stated and proved: no box, unreadable box, not-JSON box, a directory, a blank surface and a dash-shaped `--box` value all refuse rather than answer CLEAR, and the parallel-write losers exit 1 rather than claim a blow that did not land.
- `check`'s blown line carries the exact lift command, quoted correctly for a name containing a quote, so one line of output is enough to act on.
- `status` caps the listed lines but never the count (`quarantines=5` under `--max 2`, with `STATUS MORE kind=quarantine shown=2 total=5` naming the remedy), and `lockdown` over an unreadable box names the file where the old bytes are kept while saying the quarantines in them are not carried forward.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| a re-blow silently rewrites the fuse's time and reason (2026-10-02 at 1aac13259) | STILL THERE | `nova-fuse quarantine --box ./box.json a-forum "a second, different reason"` exits 0 printing only `QUARANTINE OK a-forum since=2026-10-04T00:28:06Z: a second, different reason`, and the box diff drops the earlier `"at": "2026-10-04T00:27:54Z"` and `"reason": "trial of a-forum"` |
| the unknown-option refusal omits the offending flag (2026-10-02 at 1aac13259) | FIXED | `nova-fuse status --box ./box.json --colour red` exits 2 with `nova-fuse status: unknown flag --colour; flags: --box, --max; run: nova-fuse help status` |
| three cold USE raters at 9 on 2026-10-01 recorded no way the gate itself misleads a caller | CHANGED | `nova-fuse check --box ./b.json -- 'spaced\x20name'` exits 0 CLEAR while the stored surface `spaced name` is blown (finding 1), and `nova-fuse check --box ./box.json a_forum` exits 0 while `a-forum` in the same box is blown (finding 3) |
