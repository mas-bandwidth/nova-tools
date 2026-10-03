# nova-decide READ rating, nova-tools 1.1.0

Rater: Grok
Build: 4296165394da
Score: 8/10
README: 6.5/10

## Reasons

The README sentence for this tool, in the tree named by Build, is the table cell at README.md:34: "typed decisions with probabilities, recorded so each one can be calibrated against its outcome." That sentence is also the banner's first line (main.go:53, the decide command).

The first confusion is README.md:34. The printed command is `read` with `--card`, `--diff`, `--answers` and `--record`, and the row never says which file is the evidence, which file is the scripted reply, or what a card is. The first boredom is README.md:49. "Try one on a small example" repeats "pick one tool" and then walks an install and a bus trial; this row is already complete and the section never returns to it. The first doubt is the same row, README.md:34: the want-cell says a cheap model makes a typed call, and the command it prints answers from a fixed file and calls nothing.

Past that door the tool is one thing and the code does it. The spec's sections 1 to 7 say what a decision is: a named schema of `choice` or `noul` questions, asked over one state by a backend, appended to one JSON-lines file, labelled once, and scored into bars on every call. main.go is the verb list on the shared skeleton, so the banner, the refusal line, the exit table, `--json` and `--dry-run` are the family's, not a private dialect. decide.go holds the schema and the check that refuses an answer which does not fit. record.go holds the file and the lock. calibrate.go holds the AUC and the bars, computed, never stored beside the file. Built-in decisions (read, score, attempt, grade, gate, brief) each live in their own file, and the tests pin those question tables to the spec word for word and execute the banner's examples (firstrun_test.go:34). An entry point, the verbs and the record are findable in a minute. Comments say why, in the present tense. Names are plain, except one.

It is not a 10. The title says the system is trained from its record, and the same spec says the verbs that would export or train are not built, so an AI that comes to train a model finds labels and an AUC. A deciding command with no `--op` folds the clock into the id, so the replay the spec describes does not happen when the same line is run again. The banner uses `noul` and does not say what it is. The README's install pin cannot yield this binary. A 10 would open with "calibrated" until a train verb exists, make the default id a function of the schema and the state only, gloss `noul` in the how paragraph, point the row and the install lines at a tree that contains the command, show a joined class label in `outcome`'s help, and leave the sprint's bindings in the sprint spec so sections 1 to 7 are the whole front door.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:50 | The install section says these are the 1.0.0 commands and the next block installs that tag. That tag's command directory has no nova-decide, so an AI that follows the README never obtains the binary. The want-cell at README.md:34 also promises a model call the printed command does not make. | Say this binary is built from the current tree, and make the want-cell the record-and-calibrate sentence the banner already uses. | M |
| 2 | main.go:580 | With no --op the id hashes the schema, the current second and the state, so the same ask run twice appends two decisions and, on the model backend, asks twice. The replay at SPEC-NOVA-DECIDE.md:85 is reached only when the caller already chose an id, and the usage marks --op optional. | Hash the schema and the state only, and say in the how lines that a repeated command returns the recorded decision. | S |
| 3 | main.go:1 | The file opens by calling this a decision system trained from its own record. SPEC-NOVA-DECIDE.md:17 says the export and training verbs are not built. The binary attaches a label and prints bars. An AI that comes to train a model has no verb. | Open with calibrated from its own record, in the comment and the spec title, until an export verb exists. | S |
| 4 | main.go:55 | The how paragraph names the question type noul and never says what a noul is. The gloss is one comment at decide.go:32, which a cold reader has not opened, so the first screen has a private word. | Add one clause there: a noul is one statement and the probability it is true. | S |
| 5 | main.go:216 | outcome's help says the label is one word such as ok or wrong. A score's label is class names joined by +, which the checker allows and which calibrate counts per word, and the help never shows that form. | Name the joined form in the flag text, with one example of two class names. | S |

## Good, keep

The banner's first sentence and the README cell are the same sentence, and the fixed backend lets that sentence be tried with no key and no network (main.go:53, README.md:34).

The record names a bad line by file and number, refuses a second decision id, and refuses a second outcome; a conflicting label is exit 1 and anything that could not run is exit 2 (record.go:97, main.go:60).

A backend answer that does not fit the schema is a failure, never a repaired guess, and each built-in question table is held to the spec by a test (decide.go:123).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |
