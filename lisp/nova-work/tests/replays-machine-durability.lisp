;;;; replays-machine-durability.lisp --- `machine --register/--retire` is a
;;;; journaled command.
;;;;
;;;; docs/SPEC-WORK.md:327 orders a mutation journal-durable THEN applied;
;;;; :1056-1058 rules the `fleet` section "indexes in the resident model under
;;;; the one writer and the one journal"; :2603-2616 rule 6 makes a mutation
;;;; outside the command loop a defect. `machine --register` wrote the member
;;;; and opened its allocator from the caller's thread with no event, no
;;;; envelope, no journal record and no revision, so none of the three held.
;;;;
;;;; EVERY CASE HERE RUNS AGAINST THE REAL FILE JOURNAL, not the permissive
;;;; `ordering-journal` fake: the fake accepts what `open-file-journal` refuses
;;;; and it is what hid this whole class in the first place.

(in-package #:nova-work/tests)

(defun machine-durability-request (&key (id "m-a1") (name "studio") (owner "glenn")
                                        (connect "profile:studio") (roles '(:build :test))
                                        (permits '("go-test")) (excludes '("bench:schema"))
                                        (limits '(:concurrent 4)) (facts nil)
                                        (declared-by "glenn") (request "mreq-1")
                                        (change :register)
                                        (stamp "2026-09-15T01:05:00Z"))
  (list :verb :machine :change change :machine id :name name :owner owner
        :connect connect :roles roles :permits permits :excludes excludes
        :limits limits :facts facts :declared-by declared-by
        :request request :stamp stamp :clock :tool :generation-owner "gen-1"))

(defmacro with-machine-journal ((k j &key (name "machine") (seed '*seed*)) &body body)
  "A kernel over a REAL file journal at a fresh path, closed on the way out.
PATH is bound too, so a case can reopen the same journal and replay it."
  (let ((digest (gensym "DIGEST")))
    `(let* ((path (test-journal-path ,name))
            (,digest (root-digest (make-seed-state ,seed)))
            (init-digest ,digest)
            (,j (open-file-journal path :initial-state-hash ,digest))
            (,k (make-kernel :state (make-seed-state ,seed) :journal ,j
                             :friends '("glenn" "rowan"))))
       (declare (ignorable init-digest))
       (unwind-protect (progn ,@body)
         (ignore-errors (close-file-journal ,j))))))

;;; ------------------------------------------------------------------
;;; 1. The register is a record in the journal, and the member comes back
;;; ------------------------------------------------------------------

(deftest "a-machine-register-is-a-journaled-command-and-survives-a-replay"
    "docs/SPEC-WORK.md:327"
    "expected=one-durable-record;recorded-line-is-the-receipt;member-recovered-by-replay-journal"
  (with-machine-journal (k j :name "machine-replay")
    (multiple-value-bind (okp line) (submit k (machine-durability-request :request "mreg-1"))
      (ok okp "the register was refused: ~A" line)
      (ok (search "MACHINE OK" line) "the receipt is not a MACHINE OK: ~A" line)
      (multiple-value-bind (found digest recorded) (journal-lookup j "mreg-1")
        (declare (ignore digest))
        (ok found "the register wrote NO journal record")
        (check-string= line recorded "the recorded line is not the receipt"))
      (ok (fleet-member (kernel-fleet k) "m-a1") "the live register wrote no member"))
    ;; CLOSE, REOPEN, REPLAY. A member that is not in the journal is not in the
    ;; total order and a kernel rebuilt from the journal does not have it.
    (close-file-journal j)
    (let* ((j2 (open-file-journal path :initial-state-hash init-digest))
           (k2 (make-kernel :state (make-seed-state *seed*) :journal j2
                            :friends '("glenn" "rowan"))))
      (unwind-protect
           (progn
             (replay-journal j2 k2)
             (ok (fleet-member (kernel-fleet k2) "m-a1")
                 "the machine did not survive replay-journal"))
        (ignore-errors (close-file-journal j2))))))

;;; ------------------------------------------------------------------
;;; 2. Retire replays as still gone
;;; ------------------------------------------------------------------

(deftest "a-machine-retire-replays-as-still-gone"
    "docs/SPEC-WORK.md:327"
    "expected=retired-record-recorded;fleet-member-nil-after-replay;member-count-zero"
  (with-machine-journal (k j :name "machine-retire")
    (multiple-value-bind (okp line)
        (submit k (machine-durability-request :request "mret-1"))
      (ok okp "the register was refused: ~A" line))
    (multiple-value-bind (okp line)
        (submit k (machine-durability-request :change :retire :request "mret-2"))
      (ok okp "the retire was refused: ~A" line)
      (check-equal nil (fleet-member (kernel-fleet k) "m-a1")
                   "the retire left the member"))
    (close-file-journal j)
    (let* ((j2 (open-file-journal path :initial-state-hash init-digest))
           (k2 (make-kernel :state (make-seed-state *seed*) :journal j2
                            :friends '("glenn" "rowan"))))
      (unwind-protect
           (progn
             (replay-journal j2 k2)
             (check-equal nil (fleet-member (kernel-fleet k2) "m-a1")
                          "the retired member came back on replay")
             (check-equal 0 (fleet-member-count (kernel-fleet k2))
                          "the replay left a member behind"))
        (ignore-errors (close-file-journal j2))))))

;;; ------------------------------------------------------------------
;;; 3. The replay answer comes before any mutable state
;;; ------------------------------------------------------------------

(deftest "a-retried-register-answers-its-original-receipt-after-the-fleet-moved"
    "docs/SPEC-WORK.md:327"
    "expected=identical-payload-answers-the-recorded-line;no-second-record;revision-unmoved"
  (with-machine-journal (k j :name "machine-retry")
    (let (first-line)
      (multiple-value-bind (okp line)
          (submit k (machine-durability-request :request "mreq-1"))
        (ok okp "the register was refused: ~A" line)
        (setf first-line line))
      ;; The fleet moves under the request: a second machine is registered, the
      ;; revision advances, the history grows.
      (ok (submit k (machine-durability-request :id "m-b2" :name "other"
                                                :connect "profile:other"
                                                :request "mreq-2"))
          "the intervening register was refused")
      (let ((rev (state-revision (kernel-state k)))
            (count (fleet-member-count (kernel-fleet k)))
            (hist (length (state-history (kernel-state k)))))
        ;; THE IDENTICAL REQUEST, RETRIED. The durable contract is about the
        ;; RECORDED payload and not about the fleet now.
        (multiple-value-bind (okp line)
            (submit k (machine-durability-request :request "mreq-1"))
          (ok okp "the retry was refused: ~A" line)
          (check-string= first-line line "the retry did not answer its original receipt"))
        (check-equal rev (state-revision (kernel-state k))
                     "the retry advanced the revision")
        (check-equal count (fleet-member-count (kernel-fleet k))
                     "the retry wrote a second member")
        (check-equal hist (length (state-history (kernel-state k)))
                     "the retry wrote a second event")))))

(deftest "a-machine-id-reused-with-a-different-payload-is-refused"
    "docs/SPEC-WORK.md:327"
    "expected=exit-1-reused-with-a-different-payload;nothing-written"
  (with-machine-journal (k j :name "machine-conflict")
    (multiple-value-bind (okp line)
        (submit k (machine-durability-request :request "mc-1"))
      (ok okp "the register was refused: ~A" line))
    (let ((rev (state-revision (kernel-state k)))
          (count (fleet-member-count (kernel-fleet k)))
          (hist (length (state-history (kernel-state k)))))
      (multiple-value-bind (okp line code)
          (submit k (machine-durability-request :name "renamed" :request "mc-1"))
        (ok (not okp) "a reused id with a different payload was accepted")
        (check-equal 1 code "the refusal is not exit 1")
        (ok (search "reused with a different payload" line)
            "the line does not name the reuse: ~A" line))
      (check-equal rev (state-revision (kernel-state k)) "the refusal advanced the revision")
      (check-equal count (fleet-member-count (kernel-fleet k))
                   "the refusal wrote a member")
      (check-equal hist (length (state-history (kernel-state k)))
                   "the refusal wrote an event"))))

;;; ------------------------------------------------------------------
;;; 4. A failed durable append writes nothing
;;; ------------------------------------------------------------------

(deftest "a-register-whose-journal-append-fails-leaves-no-member"
    "docs/SPEC-WORK.md:327"
    "expected=refusal;member-absent;revision-unmoved;history-unmoved"
  (let* ((path (test-journal-path "machine-prewrite"))
         (digest (root-digest (make-seed-state *seed*)))
         ;; The record-then-apply order is the whole point: a pre-write failure
         ;; must leave the state exactly as it was, not a member with no record.
         (j (open-file-journal path :initial-state-hash digest :fail-pre-write-on "mpw-1"))
         (k (make-kernel :state (make-seed-state *seed*) :journal j
                         :friends '("glenn" "rowan"))))
    (unwind-protect
         (let ((rev (state-revision (kernel-state k)))
               (hist (length (state-history (kernel-state k)))))
           (let ((condition nil))
             (multiple-value-bind (okp line)
                 (handler-case (submit k (machine-durability-request :request "mpw-1"))
                   (error (c) (setf condition c) (values nil (princ-to-string c))))
               (declare (ignore line))
               (ok (not okp) "a register survived a failed durable append")
               ;; The refusal must be THE INJECTED APPEND FAILURE. Without this
               ;; the case passes vacuously anywhere `machine` is not a journaled
               ;; command at all, which is exactly where it must fail.
               (ok condition
                   "the register was refused before the durable append was ever attempted, so this case proves nothing about the append")
               (ok (search "write or sync failed" (princ-to-string condition))
                   "the refusal is not the injected pre-write failure: ~A"
                   (princ-to-string condition))))
           (check-equal nil (fleet-member (kernel-fleet k) "m-a1")
                        "the failed append left a member")
           (check-equal rev (state-revision (kernel-state k))
                        "the failed append advanced the revision")
           (check-equal hist (length (state-history (kernel-state k)))
                        "the failed append wrote an event"))
      (ignore-errors (close-file-journal j)))))

;;; ------------------------------------------------------------------
;;; 5. The event id is the event's own revision, never the subject
;;; ------------------------------------------------------------------

(deftest "two-registers-of-one-machine-give-two-different-event-ids"
    "docs/SPEC-WORK.md:327"
    "expected=two-registers-of-one-id-differ-event-ids;id-is-no-function-of-the-subject"
  (with-machine-journal (k j :name "machine-ids")
    (let (first-id third-id)
      (multiple-value-bind (okp line code envelope)
          (submit k (machine-durability-request :request "mid-1"))
        (declare (ignore code))
        (ok okp "the first register was refused: ~A" line)
        (setf first-id (event-id (first (getf envelope :events)))))
      (ok (submit k (machine-durability-request :change :retire :request "mid-2"))
          "the retire was refused")
      (multiple-value-bind (okp line code envelope)
          (submit k (machine-durability-request :request "mid-3"))
        (declare (ignore code))
        (ok okp "the second register was refused: ~A" line)
        (setf third-id (event-id (first (getf envelope :events)))))
      (ok first-id "the first register wrote no event id")
      (ok third-id "the second register wrote no event id")
      (ok (not (string= first-id third-id))
          "two registers of one machine share one event id, so the id is a function of the subject: ~A" first-id))))

;;; ------------------------------------------------------------------
;;; 6. A restart over a reconstructed state keeps the fleet
;;;    (MEDIUM 2: make-kernel seeded CONFIG unconditionally, on whatever
;;;    state it was handed, so it threw away the fleet reconstruct-state
;;;    had just replayed)
;;; ------------------------------------------------------------------

(deftest "a-restart-over-a-reconstructed-state-keeps-the-fleet"
    "docs/SPEC-WORK.md:1578"
    "expected=reconstruction-replays-the-machine;restart-keeps-the-fleet;limits-permits-excludes-facts-and-allocator"
  (with-machine-journal (k j :name "machine-restart")
    (multiple-value-bind (okp line code)
        (submit k (machine-durability-request
                   :request "mreq-restart"
                   :limits '(:concurrent 4 :cores 8)
                   :permits '("go-test")
                   :excludes '("bench:schema")
                   :facts '(:arch "arm64" :os "macos" :declared-by "glenn")))
      (declare (ignore code))
      (ok okp "the register was refused: ~A" line))
    ;; Reconstruct exactly as src/transport.lisp:1095-1097 does, then hand THAT
    ;; state to a fresh make-kernel. Every seed state already carries a config,
    ;; so the reconstructed config is the one the restart must keep.
    (let* ((rebuilt (reconstruct-state
                     (canonical-string (state-canonical-form (kernel-state k)))))
           (k2 (make-kernel :state rebuilt :friends '("glenn" "rowan"))))
      (let ((m (fleet-member (kernel-fleet k2) "m-a1"))
            (alloc (gethash "m-a1"
                            (fleet-registry-allocators (kernel-allocations k2)))))
        (ok m "make-kernel threw away the reconstructed fleet: no member m-a1")
        (check-equal '(:concurrent 4 :cores 8) (machine-limits m)
                     "the declared limits did not survive the restart")
        (check-equal '("go-test") (machine-permits m)
                     "the declared permits did not survive the restart")
        (check-equal '("bench:schema") (machine-excludes m)
                     "the declared excludes did not survive the restart")
        (check-equal '(:arch "arm64" :os "macos" :declared-by "glenn")
                     (machine-facts m)
                     "the declared facts did not survive the restart")
        (ok alloc "the rebuilt allocator is missing")
        (check-equal 8 (fleet-allocator-cores alloc)
                     "the allocator's declared cores did not survive the restart")
        (check-equal 4 (fleet-allocator-concurrent alloc)
                     "the allocator's declared concurrency did not survive the restart")))))

;;; ------------------------------------------------------------------
;;; 7. The CONFIG deep copy (MEDIUM 1). These two are GREEN from the moment
;;;    they are written: they are the missing COVERAGE for a guarantee that
;;;    already holds, and control C1 (copy-work-config returning its argument)
;;;    is what crosses their bound.
;;; ------------------------------------------------------------------

(deftest "copy-work-config-shares-no-record-with-its-argument"
    "docs/SPEC-WORK.md:307"
    "expected=no-shared-machine-no-shared-allocator-no-shared-allocation-record-no-shared-container;mutation-isolated"
  (with-machine-journal (k j :name "machine-deep-copy")
    (ok (submit k (machine-durability-request :id "m-a1" :request "copy-m1"))
        "the first register was refused")
    (ok (submit k (machine-durability-request :id "m-a2" :name "lab" :connect "profile:lab"
                                              :owner "rowan" :request "copy-m2"))
        "the second register was refused")
    (ok (submit k (list :verb :take :machine "m-a1" :node "N1" :slots 1
                        :offer "offer-1" :attempt "attempt-1" :generation 1
                        :request-ref "op-1" :batch "batch-1" :request "copy-take-1"
                        :holder "rowan" :allocation-id "alloc-copy-1"))
        "the take was refused")
    (let* ((orig (nova-work::wstate-config (kernel-state k)))
           (copy (copy-work-config orig)))
      ;; The three containers are distinct objects.
      (ok (not (eq (work-config-fleet orig) (work-config-fleet copy)))
          "the fleet container is shared with the copy")
      (ok (not (eq (work-config-routes orig) (work-config-routes copy)))
          "the routes container is shared with the copy")
      (ok (not (eq (work-config-allocations orig) (work-config-allocations copy)))
          "the allocations container is shared with the copy")
      ;; No machine OBJECT is shared. A one-level copy that shares the member
      ;; still satisfies a container-only check; this is on the objects.
      (let ((shared nil))
        (dolist (m (fleet-members (work-config-fleet orig)))
          (let ((c (fleet-member (work-config-fleet copy) (machine-id m))))
            (when (or (null c) (eq m c)) (setf shared t))))
        (ok (not shared) "a machine object is shared with the copy"))
      ;; No allocator OBJECT is shared.
      (let ((shared nil))
        (maphash (lambda (id a)
                   (let ((c (gethash id (fleet-registry-allocators
                                         (work-config-allocations copy)))))
                     (when (or (null c) (eq a c)) (setf shared t))))
                 (fleet-registry-allocators (work-config-allocations orig)))
        (ok (not shared) "an allocator object is shared with the copy"))
      ;; No allocation-record OBJECT is shared.
      (let ((orig-records '()) (copy-records '()) (shared nil))
        (maphash (lambda (id a)
                   (declare (ignore id))
                   (setf orig-records (append orig-records
                                              (fleet-allocator-allocations a))))
                 (fleet-registry-allocators (work-config-allocations orig)))
        (maphash (lambda (id a)
                   (declare (ignore id))
                   (setf copy-records (append copy-records
                                              (fleet-allocator-allocations a))))
                 (fleet-registry-allocators (work-config-allocations copy)))
        (dolist (r orig-records)
          (when (member r copy-records :test #'eq) (setf shared t)))
        (ok orig-records
            "no allocation record was written, so the record copy is unverified")
        (ok (not shared) "an allocation record is shared with the copy"))
      ;; A copy that shares nothing but is never written to proves less than
      ;; one that is: writing the copied machine leaves the original alone.
      (let ((c (fleet-member (work-config-fleet copy) "m-a1")))
        (setf (machine-name c) "changed-in-the-copy")
        (check-equal "studio"
                     (machine-name (fleet-member (work-config-fleet orig) "m-a1"))
                     "writing the copy's machine changed the original's")))))

(deftest "a-failed-apply-after-the-machine-event-leaves-the-installed-fleet-untouched"
    "docs/SPEC-WORK.md:307"
    "expected=all-or-none;candidate-discarded;no-member-no-count-no-revision-no-history;installed-object-same"
  (with-machine-journal (k j :name "machine-atomic-apply")
    (ok (submit k (machine-durability-request :id "m-a1" :request "atomic-m1"))
        "the register was refused")
    (let* ((count (fleet-member-count (kernel-fleet k)))
           (rev (state-revision (kernel-state k)))
           (hist (length (state-history (kernel-state k))))
           (installed (fleet-member (kernel-fleet k) "m-a1"))
           ;; Event 1 mutates the CANDIDATE's CONFIG; event 2 makes apply-event
           ;; raise, so the candidate is discarded before it is installed.
           (machine-event
             (nova-work::%machine-canonical-event
              k (machine-durability-request :id "m-b2" :name "lab"
                                            :connect "profile:lab"
                                            :request "atomic-m2")
              :register))
           (bad-event
             (make-work-event
              :kind :revive :node "acme/work/f1/t1" :by "rowan"
              :fields (list :reason "atomic-bad")
              :stamp "2026-09-15T01:05:00Z" :clock :tool
              :request "atomic-bad" :generation-owner "gen-1"
              :rev (1+ (work-event-rev machine-event))
              :session-written-p nil))
           (condition nil))
      (handler-case
          (nova-work::%install-envelope k "atomic-rid-1" "atomic-digest-1"
                                        "MACHINE OK machine=m-b2"
                                        (list machine-event bad-event))
        (unsupported-input (c) (setf condition c)))
      ;; Anti-vacuity, as replays-lease-durability.lisp:160-164 does: without
      ;; this the case passes wherever nothing was applied at all.
      (ok condition
          "the two-event envelope did not signal, so no candidate was built and this case proves nothing")
      (ok (search "rule 18" (princ-to-string condition))
          "the refusal was not the second event's: ~A" (princ-to-string condition))
      (check-equal nil (fleet-member (kernel-fleet k) "m-b2")
                   "the discarded candidate leaked its machine into the installed fleet")
      (check-equal count (fleet-member-count (kernel-fleet k))
                   "the discarded candidate moved the installed fleet count")
      (check-equal rev (state-revision (kernel-state k))
                   "the discarded candidate moved the installed revision")
      (check-equal hist (length (state-history (kernel-state k)))
                   "the discarded candidate wrote a history record")
      (ok (eq installed (fleet-member (kernel-fleet k) "m-a1"))
          "the installed machine object was replaced"))))
