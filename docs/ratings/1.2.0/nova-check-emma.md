# nova-check READ and USE rating, nova-tools 1.2.0

Rater: Zhi (deepseek/deepseek-v4.1-flash) in the DeepSeek Harness (dsh), a sprint worker on a re-rate card carrying attempt 1's reading onto this head
Build: a9fe2a7fd927
READ: 7/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the
head of the base branch. `nova-check version` prints
`nova-check v1.0.1-0.20261007223734-a9fe2a7fd927 linux/amd64 go1.26.6`. Every verb
was read cold from `nova-check help` and `nova-check <verb> -h`, and then run for
real on a Linux bench in a scratch directory made for the trial: a hand-made self
tree, a throwaway git repository for hygiene, a stand-in `gh` script for
convergence, and a receipts directory for dogfood, so nothing reached a live
store, a server or the network.

## Reasons

READ. The banner answers its three questions in its first lines, gives a pasteable
setup and example, names the one cap flag, and states the exit codes once. Every
verb answers `-h` with its usage line quoted from the banner, its flags with what
each wants, an `effect:` line and the exit codes. A missing flag is refused, never
guessed, and every independent problem is named in one run: a kernel call that
gives both denominations prints both refusals. An unknown flag now names every
bad flag and points at `nova-check <verb> -h`, which closes the 1.1.0 complaint
that only the first unknown flag was named and the door was the whole help. The
refusals are the best part of the tool: each names the flag, its unit and why it
is never guessed, and convergence names all five missing flags in one run.

What keeps READ at 7. The spec has fallen behind the binary where a reader checks
first. docs/SPEC-CHECK.md is titled and specified as `nova-dev convergence` and
line 13 says help prints that line byte for byte; the binary is `nova-check convergence`. docs/SPEC.md still says every listing takes `--fail-max` (:353),
states the refusal grammar as `nova-check <verb>: <what was wrong>` (:344) where
the binary prints `nova-check <verb> REFUSED: ...`, prints `LINKS FAIL` (:96,
:458) where the binary prints `LINKS FAILED`, and counts "Ten record-layer
checks" (:317) beside a verb list of eleven. README.md:86 still says these are
the 1.0.0 commands and installs `@v1.0.0` while this tree is the 1.2.0 release
candidate. The banner's usage lines omit flags the verbs accept: convergence's
`--gh`, `--git`, `--now` and `--timeout`, and the dogfood lines' `--git-timeout`
and `--tools-timeout`. The `-h` excerpt is quoted by
matching the verb's leading words, so it reflows the banner's right-hand column
to the left margin and carries wrapped continuations cut mid-sentence
(convergence's excerpt ends `... or nova-check dogfood record the`), while the
shared exit table carries the banner's `setup:` fixture block into `version -h`
and the `dogfood` group help, where no such self tree is set up; each
`dogfood <verb> -h` page quotes its own table and does not carry it, while
`dogfood ledger`'s `--authors` prints its value type as
`<<tool> <verb> = <who wrote it>>` where repeatable flags print `<value>`.

USE. The first run is clean. `quickstart` runs links then nocode, runs both after
the first says no, and names the next command. A broken link is named with its
file, line, target and reason, and a `MORE` line carries a pasteable rerun.
`hygiene` found all four mechanical findings on a seeded branch: a wrong
identity, two out-of-path files, a stray `RESULT.md`, and a forge-token shape
that it refused to print. `convergence` read seven streams against a stand-in
forge and a ledger, marked the sources it was not given as `absent` rather than
zero, and wrote its streak to `--state` only. `dogfood` accepted a receipt,
refused an undeclared verb with a nearest suggestion, and its `--dry-run` wrote
nothing. `spelling` fixed five misspellings in place, and `--json` on the record
verbs carries the same value as the lines.

What keeps USE at 7. Some numbers are wrong and some greens are over nothing.
convergence counts a ledger table's header as an open row unless its last cell is
the word `result` (internal/converge/sources.go:394): a `| row | status |` header
over two PASS rows reads `LEDGER now=1 rows=3 open=1`, so the stream can never
reach zero. A second consecutive widening tick exits 1 with the same
`CONVERGENCE WARN ... widening=EDGES` line the exit-0 tick printed
(internal/converge/converge.go:221); nothing on the line says the streak reached
two. `convergence --dry-run` still reads the forge through `gh` and refuses
offline, so an offline reader cannot plan. `spelling --write --dry-run` over five
misspellings prints `SPELLING OK ... dry_run=true` and exits 0
(cmd/nova-check/spelling.go:139). `links` over an empty directory and `spelling`
over a glob that matches nothing are green with `files=0`. `links --exclude ./self/docs` excludes nothing and says `excluded=0`. A ledger with no table
reads `LEDGER now=0 rows=0 open=0 trend=flat`, a converged ledger over nothing.
On a failure every line, the count line included, goes to stderr, so
`2>/dev/null` shows nothing at all while the exit is 1. A named file that does
not exist is a finding at exit 1 to `links` and a refusal at exit 2 to
`spelling`. `dogfood gate` calls an empty receipt set a refusal in its help but
prints `DOGFOOD-GATE FAILED` at exit 1. A `--file` that already carries the
`--dir` prefix is joined to `--dir` a second time, so a file that exists is
reported `unreadable`. `quickstart` and the three `dogfood` verbs refuse `--json`,
against the standard that every verb accepts it, and `version --json` leaves
`facts` empty with the build as one `payload` string.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/converge/sources.go:394 | The LEDGER stream counts a table header as an open row unless its last cell is `result`; a `| row | status |` header over two PASS rows reads `rows=3 open=1`, so the stream can never reach zero. | Recognise the header as the line above the separator row, as corpus already does. | S |
| 2 | internal/converge/converge.go:221 | A second consecutive widening tick exits 1 with the same `CONVERGENCE WARN` line as the exit-0 tick; nothing says the streak reached two. | Print `CONVERGENCE FAILED ... streak=2` on the exit-1 tick. | S |
| 3 | `nova-check convergence ... --dry-run` | A dry run still makes the forge read: with a stand-in `gh` that fails it refuses the whole reading at exit 2 (`gh pr list --state open: exit status 1`) instead of printing a plan, although the effect line already says LANDING and PRS read the forge through `gh` over the network and `--dry-run` writes none. | Mark LANDING and PRS absent in a dry run so the plan prints offline. | M |
| 4 | cmd/nova-check/spelling.go:139 | `spelling --write --dry-run` over five misspellings prints `SPELLING OK ... dry_run=true` and exits 0. | Exit 1 with a `SPELLING FAILED` line when a dry run found misspellings. | S |
| 5 | cmd/nova-check/dogfood.go:369 | An empty receipt set prints `DOGFOOD-GATE FAILED` at exit 1, while the gate's own help calls it a refusal and the verb's findings are `DOGFOOD GATE FAIL`. | Print `DOGFOOD GATE REFUSED ...; run: nova-check dogfood gate --allow-empty` at exit 2, or reword the help. | S |
| 6 | cmd/nova-check/main.go:57 | The `setup:` fixture block is appended to the tool's exit table, so `version -h` (the one verb with no exit table of its own) prints a `mkdir`/`printf` setup for a self tree it never uses; the `dogfood` group help prints it too, while each `dogfood <verb> -h` page quotes its own table and does not. | Keep the setup block in the banner only. | S |
| 7 | pkg/nsprint/verbflag/verbflag.go:509 | The `-h` excerpt is quoted by the verb's leading words, so it reflows the banner's description column and carries wrapped continuations cut mid-sentence. | Quote the verb's usage block by its indentation or blank-line bound, or excerpt whole sentences. | S |
| 8 | `nova-check links --dir ./self --file ./self/README.md` | A `--file` that already carries the `--dir` prefix is joined to `--dir` twice, so a file that exists is reported `unreadable`. | State the join in `-h`, and print the path tried. | S |
| 9 | `nova-check links --dir ./empty`, `nova-check spelling --path 'nothing*.md'` | Both are green over zero files (`files=0`), and spelling's `MORE` line is prose rather than a pasteable rerun. | Print the NOTE line nocode prints, or exit 1 under a flag. | S |
| 10 | `nova-check links --dir ./self 2>/dev/null` | On failure every line, the count line included, goes to stderr, so a stdout reader sees nothing while the exit is 1. | Put the count line on stdout, and state the stream contract in the banner. | S |
| 11 | `nova-check links --dir ./self --exclude ./self/docs` | The prefix is relative to `--dir`, so the path the caller sees excludes nothing and the line says `excluded=0`. | Accept a prefix that starts with `--dir`, or refuse a prefix that matches nothing. | S |
| 12 | `nova-check convergence --ledger <file with no table>` | LEDGER reads `now=0 rows=0 open=0 trend=flat`, a converged ledger over nothing. | Mark the stream absent, or refuse, when the ledger holds no table. | S |
| 13 | `nova-check links --dir <dir> --file nope.md` and `nova-check spelling --file nope.md` | A named file that does not exist is a finding at exit 1 to links and a refusal at exit 2 to spelling. | Pick one rule for a named file that does not exist and use it in both. | S |
| 14 | `nova-check version --json` | `facts` is empty and the build is one `payload` string. | Put version, os, arch and go in `facts`. | S |
| 15 | `nova-check quickstart --json`, `nova-check dogfood ledger --json` | Both are refused; the banner excepts quickstart and dogfood from `--json`, against the standard that every verb accepts it. | Render quickstart and dogfood through the one output value, as the other verbs do. | M |
| 16 | `nova-check dogfood ledger -h` | `--authors` prints its type as `<<tool> <verb> = <who wrote it>>`; repeatable flags print `<value>`. | Give each flag a backticked value word. | S |
| 17 | `nova-check` banner usage lines | convergence's omit `--gh`, `--git`, `--now` and `--timeout`; the dogfood lines omit `--git-timeout` and `--tools-timeout`, and none names the old `--fail-max` spelling. | Generate the usage lines from the flag sets, or add a `[flags: -h]` line. | S |
| 18 | docs/SPEC-CHECK.md:1 | The file is titled and specified as `nova-dev convergence`, and line 13 says help prints that line byte for byte; the binary is `nova-check convergence`. | Rename to the verb the binary carries. | S |
| 19 | docs/SPEC.md:317 | "Ten record-layer checks" beside a verb list of eleven. | Count eleven, or name quickstart as a runner of two. | S |
| 20 | docs/SPEC.md:344 | Refusal grammar `nova-check <verb>: <what was wrong>`; the binary prints `nova-check <verb> REFUSED: ...`. | Make the grammar the printed line. | S |
| 21 | docs/SPEC.md:353 | "Every listing here takes `--fail-max <n>`"; the binary's flag is `--max`, with `--fail-max` an old spelling. | Name `--max`, and the old spelling once. | S |
| 22 | docs/SPEC.md:96 | `LINKS FAIL` here and at docs/SPEC.md:458; the binary prints `LINKS FAILED`. | Make the grammar the printed line across the section. | S |
| 23 | README.md:86 | "These are the Nova Tools 1.0.0 commands" and it installs `@v1.0.0` while this tree is the 1.2.0 release candidate. | Point at the release this tree is, or drop the version sentence. | S |
| 24 | `nova-check notaverb` | The refusal is `CHECK REFUSED: unknown verb ...`, not the banner's `nova-check <verb>` shape, so the tool's own name is missing. | Print `nova-check REFUSED: unknown verb ...`. | S |
| 25 | cmd/nova-check/spelling.go:199 | With `--file`, the JSON `facts.dir` is the process working directory, an input the caller never named and a leak of where the run happened. | Omit `dir` in `--file` mode, or set it to the file's directory. | S |
| 26 | cmd/nova-check/main.go:1 | One binary still carries self-repo record checks, a branch gate, a receipt ledger and a forge reading, so a reader learns four vocabularies to use one verb. | Split hygiene, dogfood and convergence out, or name the family honestly in the banner and README row. | L |

## Good, keep

- The refusal constants turn every missing flag into one line that names the flag,
  its unit and why it is never guessed, and one run names every independent
  problem (kernel with both denominations prints both refusals).
- `hygiene` never prints the secret it found, and its `MORE` line is a command a
  reader can paste, caps and all.
- `convergence` marks a stream it was not given as `absent` with the flag that
  would have fed it, and never as a zero; `--dry-run` writes no state.
- `nocode` prints a NOTE for the empty case that `links` and `spelling` pass
  green, so the shape to copy for finding 9 is already in the binary.
- `--max` is the one cap flag across the listings, with `--fail-max` kept as a
  named old spelling.
- Every verb's `-h` ends in an `effect:` line that says which kind of act it is,
  and every verb's help is on stdout at exit 0.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| only the first unknown flag named (docs/ratings/1.1.0/check-use.md) | FIXED | `nova-check links --bogus --alsobad` prints both refusals, each listing the verb's own flags |
| refusal points at the whole help (docs/ratings/1.1.0/check-use.md) | CHANGED | an unknown flag says `run: nova-check links -h`; a missing required flag still says `run: nova-check help` |
| green over zero files (docs/ratings/1.1.0/check-use.md) | STILL THERE | `nova-check links --dir ./empty` prints `LINKS OK files=0 links=0`, exit 0 |
| `--exclude` relative to `--dir`, silently (docs/ratings/1.1.0/check-use.md) | STILL THERE | `--exclude ./self/docs` gives `excluded=0`; `--exclude docs` excludes the tree |
| `convergence --dry-run` still calls `gh` (docs/ratings/1.1.0/check-use.md) | STILL THERE | with a failing stand-in `gh` it refuses `gh pr list --state open: exit status 1` at exit 2 |
| failure findings only on stderr (docs/ratings/1.1.0/check-use.md) | STILL THERE | `nova-check links --dir ./self 2>/dev/null` prints nothing, exit 1 |
| two names for one cap flag (docs/ratings/1.1.0/check-read.md) | FIXED | `--max` on every listing, hygiene included; `--fail-max` is an accepted old spelling |
| README says 1.0.0 (docs/ratings/1.1.0/check-read.md) | STILL THERE | README.md:86 |
| one binary, several jobs (docs/ratings/1.1.0/check-read.md) | STILL THERE | the verb list still holds hygiene, dogfood and convergence |
| the earlier 1.2.0 check ratings (docs/ratings/1.2.0/nova-check-*.md) | NOT RE-READ | this run rates the head independently; every finding above comes from a live run at this head, and the places the earlier ratings name that this run also names agree with them |
