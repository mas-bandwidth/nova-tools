RESULT tools22-pre-864-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#864 at head d727576bd90f: nova-bus: wake, verdict rows and close --older-than (class K of #828)

PREREAD 864 claims=4 proven=4 unproven=0 defects=0 high=0

PR 864
HEAD d727576bd90fd83a7073bce41f74d5f2d56dd598
BASE dev
MERGE-BASE 9e0ae911b262aac6dfc7cbc475f440d7e0626534
BEHIND 374
FILES 5 production, 2 test
LINES +911 -18

1. `wake` prints at most three lines (pin, one note, counts) without listing open notes
   PROVEN-BY cmd/nova-bus/wake_test.go:65 TestWakeIsThreeLinesOnADaySizedBus — asserts wake prints exactly 3 lines under 400 bytes over 200-note backlog

2. `receipt --verdict` writes a TSV row per target that machines can read without opening notes
   PROVEN-BY cmd/nova-bus/wake_test.go:176 TestReceiptVerdictWritesARowThatReceiptsReadsBack — asserts row is 4-field TSV, readable via `receipts`

3. `receipts` reads verdict rows without git, matching by id and path
   PROVEN-BY cmd/nova-bus/wake_test.go:195 TestReceiptVerdictWritesARowThatReceiptsReadsBack — asserts `receipts` returns rows matching id and path

4. `close --older-than` accepts window notation (3d, 2w, 36h) and computes instant from current clock
   PROVEN-BY cmd/nova-bus/wake_test.go:206 TestCloseOlderThanClosesExactlyTheNotesPastTheWindow — asserts window parsing, mutual exclusion with --before, and correct closing behavior

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. Is the 32-character limit on verdict words (VerdictMax) documented in SPEC.md as an explicit constraint, or should it be added?
2. The `wake` verb returns exit 3 for quiet runs—should this be added to the exit code table in SPEC.md alongside the 0/1/2 codes?
3. Should `receipts` accept the same --as flag as other verbs to filter rows by a specific participant, or is returning all lanes intentional?

Left owed
- internal/bus/rows.go and internal/bus/wake.go: not read in full (new files from this diff); reviewed only via diff hunks
- docs/SPEC.md: full content not read, only diff changes reviewed

git status --short
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
