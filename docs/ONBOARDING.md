# ONBOARDING — the standard every command in this repo meets

**A newcomer's first stumble is the spec for this page.** Two new lines ran two
of these tools for the first time today, and both stumbles were ours: a usage
banner with no runnable line in it, and a refusal that named what was wrong
without saying what it wanted. Every binary under `cmd/` meets all five points.

1. **`<tool> help` prints usage, the usage ends in an `example:` block whose
   lines actually run, and a bare command NAMES that door in one line.** Not
   sketches — commands a stranger can paste. A line that runs answers 0 or 1;
   exit 2 is "could not run", and an example exiting 2 is a broken example.

   The bare command used to BE the banner, and that half is now the other way
   round: an invocation the tool cannot run prints one line —
   `<tool>[ <verb>]: <what was wrong>; run: <tool> help` — and exits 2, while
   `<tool> help` prints the banner on stdout and exits 0. The reason is the same
   newcomer: a flag typo cost between 1,900 and 6,500 bytes of banner to say
   that a dash was in the wrong place, and a harness reading a tool's stderr
   pays that on every typo. The door has to be named on the refusal, in words
   the reader can type, or this is just a tool that stopped explaining itself.
   Point 2 still governs the words: where the guidance is a sentence of its own
   it follows on one indented line, and two lines is the ceiling.
2. **Every refusal says what the flag or input WANTS, not only what was wrong,
   and one run reports every problem it can find.** The no-guessing law is
   unchanged: a missing flag is still a refusal, never a default. But a refusal
   naming only the fault has moved the guessing onto the reader, and sending a
   first run back three times for three independent flags is three refusals the
   first one already knew about. Flags that depend on one another may still be
   reported in order.
3. **The tool's section in [`docs/CLI.md`](CLI.md) opens with `### First run`:** the one or two
   commands a stranger runs first, the real transcript shape they print, how to
   read it, and the things a first run gets wrong with what each one wants. The
   transcript is produced by RUNNING the tool on its fixture, never written by
   hand.
4. **A `quickstart` verb where the tool has a natural first run** — one line
   needing nothing the caller has to invent. Where there is none, the command
   reference says so and why: a verb added for symmetry writes state nobody
   asked for.
5. **Tests pin the first three.** (a) by EXECUTING the example lines against
   the tool's fixture; (b) by asserting the sentence each refusal must carry,
   and that one run names every independent problem; (c) by comparing the
   [`docs/TESTS.md`](TESTS.md) transcript's event prefixes and field names
   against what the tool
   actually prints — values are deliberately not compared, so the transcript
   stays a document instead of becoming a fixture.

`internal/ci/onboarding_test.go` asserts (a) and (c) for every directory under
`cmd/`, so a new binary joins the standard on the day it appears; each
binary's own `firstrun_test.go` does the rest, and `internal/onboarding` holds
the shared parsing so that "the examples run" means one thing here rather than
five similar things. Each tool's fixture lives in `cmd/<tool>/testdata/`, small
enough to read in a sitting and referenced by nothing outside it.

## How feedback arrives

From Glenn, through Emma, on the day nova-bus went live: *"We get better tools this way."* When you try a tool, say
three things, in this order: what works, so the author knows what to protect; where it catches or surprises you,
plainly, with the exact sentence it printed; and the fix you would make, a README line, a hint, a flag. Pure
criticism reads as an audit; pure praise leaves rough edges rough. Every stumble named this way became a fix within
the hour on the first day, and the author was glad of each one.
