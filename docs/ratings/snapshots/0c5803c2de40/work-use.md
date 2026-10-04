# nova-work USE rating, current baseline 0c5803c2de40

Rater: DeepSeek V4.1 Flash (deepseek-v4.1-flash)
Build: 0c5803c2de40
Score: 7/10

## Reasons

Cold USE rating of nova-work at source snapshot 0c5803c2de406c1b0b2b0841f579c9bf73406b1c, built only from that snapshot (exported with git archive into job-local scratch, then `go build -o $JOB/bin/ ./cmd/...`, exit 0). The staged checkout HEAD is 9d7802ac5b52328b06c4d56f79fe1cac7a12cec3, which is not the rated source; the full SHA above is the only revision this score describes. Build provenance is weak: the binary's own line reads `nova-work devel linux/amd64 go1.26.6`, naming no revision (finding 4), so provenance here rests on the build command, not the binary. Everything below ran in $JOB/scratch with only job-local files and a fake transport passed through `--gh`, no network and no credentials; commands are shown relative to $JOB/scratch.

The tool is small and honest. `nova-work help` answers what it does, how it works and its first run; `help <verb>` and `<verb> -h` list every flag with its want; refusals name every independent problem at once and exit 2; exit codes are truthful (verify no difference 0, differences 1, could-not-run 2); import's own round trip encodes, reads back and compares the tree, printing bytes and sha256; and verify reports each difference as one DRIFT line with path, field, want and got. With a fake `gh` I ran import --dry-run, a real import writing a tree, an equal verify (differences=0, exit 0) and a changed verify (one DRIFT, exit 1) end to end.

The advertised first run cannot run cold: it needs a logged-in forge CLI and a real org (finding 1), and the help documents `--gh` only as "the GitHub CLI (default gh on PATH)", not as a seam for a recorded or fake transport, so a cold reader must reverse-engineer the GraphQL calls to exercise the tool offline. The verb that carries the comparison, verify, has no `--json` (finding 2), and the tree's value shapes appear nowhere in the help (finding 3). A 10 needs: a shipped recorded transport documented in help, `--json` on import and verify of the one result value, the tree shape in help, a revision-stamped version line, verb-specific refusal remedies, and a missing-tree refusal that points at import.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-work import --org example --repo example/thing --dry-run --gh ./fake` | the advertised first run needs a logged-in forge CLI and network; `--gh` is documented only as the CLI path, not as a recorded or fake transport, so cold offline use means guessing the GraphQL plan, org and issue-page queries before any local job can run | document `--gh` as a transport seam and ship a recorded fixture; state that `--dry-run` still makes the live reads | M |
| 2 | `nova-work verify --tree ./tree.lisp --json --gh ./fake` | verify, the verb that reports differences, refuses `--json` (`unknown flag --json`); only version takes `--json`, while the banner sentence "Every verb but import, verify takes --json" is ambiguous; an AI cannot parse the comparison structurally | give import and verify `--json` of the one result value, or state the exception and the backslash-x20 escaping in help | M |
| 3 | `nova-work help` | the tree's value shapes are undocumented: field names, state enums, `:origin`, node-id and fetched timestamp appear only after a successful import; there is no `help tree` or format section | add the `(work-tree ...)` shape to help or a `nova-work help tree` | M |
| 4 | `nova-work version` | prints `nova-work devel linux/amd64 go1.26.6`, naming no revision, so the binary cannot be tied to a snapshot; other tools carry the twelve-hex | stamp the build with the commit | S |
| 5 | `nova-work import` | missing or bad flags end `run: nova-work help`, the whole banner, while an unknown flag ends `run: nova-work import -h`; recovery is uneven | point every verb refusal at `nova-work help <verb>` | S |
| 6 | `nova-work verify --tree ./no-such.lisp --gh ./fake` | a missing tree yields a raw `stat ... no such file or directory` and remedy `nova-work verify -h`; the useful next command is import | name the tree and suggest `nova-work import` | S |

## Good, keep

With a fake transport, import --dry-run, a real import, an equal verify and a changed verify all ran offline end to end, and import's round trip printed bytes and sha256 of the tree it wrote. Refusals name every independent problem in one run (`nova-work import` names both --org and --out; multi-bad values print both lines) and exit codes tell the truth (0 equal, 1 differences, 2 could-not-run). Every verb answers `-h` and `help <verb>` with each flag's want, and `--gh` echoes the path it found.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| import cannot be tried without a forge login | STILL THERE | help says "first run: needs gh logged in"; import and verify only ran here through a fake passed to `--gh` |
| value shapes undocumented | STILL THERE | `nova-work help` lists flags only; the `(work-tree ...)` shape first appears after `nova-work import --out ./tree.lisp` |
| tree errors one per run | STILL THERE | `nova-work verify --tree ./tree-bad.lisp` reports only the unknown `:bogus` key, not the `:title 123` type error in the same file |
| fake import and offline drift verification work | CHANGED | confirmed: import wrote a tree (bytes=701 sha256=226e0e4b...), equal verify printed differences=0 exit 0, changed verify printed one DRIFT exit 1 |
