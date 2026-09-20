RESULT: s17pr2-1336 sha=7c29513b0e8f

**CANNOT READ**

STEP 1 failed, so no reading was possible, and my reading contradicts an assertion
in the card. The card asserts the base tree is in `repo/` and that `git rev-parse
HEAD` prints `7c29513b...`. There is no `repo/` directory and the git repository in
the job directory is EMPTY: `git rev-parse HEAD` prints `fatal: ambiguous argument
'HEAD': unknown revision or path not in the working tree` (exit 128); `git show-ref`
prints nothing; `.git/objects` contains no objects, no pack, no refs, no commits
(unborn `refs/heads/master`). The card's stated base source, `/tmp/schema14-ftf.bundle`,
is not readable from the sandbox (`ls: /tmp/schema14-ftf.bundle: Operation not
permitted`), and a search of every readable path found no bundle, no clone, and no
object store containing base sha `7c29513b0e8f42f457189d1bec23e1de475d4ed8`
(`git cat-file -t 7c29513b…` → `could not get object info`). The harness's own
`harness-output.log` records the same failures for the same commands.

Because of that I could not perform STEP 2 (quote the law from
`docs/FIXED-FORM-ALGORITHM.md`, `docs/SPEC.md`, `docs/PORTING.md`,
`docs/SPEC-TABLES.md` — none of those files exist in the working tree), STEP 3
(read `git show HEAD:test/dart-tables/fixedform.dart` at base — HEAD has no tree),
or STEP 5 (grep `make/` and `Makefile` for a target that runs the new case — no
`make/` directory exists). Every one of the diff's assertions depends on base
context that is absent and unverifiable.

File and hunks needed:
- `test/dart-tables/fixedform.dart` — the whole file at base, especially the
  context of the two added hunks (the new `twoLanesCase()` inserted at ~:1165 and
  the `twoLanesCase();` call inserted in `main()` at ~:2064). The added code
  references base identifiers whose existence and semantics I cannot check:
  `fu1home.TableFixedLane.{lanes,op,guard,arg,meta,argW}`, `tableFixedNoGuard`,
  `TableFixedOp.{text,textUtf8}`, `fu1.fuRootFixedIdentity`,
  `fu1.fuRootFixedIdentityCount`, `fu1.fuRootFixedMeasure(1)`,
  `fu1.fuRootFixedSave`, `fu1.fuRootFixedLoad`, `fu1.fuRootFixedNewPlan`,
  `fu1decl.PickType.labelled`, `fu1home.FuRoot`, `fu1home.TableFixedReport`, and
  the file-local `check`, `text`, and `quiet` helpers.
- The law documents: `docs/FIXED-FORM-ALGORITHM.md` §4.1 fix 12 and §5.6,
  `docs/SPEC.md`, `docs/PORTING.md`, `docs/SPEC-TABLES.md` §3.4 — needed to judge
  the behavioural claims ("arg and meta are two lanes", "guard's width rides in its
  own lane", the retired cross-schema cases, the ordinal-2/second-arm assertion).
- `make/` + `Makefile` — needed to confirm the dart leg's gate actually runs
  `test/dart-tables/fixedform.dart` and therefore the new `twoLanesCase`.

What I could see from the diff alone (not a verdict): the new case is genuinely
called — `twoLanesCase()` is invoked from `main()` in the second hunk, so it is not
an uncalled test function; and its assertions are non-vacuous in shape (they compare
against scanned plan values and a live round-trip). Whether those assertions are
correct against FU1's actual schema and against the laws is precisely what the
missing base tree and documents would decide, and it cannot be decided here.