# nova-bus READ rating, current baseline 0c5803c2de40

Rater: qwen3.8-flash, an AI worker on the flash tier, reading cold, running nothing
Build: 0c5803c2de40
Score: 7.5/10
README: 7.5/10

## Reasons

The rated source is the full SHA 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; the staged
checkout's HEAD is that same commit, so every file:line below is read at the head. This is
a cold READ: no binary run, no test run, no memory of this tool's code or of anyone's
earlier reading of it.

The README's sentence for the tool is "notes between AIs, over a git repository"
(README.md:24) and the banner's line 1 is that same sentence (cmd/nova-bus/main.go:61), so
the two agree. The idea is worth adopting: a bus is a git repository, one lane directory per
sender, one markdown file per note, and ten verbs over it (docs/CLI.md:420). Nothing about
it needs a store, a server or a network identity, and a stranger can build one in a scratch
directory with git alone (cmd/nova-bus/main.go:270-277). The writing does what the standard
asks: docs/CLI.md:636-640 states the cursor's price out loud ("You are asked twice; you are
never told a note is answered when it is not"), docs/SPEC.md:2829-2842 maps each failure to
the verb that closes it, and docs/CLI.md:670-672 names the rule the tool deliberately does
not enforce. The first run even shows a refusal (docs/CLI.md:461-462) instead of only the
happy path, which is the most honest thing a command reference can do.

Where I first got confused: README.md:24. Its own first command reads
`nova-bus inbox --bus ./trial-bus --as Ada`, but ./trial-bus is only made 45 lines later at
README.md:64-69, and the cell never says that `--as Ada` means anything only because
participants.json says so. I understood what a bus is after docs/CLI.md:420, not after the
table. Where I first got bored: README.md:46-59, six paragraphs about release versions,
`go install` and trial-directory naming that repeat for every tool, standing between the
table's promise and the bus's own four setup lines. Where I first doubted a claim:
README.md:102, "tested transcripts, which the tests execute line by line" — for the bus the
page says the tests "execute that block as written" (docs/CLI.md:441), and they do, but only
inside a build tag (cmd/nova-bus/firstrun_functional_test.go:1), while the same five lines
are carried as not executed on the default path
(internal/ci/testdata/unexecuted_examples.txt:15). The claim is true in the container and
not true on the unit path, and the page says neither.

What earns the score: the model is small and stated in three lines; the entry point, the
ten verbs and the data are findable in a minute of docs/CLI.md; refusal lines name what the
input wants and hand over the command that fixes it
(cmd/nova-bus/main.go:1458, cmd/nova-bus/main.go:538); output is bounded and keeps its
totals (cmd/nova-bus/main.go:133-138); the write verbs have dry runs; and the read-cost
promise is a number a test asserts rather than a hope, counted inside the code
(internal/bus/instrument.go:49). 513 tests across the two packages, named as rules
(cmd/nova-bus/broadcast_functional_test.go:15 "TestSendToAllExpandsToExactlyTheParticipants"),
teach the contract instead of covering lines.

What costs it: one 466-line function in the middle of the read path
(cmd/nova-bus/main.go:1618-2083); no `--json` on any verb while the family rule says every
verb takes it (docs/SPEC.md:138), so an AI that parses objects elsewhere must regex the
bus; two specs that call shipped code unimplemented (docs/SPEC-BUS.md:7,
docs/SPEC-BUS-DELIVERY.md:3) and a normative verb list missing two shipped verbs
(docs/SPEC.md:2846); `REFUSED` printed at exit 1 (cmd/nova-bus/main.go:1658) where
docs/SPEC.md:145 puts refused at 2; a 13-positional-parameter helper called
positionally at three sites (cmd/nova-bus/main.go:2171); past-tense war stories in shipped comments
(cmd/nova-bus/main.go:1400, cmd/nova-bus/main.go:632, cmd/nova-bus/main.go:720) against the
present-tense rule; and weight the tree itself books as debt
(internal/ci/testdata/dead_code_allowlist.txt:14).

A 10 for this score: inboxListing split into cursor, listing, bodies page and advance;
`--json` on every verb from the same value the lines come from; the three spec status lines
and the verb block brought back in line with the binary; one word per exit class; the
options structs the tool already uses in cmd/nova-bus/main.go:1468 carried to its helpers;
comments rewritten as the rule they prove; and the generality debt in this tool's docs and
comments spent down. A 10 README: the bus's row gives its own setup three lines after the
command instead of forty, and the transcript claim says which path proves it.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-bus/main.go:1618 | inboxListing runs 1618-2083, 466 lines: roster resolve, cursor read, four cursor refusals, the bounded walk, the open list, the bodies page and the advance, all in one function, so a reader of any one part holds the other six in mind | split into readCursorAndRefuse, listSince, pageBodies and advanceCursor, each returning a value its caller prints | L |
| 2 | cmd/nova-bus/main.go:1362 | no verb defines `--json`: inbox, wait, send, check and names print lines only, while docs/SPEC.md:138 says every verb takes it; a harness that reads objects from the other tools must parse this one's text, and the one place JSON does appear (prepare, cmd/nova-bus/main.go:751) is an artifact file, not a rendering of the same value | build each verb's one result value and render it as lines or JSON from it, with bodies carried as their own bounded field | L |
| 3 | docs/SPEC-BUS.md:7 | the status line reads "specified, not implemented; no code" and docs/SPEC-BUS.md:184 lists the verb group as absent, yet the flag ships (cmd/nova-bus/main.go:2659), its frame ships (cmd/nova-bus/main.go:3353) and a test wakes on it (cmd/nova-bus/wait_on_note_empty_tick_functional_test.go:106); a reader who trusts the spec concludes the verb does not exist | mark the shipped half implemented, keep the unshipped half named, and let a test diff the spec's verb list against the binary's flags | M |
| 4 | cmd/nova-bus/main.go:93 | the banner's wait usage lines list every other flag of the verb but `--on-note`, and docs/CLI.md never names it either, so the shipped wake mode is undiscoverable from help alone | add the flag to the wait usage block and one paragraph to the wait section of docs/CLI.md | S |
| 5 | docs/SPEC-BUS-DELIVERY.md:3 | "not implemented" over prepare and send --prepared, which ship at cmd/nova-bus/main.go:751 and cmd/nova-bus/main.go:815 with their own artifact validation (internal/bus/prepared.go:109) | correct the status line to what is shipped and name the recovery half that is not | S |
| 6 | docs/SPEC.md:2846 | the normative verb block lists eight verbs; reply (cmd/nova-bus/verb_reply.go:19) and prepare are shipped, and draft's `--out` is missing too, so the document docs/CLI.md:676 sends a reader to for "the output grammar in full" is behind the binary | add the two verbs and the flag, and hold the block against the flags the verbs define | M |
| 7 | cmd/nova-bus/main.go:1658 | an unusable cursor prints `INBOX REFUSED` and exits 1, as at cmd/nova-bus/main.go:1685, cmd/nova-bus/main.go:1694 and cmd/nova-bus/main.go:907, while docs/SPEC.md:145 puts refused at 2; the word now means both "fix your call" and "the bus said no", so a scanner cannot branch on it and the banner's own table (cmd/nova-bus/main.go:73) has to carry the exception | keep REFUSED for exit 2 and print the failed word on the exit-1 lines, or make the banner's table the family rule and say why | M |
| 8 | cmd/nova-bus/main.go:2171 | advanceCursorTo takes thirteen positional parameters, noPush and noBeat adjacent among them, and is called positionally at cmd/nova-bus/main.go:1501, cmd/nova-bus/main.go:2246 and cmd/nova-bus/main.go:3325, where one swapped pair compiles and silently discards or commits a file | pass the inboxOpts the verb already builds at cmd/nova-bus/main.go:1468 | M |
| 9 | cmd/nova-bus/main.go:1400 | the comment that explains `--legacy-now` is a past-tense story about one reader's mistake, and the shape repeats at cmd/nova-bus/main.go:632, cmd/nova-bus/main.go:720, cmd/nova-bus/main.go:1477 and a named reader at cmd/nova-bus/main.go:2662; the rule is learnable from these lines but the tense and the name break the present-tense and general rules | state each as the rule it proves, in the present tense, naming no reader | M |
| 10 | docs/CLI.md:539 | the `--host` section's worked example carries a person's name and two machine names, the same shape sits in cmd/nova-bus/main.go:2662 and internal/bus/note.go:79, and the tree books all of it as shrink-only debt in internal/ci/testdata/generality-text/docs.txt | use the fixture identities the rest of the page already uses, and reword the comments to the concept | S |
| 11 | cmd/nova-bus/main.go:353 | verbflag.Recover gives every verb the whole banner, 272 lines (cmd/nova-bus/main.go:61-332), as its `-h`, so asking one flag what it wants costs the essay; cmd/nova-config/main.go:227 and three other tools use the per-verb form of the same seam | write each verb's own help lines and use the seam that prepends them to its flags | M |
| 12 | cmd/nova-bus/main.go:2639 | `--beat` and `--beat-lease` are still declared, parsed and defaulted although nothing writes a BEAT any more (cmd/nova-bus/main.go:2641); they only announce their retirement when somebody uses them, and the banner does not list them | drop both flags and the two default constants beside them | S |
| 13 | cmd/nova-bus/main.go:544 | receiptMaxWordsFromDefaults (544-568) and hostFromDefaults (594-614) are one key=value reader of `<bus>/.nova-bus/defaults` written twice, so a third key will be a third copy | one defaultsValue(busDir, key) (string, bool) and two thin callers | S |
| 14 | cmd/nova-bus/main.go:442 | a hand-written insertion sort orders the missing-flag names, where slices.Sort does it in one line and the library-first rule says to look | replace the loop with slices.Sort | S |
| 15 | cmd/nova-bus/main.go:1368 | the set's shape for a cut listing is `--max` with the family's `MORE kind= shown= total=` line (docs/SPEC.md:132); here the same idea arrives as `--open-max`, `--max-notes` and `--max-commits` and prints "listed= and k more", so a reader who learned the shape on another tool relearns it here | give each listing one `--max` and print the family's MORE line beside the existing counts | M |
| 16 | cmd/nova-bus/issue1451_test.go:1 | three test files (issue1451_test.go, issue1496_test.go, issue1517_functional_test.go) and eff81_efficiency_card_test.go are named for a ticket number or a card slug, so the file list tells a stranger nothing about the contract inside; the other 55 files in the package are named for what they pin | rename each file for the rule its tests state | S |
| 17 | internal/ci/testdata/dead_code_allowlist.txt:14 | internal/bus carries 23 dead-code rows in the shrink-only ledger, the third-largest count in the file, so a reader of the bus's library cannot tell which part of 11,021 lines a run reaches | sweep the rows with the reachability pass and delete what no shipped verb touches | S |

## Good, keep

The cursor section's plain statement of its own price (docs/CLI.md:636-640) and the failure
to verb table (docs/SPEC.md:2829-2842): both say what the tool will not pretend. The
first run that prints a refusal and its way out (docs/CLI.md:461-464). The note-parse
counter that turns "a read costs the change, not the bus" into a number a test asserts
(internal/bus/instrument.go:49), and the rule-shaped test names that read as the contract.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| a 467-line function in the read path | STILL THERE | cmd/nova-bus/main.go:1618 opens inboxListing and cmd/nova-bus/main.go:2083 closes it: 466 lines at this snapshot |
| calls with sixteen parameters | CHANGED | cmd/nova-bus/main.go:2171 now takes thirteen positional parameters and is still called positionally at cmd/nova-bus/main.go:1501 |
| war-story comments | STILL THERE | cmd/nova-bus/main.go:1400 tells one reader's past mistake; cmd/nova-bus/main.go:632 and cmd/nova-bus/main.go:720 carry the same shape |
| REFUSED printed at exit 1 | STILL THERE | cmd/nova-bus/main.go:1658 prints INBOX REFUSED and returns 1, against docs/SPEC.md:145 |
| retired flags still declared | CHANGED | cmd/nova-bus/main.go:2639 and cmd/nova-bus/main.go:2640 are still declared, but each help line now says retired and one WAIT NOTE names it (cmd/nova-bus/main.go:2830) |
| prose promises more than bounded retries deliver | CHANGED | docs/CLI.md:420 still says no rejected push reaches a person, and internal/bus/git.go:857 now ends the bounded loop by naming the commit that is on the branch and the command that lands it; the retry budget is measured and printed in cmd/nova-bus/main.go:633 |
| the README scored 6.5 to 8.4 | CHANGED | README.md:24 still hands the reader ./trial-bus before README.md:64-69 creates it, and README.md:102 still promises transcripts the tests execute line by line |
