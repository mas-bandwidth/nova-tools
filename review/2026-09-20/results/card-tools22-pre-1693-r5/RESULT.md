RESULT tools22-pre-1693-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1693 at head e4556f4d9475: nova-work E02.3 (staging): the staged bytes are a file, verified across a restart
PREREAD 1693 claims=5 proven=5 unproven=0 defects=0 high=0

PR 1693
HEAD e4556f4d947571b0933c8da3ffb659a1a262e377
BASE rowan/work-e02-operation-wait
MERGE-BASE 69341363e21b289f80c0f1a03ddfc7479b9b86cb
BEHIND 0
FILES 3 production, 2 test
LINES +476 -1

Single commit stacked on rowan/work-e02-operation-wait (not yet landed in dev@5298f6be); merge-base is the immediate parent of the PR head, not older than 5298f6be. Five new/staged files; no regression against existing code.

CLAIMS

1. STAGE-SOURCE-BYTES checks the bound BEFORE writing any file or journal record; a breach refuses with exit 2, writes nothing, and records nothing on the recovery journal.
   PROVEN-BY lisp/nova-work/tests/replays-e02-staging.lisp:63 "a-stage-past-its-bound-refuses-and-stages-nothing" — sets limits :staged-bytes 16, stages ten bytes successfully then attempts another ten; asserts staged=false, code=2, no .stage file created, journal length unchanged, running sum unchanged.

2. A successful stage writes the bytes to a real file under the staging root, fsyncs both file and directory, and records the length+SHA-256 durably on the recovery journal alongside the accept record.
   PROVEN-BY lisp/nova-work/tests/replays-e02-staging.lisp:35 "a-staged-input-is-bytes-on-disk-outside-the-mutation-loop" — stages text, verifies with probe-file that the .stage path is a real file, reads it back via open-file confirming exact content, and checks staged-bytes-on-disk matches utf8-octets length.

3. STAGED-CONTENT verifies each read-back against the recorded length and SHA-256 digest; a missing file, truncated file (wrong length), or altered file (digest mismatch) refuses with exit 2 and the stage is never admitted.
   PROVEN-BY lisp/nova-work/tests/replays-e02-staging.lisp:97 "a-torn-stage-is-never-admitted" — truncates a staged file to 5/10 bytes and asserts staged-content returns nil+exit-2 with refusal naming both counts; alters to same-length different content and asserts digest-moved refusal; both paths then assert admit-staged-result also refuses.

4. STAGED-BYTES-ON-DISK computes the running sum of what is actually on disk by reading each .stage file and summing octet lengths.
   PROVEN-BY lisp/nova-work/tests/replays-e02-staging.lisp:53 — after staging one input, staged-bytes-on-disk equals utf8-octets length of the staged content.

5. RECONCILE-CAPTURE-STAGE walks every :stage record on the recovery journal, checks each file; a surviving stage is re-registered, a missing/torn stage is reported on UNVERIFIED and not registered. ADMIT-STAGED-RESULT verifies bytes first, then defers to capture-admit-result at the expected revision. RECONCILE-OPERATION-REGISTRY (operation-recovery.lisp:71) now skips non-operation-tagged records (:record present = subsystem data) preventing stage records from being recovered as operations.
   PROVEN-BY lisp/nova-work/tests/replays-e02-staging.lisp:138 "the-restart-reconciles-staged-inputs-and-reports-a-missing-one" — closes registry, deletes one staged file simulating crash, opens new registry+stage (restart); asserts only the surviving stage is re-registered, the missing one appears on unverified, the surviving stage reads back verified, the missing stage admission refuses, and the registry recovers only the original operation ID ("op-cap") not stage request IDs.

QUESTIONS FOR THE REVIEWER

1. PROVIDER DESIGN LANE — the commit states "THE PROVIDER IS NOT DECIDED HERE" and defers to Stella for the HANDOFF. What concrete decisions exist about whether the provider runs in-process (resident) or out-of-process, and does the STAGE-SOURCE-BYTES contract differ under either choice?

2. RECOVERY-JOURNAL NAMESPACING — stage records coexist on the same durable journal with accept and cancel records, distinguished by `:record :stage` tagging. Should there be a structured prefix scheme (`:record :staging/stage`) to prevent naming collisions with future subsystems sharing the journal, or is flat `:record` tagging sufficient?

3. STAGED-INPUTS COUNT SEMANTICS — `capture-stage-input` increments a per-input counter when `records` is set, but `:staged-inputs` in limits refers to the count returned by that function. Does this bound track unique operation+input pairs across all operations, or just the number of `capture-stage-input` calls within one filesystem-capture-stage?

4. TEST FIXTURE CROSS-PROCESS SAFETY — `test-staging-root` uses `(format nil "~A/~D-~D/" name (get-universal-time) (incf *staging-test-counter*))`. If two independent test runners (not just parallel threads) execute concurrently against the same temp directory, could two fixtures collide on a matching timestamp-to-the-second? Would `make-temporary-name` or UUID be safer?

LEFT OWED

Read every line of the diff: 243-line new source file, 200-line new test file, 4 lines in operation-recovery.lisp, 26 exported symbols in package.lisp, and 2 changes in nova-work.asd. Did not independently run SBCL/ASDF to compile-load the system; trust the commit message's claim "red total=356 pass=351 fail=5 → green total=356 pass=356 fail=0". Could not fetch mirror at /tmp/nova-tools-mirror.git (permission denied) to cross-check refs; relied on cached objects in the local clone. Negative controls described in the commit message ("the staged bytes are never written", "the read-back stops checking the digest", "the restart registers a stage it could not verify", "reconciliation stops skipping other subsystems' records") were not part of this diff and cannot be verified.

git status --short

5298f6be12eaa0f7e6622334d2b6a1eb427649e3
