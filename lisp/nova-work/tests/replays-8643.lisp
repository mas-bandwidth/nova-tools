;;;; replays-8643.lisp --- five acceptance replays for the dry run, the cost
;;;; lineage, savepoint compaction and copied journals of docs/SPEC-WORK.md.
;;;;
;;;; The names are the spec's own; each deftest sets up the state its paragraph
;;;; describes, drives the pure model the paragraph promises and asserts the
;;;; promised outcome. The live session, journal I/O and CLI wiring those
;;;; paragraphs sit on is out of this slice; where a name's paragraph fixes the
;;;; reading, the reading is stated in RESULT.md as `read: <name>: ...`.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; dry-run-writes-nothing                       SPEC-WORK.md:5678
;;; ------------------------------------------------------------------

(deftest "dry-run-writes-nothing" "docs/SPEC-WORK.md:5678"
    "expected=green-preview-mutates-nothing;request-still-new-to-dedup;counters-unchanged;accepted-mutation-moves-rev;stale-expect-refused-by-name;apply-at-current-rev-succeeds"
  (let* ((s (make-replay-session :revision 7 :events 4 :pending 1 :pushed 3))
         (before s))
    ;; a green preview at revision R validates and projects without mutating.
    (multiple-value-bind (line after) (preview-mutation s '(:id "req-preview" :expect 7))
      (ok (search "SESSION OK" line) "the preview answers OK: ~A" line)
      (ok (search "dry-run=true" line) "the preview is marked dry-run: ~A" line)
      (check-equal before after "the preview leaves the session state unchanged")
      (check-equal nil (member "req-preview" (getf after :dedup) :test #'equal)
                   "the preview leaves its request id new to the dedup index")
      (check-equal 4 (getf after :events) "events= is unchanged by the preview")
      (check-equal 1 (getf after :pending) "pending= is unchanged by the preview")
      (check-equal 3 (getf after :pushed) "pushed= is unchanged by the preview"))
    ;; an accepted mutation at R moves the revision to R+1.
    (multiple-value-bind (ok1 line1 s1) (apply-mutation s '(:id "req-real" :expect 7))
      (ok ok1 "a real apply at the current revision is admitted: ~A" line1)
      (check-equal 8 (getf s1 :revision) "the accepted mutation moves the revision to R+1")
      (check-equal 5 (getf s1 :events) "the accepted mutation appends one event")
      (ok (member "req-real" (getf s1 :dedup) :test #'equal)
          "the accepted mutation records its request id")
      ;; then a real apply --expect R is refused `stale` by name.
      (multiple-value-bind (ok2 line2 s2) (apply-mutation s1 '(:id "req-late" :expect 7))
        (check-equal nil ok2 "an apply at the stale revision is refused")
        (ok (search "stale" line2) "the stale refusal names stale: ~A" line2)
        (check-equal s1 s2 "the stale refusal writes nothing")
        ;; but an apply at the current R+1 is newly validated and may succeed.
        (multiple-value-bind (ok3 line3 s3) (apply-mutation s1 '(:id "req-next" :expect 8))
          (ok ok3 "an apply at the current revision is newly validated: ~A" line3)
          (check-equal 9 (getf s3 :revision) "the apply at R+1 moves the revision again"))))))

;;; ------------------------------------------------------------------
;;; complete-cost-lineage                         SPEC-WORK.md:4852
;;; ------------------------------------------------------------------

(deftest "complete-cost-lineage" "docs/SPEC-WORK.md:4852"
    "expected=parent-child-retry-join-once;failed-attempts-count;cache-subsets-do-not-double-count;implementation-separate;gaps-unknown"
  (let ((joined (join-cost-lineage
                 '((:id "p1" :role :parent :cost 10)
                   (:id "c1" :role :child :of "p1" :cost 5)
                   (:id "r1" :role :retry :of "c1" :attempt :failed :cost 3)
                   (:id "cache1" :role :cache-subset :of "c1" :cost 4)
                   (:id "impl1" :role :implementation :cost 7)
                   (:id "c1" :role :child :of "p1" :cost 5)))))
    (check-equal 18 (getf joined :operational)
                 "parent, child and a failed retry join once into the operational cost")
    (check-equal 1 (getf joined :failed) "a failed attempt is counted")
    (check-equal 1 (getf joined :cache-merged)
                 "a cache subset is joined but never added a second time")
    (check-equal 7 (getf joined :implementation)
                 "implementation cost stays separate from operational cost")
    (check-equal 5 (getf joined :joined)
                 "a repeated receipt id joins exactly once"))
  ;; a gap remains unknown rather than reading as zero or a partial sum.
  (let ((joined (join-cost-lineage
                 '((:id "p1" :role :parent :cost 10)
                   (:id "x1" :role :child :cost :unknown)))))
    (check-equal :unknown (getf joined :operational)
                 "a gap leaves the joined cost unknown, never zero")
    (check-equal 1 (getf joined :gaps) "the gap is counted, not hidden")))

;;; ------------------------------------------------------------------
;;; compaction-keeps-the-last-copy                SPEC-WORK.md:5791
;;; ------------------------------------------------------------------

(deftest "compaction-keeps-the-last-copy" "docs/SPEC-WORK.md:5791"
    "expected=newest-verified-kept;only-copy-never-removed;unverified-pruned;nothing-verified-prunes-nothing"
  ;; with two verified copies, compaction keeps the newest and may drop the older.
  (let ((plan (compact-copies '((:id "sp-old" :verified t)
                                (:id "sp-new" :verified t)))))
    (check-equal '("sp-new") (getf plan :keep) "the newest verified copy is kept")
    (check-equal '("sp-old") (getf plan :pruned) "the older verified copy may be dropped"))
  ;; the only recoverable copy is never removed.
  (let ((plan (compact-copies '((:id "sp-only" :verified t)))))
    (check-equal '("sp-only") (getf plan :keep) "the only recoverable copy is kept")
    (check-equal '() (getf plan :pruned) "the only recoverable copy is never removed"))
  ;; an unverified copy is not recoverable and is pruned; the verified copy stays.
  (let ((plan (compact-copies '((:id "sp-bad" :verified nil)
                                (:id "sp-good" :verified t)))))
    (check-equal '("sp-good") (getf plan :keep) "the verified copy is kept")
    (ok (member "sp-bad" (getf plan :pruned) :test #'equal)
        "the unverified copy is pruned"))
  ;; with nothing verified, no recoverable copy exists and compaction removes nothing.
  (let ((plan (compact-copies '((:id "a" :verified nil)))))
    (check-equal '() (getf plan :pruned)
                 "compaction removes nothing when nothing is recoverable")))

;;; ------------------------------------------------------------------
;;; copied-journal-grants-nothing                 SPEC-WORK.md:6017
;;; ------------------------------------------------------------------

(deftest "copied-journal-grants-nothing" "docs/SPEC-WORK.md:6017"
    "expected=restore-inspects-in-isolation;no-ownership-no-dispatch;start-over-copy-fenced;journal-id-notwithstanding;image-and-replies-loaded"
  (let* ((records (list (list :seq 1 :request "r1" :reply '("OK r1") :payload-sha256 "p1" :events '(1))
                        (list :seq 2 :request "r2" :reply '("OK r2") :payload-sha256 "p2" :events '(2))))
         (journal (make-journal-chain
                   :id "j-abc"
                   :segments (list (make-journal-segment :path "journal.1"
                                                         :header '() :records '()))))
         (sp (savepoint-store-verified
              (savepoint-write (make-savepoint-store) "sp-1" 2 1 records :journal journal)))
         (copy (list :journal-id "j-abc" :savepoint sp :bench "bench-a"
                     :journal journal :image (savepoint-image-events 1 records)
                     :replies (savepoint-retained-replies records)
                     :records records))
         (restored (restore-copy copy)))
    (check-equal "j-abc" (getf restored :journal-id) "the copy's journal id is read")
    (check-equal :read-only (getf restored :isolation)
                 "a copied restore inspects in isolation")
    (check-equal nil (getf restored :ownership) "a copied restore takes no ownership")
    (check-equal 0 (getf restored :dispatches) "a copied restore dispatches nothing")
    (check-equal 0 (getf restored :side-effects)
                 "a copied restore duplicates no external side effect")
    ;; the very same isolated read-only session a local restore opens, with the
    ;; image and its retained replies loaded and nothing dispatched.
    (let ((session (getf restored :session)))
      (ok session "the copied restore opened a session")
      (ok (restore-session-read-only-p session) "the copied session is read-only")
      (check-equal (savepoint-image-events 1 records) (restore-session-image session)
                   "the copied session loaded the image")
      (check-equal '(2) (mapcar (lambda (r) (getf r :seq))
                                (restore-session-replayed-records session))
                   "the copied session replayed the records after the cut"))
    ;; a session start over the copy is refused by the fencing rules.
    (multiple-value-bind (ok line code) (start-over-copy copy)
      (check-equal nil ok "a session start over a copy is refused")
      (check-equal 1 code "the fencing refusal is exit 1")
      (ok (search "fence" line) "the refusal names the fencing rules: ~A" line))
    ;; the journal id notwithstanding: an exact id match is still refused.
    (multiple-value-bind (ok line) (start-over-copy (list :journal-id "j-abc"))
      (check-equal nil ok "a matching journal id grants no start")
      (ok (search "fence" line) "the refusal still names the fencing rules: ~A" line))))

;;; ------------------------------------------------------------------
;;; cost-joins-include-the-coordinator           SPEC-WORK.md:6379
;;; ------------------------------------------------------------------

(deftest "cost-joins-include-the-coordinator" "docs/SPEC-WORK.md:6379"
    "expected=coordinator-overhead-and-rework-join;elapsed-attributed-by-phase;unobservable-stays-unknown;implementation-sunk;comparable-only-after-adoption"
  (let ((joined (join-experiment-cost
                 '(:id "exp-1" :after-adoption t
                   :coordinator-overhead 4 :rework 3
                   :execution 10 :queueing 2 :review 5 :ci-waiting 1
                   :implementation-cost 100))))
    (check-equal 25 (getf joined :operational)
                 "coordinator overhead and rework join the operational cost")
    (check-equal 4 (getf joined :coordinator-overhead)
                 "the coordinator overhead is joined, not omitted")
    (check-equal 3 (getf joined :rework) "the rework is joined, not omitted")
    (check-equal '((:execution . 10) (:queueing . 2) (:review . 5) (:ci-waiting . 1))
                 (getf joined :phases)
                 "elapsed time is attributed to each observable phase")
    (check-equal 100 (getf joined :implementation)
                 "implementation cost is recorded")
    (check-equal t (getf joined :implementation-sunk)
                 "implementation cost is sunk and kept apart")
    (ok (not (member 100 (mapcar #'cdr (getf joined :phases))))
        "implementation cost is not attributed to an operational phase")
    (check-equal t (getf joined :comparable)
                 "an after-adoption experiment is comparable work"))
  ;; unobservable elapsed time stays unknown rather than reading as zero.
  (let ((joined (join-experiment-cost
                 '(:id "exp-2" :after-adoption t
                   :coordinator-overhead 0 :rework 0
                   :execution :unknown :queueing 2 :review :unknown :ci-waiting 0
                   :implementation-cost 5))))
    (check-equal 2 (getf joined :operational)
                 "only observable cost joins the operational total")
    (check-equal 2 (getf joined :unknown-phases)
                 "each unobservable phase stays unknown, not zero")
    (check-equal '((:queueing . 2) (:ci-waiting . 0)) (getf joined :phases)
                 "unknown phases are absent from the attributed phases"))
  ;; the hypothesis run before adoption is not comparable work.
  (let ((joined (join-experiment-cost
                 '(:id "exp-3" :after-adoption nil :execution 1
                   :implementation-cost 50))))
    (check-equal nil (getf joined :comparable)
                 "the token-saving hypothesis is comparable only after adoption")))
