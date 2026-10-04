# nova-self-talk READ rating, nova-tools 1.1.0

Rater: Grok
Build: 86a5fb91119e
Score: 8/10
README: 7/10

## Reasons

The README row at README.md:35 says the tool flags sentences where a writer passes a standing verdict on themselves, and that sentence is line 1 of the banner. The first confusion is README.md:35, where the same cell calls the findings advisory and says they produce exit 1, with no sentence that exit 1 is the finding rather than a broken run. The first boredom is README.md:47, the release-install block that restates the table after every row already gave a first command. The first doubt is that same README.md:35 claim that a command can tell a standing verdict; the cell gives no specimen. The command reference and the spec then earn the claim: two classes, a dated record counted and not quoted, a banner on a caller-named rule document, and a note on every run that a green clears known shapes only.

The code does that job. The scan names every file, skips nothing by default, caps the two classes apart, and renders one report as lines or as one JSON object. The shapes table is the same table the tests run, so the help cannot claim a shape the scan misses. The names a stranger needs, standing and dated and installation and the four shapes, are glossed in the banner before the example. Comments say why. The opening of the banner meets the five-line how-it-works rule, and the shape list in that banner is what the verb tests hold to the scan, so those lines earn their keep.

A 10 would make the front door say what the banner's closing lines already say, so an automated caller does not edit prose until exit 1 becomes exit 0. It would make the first-class package header describe the command that ships: two scanners, counts, no score. It would name, in the first-run section, the example line the scan passes on purpose. The same argument sits in the banner, the command reference, the spec, and the package headers, and those copies already disagree. A 10 would keep one normative copy.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:35 | The cell says findings are advisory and produce exit 1. The wire word is FAIL, and the JSON status for a finding is failed. An automated caller treats that as a broken run and edits the prose until the exit goes green, which is the failure this tool exists to prevent. | In that cell, say exit 1 means sentences to date, cut, relocate, or keep, and show the example verb before the page rather than only a checkout path. | S |
| 2 | internal/selftalk/selftalk.go:22 | The package header says the two classes cannot be one tool, and the next paragraph says a falling score means the input got better. The command is one tool with both scanners, and it prints counts, never a score. | State two scanners in one command, and say a falling finding count is the input changing. | S |
| 3 | cmd/nova-self-talk/testdata/example-pages/RULES.md:7 | The worked rule page says it cannot accept an instruction, and the first-run transcript never mentions that line. The closed cannot-verb list passes that verb. A reader comparing the page with the transcript cannot tell a miss from a licence. | In the first-run section, name this line and say the scan passes it because that verb is outside the closed list. | S |
| 4 | internal/selftalk/installation.go:670 | The comment says the binary derives its exit code from this helper. The scan uses its own counters and never calls the helper. | Delete the helper, or point the comment at the scan counters. | S |

## Good, keep

The note on every completed run says a green clears the known shapes, never the file. That sentence is the tool's honesty, and it is in the banner, the command reference, and the output.

One detector table feeds the shapes verb and the test that runs each row's finds sentence and its near miss. The listing cannot drift from the scan.

A dated claim is a count, not a quoted sentence, and a rule document is scanned only when the caller names its basename, under a banner that forbids softening a rule to clear a finding.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| off the skeleton | FIXED | cmd/nova-self-talk/main.go:27 imports the shared tool package, and main.go:182 prints the shared refusal line with a run door |
| an 88-line banner | STILL THERE | cmd/nova-self-talk/main.go:31 opens a help string that still runs to main.go:113, about 83 lines |
| comments in capitals | STILL THERE | internal/selftalk/installation.go:3 and internal/selftalk/selftalk.go:5 still shout the why in capitals |
| a class name nobody glosses | FIXED | cmd/nova-self-talk/main.go:61 glosses installation as a standing self-verdict built from neutral words |
| jargon and a semantic overstatement | CHANGED | main.go:55 glosses the shape words, and internal/selftalk/selftalk.go:25 still says a falling score where the command prints counts |
| README scored 6.5 to 7, and 8.4 | CHANGED | README.md:35 still pairs advisory with exit 1; this read scores that page 7 |
