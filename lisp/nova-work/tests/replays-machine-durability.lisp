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
