# nova-check READ rating, current baseline 0c5803c2de40

Rater: openrouter/deepseek/deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 8.5/10
README: 8/10

## Reasons

The README line for the tool is README.md:34: "checks over markdown records
and repositories, each finding named by file and line", and the binary's banner
opens with the same sentence (cmd/nova-check/main.go:26), so the row and line 1
agree as the onboarding standard asks. The code behind that line is unusually
careful: every refusal names every missing flag in one run with a hint that says
what the flag wants (cmd/nova-check/main.go:306), findings are capped with a
MORE line that carries the command to lift the cap, and the comments say why a
rule exists rather than what the line does. I would trust this code and enjoy
working in it; the reasons it is not a 10 are breadth and a few seams.

The first place I was confused: README.md:34 says the tool checks markdown
records and repositories, but the tool also gates a branch (hygiene), keeps a
family-wide dogfood ledger (dogfood), reads a forge and a checkout (convergence)
and spell-checks prose (spelling). The one-line promise covers the six record
checks and not the other five verbs.

The first place I was bored: docs/CLI.md:48 opens "The dogfood ledger" and runs
about a hundred lines of receipts, edges, ids and closing rules before a reader
who came for links or the kernel reaches the next check. The reference is a
record of a large family's process as much as a manual for the tool.

The first claim I doubted: cmd/nova-check/main.go:8 states "Every path and every
budget comes from a flag. There are no defaults: a missing flag is a refusal,
never a guess." cmd/nova-check/spelling.go:58 does the opposite: with no --dir it
takes the working directory as the resolution root.

What a 10 would need: one clear boundary for what the binary is (a record-check
tool, or a suite whose verbs each say their layer), shipped examples a stranger
can read without the family's private names, one cap flag, and the same care
spent on the five verbs that do not fit the README sentence as on the six that
do.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:7 | One binary carries eleven verbs across five unrelated layers: record checks, a branch accept gate, a family dogfood ledger, a forge-reading convergence reading and a spell-checker. The synopsis alone is eighteen dense lines, and the README sentence covers only the record checks. | Give hygiene, dogfood, convergence and spelling their own binaries, or make the banner name the layers and let each verb say which layer it is. | L |
| 2 | docs/CLI.md:172 | The shipped worked examples use a colleague's first name as the git identity and a second person's home directory as the ledger path, while the README uses a generic placeholder; the tree carries a shrink-only debt ledger for these names (internal/ci/testdata/generality-text/docs.txt:3). A stranger cannot resolve them and the generality rule forbids them. | Use the same generic placeholder the README uses, everywhere. | M |
| 3 | cmd/nova-check/hygiene.go:43 | Two cap flags for one idea inside one tool: seven listing verbs take --fail-max (cmd/nova-check/main.go:331) and hygiene takes --max, against the standard's one --max with its MORE line. | Rename hygiene's flag to fail-max, or move every listing verb to --max. | S |
| 4 | cmd/nova-check/main.go:1 | main.go holds dispatch plus eight verb handlers and the shared parse and cap helpers in 877 lines, while hygiene, dogfood, convergence, spelling and staged each have their own file. The split is inconsistent and the entry file does many things. | Move each record-check handler beside its verb file so main.go is dispatch and the shared helpers only. | M |
| 5 | internal/check/links.go:44 | The exported Links wrapper is reached only by tests; the production path calls LinksExcluding and LinksFiles. It is a second entry point kept alive by its own test. | Delete it and point the tests at LinksExcluding. | S |
| 6 | cmd/nova-check/spelling.go:58 | With no --dir the spelling verb takes the working directory as the resolution root, which is the guessed path the banner at cmd/nova-check/main.go:8 says the tool never makes. | Require --dir, or state in the help and the reference that the working directory is the root and why. | S |
| 7 | internal/check/links.go:57 | The root-resolution block and its "resolve before walking, a symlink root passes as one entry" comment are copied between links and nocode (internal/check/nocode.go:323). | Extract one shared helper that resolves and validates a walk root. | S |
| 8 | cmd/nova-check/spelling.go:90 | The selector reads `len(paths) > 0 || (len(files) > 0 && len(paths) > 0)`; the second clause can never add a case. | Drop the redundant clause. | S |
| 9 | cmd/nova-check/hygiene_test.go:210 | Test comments name tickets, dates and a colleague, so a reader cannot tell what the test pins without the issue tracker. The shape recurs at cmd/nova-check/hygiene_test.go:242. | State the behaviour the test pins; keep the ticket in the commit, not the test. | M |
| 10 | go.mod:9 | The spelling corpus module is a direct dependency, but the standard's adopted-modules list (docs/STANDARD.md section 7) does not name it. | Add it to the list with its reason, or keep it custom with the reason beside it. | S |

## Good, keep

- The refusal grammar is the best part: one line, every missing flag at once,
  each with a hint that says what the flag is and what a first run puts there
  (cmd/nova-check/main.go:140).
- The comments explain why, in the present tense, and cite the rule or the
  SPEC section a check implements, so the reading of the code is the reading of
  the contract.
- The tests teach the contract: firstrun_test.go executes the banner's example
  lines and holds the docs/TESTS.md transcript against what the binary prints.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| several unrelated tools behind one name | CHANGED | docs/SPEC.md:329 now owns the grouping and names the three outlying checks, but the binary still spans layers and the README sentence still covers only the record checks (docs/CLI.md:7). |
| a private vocabulary | FIXED | docs/TERMINOLOGY.md defines the words the specs use and README.md:100 links it as the glossary, so a newcomer has one place to look. |
| two cap flags | STILL THERE | cmd/nova-check/main.go:331 adds fail-max for seven verbs while cmd/nova-check/hygiene.go:43 adds max for hygiene; the two coexist in one binary. |
| a doc that says a shipped verb does not exist | FIXED | every verb named in docs/CLI.md:10-24 dispatches in cmd/nova-check/main.go:235-268; none is documented and absent. |
| the newcomer path and vocabulary assume one self-repository workflow | CHANGED | README.md:12 invites the reader to use their own repositories, but the core nouns still say self repo (docs/SPEC.md:331, cmd/nova-check/main.go:51). |