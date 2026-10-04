# nova-check READ rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: c28448a54d56
Score: 7.5/10
README: 7.5/10

## Reasons

nova-check is a careful, honest piece of work. The entry point is real: a
stranger types `quickstart` and gets two checks in one line, the banner states
what the tool does and where its state lives, every refusal names what the flag
wanted, and `docs/TESTS.md` executes the first-run transcript so the doc cannot
drift from the binary. The comments are the best in the tree I read: they say
why a rule exists and which wrong turn it prevents, not what the line does.

What holds it below a 10 is shape, not craft. One binary still carries several
jobs that share only the word "check": self-repo records, a branch range, a
tool-usage ledger and a seven-stream work reading. A cold reader must learn a
private vocabulary before using one verb, and the cap flag changes name between
listings. Two small docs disagree with their own code and spec. A 10 would name
the family honestly or split it, define each noun once, keep one cap flag, and
keep every citation and function doc true at the head.

README: 7.5/10. The problem table is clear and the install path works, but it
is a wall of eighteen rows and it still says 1.0.0 while the tree is 1.1.0.

Reading notes, as a visitor cold. Confused at README.md:36, where the first
command points at a source-checkout fixture while README.md:50 tells a stranger
to install a released binary, so the two instructions cannot both be followed.
Bored at README.md:21, the eighteen-row HTML table, which repeats "needs a
running Redis" and "prints help without a store" until the eye skips it.
Doubted at README.md:50, "These are the Nova Tools 1.0.0 commands", because the
head carries docs/RELEASE-NOTES-1.1.0.md and docs/ratings/1.1.0.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-check/main.go:30 | The banner's first line promises checks over markdown records and repositories, but the same binary also runs branch hygiene, a dogfood receipt ledger and a convergence reading; the package doc at internal/check/attest.go:1 says three of the ten are not self-repo checks at all | Split hygiene, dogfood and convergence into their own binaries, or reword the banner and the README row to name the whole family honestly | L |
| 2 | cmd/nova-check/hygiene.go:43 | This listing caps findings with --max while every other listing in the binary uses --fail-max, so a reader who learned one flag meets a second name for one idea | Give hygiene --fail-max and drop --max, or move every listing to --max | S |
| 3 | internal/check/corpus.go:92 | The ParseLedger doc says an anchor table is the run whose second line is a separator, but the state machine below recognizes the table anywhere in the run; docs/SPEC.md:1232 states the anywhere rule, so the doc is the odd one out | Rewrite the doc to state the anywhere-in-the-run rule the code and the spec already hold | S |
| 4 | internal/check/nocode.go:455 | The path-side name, location and extension rules are cited as SPEC.md 858, but that line is the staged diff-index record; those rules live near docs/SPEC.md:628 | Cite the section by name, or the correct lines | S |
| 5 | README.md:50 | The README says these are the 1.0.0 commands and installs v1.0.0 while the tree, the release notes and the ratings are 1.1.0, so a stranger installs a build that lacks the verbs shown | Point at the 1.1.0 release, or drop the version sentence until 1.1.0 is cut | S |
| 6 | docs/CLI.md:32 | The first run says ./self is a self repo of yours, then uses the door, floors, kernel and corpus without defining any of them, so the newcomer must infer a private vocabulary | Define each noun where it first appears, or link TERMINOLOGY.md from the banner | M |
| 7 | docs/CLI.md:45 | The bounded-run sentence lists attest, links, nocode, corpus and quickstart as the --fail-max verbs and omits spelling, which takes it too | Add spelling to the list | S |

## Good, keep

- The hint constants at cmd/nova-check/main.go:151 turn each missing flag into
  one line that says what the flag wants, so recovery takes one turn.
- The comments state the wrong turn a rule prevents, with the measured incident
  behind it (internal/check/kernel.go:72, internal/check/nocode.go:148).
- The first-run transcript is executed by tests, so docs/TESTS.md cannot drift
  from what the binary prints (docs/TESTS.md:3).

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| several unrelated tools behind one name | STILL THERE | cmd/nova-check/main.go:30 says records and repositories; internal/check/attest.go:1 puts dogfood, hygiene and convergence outside that |
| a private vocabulary | STILL THERE | docs/CLI.md:32 uses self repo undefined; cmd/nova-check/main.go:69 asks for the door and its floor set |
| two cap flags | STILL THERE | cmd/nova-check/main.go:370 defines --fail-max while cmd/nova-check/hygiene.go:43 defines --max |
| a doc that says a shipped verb does not exist | FIXED | docs/CLI.md:10 lists every verb; cmd/nova-check/main.go:220 dispatches them all |
| the newcomer path assumes one self-repository workflow | STILL THERE | docs/CLI.md:32 says ./self is a self repo of yours; cmd/nova-check/main.go:69 asks for SEED-CORE.md and SEED.md |
