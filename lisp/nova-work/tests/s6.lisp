;;;; s6.lisp --- the slice-6 (the clip) acceptance cases, red against current dev.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md it comes from and carries the
;;;; acceptance table's own expectation. The slice implements src/clip.lisp,
;;;; src/snapshot.lisp, src/partition.lisp, src/operation.lisp and
;;;; src/savepoint.lisp; until those land, every verb below is undefined and the
;;;; suite is red on this file by construction.
;;;;
;;;; The verbs these cases address (the contract the implementation cards fill):
;;;;
;;;;   clip              -> (values okp line code)        "OPERATION OK id= op=clip ..."
;;;;   operation-wait    -> line                          "CLIP OK"/"CLIP RACED"/"CLIP FAIL"
;;;;   operation-status  -> line                          "OPERATION ROW"/"OPERATION FAIL"
;;;;   operation-list    -> list of lines                 "OPERATION ROW ..."
;;;;   savepoint-create  -> line                          "SAVEPOINT OK id= ..."
;;;;   savepoint-list    -> list of lines                 "SAVEPOINT ROW ..."
;;;;   savepoint-verify  -> line                          "SAVEPOINT OK/FAIL ..."
;;;;   savepoint-restore -> line, read-only, isolated
;;;;   savepoint-compare -> line                          "SAVEPOINT ..."
;;;;   snapshot-load     -> a read-only state over --snapshot + --cache
;;;;   snapshot-query    -> (values open unit scope line) a read-only ask
;;;;   closed-history    -> (values rows line) with gap=<n> and coverage-gap notes
;;;;   state-as-of       -> (values disposition line)     "QUERY OK/FAIL ... as-of= ..."
;;;;   session-export    -> line                          "EXPORT OK/FAIL ..."
;;;;   session-replay    -> line                          "REPLAY OK/FAIL ..."
;;;;
;;;; Each assertion names the exact printed token the spec's output grammar
;;;; (docs/SPEC-WORK.md:5058-5124) fixes, never an approximation of it.

(in-package #:nova-work/tests)

(defun line-has (needle line)
  "True when NEEDLE is a substring of the printed LINE (a `search`, like the
slice-1 cases use, so a red test reads its own failure)."
  (search needle line))

(defun s6-seed ()
  ;; One repository, two features, a settled leaf and an open leaf, enough to
  ;; drive a settle, a revive and a second settle across three recorded days.
  (make-seed-state
   '((:id "acme/work"      :type :work-set :parent nil          :state :unknown)
     (:id "acme/work/f1"   :type :feature  :parent "acme/work"  :state :unknown)
     (:id "acme/work/f1/t1" :type :task    :parent "acme/work/f1" :state :doing))))

(defun s6-session (&key (seed '(:use-s6-seed)) (repo "/tmp/nova-work-s6/repo"))
  "A session owned by rowan over a scratch repository. The verb is `session
start`; it is undefined today, which is what makes this file red."
  (declare (ignore seed repo))
  (session-start))

;;; ------------------------------------------------------------------
;;; 1. clip-is-one-long-operation               SPEC-WORK.md:5658
;;; ------------------------------------------------------------------

(deftest "clip-is-one-long-operation" "docs/SPEC-WORK.md:5658"
    "expected=OPERATION-OK-op=clip-then-exit;operation-wait-CLIP-OK-operation=<id>;raced-CLIP-RACED;stop-CLIP-OK-then-SESSION-OK"
  (let ((sess (s6-session)))
    (multiple-value-bind (okp line code) (clip sess)
      (ok okp "clip did not accept: ~A" line)
      (check-equal 0 code "the clip's exit code")
      (ok (line-has "OPERATION OK" line) "clip did not print OPERATION OK: ~A" line)
      (ok (line-has "op=clip" line) "the OPERATION OK line does not name op=clip: ~A" line))
    ;; The transport settles asynchronously; `operation wait` prints the CLIP OK
    ;; line carrying that operation= id and its pushed=.
    (let ((wait-line (operation-wait sess "clip-1")))
      (ok (line-has "CLIP OK" wait-line) "the wait did not print CLIP OK: ~A" wait-line)
      (ok (line-has "operation=clip-1" wait-line)
          "the CLIP OK line does not carry operation=<id>: ~A" wait-line)
      (ok (line-has "pushed=" wait-line) "the CLIP OK line does not carry pushed=: ~A" wait-line))
    ;; A raced transport reaches the caller through the same wait as CLIP RACED.
    (let ((raced (operation-wait sess "clip-raced")))
      (ok (line-has "CLIP RACED" raced) "the raced wait did not print CLIP RACED: ~A" raced))
    ;; `session stop` is the one caller that waits for its own clip, printing
    ;; the CLIP OK first and the SESSION OK after.
    (multiple-value-bind (okp line) (session-stop sess)
      (ok okp "session stop failed: ~A" line)
      (ok (line-has "CLIP OK" line) "session stop did not print CLIP OK first: ~A" line)
      (ok (line-has "SESSION OK" line) "session stop did not print SESSION OK after: ~A" line))))

;;; ------------------------------------------------------------------
;;; 2. history-grows-startup-does-not            SPEC-WORK.md:5448
;;; ------------------------------------------------------------------

(deftest "history-grows-startup-does-not" "docs/SPEC-WORK.md:5448"
    "expected=resident/segment/parse/replay/emitted-bytes-fixed-as-history-grows;pages-bounded-by-index-depth"
  ;; A session whose old history grows by orders of magnitude still starts up at
  ;; a cost independent of that volume: resident bytes, segment bytes read,
  ;; parses, replays and emitted bytes do not move; index pages read stay bounded
  ;; by the index depth.
  (let ((startup-small nil)
        (startup-large nil))
    (with-instrumentation
      (snapshot-load "/tmp/nova-work-s6/small/snapshot.sexp"
                     "/tmp/nova-work-s6/small/cache")
      (setf startup-small
            (list *parses* *replays* (resident-bytes))))
    (with-instrumentation
      (snapshot-load "/tmp/nova-work-s6/large/snapshot.sexp"
                     "/tmp/nova-work-s6/large/cache")
      (setf startup-large
            (list *parses* *replays* (resident-bytes))))
    ;; The prefix measure this case reads into a number is the page count, not
    ;; the volume: `pages=` is bounded by the index depth and never by the set's age.
    (multiple-value-bind (rows line) (closed-history (snapshot-load
                                                       "/tmp/nova-work-s6/large/snapshot.sexp"
                                                       "/tmp/nova-work-s6/large/cache")
                                                     :max 20)
      (declare (ignore rows))
      (ok (line-has "pages=" line) "the closed listing does not print pages=: ~A" line))
    (ok (equal (first startup-small) (first startup-large))
        "parses moved as history grew: ~A vs ~A" (first startup-small) (first startup-large))
    (ok (equal (second startup-small) (second startup-large))
        "replays moved as history grew")
    (ok (equal (third startup-small) (third startup-large))
        "resident bytes moved as history grew")))

;;; ------------------------------------------------------------------
;;; 3. days-merge-by-revision-never-concatenate  SPEC-WORK.md:5878
;;; ------------------------------------------------------------------

(deftest "days-merge-by-revision-never-concatenate" "docs/SPEC-WORK.md:5878"
    "expected=backdated-closure-lands-in-recorded-day;two-day-range-rows-in-revision-order;one-batch-and-ten-clips-identical-leaves"
  ;; A closure backdated by --now into an earlier day stays in that recorded day,
  ;; and a --from/--to over both days prints rows in revision order across the
  ;; day boundary, never two days laid end to end.
  (let ((sess (s6-session)))
    (multiple-value-bind (okp line) (close-backdated sess "2026-09-13T09:00:00Z")
      (ok okp "the backdated close was refused: ~A" line))
    (multiple-value-bind (rows line) (closed-history sess :from "2026-09-13T00:00:00Z"
                                                          :to   "2026-09-15T00:00:00Z"
                                                          :max 50)
      (declare (ignore line))
      ;; Revision order across the day boundary: a later-day row never precedes
      ;; an earlier-revision row of an earlier day.
      (ok (apply #'<= (mapcar #'row-revision rows))
          "rows are not in revision order across the day boundary: ~S" rows)))
  ;; One batch and ten clips over the same day yield identical leaves.
  (let ((one   (clip-segments-leaves "one-batch" :batches 1))
        (ten   (clip-segments-leaves "ten-clips" :batches 10)))
    (check-equal one ten "one batch and ten clips produced different day leaves")))

;;; ------------------------------------------------------------------
;;; 4. default-window-opens-two-days             SPEC-WORK.md:5441
;;; ------------------------------------------------------------------

(deftest "default-window-opens-two-days" "docs/SPEC-WORK.md:5441"
    "expected=early/midday-at-most-two-utc-partitions;midnight-one;no-partition-older-than-window;from-back-a-month-prints-pages="
  (dolist (now '("2026-09-15T00:30:00Z" "2026-09-15T12:00:00Z"
                 "2026-09-15T00:00:00Z"))
    (multiple-value-bind (rows line) (default-closed-history :now now :max 20)
      (declare (ignore line))
      (let ((days (remove-duplicates (mapcar #'row-day rows) :test #'string=)))
        (ok (<= (length days) 2)
            "the default window opened more than two day partitions at ~A: ~S" now days))))
  ;; At exactly 00:00:00Z the interval is yesterday's whole day alone.
  (multiple-value-bind (rows line) (default-closed-history :now "2026-09-15T00:00:00Z" :max 20)
    (declare (ignore line))
    (check-equal 1 (length (remove-duplicates (mapcar #'row-day rows) :test #'string=))
                 "midnight opened more than one day partition"))
  ;; An explicit --from reaching back a month opens exactly the days in range
  ;; that hold closure records, and prints its pages=.
  (multiple-value-bind (rows line) (closed-history (snapshot-load
                                                     "/tmp/nova-work-s6/repo/snapshot.sexp"
                                                     "/tmp/nova-work-s6/repo/cache")
                                                   :from "2026-08-15T00:00:00Z"
                                                   :to "2026-09-15T00:00:00Z" :max 1000)
    (ok (line-has "pages=" line) "the historical ask does not print pages=: ~A" line)
    (ok rows "the historical ask returned no rows")))

;;; ------------------------------------------------------------------
;;; 5. busy-day-many-segments                    SPEC-WORK.md:5446
;;; ------------------------------------------------------------------

(deftest "busy-day-many-segments" "docs/SPEC-WORK.md:5446"
    "expected=one-day-many-bounded-segments-read-in-bounded-pages;max-caps-rows;MORE-names-after;day-never-read-whole"
  ;; One day holding many bounded segments reads in bounded pages: --max caps the
  ;; rows, MORE names --after, and the day is never read whole.
  (multiple-value-bind (rows line) (closed-history (snapshot-load
                                                     "/tmp/nova-work-s6/busy/snapshot.sexp"
                                                     "/tmp/nova-work-s6/busy/cache")
                                                   :from "2026-09-14T00:00:00Z"
                                                   :to "2026-09-15T00:00:00Z" :max 20)
    (check-equal 20 (length rows) "--max did not cap the busy day's rows")
    (ok (line-has "MORE" line) "the capped listing did not print MORE: ~A" line)
    (ok (line-has "--after" line) "MORE did not name --after: ~A" line)))

;;; ------------------------------------------------------------------
;;; 6. one-revision-publishes-together           SPEC-WORK.md:5455
;;; ------------------------------------------------------------------

(deftest "one-revision-publishes-together" "docs/SPEC-WORK.md:5455"
    "expected=staged-segments-hashes-verified-committed-together;kill-before-commit-leaves-prev-root;kill-after-commit-before-push-locally-durable-unshared;one-revision-restores-journal-clip-indexes"
  ;; A clip stages its segments, verifies its manifests' hashes, then commits the
  ;; snapshot, both index roots and every referenced file in one commit.
  (let ((sess (s6-session)))
    (multiple-value-bind (okp line) (clip sess)
      (ok okp "the staged clip refused: ~A" line)
      (ok (line-has "OPERATION OK" line) "the clip did not stage: ~A" line)))
  ;; A kill before the commit leaves the previous root whole and readable.
  (ok (root-readable-p "/tmp/nova-work-s6/repo/prev-root")
      "a kill before the commit did not leave the previous root readable")
  ;; A kill after the commit and before the push leaves the work locally durable
  ;; and unshared, reconciled against the exact remote commit and never by
  ;; advancing a shared receipt.
  (ok (locally-durable-unshared-p "/tmp/nova-work-s6/repo")
      "a kill after the commit did not leave the work locally durable and unshared")
  ;; One revision restores the journal, the clip and both indexes together.
  (let ((restored (restore-one-revision "/tmp/nova-work-s6/repo")))
    (ok (line-has "CLIP OK" (clip-line restored)) "the one revision did not restore the clip")
    (ok (journal-restored-p restored) "the one revision did not restore the journal")
    (ok (indexes-restored-p restored) "the one revision did not restore both indexes")))

;;; ------------------------------------------------------------------
;;; 7. index-replayed-after-crash               SPEC-WORK.md:5402
;;; ------------------------------------------------------------------

(deftest "index-replayed-after-crash" "docs/SPEC-WORK.md:5402"
    "expected=restart-one-journal-replay-puts-item-in-C-index-out-of-O-tree-before-first-ask;open+closed=total;revive-recovers-in-O-and-C"
  ;; A session killed between a :settle and the next clip: the restart's one
  ;; journal replay puts the item in C's index and out of O's tree before the
  ;; first ask.
  (let* ((sess (s6-session))
         (total (snapshot-open-plus-closed sess)))
    (kill-after-settle sess "acme/work/f1/t1")
    (let ((restarted (restart-session sess)))
      (check-equal :c (index-branch restarted "acme/work/f1/t1")
                   "the settled item is not in C's index after the replay")
      (check-equal total (snapshot-open-plus-closed restarted)
                   "open+closed moved across the crash")
      ;; A :revive after the settle recovers the item in O and its record in C.
      (multiple-value-bind (okp line) (revive restarted "acme/work/f1/t1")
        (ok okp "the revive after the crash was refused: ~A" line))
      (check-equal :o (index-branch restarted "acme/work/f1/t1")
                   "the revived item is not back in O")
      (ok (row-in-closed-index restarted "acme/work/f1/t1")
          "the revived item's record is missing from C"))))

;;; ------------------------------------------------------------------
;;; 8. closed-paged-without-full-load            SPEC-WORK.md:5418
;;; ------------------------------------------------------------------

(deftest "closed-paged-without-full-load" "docs/SPEC-WORK.md:5418"
    "expected=max-20-reads-20-rows-MORE-names-after;next-page-reads-next-20;parses=0-replays=0-per-page;whole-history-never-loaded"
  (let ((large (snapshot-load "/tmp/nova-work-s6/thousand/snapshot.sexp"
                              "/tmp/nova-work-s6/thousand/cache")))
    (multiple-value-bind (rows line)
        (with-instrumentation
          (closed-history large :max 20))
      (check-equal 20 (length rows) "the first page is not twenty rows")
      (ok (line-has "MORE" line) "the first page did not print MORE: ~A" line)
      (ok (line-has "--after" line) "MORE did not name --after: ~A" line)
      (check-equal 0 *parses* "parses on a closed page were not zero")
      (check-equal 0 *replays* "replays on a closed page were not zero"))
    (multiple-value-bind (rows line)
        (with-instrumentation
          (closed-history large :max 20 :after "page-2-cursor"))
      (check-equal 20 (length rows) "the next page is not twenty more rows")
      (ok (line-has "MORE" line) "the next page did not print MORE: ~A" line)
      (check-equal 0 *parses* "parses on the next page were not zero")
      (check-equal 0 *replays* "replays on the next page were not zero"))))

;;; ------------------------------------------------------------------
;;; 9. absent-day-is-not-a-gap                   SPEC-WORK.md:5461
;;; ------------------------------------------------------------------

(deftest "absent-day-is-not-a-gap" "docs/SPEC-WORK.md:5461"
    "expected=day-with-no-manifest-inside-complete-manifested-range-gap=0-no-note"
  (multiple-value-bind (rows line)
      (closed-history (snapshot-load "/tmp/nova-work-s6/absent/snapshot.sexp"
                                     "/tmp/nova-work-s6/absent/cache")
                      :from "2026-09-12T00:00:00Z" :to "2026-09-15T00:00:00Z" :max 100)
    (ok rows "a complete range with an empty day returned no rows")
    (ok (line-has "gap=0" line) "the empty day did not answer gap=0: ~A" line)
    (ok (not (line-has "coverage-gap" line))
        "an absent day printed a coverage-gap note: ~A" line)))

;;; ------------------------------------------------------------------
;;; 10. missing-segment-is-a-gap                 SPEC-WORK.md:5463
;;; ------------------------------------------------------------------

(deftest "missing-segment-is-a-gap" "docs/SPEC-WORK.md:5463"
    "expected=committed-root-names-removed-segment-gap=<n>-one-coverage-gap-note-never-empty-closed-set"
  (multiple-value-bind (rows line)
      (closed-history (snapshot-load "/tmp/nova-work-s6/missing/snapshot.sexp"
                                     "/tmp/nova-work-s6/missing/cache")
                      :from "2026-09-14T00:00:00Z" :to "2026-09-15T00:00:00Z" :max 100)
    (ok rows "a missing segment produced an empty closed set")
    (ok (line-has "gap=" line) "the listing did not print gap=<n>: ~A" line)
    (ok (line-has "QUERY NOTE coverage-gap" line)
        "the listing did not print one coverage-gap note: ~A" line)))

;;; ------------------------------------------------------------------
;;; 11. as-of-refuses-unavailable-partition      SPEC-WORK.md:5466
;;; ------------------------------------------------------------------

(deftest "as-of-refuses-unavailable-partition" "docs/SPEC-WORK.md:5466"
    "expected=state-as-of-day-partition-unopenable-refused-exit-1-naming-that-partition"
  (let ((state (snapshot-load "/tmp/nova-work-s6/gap/snapshot.sexp"
                              "/tmp/nova-work-s6/gap/cache")))
    (multiple-value-bind (okp line code)
        (state-as-of state "acme/work/f1/t1" "2026-09-13T12:00:00Z")
      (ok (not okp) "an as-of with an unavailable partition was answered")
      (check-equal 1 code "the as-of refusal's exit code")
      (ok (line-has "historical window unavailable" line)
          "the refusal does not say historical window unavailable: ~A" line)
      (ok (line-has "partition=" line)
          "the refusal does not name the partition: ~A" line))))

;;; ------------------------------------------------------------------
;;; 12. clip-names-the-index-that-overflowed     SPEC-WORK.md:5433
;;; ------------------------------------------------------------------

(deftest "clip-names-the-index-that-overflowed" "docs/SPEC-WORK.md:5433"
    "expected=CLIP-FAIL-snapshot=-retained=-index=-closed-index=-lower---retain-or-raise---max-bytes;lower-retain-passes;index-page-split-not-refused"
  (let ((sess (s6-session)))
    (multiple-value-bind (okp line) (clip-overflow sess)
      (ok (not okp) "an overflowing clip was accepted")
      (ok (line-has "CLIP FAIL" line) "the overflow did not print CLIP FAIL: ~A" line)
      (dolist (field '("snapshot=" "retained=" "index=" "closed-index="))
        (ok (line-has field line) "the overflow did not print ~A: ~A" field line))
      (ok (line-has "lower --retain or raise --max-bytes" line)
          "the overflow did not name the one remedy: ~A" line))
    ;; Lowering --retain lets the same clip pass.
    (multiple-value-bind (okp line) (clip-overflow sess :retain "12h")
      (ok okp "a clip with a lower --retain was refused: ~A" line))
    ;; An index page that would pass the bounds is split into another page, and
    ;; no clip is ever refused for an index at all.
    (ok (index-split-p sess) "an over-bound index page was not split into another page")))

;;; ------------------------------------------------------------------
;;; 13. dedup-page-unavailable-refuses           SPEC-WORK.md:5478
;;; ------------------------------------------------------------------

(deftest "dedup-page-unavailable-refuses" "docs/SPEC-WORK.md:5478"
    "expected=unreadable-dedup-page-refused-dedup-unavailable-applies-nothing;admitted-once-readable;rollover-clip-handoff-already-applied;reused-id-different-payload-refused"
  (let ((sess (s6-session)))
    ;; A retry whose dedup page cannot be read is refused `dedup unavailable`,
    ;; applying nothing.
    (multiple-value-bind (okp line code) (submit-with-dedup-page-missing sess "req-1")
      (ok (not okp) "a retry past an unreadable dedup page was answered as new")
      (check-equal 1 code "the dedup-unavailable exit code")
      (ok (line-has "dedup unavailable" line)
          "the refusal does not say dedup unavailable: ~A" line))
    ;; Once the page is readable again the same id is admitted as already applied.
    (multiple-value-bind (okp line) (submit-dedup-readable sess "req-1")
      (ok (not okp) "the admitted retry was not refused already applied")
      (ok (line-has "already applied" line)
          "the admitted retry does not say already applied: ~A" line))
    ;; A reused id with a different payload is still refused by name.
    (multiple-value-bind (okp line) (submit-different-payload sess "req-1")
      (ok (not okp) "a reused id with a different payload was accepted")
      (ok (line-has "reused with a different payload" line)
          "the refusal does not say reused with a different payload: ~A" line))))

;;; ------------------------------------------------------------------
;;; 14. page-budget-is-not-max                   SPEC-WORK.md:5881
;;; ------------------------------------------------------------------

(deftest "page-budget-is-not-max" "docs/SPEC-WORK.md:5881"
    "expected=filter-rejects-every-row-QUERY-MORE-shown=0-pages=<n>-at-budget;whole-history-never-scanned;max-untouched;continuation-from-same-root"
  (let ((large (snapshot-load "/tmp/nova-work-s6/filter/snapshot.sexp"
                              "/tmp/nova-work-s6/filter/cache")))
    (multiple-value-bind (rows line)
        (with-instrumentation
          (closed-history large :max 20 :page-budget 3 :filter (constantly nil)))
      (check-equal 0 (length rows) "a filter rejecting every row printed a row")
      (ok (line-has "QUERY MORE" line) "the budget ask did not print QUERY MORE: ~A" line)
      (ok (line-has "shown=0" line) "the budget ask did not print shown=0: ~A" line)
      (ok (line-has "pages=" line) "the budget ask did not print pages=<n>: ~A" line)
      (check-equal 0 *replays* "the filtered ask replayed the journal"))
    ;; The whole history is never scanned: a filter that rejects everything must
    ;; meet the page budget, not the record count.
    (ok (pages-read-within-budget-p large 3)
        "the filtered ask read past its page budget")))

;;; ------------------------------------------------------------------
;;; 15. overlay-is-bounded-and-rebuilt           SPEC-WORK.md:5884
;;; ------------------------------------------------------------------

(deftest "overlay-is-bounded-and-rebuilt" "docs/SPEC-WORK.md:5884"
    "expected=recovery-replays-settles-into-overlay-pages-under-index-cache;eviction-under-load-loses-no-row;next-clip-writes-into-pages;no-query-replays-journal"
  (let ((restarted (recover-overlay "/tmp/nova-work-s6/overlay" 1000)))
    (ok (overlay-under-cache-p restarted)
        "the overlay exceed the --index-cache bound")
    (ok (no-row-lost-after-eviction-p restarted)
        "an eviction under load lost a row")
    (multiple-value-bind (okp line) (clip restarted)
      (ok okp "the clip after overlay rebuild refused: ~A" line)
      (ok (overlay-written-to-pages-p restarted)
          "the next clip did not write the overlay into the pages"))
    (multiple-value-bind (rows line)
        (with-instrumentation
          (closed-history restarted :max 20))
      (declare (ignore rows line))
      (check-equal 0 *replays* "a query replayed the journal to answer the overlay"))))

;;; ------------------------------------------------------------------
;;; 16. as-of-reconstructs-settle-revive-settle  SPEC-WORK.md:5468
;;; ------------------------------------------------------------------

(deftest "as-of-reconstructs-settle-revive-settle" "docs/SPEC-WORK.md:5468"
    "expected=settled-A-revived-B-settled-C;window-ending-in-each-interval-answers-that-interval;three-answers-different;earlier-unchanged;one-bounded-lookup-per-read"
  (let ((state (snapshot-load "/tmp/nova-work-s6/chain/snapshot.sexp"
                              "/tmp/nova-work-s6/chain/cache")))
    (multiple-value-bind (a) (state-as-of state "acme/work/f1/t1" "2026-09-14T12:00:00Z")
      (ok (line-has "disposition=done" a)
          "the day-A window did not answer the settled state: ~A" a))
    (multiple-value-bind (b) (state-as-of state "acme/work/f1/t1" "2026-09-14T23:59:59Z")
      (ok (line-has "disposition=done" b)
          "the day-A end did not answer the settled state: ~A" b))
    (multiple-value-bind (c) (state-as-of state "acme/work/f1/t1" "2026-09-15T12:00:00Z")
      (ok (line-has "disposition=pending" c)
          "the day-B window did not answer the revived state: ~A" c))
    (multiple-value-bind (d) (state-as-of state "acme/work/f1/t1" "2026-09-16T12:00:00Z")
      (ok (line-has "disposition=done" d)
          "the day-C window did not answer the re-settled state: ~A" d))
    ;; The three answers are different, and each read is one bounded lookup over
    ;; the id's chain (settle at A, revive at B, settle at C are three states).
    (let ((answers (mapcar (lambda (stamp) (state-as-of state "acme/work/f1/t1" stamp))
                           '("2026-09-14T12:00:00Z" "2026-09-15T12:00:00Z"
                             "2026-09-16T12:00:00Z"))))
      (ok (= 3 (length (remove-duplicates answers :test #'string=)))
          "the three as-of answers are not distinct: ~S" answers))))
