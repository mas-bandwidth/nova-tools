# nova-self-talk USE rating, nova-tools 1.1.0

Rater: Grok
Build: 86a5fb91119e
Score: 8.5/10

## Reasons

Help answers the three questions in the first lines, names the effect of each verb, and the example block runs as printed. `nova-self-talk example --dry-run ./pages` writes nothing and prints the next command. `nova-self-talk example ./pages` writes the two pages. `nova-self-talk pages/journal.md` exits 1 with two findings, a dated count, and the note that a green clears known shapes only. That is the first successful run, and the banner says exit 1 is the tool working before the run.

The first real job is a page with one standing sentence, one dated sentence, and an aspiration. `nova-self-talk --json own-note.md` returns one item with file, line, match, and text. Dating that sentence and running `nova-self-talk own-note.md` then prints SELFTALK OK and DATED n=2, exit 0. The second job puts a finds sentence and a passes sentence from `nova-self-talk shapes` on another page. The scan reports the two finds and not the two passes.

Four refusals each name the problem and the next command. `nova-self-talk --max -1` with no files prints both the bad ceiling and the missing files, then one hint. `--json` and `--dry-run` match the help: one object, and a dry run that leaves no directory. No verb needs a service. scan, shapes, example, version, and help all run, including stdin. The one guess is whether status failed means stop. The banner's last lines and the JSON note say it does not.

A 10 would not call an advisory finding failed, would not print OK over a non-text file, and would not lead a missing flag with the flag package's own sentence.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-self-talk --json own-note.md` | status is failed and exit is 1 for one sentence the banner calls never a failure. A caller that branches on status stops instead of dating, cutting, or keeping the line. | Use a status that means findings, or add a fact that exit 1 here is the advisory result, and keep the counts. | S |
| 2 | `nova-self-talk use/binary.bin` | A file of non-text bytes exits 0 and prints SELFTALK OK files=1. The note says a green is partial, and it does not say the file is not text, so OK reads as a clean page. | Refuse a file that is not text, or print a line that names it as not scanned as prose. | S |
| 3 | `nova-self-talk --skip` | The first line is the flag package's sentence "flag needs an argument: -skip", with one dash, and the useful sentence is a second hint. | One refusal line that says --skip needs a basename and shows --skip RULES.md. | S |

## Good, keep

The example verb is the whole first run. A dry run writes nothing, the real run names the next command, and that command prints the findings the banner promised.

A repair is visible. Dating the one standing sentence turns exit 1 into SELFTALK OK with the dated count, and the aspiration line stays quiet.

Refusals stack. A bad --max and two missing files are three lines in one run, each with the same run door, and --json carries the same why list.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| raw Go flag errors | STILL THERE | nova-self-talk --skip prints flag needs an argument: -skip before the basename hint |
| a green over a binary file | STILL THERE | nova-self-talk use/binary.bin exits 0 and prints SELFTALK OK files=1 claims=0 |
| the unknown-option refusal omits the offending flag | FIXED | nova-self-talk --bogus own-note.md prints unknown flag -bogus and lists --skip, --rule-doc, --max, --json |
| cold scores 4, 5, and 8 | CHANGED | example, a dated repair, and a shapes trial all complete from help; this use scores 8.5 |
