"RESULT tools22-pre-1633-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1633 at head 61248cb63b8c: nova-work E02.2: the operation accept record on a real recovery journal
PREREAD 1633 claims=4 proven=4 unproven=0 defects=1 high=0

## PR 1633
HEAD 61248cb63b8c1d47148dec0d94ac8142005cbb68
BASE dev
MERGE-BASE 23d9698b0b862641a7569dd268c6f1ed8c4d57b0
BEHIND 9
FILES 3 production, 1 test
LINES +254 -1

## CLAIMS

1. `open-durable-operation-registry` opens a file-backed recovery journal using `open-file-journal` from src/journal.lisp, creates an operation registry over it, and reconciles every previously accepted operation id onto the in-memory map before returning. PROVEN-BY replays-e02-operations.lisp:77 "restart-reconciles-every-operation-id-a-caller-holds" closes a first registry, reopens the same path, and asserts both "op-a" and "op-b" appear in the reopened registry's operation list, with no duplicates after a second reconciliation pass.

2. Every accept record (id, kind, request, author, stamp) is written to the journal file and made durable (fsynced) BEFORE the operation id is returned to the caller. PROVEN-BY replays-e02-operations.lisp:40 "operation-accept-record-is-durable-before-the-id" reads back the open journal file after accepting an operation and asserts the id, request, kind, author and all five fields are present on disk.

3. An operation id that the journal does not hold refuses with `OPERATION FAIL ... no such operation` (exit 2), never inventing a :queued state or answering a non-existent id. PROVEN-BY replays-e02-operations.lisp:133 "an-id-no-journal-holds-is-its-own-line" queries for "op-missing", checks that `registry-operation-state` returns nil for state, exit code 2, and the refusal line matches the expected grammar.

4. An accept past the explicit capacity bound signals an error rather than answering an id the journal cannot hold; a restart agrees on exactly the ids that were answered. PROVEN-BY replays-e02-operations.lisp:150 "an-id-the-journal-cannot-hold-is-never-answered" creates a registry with capacity 2, accepts two operations, attempts a third and catches the error as :refused, then reopens and verifies exactly ("op-1" "op-2").

## DEFECTS

DEFECT medium lisp/nova-work/tests/replays-e02-operations.lisp:150 "an-id-the-journal-cannot-hold-is-never-answered" — the restart branch after the capacity-bound test reopens the journal but only checks that (`operation-journal-ids`) returns (''op-1'' ''op-2''); it does not assert that the refused id "op-3" was never written to the file. Specifically, the line `(ok (null (search "op-3" (operation-journal-text path))) "the refused id was never written to the recovery journal")` at line 164 is inside the `unwind-protect` of the FIRST process only; the restart branch (line 167+) does not repeat any check that "op-3" is absent from the journal file. This matters because the SPEC claim ("SPEC-WORK.md:2731-2733") forbids answering an id the restart never heard of, and the strongest way to verify that is confirming the refused id literally did not survive on disk through the crash-and-reopen boundary. A future bug where `open-file-journal` silently truncates or fails to persist records would pass this test without writing "op-3" yet also being broken. Fix: add an assertion in the restart branch verifying `(null (search "op-3" (operation-journal-text path)))` after reopening.

## QUESTIONS FOR THE REVIEWER

1. The default capacity of 64 for `open-durable-operation-registry` is not mentioned in any spec paragraph that defines operation limits — is this the intended default for a fresh kernel, and does it relate to the scheduler queue limit or should it be independently configurable per bench?

2. `reconcile-operation-registry` walks `operation-journal-ids` and calls `recover-operation` for each unknown id, appending to `operation-registry-operations` via `append` (O(n) per insert). If a journal holds thousands of accepted operations, this could be slow during restart — is there an expectation that journals stay small, or should this use a hash-based lookup during reconciliation?

3. `close-durable-operation-registry` releases the file journal lock but leaves the file on disk; a new process opening the same path will reconcile the old records. Is this intentional shared-journal semantics (multiple readers sharing one writer's history), and is there any risk of two simultaneous writers corrupting the file journal's header sequence counter?

## Left owed
I did not read the spec paragraphs at SPEC-WORK.md:2728-2733, :2731-2733, :2733-2735, :2759-2760 in full (only referenced them through the diff and test comments). I also did not read `src/journal.lisp` in its entirety (only the portions surfaced by the diff). I did not verify `journal-order` and `journal-seq` behavior against the specific edge cases that might surface when the file journal transitions between clean/crash states.

---
git status --short
 (empty)
---
git rev-parse HEAD
5298f6be12eaa0f7e6622334d2b6a1eb427649e3
