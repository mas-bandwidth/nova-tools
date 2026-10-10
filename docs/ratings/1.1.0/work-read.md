# nova-work READ rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: 3d7aab8eb412
Score: 8.5/10
README: 8/10

## Reasons
The README line for this tool is README.md:26: "every issue of an organization's repositories in one tree file, verified field for field". The tool's own banner opens with the same words (cmd/nova-work/main.go:54), and the reference says it again (docs/CLI.md:2671), so the three agree; that agreement is the best thing here. The first place I was confused is README.md:50: at a 1.1.0 head the reader is told "These are the Nova Tools 1.0.0 commands" and sent to that release. The first place I was bored is README.md:21: the catalogue is one HTML table of nineteen rows, so finding the row in hand is a scan, not a lookup. The first place I doubted a claim is docs/SPEC-WORK-V1.md:7: "The tree is the working store the verbs above query and change", yet the binary answers only import and verify; the modes that query and change the tree are "specified, not built" (docs/SPEC-WORK-V1.md:189).

The tool itself is disciplined and is built the way the standard says: one pkg/tool.Tool (cmd/nova-work/main.go:51), one result value rendered as typed lines or --json, refusals that name a remedy, a dry run that says which reads it still makes (cmd/nova-work/import.go:139), an exit table, and a first run with no login (docs/CLI.md:2678). The reading left four real leftovers. First, the shared reader's own word for what it reads is "plan" (internal/worklang/worklang.go:32,147), though its only caller is this tree file, and the caller patches the message with a string replace (internal/workfile/decode.go:29): a reader meets the wrong noun, and the workaround hides it. Second, the spec above layer 1 is written in the present tense (docs/SPEC-WORK-V1.md:189) and sends the reader to a page that does not exist (docs/SPEC-WORK-V1.md:11), so a cold reader learns a destructive mode and an export mode the binary cannot run. Third, two conversions are unreachable from any command root and kept only by their tests and one spec sentence (internal/workfile/tree.go:119, internal/ci/testdata/dead_code_allowlist.txt:40). Fourth, the failure word is wrong in three documents (docs/CLI.md:2690, docs/SPEC-WORK-V1.md:147, docs/TESTS.md:1194): they write `VERIFY FAIL` where the tool prints `VERIFY FAILED` (cmd/nova-work/main.go:155).

A 10/10 would need: the doc set in the present tense, with the unbuilt modes moved to a proposal; the shared reader naming what it holds without a patch; no helper no command reaches; and one word for the failure line across the README, the reference, the spec and the transcript.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-WORK-V1.md:189 | Section 1.7, "The modes above layer 1 (specified, not built)", describes a destructive close, an export and a dry-run mode in the present tense, and the opening points at a page for "the verbs that query and change the tree" (docs/SPEC-WORK-V1.md:11) that does not exist; the binary answers only import and verify, so a cold reader learns verbs that are not there | Move the unbuilt modes and the open decisions about them to a proposal and leave this page at the tree, import and verify | M |
| 2 | internal/worklang/worklang.go:147 | The shared reader says a `#` is refused because "a plan is data, never a program", and its refusal prefixes "plan" (internal/worklang/worklang.go:32); its one caller is nova-work's tree file, and internal/workfile/decode.go:29 undoes the wording with a string replace, so the caller patches the reader's noun instead of the reader naming what it reads | Name the form a tree or the input, or let the caller pass the noun, and delete the replace | M |
| 3 | internal/workfile/tree.go:119 | PathOfURL, and URLOfPath at internal/workfile/tree.go:136, are unreachable from every command root; internal/ci/testdata/dead_code_allowlist.txt:40 records the package as two dead functions, and only a test and one spec sentence (docs/SPEC-WORK-V1.md:121) keep them | Delete both and the spec sentence, or give them the living caller the spec claims | S |
| 4 | docs/CLI.md:2690 | The reference and two more documents name the failure line `VERIFY FAIL` (docs/CLI.md:2681, docs/SPEC-WORK-V1.md:147, docs/TESTS.md:1194), while the tool prints `VERIFY FAILED` (cmd/nova-work/main.go:155; pkg/tool/out.go:28) | Write `VERIFY FAILED` once and let the other sites point at it | S |
| 5 | docs/SPEC-WORK-V1.md:13 | "The old nova-work and its modules live in the repository `nova-work-old` ... for reference only" names a parked tool in the spec of the living one, where the standard says a parked tool has no entry or link | Drop the sentence; a reader of this tool needs no predecessor | S |
| 6 | README.md:50 | At a 1.1.0 head the README says "These are the Nova Tools 1.0.0 commands" and sends the reader to that release (README.md:51), while the tree carries 1.1.0 notes and ratings | Make the version line current or version-neutral | S |
| 7 | README.md:21 | The catalogue is one HTML table of nineteen rows, one per tool; a reader who has the tool in hand scans every row, and the nova-work row (README.md:26) is a long paragraph inside a cell | Keep the choice, but make each row scannable, or add an index by problem | M |
| 8 | docs/CLI.md:2667 | The nova-work section opens on prose and jumps to `### import` (docs/CLI.md:2695); it carries no `### First run` block the standard names, and its executed transcript lives only in docs/TESTS.md:1170 | Open the section with the `### First run` block, as the sections for other tools do (docs/CLI.md:1537) | S |
| 9 | internal/workgh/query.go:1 | The package comment describes the package as "the recut of the GitHub capture that nova-work carried before", naming its old page query and mutation refusal; a reader who meets the package now is given its history | Say what the package does now | S |

## Good, keep
The banner's first line and the README row are one sentence, and the reference repeats it (README.md:26, cmd/nova-work/main.go:54).
Every refusal names a remedy that runs, and each caller value in it stays one shell word (cmd/nova-work/import.go:158, cmd/nova-work/remedy_test.go:96).
The tests teach the contract: import then verify is zero differences, and one changed field is one DRIFT line and exit 1 (cmd/nova-work/main_test.go:96).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| one rater, 8: the one tool built the way the standard says, small leftovers only | STILL THERE | the skeleton is kept (cmd/nova-work/main.go:51), and the leftovers remain (internal/ci/testdata/dead_code_allowlist.txt:40, docs/CLI.md:2690) |
| another, 8.8: adoption still starts on the harder path | CHANGED | README.md:16 sends the reader to install one tool first, and the tool row's first command is the no-login `nova-work verify -h` (README.md:26, docs/CLI.md:2678) |
| README then 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:26 states the tool's sentence and README.md:16 starts adoption at one tool; still the 1.0.0 line at README.md:50 |