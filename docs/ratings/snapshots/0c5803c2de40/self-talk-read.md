# nova-self-talk READ rating, current baseline 0c5803c2de40

Rater: grok-4.7, cold read, no memory of this tool or its earlier ratings
Build: 0c5803c2de40
Score: 9/10
README: 8/10

## Reasons
Rated source is 0c5803c2de406c1b0b2b0841f579c9bf73406b1c. The staged checkout is 586fe88aaa22feb9f9b000b786fecfd88b4aaaaa, which contains that commit; every line below was read from the rated snapshot, not from a later revision.

The README sentence for this tool is "flags sentences where a writer passes a standing verdict on themselves" (README.md:35), and the banner's first line is that same sentence (cmd/nova-self-talk/main.go:30).

The writing holds to its own advice. The spec section argues the construct before any pattern (docs/SPEC.md:1708-1929) and the code follows that section: two disjoint classes, licences, and a permanent miss printed on every run. The banner answers the three onboarding questions in order. One detector table is shared three ways: the scan walks it, shapes prints it, and a test runs every row's two sentences through the scan (internal/selftalk/installation.go:503-507), so help, code and tests cannot drift. Every closed word set carries the measurement that closed it (internal/selftalk/installation.go:411-434, internal/selftalk/selftalk.go:66-73), the limit prints on every run (cmd/nova-self-talk/main.go:145-147), and the version verb's comment is a small essay on why hand-maintained constants rot (cmd/nova-self-talk/version.go:1-13).

What costs the score, all of it writing rather than a wrong answer from the scanner: a dead exported helper whose comment claims the binary uses it (finding 1), topic sentences shouted in capitals across the package (finding 2), and a banner that says its first-run instruction and its exit-1 gloss twice (finding 3). Smaller: a gloss that overclaims first person, a positional walk that re-derives flag values, and an example line the first-run story never accounts for. A 10 would need no dead code, no claim the code does not bear out, comments whose emphasis survives in sentence case, a banner that says the first run once, every gloss matching the pattern it describes, and the example page's passing line named where a first run is taught.

The match spans do not cost the score. traitLead's first capture is the verb (internal/selftalk/installation.go:474). Two bytes before that capture are the subject and the blank after it, so the slices at internal/selftalk/installation.go:594 and internal/selftalk/installation.go:608 start on the subject. The dash alternatives in the same pattern sit before the subject and are not entered by that offset.

First places, read cold in order. First confused: README.md:35, "passes a standing verdict on themselves" names no sentence shape a newcomer can picture until the banner's classes block (cmd/nova-self-talk/main.go:53-67) resolves it a file later. First bored: README.md:24-30, the opening rows of the HTML table, each cell cramming setup prose into one unbroken line; the self-talk row itself is among the clearer ones. First doubted a claim: README.md:35, "Findings are advisory and produce exit 1" is a tension on its face, which docs/CLI.md:275 and the banner (cmd/nova-self-talk/main.go:111-113) later bear out rather than explain away.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/selftalk/installation.go:670 | An exported helper's comment says the binary's exit code is derived from it, and nothing outside its own test calls it; the exit is counted at cmd/nova-self-talk/main.go:493 and cmd/nova-self-talk/main.go:529 instead. A cold reader who checks the claim finds it false, and the helper is dead production code. | Delete the helper, or derive the exit through it so the comment's claim is true. | S |
| 2 | cmd/nova-self-talk/main.go:300 | Topic-sentence comments in full capitals run through the package (also cmd/nova-self-talk/main.go:320, cmd/nova-self-talk/main.go:450, cmd/nova-self-talk/main.go:484, internal/selftalk/selftalk.go:5, internal/selftalk/installation.go:3): emphasis by shouting, and the load-bearing sentences lose the hierarchy sentence case would give them. | Put the topic sentences into sentence case and let the wording carry the weight; keep every why. | S |
| 3 | cmd/nova-self-talk/main.go:101 | The banner says the first run twice: the instruction and the exit-1 explanation appear at cmd/nova-self-talk/main.go:37-38 and again at cmd/nova-self-talk/main.go:101-113. | Keep the first statement and end the banner at the exit table and the example block, leaving the working gloss in one of the two places. | S |
| 4 | internal/selftalk/selftalk.go:62 | The gloss says the first class matches a first-person assertion, yet its marker list includes reliably, every time, in one direction and makes me, none of which needs an I; the help's classes block (cmd/nova-self-talk/main.go:55) repeats the first-person gloss. | Say first-person or self-referential, the words the spec already uses for the second class at docs/SPEC.md:1772. | S |
| 5 | cmd/nova-self-talk/main.go:383 | Positional collection re-walks the parsed prefix to re-derive which arguments were flag values, with a special case for the json name at cmd/nova-self-talk/main.go:393; it is the one place a reader can mistrace the tool's own grammar, and it duplicates what the flag seam already knows. | Have the shared flag seam report the value spans, and keep this function to the terminator and late-flag rules. | S |
| 6 | docs/CLI.md:283 | The example rule page holds a cannot-verb its own transcript never flags (cmd/nova-self-talk/testdata/example-pages/RULES.md:7, cannot accept), because the failure-word list is closed; the first-run story never says that line passes, so a reader comparing page with transcript cannot tell a miss from a licence. | Add the sentence to What a first run gets wrong: that line passes because accept is outside the closed verb list, and the closed list is the point. | S |

## Good, keep
- The limit printed on every run, twice: the NOTE line (cmd/nova-self-talk/main.go:145-147) and the spec's enumerated permanent misses (docs/SPEC.md:1891-1923) mean a green never overstates its coverage.
- One detector table shared three ways (internal/selftalk/installation.go:503-507): the scan walks it, shapes prints it, a test runs every row's two sentences, so the listing cannot claim a shape the scan misses.
- Every closed set carries the measurement that closed it (internal/selftalk/installation.go:411-434); a reader can disagree with the evidence instead of trusting the list.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| off the skeleton | FIXED | cmd/nova-self-talk/main.go:27 imports the shared tool package; both renderings come from tool values (tool.Refuse at cmd/nova-self-talk/main.go:173, tool.Done at cmd/nova-self-talk/main.go:501); dispatch stays in main for the file-or-verb seam the spec documents at docs/SPEC.md:1719-1722 |
| an 88-line banner | CHANGED | the usage const now runs cmd/nova-self-talk/main.go:30-114, 85 lines: the three questions lead, the classes block follows; finding 3 names the first-run lines still said twice |
| comments in capitals | STILL THERE | cmd/nova-self-talk/main.go:300 and internal/selftalk/installation.go:3-35 open topic sentences in full capitals (finding 2) |
| a class name nobody glosses | FIXED | cmd/nova-self-talk/main.go:61-67 glosses INSTALLATION inside the banner's classes block; docs/CLI.md:275 explains each printed token on first use |
| jargon and a semantic overstatement | FIXED | every printed token is glossed where it first appears (cmd/nova-self-talk/main.go:72-83); advisory findings behind exit 1 are stated as the tool working in three places (cmd/nova-self-talk/main.go:111-113, docs/CLI.md:275, docs/SPEC.md:1843) |
| the README then read 6.5 to 8.4 | CHANGED | this rater reads 8 (README line above): the honest limits and the runnable first commands hold, the dense HTML table is the remaining cost (README.md:24-30) |
