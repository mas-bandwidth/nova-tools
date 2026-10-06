# nova-check READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: ba3d867c0e31
READ: 7/10
USE: 7.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-check version` prints `nova-check v1.0.1-0.20261006133824-ba3d867c0e31 linux/amd64 go1.26.6`. Built and run on a Linux bench machine, in a scratch directory made for the trial; no live store, no forge read, no server.

## Reasons

READ. The banner is now one door: a one-line purpose, a "how it works" paragraph that says which verbs write (`spelling --write`, `dogfood record`, `convergence --state`) and that `--dry-run` stops both, a setup block, and three pasteable commands (cmd/nova-check/main.go:35). One ceiling flag, `--max`, with `--fail-max` kept as an alias and labelled as such on every verb's `-h` (cmd/nova-check/main.go:140). Every verb's `-h` prints its banner excerpt, an `effect:` line saying whether it writes, its flags and the exit codes. That is the shape an AI wants.

What keeps READ at 7. The private vocabulary is still on the first screen: attest is "did the full self load" (cmd/nova-check/main.go:48), floors checks "the door's floor set", and hygiene's identity finding says "the pool's identity". The quickstart OK line still points the newcomer at kernel, attest, floors and corpus, the four that need house files, and not at spelling (cmd/nova-check/main.go:515). The spec has drifted from the binary in two places: docs/SPEC.md:353 still says every listing takes `--fail-max`, and docs/SPEC-CHECK.md:1 and :13 title the convergence contract `nova-dev convergence` and quote a `help` line under that name. `nova-check dogfood -h` ends with a stray `nova-check dogfood record the finding`, which is the tail of convergence's usage text (cmd/nova-check/main.go:126) caught by the excerpt.

USE. The first run is the banner's: the two setup lines, then `nova-check quickstart --dir ./self` exits 0 with `QUICKSTART OK done=2 worst-exit=0`. Links on a two-file tree with one broken link exits 1 with `LINKS FAILED index.md:4: gone.md (does not exist)`, ignores a link inside inline code, and `--json` carries file, line, target and reason. Four broken links under `--max 2` print two, then `LINKS MORE kind=broken shown=2 total=4 --max <n> raises the ceiling`, then the count. Spelling caught `teh` and `recieve`, left a fenced `teh` alone, `--write --dry-run` changed nothing, `--write` fixed both, and a second run was green. attest, kernel, nocode (tree and `--staged`), corpus, floors, hygiene and all three dogfood sub-verbs ran from the help alone; hygiene found the out-of-path files and an AWS-key-shaped line without printing it; `dogfood record --dry-run` wrote no file and the real record wrote one; an unknown sub-verb and a misspelt verb both get a did-you-mean. Convergence was refused correctly with all five missing flags named, each with a hint; its forge read was not tried.

What keeps USE at 7.5. An empty directory is still a green for links, spelling and quickstart. `spelling --dir` skips `.txt` files silently, so a directory of prose with typos is `files=0` OK while `--file` on the same file fails. `spelling --write --dry-run` with two misspellings prints `SPELLING OK` and exits 0. Spelling columns are 0-based while lines are 1-based. `sentance` still passes. A refusal names one class of problem per run, and every refusal still sends the reader to the whole banner, not the verb's `-h`.

A 10 would refuse a scan that saw no files, report every problem in one invocation, point the door at `<verb> -h`, scan the same files with `--dir` as with `--file`, keep the spec and the binary on one name per flag and verb, and say in the help that spelling is a list of known typos.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-check links --dir ./empty` | Exit 0 with `LINKS OK files=0 links=0 excluded=0`; `spelling --dir ./empty` is `SPELLING OK files=0`; `quickstart --dir ./empty` prints `QUICKSTART OK` and only then a stderr `NOCODE NOTE classified NOTHING` (cmd/nova-check/main.go:836), after the verdict line. A wrong directory reads as a pass. | Exit 2, or 1, when the walk saw no files, and print any note before the verdict line. | S |
| 2 | `nova-check spelling --dir ./txt` | A directory holding only `a.txt` with `teh recieve` is `SPELLING OK files=0`; `spelling --file ./txt/a.txt` on the same file fails with both. The walk keeps only .md, .markdown and .mdown (internal/check/spelling.go:164), while the help says "markdown or prose". | Say in the usage line that `--dir` walks markdown only, or count skipped files in the OK line. | S |
| 3 | `nova-check spelling --file ./prose/note.md --write --dry-run` | Prints two `SPELLING FIX` lines, then `SPELLING OK files=1 misspellings=2 written=0 dry_run=true`, exit 0 (cmd/nova-check/spelling.go:127). The file still has two misspellings. | Print `SPELLING FAILED` and exit 1 on a dry run that would change something, as the run without `--write` does. | S |
| 4 | `nova-check spelling --file ./txt/a.txt` | Prints `txt/a.txt:1:0: teh -> the`: line 1-based, column 0-based (cmd/nova-check/spelling.go:143). Editors and `file:line:col` jumpers expect 1-based columns. | Add one to the column on every SPELLING line and in the JSON, or say 0-based in the help. | S |
| 5 | `nova-check links --fail-max -1` | Names only `--dir is required`, not the bad ceiling. `links --dir ./records --max -1 --nope` names only the unknown flag. `nova-check hygiene` names `--repo, --base and --head` (cmd/nova-check/hygiene.go:52), then `--identity` only on the next run (cmd/nova-check/hygiene.go:93); `hygiene -h` does not mark `--identity` required. | Collect every missing flag and bad value before refusing; mark `--identity` (required) in `-h`. | S |
| 6 | cmd/nova-check/main.go:237 | Every refusal ends `run: nova-check help`, the whole 8 KB banner, including the JSON `remedy` on a hygiene refusal. The verb's own `-h` is under 1 KB and is what the reader needs. | Make the door `run: nova-check <verb> -h` for a refusal after a verb. | S |
| 7 | `nova-check dogfood -h` | The excerpt ends with `nova-check dogfood record the finding`, a fragment of convergence's usage text (cmd/nova-check/main.go:126) matched because it begins with the verb's name. Every excerpt also drops the banner's indentation, so wrapped descriptions read as separate lines. | Excerpt by synopsis block, not by matching lines, and keep the indentation. | S |
| 8 | docs/SPEC.md:353 | Says every listing takes `--fail-max <n>`. The binary's flag is `--max`, and `--fail-max` is the alias kept for one release (cmd/nova-check/main.go:140). hygiene and dogfood ledger take `--max` but are not in the banner's `--max` list, and hygiene refuses `--fail-max`. | Rename the spec paragraph to `--max`, and list every verb that takes it in the banner. | S |
| 9 | docs/SPEC-CHECK.md:1 | Title and quoted help line (docs/SPEC-CHECK.md:13) say `nova-dev convergence`; the verb is `nova-check convergence`, and no help prints the quoted line. | Retitle to nova-check convergence and quote the banner as it prints. | S |
| 10 | `nova-check dogfood gate --cli cli.md --receipts ./rc` | With receipts for `run` and none for `stop`, exits 0 `DOGFOOD GATE OK verbs=2 by-nonauthor=1 ... require-all=no`. The banner says gate exits 1 with the verbs no non-author has run; only `--require-all` does that. | Name `--require-all` in the gate sentence of the banner, or make it the default. | S |
| 11 | cmd/nova-check/main.go:515 | The quickstart OK line's next= names kernel, attest, floors and corpus, all needing house files; spelling, which needs only the same directory, is not offered. | Put spelling first in next=. | S |
| 12 | `nova-check spelling --file ./prose/note.md` | After the fixes, the file still holds `sentance`, and the run is `SPELLING OK files=1 misspellings=0`; the help says "check markdown or prose for misspellings" (cmd/nova-check/main.go:131). | Say known typos in the usage line. | S |
| 13 | `nova-check spelling --dir ./prose --file ./prose/note.md` | `--file` is resolved under `--dir`, so this refuses with `prose/prose/note.md` not found. links documents that `--dir` is the resolution root; spelling's `-h` does not. | Say it in spelling's `--file` flag text, or resolve a path that exists as given. | S |

## Good, keep

An unknown flag now names every flag the verb takes (internal/nsprint/verbflag/verbflag.go:113), so the second turn needs no help read. The MORE line says how to raise the ceiling, and the count line prints the total either way. `spelling --write --dry-run` and `dogfood record --dry-run` exist and write nothing. Every `-h` carries an `effect:` line naming what it writes. hygiene's secret finding names the shape and the line and never prints the text. corpus's ABSENT line says which ledger row and what to do in the same commit.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a green over zero files (links, spelling) | STILL THERE | `nova-check links --dir ./empty` prints LINKS OK files=0 and exits 0; `nova-check quickstart --dir ./empty` prints QUICKSTART OK |
| two refusals name one problem | STILL THERE | `nova-check links --fail-max -1` names only --dir; bare `nova-check hygiene` names --identity only on the second run |
| every remedy is the whole help | STILL THERE | every REFUSED line ends `run: nova-check help` (cmd/nova-check/main.go:237) |
| no dry run, including on spelling --write | FIXED | `spelling --write --dry-run` printed SPELLING FIX lines and left the file unchanged; the banner says --dry-run stops both writes (cmd/nova-check/main.go:35) |
| two cap flags (--fail-max and hygiene's --max) | CHANGED | --max is the one name and --fail-max is a labelled alias; docs/SPEC.md:353 still says --fail-max, and hygiene refuses --fail-max |
| quickstart rejects --json | STILL THERE | `nova-check quickstart --dir ./self --json` is an unknown flag; the banner says quickstart uses typed lines (cmd/nova-check/main.go:138) |
| spelling passes `sentance` | STILL THERE | the usage line still says misspellings (cmd/nova-check/main.go:131) |
| dogfood gate exits 0 with verbs no non-author ran | STILL THERE | gate without --require-all is OK with `stop` never run |
| a private vocabulary on the first screen | STILL THERE | cmd/nova-check/main.go:48 says did the full self load |
| quickstart next= points at house checks only | STILL THERE | cmd/nova-check/main.go:515 |
