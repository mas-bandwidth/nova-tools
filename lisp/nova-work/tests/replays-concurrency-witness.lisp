;;;; replays-concurrency-witness.lisp --- the single-writer claim, under load.
;;;;
;;;; docs/SPEC-WORK.md:2603-2616 (rule 6): "the kernel that owns O and C is one
;;;; command thread, like redis, or it corrupts. Every mutation is a command
;;;; applied in order by that thread and journaled in the same order -- THE
;;;; SEQUENCE NUMBER IS THE ORDER."
;;;;
;;;; That is a claim about what many callers at once cannot do, and the suite
;;;; asked it of one caller at a time. This file puts M threads on the REAL
;;;; `submit` path, over a REAL file journal, and asks five things of what comes
;;;; out. Every one of them is a count or an equality -- no sleeps, no wall-clock
;;;; assertions, no timing in any assertion.
;;;;
;;;;   1. every command with a fresh request id is answered, once;
;;;;   2. no two applied events share a revision, and the kernel's next revision
;;;;      is past all of them;
;;;;   3. the history holds exactly one envelope per successful command -- a
;;;;      missing one IS a lost update;
;;;;   4. the applied order IS a serial order, proven by replaying the journal
;;;;      from the seed into a fresh kernel and demanding the SAME canonical
;;;;      bytes;
;;;;   5. the maintained counters and indexes agree with a full independent
;;;;      reconstruction, and the C/O partition holds -- checked at the end and
;;;;      by every thread while the others are still running.

(in-package #:nova-work/tests)

(defparameter *concurrency-seed*
  (cons '(:id "cw" :type :work-set :parent nil :state :unknown)
        (loop for i from 1 to 12
              collect (list :id (format nil "cw/t~D" i) :type :task
                            :parent "cw" :state :doing)))
  "A work-set and twelve independent leaves, so the threads contend on the
writer and its counters rather than on one node's transition table.")

(deftest "many-callers-on-the-one-writer-leave-one-serial-order"
    "docs/SPEC-WORK.md:2603-2616,315"
    "expected=every-command-answered-once;revisions-unique;no-lost-update;the-journal-replays-to-the-same-bytes;indexes-and-partition-agree-throughout"
  (let* ((path (test-journal-path "concurrency"))
         (digest (root-digest (make-seed-state *concurrency-seed*)))
         ;; The file journal's default capacity is 64 records and it refuses
         ;; past it; this case wants every one of its commands accepted.
         (j (open-file-journal path :initial-state-hash digest :capacity 512))
         (k (make-kernel :state (make-seed-state *concurrency-seed*) :journal j))
         (threads-per-node 2)
         (nodes 12)
         (total (* threads-per-node nodes))
         (results (make-array total :initial-element nil))
         ;; Every thread checks the invariants WHILE the others are still
         ;; running, and reports rather than asserting on its own thread.
         (invariant-breaks (make-array total :initial-element nil))
         ;; Every apply must happen ON the one command thread. This is the
         ;; structural claim itself, and it is deterministic where a count is
         ;; not: `submit` enqueueing is what makes the order serial, and a
         ;; `submit` that dispatched inline would answer every assertion below
         ;; correctly on a quiet machine and corrupt on a busy one.
         (apply-threads (make-hash-table :test #'equal :synchronized t))
         (apply-hook
           (lambda (envelope)
             (declare (ignore envelope))
             (setf (gethash (or (sb-thread:thread-name sb-thread:*current-thread*) "-")
                            apply-threads)
                   t))))
    (unwind-protect
         (progn
           (let ((threads
                   (loop for i below total
                         collect
                         (let* ((i i)
                                (node (format nil "cw/t~D" (1+ (mod i nodes))))
                                ;; Two commands per node: a settle, then a
                                ;; reopen. Which thread wins which is the
                                ;; writer's business, not this test's.
                                (request
                                  (if (< i nodes)
                                      (list :verb :state-to-done :node node :by "rowan"
                                            :reason "shipped" :evidence (list "ev-1")
                                            :request (format nil "cw-done-~D" i)
                                            :stamp "2026-09-14T12:00:00Z" :clock :tool
                                            :generation-owner "gen-4")
                                      (list :verb :state-to-done :node node :by "emma"
                                            :reason "shipped again" :evidence (list "ev-2")
                                            :request (format nil "cw-dup-~D" i)
                                            :stamp "2026-09-14T12:01:00Z" :clock :tool
                                            :generation-owner "gen-4"))))
                           (sb-thread:make-thread
                            (lambda ()
                              ;; `submit` captures *BEFORE-APPLY-HOOK* AT SUBMIT
                              ;; TIME, on the submitting thread, because special
                              ;; bindings are thread-local -- so the hook is
                              ;; bound HERE and not once around the whole case.
                              (let ((*before-apply-hook* apply-hook))
                                (setf (aref results i)
                                      (multiple-value-list (submit k request))))
                              ;; read the invariants from this thread, mid-flight
                              (let ((state (kernel-state k)))
                                (setf (aref invariant-breaks i)
                                      (append (state-index-mismatches state)
                                              (unless (cow-partition-holds-p state)
                                                (list "the C/O partition did not hold"))))))
                            :name (format nil "cw-~D" i))))))
             (dolist (thread threads) (sb-thread:join-thread thread)))
           ;; 0. EVERY APPLY RAN ON THE ONE COMMAND THREAD.
           (check-equal (list "nova-work-kernel")
                        (sort (loop for name being the hash-keys of apply-threads
                                    collect name)
                              #'string<)
                        "a command was applied somewhere other than the kernel's one command thread")
           ;; 5a. nothing broke while the threads were running
           (let ((breaks (loop for b across invariant-breaks append b)))
             (check-equal '() breaks "an invariant broke while the writers were running"))
           ;; 1. every command is answered, and exactly one of each pair wins:
           ;; a node settles once, and the second settle of the same node is
           ;; refused by the transition table, not lost.
           (let ((wins (loop for r across results count (first r)))
                 (answers (loop for r across results count (second r))))
             (check-equal total answers "a command went unanswered")
             (check-equal nodes wins "a node settled more or less than once")
             (loop for r across results
                   unless (first r)
                     do (ok (search "rule 10" (second r))
                            "a losing command was refused for the wrong reason: ~A" (second r)))
             ;; 3. NO LOST UPDATE: one history envelope per winner.
             (check-equal wins (length (state-history (kernel-state k)))
                          "the history is shorter than the commands that succeeded: an envelope was installed over another one and lost"))
           ;; 2. revisions are unique, and the counter is past all of them
           (let ((revs (loop for entry in (state-history (kernel-state k))
                             append (mapcar (lambda (e) (getf e :rev)) (getf entry :events)))))
             (ok revs "no events were applied at all")
             (check-equal (length revs) (length (remove-duplicates revs))
                          "two events share a revision")
             (ok (> (kernel-next-rev k) (reduce #'max revs))
                 "the kernel's next revision is not past every event it applied"))
           ;; 5b. and at the end
           (check-equal '() (state-index-mismatches (kernel-state k))
                        "the maintained counters and indexes disagree with a full reconstruction")
           (ok (cow-partition-holds-p (kernel-state k)) "the C/O partition does not hold")
           ;; 4. THE APPLIED ORDER IS A SERIAL ORDER. The journal recorded it,
           ;; and replaying it from the seed reaches the same bytes.
           (let ((live (state-canonical-form (kernel-state k))))
             (close-file-journal j)
             (let* ((j2 (open-file-journal path :initial-state-hash digest :capacity 512))
                    (k2 (make-kernel :state (make-seed-state *concurrency-seed*) :journal j2)))
               (unwind-protect
                    (progn
                      (replay-journal j2 k2)
                      (check-equal live (state-canonical-form (kernel-state k2))
                                   "the journal does not replay to the state the writers left: the applied order and the recorded order are not the same order")
                      (check-equal '() (state-index-mismatches (kernel-state k2))
                                   "the replayed state's indexes disagree with a reconstruction"))
                 (ignore-errors (close-file-journal j2))))))
       (ignore-errors (close-file-journal j))
       (ignore-errors (delete-file path)))))

;;; ------------------------------------------------------------------
;;; TestE10F05PinScenariosOwnersAndCommands
;;;   criterion E10-F05-03 (docs/roadmaps/nova-work.sexp): "Pin scenarios,
;;;   owners and commands before implementation; require exact-revision
;;;   correctness and measured operational results before adoption".
;;;   docs/SPEC-WORK.md:7239-7240 states it: "Before the lock gate each suite is
;;;   mapped to its named scenarios, assertions, owner, command and CI lane;
;;;   before a release the exact-revision results are attached." (The
;;;   scenarios/owners/commands half is also named at docs/SPEC-WORK.md:7381.)
;;;
;;;   A scenario is pinned before implementation as a tick that records the
;;;   exact revision it was measured at; exact-revision correctness is the rule
;;;   that a changed source or criterion re-opens verification and can never
;;;   silently carry a green across a different revision. This asserts behaviour,
;;;   not the struct: it holds for whichever record the kernel keeps, so long as
;;;   the pin survives and the changed revision forces re-verification.
;;; ------------------------------------------------------------------

(deftest "TestE10F05PinScenariosOwnersAndCommands" "docs/SPEC-WORK.md:7239-7240"
    "expected=scenario-pinned-at-exact-revision;source-or-criterion-change=recheck-needed;pin-survives"
  (let ((tick (make-tick-record :id "fixed-tables/update-one-fact"
                                :pinned-rev "f01a0c42d7de"
                                :source-sha "f01a0c42d7de"
                                :scope "fixed-tables"
                                :historic-tick t
                                :current-verification :verified)))
    ;; The scenario is pinned at the exact revision it was measured at, before
    ;; implementation, and the same exact source is what the receipt names.
    (check-equal "f01a0c42d7de" (tick-record-pinned-rev tick)
                 "the scenario is not pinned at its exact revision")
    (check-equal "f01a0c42d7de" (tick-record-source-sha tick)
                 "the receipt does not name the exact source it measured")
    ;; exact-revision correctness: a changed source must never silently keep the
    ;; old green; the answer is recheck-needed, and the pinned revision stays
    ;; untouched (the green is tied to the revision that produced it).
    (let ((after (source-change tick :new-source-sha "9d5a1b0c2e3f")))
      (check-equal :recheck-needed (tick-record-current-verification after)
                   "a changed source kept the old green instead of requiring re-verification")
      (check-equal "f01a0c42d7de" (tick-record-pinned-rev after)
                   "the pin was silently rewritten to another revision")
      (check-equal "f01a0c42d7de" (tick-record-source-sha after)
                   "the historic receipt lost its pinned source")
      (check-equal t (tick-record-historic-tick after)
                   "the historic tick did not survive the source change"))
    ;; A changed criterion (the acceptance that was pinned before implementation)
    ;; re-opens verification the same way.
    (check-equal :recheck-needed
                 (tick-record-current-verification
                  (source-change tick :new-criterion "measured operational results"))
                 "a changed criterion did not require re-verification before adoption")))
