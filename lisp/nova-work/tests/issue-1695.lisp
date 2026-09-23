;;;; issue-1695.lisp --- nova-tools#1695: the six CONFIG/ACTIVE verbs mutate
;;;; outside the journal -- nothing survives a restart, and a retry is not
;;;; deduped.
;;;;
;;;; The receipt, measured at dev: `machine --register` and `route --register`
;;;; through `submit` answer OK, but `journal-order` is NIL and the journal
;;;; file holds its header alone; after a restart `replay-journal` answers
;;;; replayed-records=0 and the member count is 0; a retry of the same request
;;;; id and an identical body is refused by a MUTABLE PRECONDITION
;;;; ("connect held by m1") instead of being answered with the recorded
;;;; disposition docs/SPEC-WORK.md:315 promises, and the route retry
;;;; re-registers under the same event id. The cause: src/kernel.lisp `%submit`
;;;; returned from the six CONFIG/ACTIVE branches -- `machine`, `route`,
;;;; `take`, `heartbeat`, `release` and `probe` -- before the dedup lookup,
;;;; before `journal-accept` and before `journal-record`.
;;;;
;;;; This replay drives all six verbs through the one writer door over a real
;;;; file journal: each must be journaled, a retry of the same id and body must
;;;; be answered with the recorded line, the same id under a changed body must
;;;; be refused as a reuse, and a restart must find every CONFIG member and
;;;; every ACTIVE record again.

(in-package #:nova-work/tests)

(defun %issue-1695-machine-register (rid &key (machine "m1") (connect "profile:m1"))
  "One `machine --register` request (SPEC-WORK.md:3541), through `submit`."
  (list :verb :machine :change :register :machine machine :name "box"
        :owner "rowan" :connect connect :roles '(:build) :permits '("go-test")
        :excludes '() :limits '(:concurrent 4) :facts '() :declared-by "rowan"
        :request rid :stamp "2026-09-22T20:00:00Z" :clock :tool
        :generation-owner "gen-1"))

(defun %issue-1695-route-register (rid &key (route "r1"))
  "One `route --register` request (SPEC-WORK.md:2289), through `submit`."
  (list :verb :route :change :register :route route :provider "acme"
        :endpoint "https://api.example.com" :key-location '(:env "ACME_KEY")
        :plan :flat :owner "rowan" :request rid
        :stamp "2026-09-22T20:01:00Z" :clock :tool :generation-owner "gen-1"))

(defun %issue-1695-take (rid)
  "One `take` request (SPEC-WORK.md:3646), through `submit`."
  (list :verb :take :machine "m1" :node "n1" :slots 1 :offer "o1"
        :attempt "a1" :request-ref "ref-1" :batch "b1" :holder "rowan"
        :allocation-id "alloc-t" :now 0 :deadline 500 :request rid))

(defun %issue-1695-allocation (kernel id)
  "The allocation record named exactly once by ID, live or released."
  (find id (fleet-allocator-allocations
            (fleet-allocator-of (kernel-allocations kernel) "m1"))
        :key #'allocation-record-allocation-id :test #'equal))

(deftest "issue-1695" "nova-tools#1695"
    "nova-work E09: the six CONFIG/ACTIVE verbs mutate outside the journal — nothing survives a restart, and a retry is not deduped"
  (let* ((path (test-journal-path "issue-1695"))
         (digest (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash digest))
         (header-bytes 0)
         (machine-line "")
         (route-line "")
         (take-line ""))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state *seed*) :journal j
                               :friends '("rowan"))))
           (setf header-bytes (file-byte-count path))
           ;; Receipt 1: `machine --register` through the one writer door.
           (multiple-value-bind (ok line code)
               (submit k (%issue-1695-machine-register "req-m1"))
             (ok ok "the register was refused: ~A" line)
             (check-equal 0 code "machine register exit code")
             (check-equal 1 (fleet-member-count (kernel-fleet k))
                          "one member in memory")
             (setf machine-line line)
             ;; break 1: nothing was journaled -- the file held its header alone.
             (check-equal '("req-m1") (journal-order j)
                          "the register reached the journal's order")
             (ok (< header-bytes (file-byte-count path))
                 "the journal file still holds only its header: no record was written"))
           ;; break 3: the retry of the same id and an identical body must be
           ;; answered with the recorded disposition, never by the mutable
           ;; precondition the first register left behind.
           (multiple-value-bind (ok line code)
               (submit k (%issue-1695-machine-register "req-m1"))
             (ok ok "the retry of the same request id was refused: ~A" line)
             (check-equal 0 code
                          "the retry answers the recorded disposition, exit 0")
             (check-string= machine-line line
                            "the retry does not answer the recorded OK line"))
           ;; the other half of the two-part test: the same id under a changed
           ;; payload is a reuse conflict.
           (multiple-value-bind (ok line code)
               (submit k (%issue-1695-machine-register "req-m1"
                                                     :connect "profile:other"))
             (ok (not ok) "a reused id under a changed payload was accepted")
             (check-equal 1 code "the reuse refusal exits 1")
             (ok (search "reused with a different payload" line)
                 "the refusal does not name the reuse: ~A" line))
           ;; Receipt 2: `route --register`, which on dev journaled nothing,
           ;; re-registered on retry and answered under the same event id.
           (multiple-value-bind (ok line code)
               (submit k (%issue-1695-route-register "req-r1"))
             (ok ok "the route register was refused: ~A" line)
             (check-equal 0 code "route register exit code")
             (check-equal 1 (route-member-count (kernel-routes k))
                          "one route in memory")
             (setf route-line line)
             (check-equal '("req-m1" "req-r1") (journal-order j)
                          "the route register reached the journal's order"))
           (multiple-value-bind (ok line code)
               (submit k (%issue-1695-route-register "req-r1"))
             (ok ok "the route retry was refused: ~A" line)
             (check-equal 0 code "the route retry answers the recorded line")
             (check-string= route-line line
                            "the route retry does not answer the recorded line")
             (check-equal 1 (route-member-count (kernel-routes k))
                          "the route retry re-registered the member"))
           ;; The fleet's ACTIVE half: take, heartbeat, release and probe are
           ;; mutations of the same one writer and journal the same way.
           (multiple-value-bind (ok line code)
               (submit k (%issue-1695-take "req-take"))
             (ok ok "the take was refused: ~A" line)
             (check-equal 0 code "take exit code")
             (check-equal 1 (length (fleet-live-allocations (kernel-allocations k)
                                                           :machine "m1"))
                          "one live allocation")
             (setf take-line line)
             (check-equal '("req-m1" "req-r1" "req-take") (journal-order j)
                          "the take reached the journal's order"))
           (multiple-value-bind (ok line code)
               (submit k (%issue-1695-take "req-take"))
             (ok ok "the take retry was refused: ~A" line)
             (check-equal 0 code "the take retry answers the recorded line")
             (check-string= take-line line
                            "the take retry does not answer the recorded line")
             (check-equal 1 (length (fleet-live-allocations (kernel-allocations k)
                                                           :machine "m1"))
                          "the take retry applied a second allocation"))
           (multiple-value-bind (ok line code)
               (submit k (list :verb :heartbeat :allocation "alloc-t"
                               :generation 1 :now 100 :request "req-hb"))
             (ok ok "the heartbeat was refused: ~A" line)
             (check-equal 0 code "heartbeat exit code")
             (check-equal 1100 (allocation-record-deadline
                                (%issue-1695-allocation k "alloc-t"))
                          "the heartbeat renews the allocation's deadline"))
           (multiple-value-bind (ok line code)
               (submit k (list :verb :release :allocation "alloc-t"
                               :generation 1 :now 200 :request "req-rel"))
             (ok ok "the release was refused: ~A" line)
             (check-equal 0 code "release exit code")
             (check-equal 0 (length (fleet-live-allocations (kernel-allocations k)
                                                            :machine "m1"))
                          "the release frees exactly that allocation"))
           (multiple-value-bind (ok line code)
               (submit k (list :verb :probe :machine "m1" :slot 1
                               :source "ssh:m1" :fact :observed :at 50
                               :request "req-prb"))
             (ok ok "the probe was refused: ~A" line)
             (check-equal 0 code "probe exit code")
             (check-equal 1 (length (fleet-observations (kernel-allocations k)
                                                        :machine "m1"))
                          "one dated ACTIVE observation"))
           (check-equal '("req-m1" "req-r1" "req-take" "req-hb" "req-rel" "req-prb")
                        (journal-order j)
                        "all six verbs' requests in the journal's order"))
      (ignore-errors (close-file-journal j)))
    ;; RESTART: a fresh kernel over the same seed and the same journal file.
    ;; On dev this answered replayed-records=0 and members=0: nothing survived.
    (let* ((j2 (open-file-journal path :initial-state-hash digest))
           (k2 (make-kernel :state (make-seed-state *seed*) :journal j2
                            :friends '("rowan"))))
      (unwind-protect
           (progn
             (multiple-value-bind (kernel2 replayed-events replayed-records)
                 (replay-journal j2 k2)
               (declare (ignore kernel2))
               (check-equal 6 replayed-records
                            "the restart replayed the six journaled records")
               (check-equal 6 replayed-events
                            "the restart replayed the six journaled events")
               (check-equal 1 (fleet-member-count (kernel-fleet k2))
                            "the registered machine is gone after the restart")
               (let ((member (fleet-member (kernel-fleet k2) "m1")))
                 (ok member "the member m1 did not survive the restart")
                 (check-string= "profile:m1" (machine-connect member)
                                "the member's connect did not survive"))
               (ok (fleet-allocator-of (kernel-allocations k2) "m1")
                   "the machine's one allocator did not survive the restart")
               (check-equal 1 (route-member-count (kernel-routes k2))
                            "the registered route is gone after the restart")
               (let ((allocation (%issue-1695-allocation k2 "alloc-t")))
                 (ok allocation
                     "the ACTIVE allocation did not survive the restart")
                 (check-equal 1100 (allocation-record-deadline allocation)
                              "the renewed deadline did not survive the restart")
                 (ok (allocation-record-released allocation)
                     "the release did not survive the restart"))
               (check-equal 1 (length (fleet-observations (kernel-allocations k2)
                                                          :machine "m1"))
                            "the probe observation is gone after the restart"))
             ;; And the two-part retry AFTER the restart: the journal holds the
             ;; disposition, so the same id and body is answered with it and
             ;; nothing is applied a second time.
             (multiple-value-bind (ok line code)
                 (submit k2 (%issue-1695-machine-register "req-m1"))
               (ok ok "the post-restart retry was refused: ~A" line)
               (check-equal 0 code "the post-restart retry exits 0")
               (check-string= machine-line line
                              "the post-restart retry does not answer the recorded line")
               (check-equal 1 (fleet-member-count (kernel-fleet k2))
                            "the post-restart retry applied a second member"))
             (multiple-value-bind (ok line code)
                 (submit k2 (%issue-1695-take "req-take"))
               (ok ok "the post-restart take retry was refused: ~A" line)
               (check-equal 0 code "the post-restart take retry exits 0")
               (check-string= take-line line
                              "the post-restart take retry does not answer the recorded line")
               (check-equal 0 (length (fleet-live-allocations (kernel-allocations k2)
                                                             :machine "m1"))
                            "the post-restart take retry applied a second allocation")))
        (ignore-errors (close-file-journal j2))))
    (ignore-errors (delete-file path))))

;;; The record-failure boundary (Stella's HOLD on #2880, kernel.lisp
;;; %submit-config): the verb used to mutate the live CONFIG/ACTIVE registries
;;; and only then call `journal-record`, so a record that failed left a live
;;; member or allocation the journal never heard of. The verb now runs on a
;;; staged copy of the three registries, installed only after the record is
;;; durable: a record failure must leave the live state exactly as it was --
;;; the same guarantee the work path's record-then-apply order gives
;;; (SPEC-WORK.md:307).
(deftest "issue-1695-record-failure" "nova-tools#1695 (#2880 HOLD)"
    "nova-work E09: a CONFIG/ACTIVE verb whose journal record fails leaves no unjournaled live mutation"
  ;; CONFIG: `machine --register` whose record fails before the write.
  (let* ((path (test-journal-path "issue-1695-record-fail-machine"))
         (digest (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash digest
                                    :fail-pre-write-on "req-m-fail")))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state *seed*) :journal j
                               :friends '("rowan")))
               (signaled nil))
           (handler-case (submit k (%issue-1695-machine-register "req-m-fail"))
             (journal-uncertain-write () (setf signaled t)))
           (ok signaled "the failed record did not signal journal-uncertain-write")
           (check-equal '() (journal-order j) "the failed record reached the journal's order")
           (check-equal 0 (fleet-member-count (kernel-fleet k))
                        "a member was left live although its record failed")
           (ok (null (fleet-allocator-of (kernel-allocations k) "m1"))
               "an allocator was left live although its record failed"))
      (ignore-errors (close-file-journal j))
      (ignore-errors (delete-file path))))
  ;; ACTIVE: a `take` on a registered machine whose record fails.
  (let* ((path (test-journal-path "issue-1695-record-fail-take"))
         (digest (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash digest
                                    :fail-pre-write-on "req-take-fail")))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state *seed*) :journal j
                               :friends '("rowan")))
               (signaled nil))
           (multiple-value-bind (ok line) (submit k (%issue-1695-machine-register "req-m1"))
             (ok ok "the register was refused: ~A" line))
           (handler-case (submit k (%issue-1695-take "req-take-fail"))
             (journal-uncertain-write () (setf signaled t)))
           (ok signaled "the failed take record did not signal journal-uncertain-write")
           (check-equal '("req-m1") (journal-order j)
                        "the failed take reached the journal's order")
           (check-equal 1 (fleet-member-count (kernel-fleet k))
                        "the journaled member did not stay live")
           (check-equal 0 (length (fleet-live-allocations (kernel-allocations k)
                                                         :machine "m1"))
                        "an allocation was left live although its record failed"))
      (ignore-errors (close-file-journal j))
      (ignore-errors (delete-file path)))))
