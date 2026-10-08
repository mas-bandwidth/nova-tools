# nova-self-talk dogfood, 2026-10-06 (codex)

Tool: nova-self-talk. Build: `nova-self-talk devel linux/amd64 go1.26.6`, built with
`go build -o bin/nova-self-talk ./cmd/nova-self-talk` at df30ce088 and run on a bench over
ssh; scratch pages and temp files only, nothing under the job directory was pointed at real
records.

Run cold, from the binary's own help (`nova-self-talk -h`, `help`, `<verb> -h`) and the
tool's page in `docs/CLI.md` only, over about forty minutes of use. Every verb ran at least
once with its real flags: the bare command and `scan` (with `--skip`, `--rule-doc`,
`--max` at 0, 1 and 2, `--json`, `-` for stdin, `--` before a dash-named file, a file named
`./scan`), `shapes` (with `--json`), `example` (`--dry-run`, `--json`, re-run on the same
directory, on a non-directory, and on a modified page), `version` (and `--version`), and
`help` for every verb; the refusals too: no files, an unknown flag, a bad `--max` value,
missing files (one and two), a directory, a path in `--rule-doc`, `example` with no
directory, `version --json`, `help` with an unknown verb, an all-skipped run, an empty file,
and a green file. The tool was then used for real on this repository's own documentation
(62 files, 2.9MB), which is where findings 1 and 6 came from. No code changed here; a
finding is recorded, never fixed.

## 1. A "That day" sentence is flagged STANDING and the finding's text is missing its first characters — URGENT

**Command:**

    nova-self-talk n1.md

`n1.md` holds one sentence:

    That day five cards held three to five times each on the same aaaa blocker: the worker wrote `PATHS-PROPOSED:` in her report every time, the failed rule dealt the same brief again, and the coordinator widened it.

**Printed:**

    SELFTALK FAIL n1.md:1: STANDING match="failed": t day five cards held three to five times each on the same aaaa blocker: the worker wrote PATHS-PROPOSED: in her report every time, the failed rule dealt the same brief again, and the coordinator widened it.
    SELFTALK FAIL files=1 claims=1 standing=1 installations=0 dated=0 shown=1
    SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

exit 1. The same shape fires in the wild on this repository's own page
(`nova-self-talk docs/SPEC-SPRINT.md`):

    SELFTALK FAIL docs/SPEC-SPRINT.md:4137: STANDING match="failed": day five cards held three to five times each on the same PATHS blocker: the worker wrote PATHS-PROPOSED: in her report every time, the failed rule dealt the same brief again until the attempt bound, and the coordinator widened PATHS by hand through drop and add --replaces.

where the file reads "That day five cards held..." at that line.

**Expected:** "that day" is one of the measurement words the banner and the DATED row name
("a claim carrying a date or a measurement word ... is DATED: a record, counted on one
line, never quoted"), so the claim belongs on the DATED line and the run exits 0 — which is
exactly what the same sentence does once a few words are removed. And whatever fires, a
finding's sentence is the file's text: here the printed sentence starts mid-word
("t day five cards", "day five cards" in the wild case), so the line the tool prints cannot
be found in the file with grep. The eaten prefix grows with the length of a word later in
the sentence: with a one-letter word at that spot nothing is eaten and the licence holds
(green, dated=1); with two through eight letters the run eats 1, 2, 3, 5, 5, 6 and 7
characters off the sentence's start and flags STANDING each time. A stranger cannot predict
which of two nearly identical sentences scans green.

**Grade:** URGENT

## 2. The first class fires with no first-person claim anywhere — URGENT

**Command:**

    nova-self-talk c7.md

`c7.md` holds one sentence: "the machine failed every time."

**Printed:**

    SELFTALK FAIL c7.md:1: STANDING match="failed": the machine failed every time.
    SELFTALK FAIL files=1 claims=1 standing=1 installations=0 dated=0 shown=1
    SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

exit 1. "the machine failed reliably." prints the same under its own words.

**Expected:** the banner, the usage block and the STANDING row all say the first class is
"a first-person claim (I am, I cannot, I always, I never, my <noun> is, I tend, I fail
...) carrying a word of failure"; a sentence about a machine holds no first-person claim, so
I expected claims=0 and exit 0. The near synonyms do not fire: "each time", "by default",
"consistently", "invariably", "constantly", "tends to", "routinely", "habitually",
"perpetually", "chronically", "without fail", "as a rule" and "by reflex" all scan green
beside "every time" and "reliably" — exactly those two habit words leak into the first
class with no claim to bind to, and every one of them is a second-class marker the TRAIT
row already owns. In the wild this is half of what flagged the repository's own page in
finding 1 ("the failed rule dealt the same brief again" is about a rule, not the writer).

**Grade:** URGENT

## 3. `version` is the one verb that refuses `--json` — NEXT

**Command:**

    nova-self-talk version --json

**Printed:**

    nova-self-talk version REFUSED: takes no flags and no arguments, got 1; run: nova-self-talk version -h

exit 2.

**Expected:** `scan`, `shapes` and `example` all take `--json` and render the same value
as one JSON object, and the banner's `--json` line promises "print the run as one JSON
object on stdout". I expected the version line as JSON, or nothing surprising; instead a
caller gating on JSON output must special-case `version`. The verb's own `-h` says it
takes no flags, so it does not lie — the shape is just not one shape across the set.

**Grade:** NEXT

## 4. `help <unknown verb>` prints the whole banner at exit 0 — NEXT

**Command:**

    nova-self-talk help scna

**Printed:**

    nova-self-talk: flags sentences where a writer passes a standing verdict on themselves

    how it works: each named file (- is stdin) is read sentence by sentence, line numbers kept,

(the whole banner, 93 lines) exit 0.

**Expected:** the dispatch path answers an unknown name with the names there are and the
nearest — a directory argument is told "it is not a verb either (the verbs are scan,
shapes, example, version, help; a file of that name is ./pages)". I expected `help scna`,
a typo of `scan`, to answer "unknown verb scna; the verbs are ...; nearest: scan" in one
line, not the whole banner with an exit that reads as "scna was fine".

**Grade:** NEXT

## 5. No performance story at corpus size — NEXT

**Command:**

    time nova-self-talk docs/*.md

**Printed:**

    real 0m20.277s
    user 0m19.655s
    sys 0m0.147s

    SELFTALK FAIL files=62 claims=8 standing=8 installations=8 dated=0 shown=16

exit 1, for 62 files totalling 2.9MB.

**Expected:** the help and the tool's page state no performance bound and the run prints no
timing, which is fair for a sentence scanner; but 2.9MB in 20 seconds is about 150KB/s,
all of it user CPU, so a stranger pointing the tool at a few hundred pages waits minutes
with no progress word and no way to know from the help whether that is expected. A bound
in the help (or a NOTE when a run exceeds it) would make the wait legible.

**Grade:** NEXT

## 6. FORECLOSURE fires on ordinary statements about objects, not doors the writer closed — NEXT

**Command:**

    nova-self-talk docs/USAGE.md docs/SPEC-UPDATE.md

**Printed:**

    SELFTALK FAIL docs/USAGE.md:189: FORECLOSURE match="There is no quickstart: nothing here": There is no quickstart: nothing here has a default to guess.
    SELFTALK FAIL docs/SPEC-UPDATE.md:602: FORECLOSURE match="There is no -i": There is no -i: a key named on argv is a key in every ps.
    SELFTALK FAIL files=2 claims=0 standing=0 installations=2 dated=0 shown=2

exit 1.

**Expected:** the row's says= is "there is no <thing> here, in me, in my ..." and the
class is a verdict on the writer ("a door stated shut"); "There is no quickstart" is a
statement about a tool and "There is no `-i`" about a flag, so I expected claims=0 on
technical prose. The NOTE owns the honest limit (register is invisible to grammar, a green
clears the known shapes never the file), so this is friction a writer judges per finding,
not a lie; but a stranger scanning their own technical notes meets one of these per few
hundred lines and each costs a judgment.

**Grade:** NEXT

READ 8/10 — the banner answers what, how and how-to, every verb's `-h` exits 0 with its
flags and exit codes, `shapes` prints the whole detector table with a sentence each row
finds and passes, and the refusals name what a flag wants (the basename refusal teaches
in one line); the score is held down because the documented contract of the first class
does not hold on real sentences (findings 1 and 2), so a reader cannot fully predict or
verify a run from the help.

USE 7/10 — every verb, flag and refusal ran as printed, the streams separate exactly as
the banner says, `example` is idempotent and never replaces a modified page, and the JSON
rendering is the same value; the score is held down by the two wrong results on real prose
(the tool's own documentation trips both) and the 20-second wait at 2.9MB.

The card's named test, `./internal/docs TestDocsTreeIsConsistent`, does not exist at this
tip: `go test -run TestDocsTreeIsConsistent ./internal/docs` reports `ok ... [no tests to run]`. The gate that ran is the card's STEP 4 set, whole packages on a bench, and its last
lines:

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.951s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	16.223s

urgent=2 next=4
