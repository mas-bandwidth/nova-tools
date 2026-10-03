# nova-work READ rating, nova-tools 1.1.0

Rater: Grok
Build: 3a1cbd4f7245
Score: 7.5/10
README: 6.5/10

## Reasons

Layer 1 reads as a finished essay and the doors around it do not. docs/SPEC-WORK-V1.md says what this layer is for: one tree file of every issue, an import that only reads, and a verify that proves the file matches the source field for field. cmd/nova-work/main.go is that sentence, two verbs, and the shared skeleton. The code does the layer. The call estimate is checked before the first issue is read. The bytes are read back through the strict reader before a rename. A difference is one MISSING, EXTRA or DRIFT line and a long value is a length and a digest, never a body. The source seam refuses any document that is not a query. Each file holds one job. Names are plain. Comments say why, in the present tense, and cite the spec and the model. Section 1.10 names the contract one case at a time, and the first-run transcript is the banner's example.

It is not a 10. The opening calls the tree the store that later verbs query and change, and section 1.7 says those verbs are specified, not built. An AI that holds the file has no verb that asks it a question. The front door never offers the tool, and the command reference has no section for it. The banner still promises one JSON object after both verbs opt out of that rendering. A run that cannot finish does not speak the refusal line the spec states. The first run needs a live login, so the example cannot be pasted against the recording the tests already execute.

README: 6.5/10. The page is a clear chooser for the tools it lists: one problem, one command, and an honest stop at a single tool. It is not the inventory of this tree.

The first confusion is README.md:24. The table that is supposed to be every job a reader might have never names holding an organization's issues in one file, so there is no row to pick.

The first stretch that drags is README.md:47. After that long table, the trial section restates an install pin and a bus rehearsal and still never reaches this tool.

The first doubt is README.md:48. The living front door says these are the 1.0.0 commands, and the install line under it pins that tag, while the tree this page introduces already contains a tool the page does not list.

A 10 would put the banner's sentence in that table, open a command-reference section whose first run is the transcript, print one JSON value of the same lines, refuse in the one grammar with the next command, and either offer a query verb or stop promising one in the opening.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-WORK-V1.md:5 | The opening says the tree is the store later verbs query and change. Section 1.7 says those verbs are specified, not built, and the command has only import and verify. An AI that writes the file cannot ask it anything. | Say in the first three lines that this layer only imports and verifies, and add a query verb when the next layer is built. | L |
| 2 | README.md:24 | The chooser table has no row for this tool, the usage guide never names it, and the command reference opens at another tool with no section here. README.md:71 sends the reader to that missing section for inputs, effects and limits. | Add a table row whose sentence is the banner line, and a command-reference section that opens with the executed first run. | M |
| 3 | cmd/nova-work/main.go:42 | The first-run line requires a logged-in client and names no recorded conversation. The example cannot run on the fixture the tests already execute, so a cold try is a live read or nothing. | Name a replay in the help, or a first command that reads the included recording and writes nothing outside the trial directory. | M |
| 4 | cmd/nova-work/import.go:27 | Both verbs print their own lines and take no JSON flag. The banner, built for the set, still says the result is one JSON object. An AI is told a shape the command does not print. | Render the same lines as one JSON object, or state that these verbs print field lines and give that grammar. | M |
| 5 | cmd/nova-work/import.go:86 | A run that cannot finish prints IMPORT FAILED on the error stream and does not end with the next command. Verify writes difference lines on standard output and the summary on the error stream at cmd/nova-work/verify.go:131. The spec says a refusal names the problem and ends with the verb's help. | Use one line grammar on standard output, every problem, and the next command, for both a bad run and a difference. | M |
| 6 | docs/TESTS.md:1008 | The prose says the dry run is the plan and nothing else. The transcript above it, and the spec at docs/SPEC-WORK-V1.md:138, show a full read, a digest and dry_run=true, with no file written. | Say the dry run fetches and encodes, and writes no file. | S |
| 7 | cmd/nova-work/main.go:90 | The shared flag help says the default is every repository of the organization flag. Verify does not take that flag. Its default is the organization recorded in the tree. | Say the default on verify is the tree's organization. | S |

## Good, keep

The file is canonical, and the reader refuses a missing, repeated or unknown key, a value of the wrong kind, and a document that would evaluate. Import checks the call estimate before the first issue and writes only after the bytes read back equal the fetch.

A difference line carries a path and a field, and a long value is a length and a digest, never a body. The source seam refuses a mutation before anything runs. The tests in section 1.10 each name one rule this layer has to keep.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| small leftovers only, the one tool built the way the standard says | CHANGED | docs/SPEC-WORK-V1.md:5 still promises query verbs that section 1.7 does not build, and cmd/nova-work/import.go:27 leaves the shared JSON rendering |
| adoption still starts on the harder path | STILL THERE | README.md:24 has no row and cmd/nova-work/main.go:42 still requires a live login |
| the front door scored from 6.5 to 7 | STILL THERE | README.md:48 still pins the 1.0.0 commands and the table still omits this tool |
| the front door scored 8.4 | CHANGED | this read scores the front door 6.5 because README.md:24 never offers the tool |
