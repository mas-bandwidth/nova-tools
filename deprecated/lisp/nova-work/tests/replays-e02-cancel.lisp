;;;; replays-e02-cancel.lisp --- the cancellation as a request of its own,
;;;; deduplicated on the durable recovery journal (AUDIT row E02.1).
;;;;
;;;;   cancel-is-a-request-not-an-erasure-on-the-journal   SPEC-WORK.md:2740-2744
;;;;   an-uncertain-external-effect-is-never-claimed-cancelled  SPEC-WORK.md:2743-2744
;;;;   a-cancellation-survives-a-restart                   SPEC-WORK.md:2717-2720, :2740-2743
;;;;   cancelling-an-id-no-journal-holds-is-its-own-line   SPEC-WORK.md:2733-2735, :5982
;;;;
;;;; The pure model of src/operations.lisp deduplicates a cancel against
;;;; WORK-SESSION-CANCELLATIONS, an in-process alist -- which is the thing the
;;;; paragraph above the cancel sentence forbids: the disposition is answered
;;;; "by the dedup predicate of Retention above and its bounded indexed pages --
;;;; never by an unbounded resident map of request ids" (:2719-2720). These
;;;; cases drive the cancel over the bounded recovery journal instead, and the
;;;; restart case closes the file and reopens it, because a cancellation that
;;;; does not survive the process is not a final disposition.

(in-package #:nova-work/tests)

(defun cancelled-registry (path &key (capacity 64))
  (open-durable-operation-registry path :capacity capacity))

;;; ------------------------------------------------------------------
;;; cancel-is-a-request-not-an-erasure-on-the-journal   :2740-2744
;;; ------------------------------------------------------------------

(deftest "cancel-is-a-request-not-an-erasure-on-the-journal" "docs/SPEC-WORK.md:2740-2744"
    "expected=cancel-record-durable;accept-record-not-erased;replay-cancels-once;a-fresh-request-id-re-acknowledges"
  (let* ((path (test-journal-path "operation-cancel"))
         (registry (cancelled-registry path)))
    (unwind-protect
         (progn
           (operation-accept registry :id "op-a" :kind "capture" :request "req-a"
                                      :author "rowan" :stamp "2026-09-19T12:00:00Z")
           (multiple-value-bind (disposition line code replayed)
               (registry-operation-cancel registry "op-a" :request "req-cancel-1"
                                                          :author "rowan"
                                                          :stamp "2026-09-19T12:00:05Z")
             (check-equal nil line "the cancellation is acknowledged, not refused")
             (check-equal 0 code "the acknowledgement exits 0")
             (check-equal nil replayed "the first cancel is not a replay")
             (check-equal :cancelled (getf disposition :state)
                          "the cancellation's own final disposition")
             (check-string= "req-cancel-1" (getf disposition :request)
                            "the acknowledgement carries the cancel's own request id"))
           ;; The cancel record is on the journal, durable before the
           ;; acknowledgement, exactly as the accept record is.
           (let ((on-disk (operation-journal-text path)))
             (ok (search "req-cancel-1" on-disk) "the cancel record is on disk")
             (ok (search "op-a" on-disk) "the cancel record names the operation it acknowledges"))
           ;; A cancellation erases no accepted mutation: the accept record for
           ;; op-a is still the record the journal holds for op-a.
           (let ((accept (accept-record-of (operation-registry-journal registry) "op-a")))
             (check-string= "capture" (getf accept :kind)
                            "the operation's accept record is not erased by its cancellation")
             (check-string= "req-a" (getf accept :request)
                            "and it still carries the request that accepted it"))
           ;; A cancel replayed twice cancels once: the same request id answers
           ;; the recorded disposition and writes nothing more.
           (let ((before (operation-journal-length registry)))
             (multiple-value-bind (disposition line code replayed)
                 (registry-operation-cancel registry "op-a" :request "req-cancel-1"
                                                            :author "rowan"
                                                            :stamp "2026-09-19T12:00:06Z")
               (check-equal nil line "the replayed cancel is acknowledged, not refused")
               (check-equal 0 code "the replayed cancel exits 0")
               (ok replayed "the replayed cancel says it is a replay")
               (check-equal :cancelled (getf disposition :state)
                            "the replayed cancel answers the recorded disposition"))
             (check-equal before (operation-journal-length registry)
                          "a cancel replayed twice writes one record"))
           ;; A different request id is a fresh acknowledgement of the
           ;; operation's final disposition, not a second cancellation.
           (multiple-value-bind (disposition line code replayed)
               (registry-operation-cancel registry "op-a" :request "req-cancel-2"
                                                          :author "stella"
                                                          :stamp "2026-09-19T12:00:07Z")
             (check-equal nil line "a fresh request id is acknowledged")
             (check-equal 0 code "a fresh request id exits 0")
             (check-equal nil replayed "a fresh request id is not a replay")
             (check-equal :cancelled (getf disposition :state)
                          "it re-acknowledges the same final disposition")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; an-uncertain-external-effect-is-never-claimed-cancelled  :2743-2744
;;; ------------------------------------------------------------------

(deftest "an-uncertain-external-effect-is-never-claimed-cancelled" "docs/SPEC-WORK.md:2743-2744"
    "expected=uncertain-not-cancelled;the-uncertainty-is-durable;a-later-cancel-does-not-overwrite-it"
  (let* ((path (test-journal-path "operation-uncertain"))
         (registry (cancelled-registry path)))
    (unwind-protect
         (progn
           (operation-accept registry :id "op-u" :kind "clip" :request "req-u"
                                      :author "rowan" :stamp "2026-09-19T12:00:00Z")
           (multiple-value-bind (disposition line code)
               (registry-operation-cancel registry "op-u" :request "req-cancel-u"
                                                          :author "rowan"
                                                          :stamp "2026-09-19T12:00:05Z"
                                                          :external-effect :uncertain)
             (check-equal nil line "an uncertain external effect is acknowledged, not refused")
             (check-equal 0 code "the acknowledgement exits 0")
             (check-equal :uncertain (getf disposition :state)
                          "an external effect that may have happened is never claimed cancelled"))
           (check-equal :uncertain
                        (getf (cdr (assoc "op-u" (operation-registry-operations registry)
                                          :test #'equal))
                              :state)
                        "the operation is left uncertain, not cancelled")
           ;; A later cancel under a new request id re-acknowledges the
           ;; uncertainty rather than converting it to a cancellation.
           (multiple-value-bind (disposition line code)
               (registry-operation-cancel registry "op-u" :request "req-cancel-u2"
                                                          :author "rowan"
                                                          :stamp "2026-09-19T12:00:06Z")
             (check-equal nil line "the second cancel is acknowledged")
             (check-equal 0 code "the second cancel exits 0")
             (check-equal :uncertain (getf disposition :state)
                          "an uncertain effect stays uncertain under a fresh request id")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-cancellation-survives-a-restart          :2717-2720, :2740-2743
;;; ------------------------------------------------------------------

(deftest "a-cancellation-survives-a-restart" "docs/SPEC-WORK.md:2717-2720,2740-2743"
    "expected=state-cancelled-after-reopen;dedup-answered-from-the-journal-not-a-resident-map"
  (let ((path (test-journal-path "operation-cancel-restart")))
    (let ((first (cancelled-registry path)))
      (unwind-protect
           (progn
             (operation-accept first :id "op-a" :kind "capture" :request "req-a"
                                     :author "rowan" :stamp "2026-09-19T12:00:00Z")
             (operation-accept first :id "op-b" :kind "export" :request "req-b"
                                     :author "rowan" :stamp "2026-09-19T12:00:01Z")
             (registry-operation-cancel first "op-a" :request "req-cancel-1"
                                                     :author "rowan"
                                                     :stamp "2026-09-19T12:00:05Z"))
        (close-durable-operation-registry first)))
    ;; The restart: nothing of the first process survives but the file.
    (let ((second (cancelled-registry path)))
      (unwind-protect
           (progn
             (check-equal :cancelled
                          (getf (cdr (assoc "op-a" (operation-registry-operations second)
                                            :test #'equal))
                                :state)
                          "the cancelled operation is still cancelled after the restart")
             (check-equal :queued
                          (getf (cdr (assoc "op-b" (operation-registry-operations second)
                                            :test #'equal))
                                :state)
                          "the operation that was not cancelled is not cancelled by the replay")
             ;; The dedup predicate is the journal's: a fresh process replaying
             ;; the same cancel request id cancels once and writes nothing.
             (let ((before (operation-journal-length second)))
               (multiple-value-bind (disposition line code replayed)
                   (registry-operation-cancel second "op-a" :request "req-cancel-1"
                                                            :author "rowan"
                                                            :stamp "2026-09-19T12:00:08Z")
                 (check-equal nil line "the restarted process acknowledges the replay")
                 (check-equal 0 code "the replay exits 0")
                 (ok replayed "the restarted process knows the request id from the journal")
                 (check-equal :cancelled (getf disposition :state)
                              "and answers the recorded disposition"))
               (check-equal before (operation-journal-length second)
                            "the replay after a restart writes no second record")))
        (close-durable-operation-registry second)))))

;;; ------------------------------------------------------------------
;;; cancelling-an-id-no-journal-holds-is-its-own-line  :2733-2735, :5982
;;; ------------------------------------------------------------------

(deftest "cancelling-an-id-no-journal-holds-is-its-own-line" "docs/SPEC-WORK.md:2733-2735,5982"
    "expected=OPERATION-FAIL-no-such-operation;exit=2;nothing-written"
  (let* ((path (test-journal-path "operation-cancel-unknown"))
         (registry (cancelled-registry path)))
    (unwind-protect
         (progn
           (operation-accept registry :id "op-a" :kind "capture" :request "req-a"
                                      :author "rowan" :stamp "2026-09-19T12:00:00Z")
           (let ((before (operation-journal-length registry)))
             (multiple-value-bind (disposition line code)
                 (registry-operation-cancel registry "op-missing" :request "req-cancel-x"
                                                                  :author "rowan"
                                                                  :stamp "2026-09-19T12:00:05Z")
               (check-equal nil disposition "an id no journal holds has no disposition")
               (check-string= "OPERATION FAIL id=op-missing op=- state=-: no such operation" line
                              "the refusal is the line the output grammar fixes")
               (check-equal 2 code "the refusal exits 2"))
             (check-equal before (operation-journal-length registry)
                          "a cancel of an id no journal holds writes nothing")
             (ok (null (search "req-cancel-x" (operation-journal-text path)))
                 "and its request id never reaches the journal")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-cancel-request-reused-with-a-different-payload-refuses  :2740-2743
;;; ------------------------------------------------------------------
;;;
;;; Stella's scheduler ruling (comment 5742253361 on #1676, answering the token
;;; this file flagged for review) fixes three things. The reason text is the
;;; kernel's own, byte for byte -- `reused with a different payload`, the
;;; spelling src/node-verbs.lisp:478, src/fleet.lisp:1210 and
;;; src/edit-undo.lisp:39 already carry -- inside the operation grammar's line,
;;; exit 2, with no new record and no state change. The compared fields are
;;; named rather than implied: the target operation AND the author; a later
;;; observation timestamp is not a new payload. And a cancel request id that
;;; collides with another record KIND on the same journal is a conflict too,
;;; not an invitation to write a cancel under an operation id.

(deftest "a-cancel-request-reused-with-a-different-payload-refuses" "docs/SPEC-WORK.md:2740-2743"
    "expected=changed-target-refuses;changed-actor-refuses;stamp-only-retry-replays;cross-record-kind-collision-refuses;journal-length-and-state-unchanged"
  (let* ((path (test-journal-path "operation-cancel-payload"))
         (registry (cancelled-registry path))
         (state-of (lambda (id)
                     (getf (cdr (assoc id (operation-registry-operations registry)
                                       :test #'equal))
                           :state))))
    (unwind-protect
         (progn
           (operation-accept registry :id "op-a" :kind "capture" :request "req-a"
                                      :author "rowan" :stamp "2026-09-19T12:00:00Z")
           (operation-accept registry :id "op-b" :kind "export" :request "req-b"
                                      :author "rowan" :stamp "2026-09-19T12:00:01Z")
           (registry-operation-cancel registry "op-a" :request "req-cancel-1"
                                                      :author "rowan"
                                                      :stamp "2026-09-19T12:00:05Z")
           ;; A changed TARGET under the same cancel request id.
           (let ((before (operation-journal-length registry))
                 (before-b (funcall state-of "op-b")))
             (multiple-value-bind (disposition line code replayed)
                 (registry-operation-cancel registry "op-b" :request "req-cancel-1"
                                                            :author "rowan"
                                                            :stamp "2026-09-19T12:00:06Z")
               (check-equal nil disposition "a changed target has no disposition")
               (check-string= "OPERATION FAIL id=op-b op=- state=-: reused with a different payload"
                              line "the refusal carries the kernel's own reason text")
               (check-equal 2 code "the refusal exits 2")
               (check-equal nil replayed "a refusal is not a replay"))
             (check-equal before (operation-journal-length registry)
                          "a changed target writes no record")
             (check-equal before-b (funcall state-of "op-b")
                          "and does not cancel the operation it named"))
           ;; A changed ACTOR under the same cancel request id and target.
           (let ((before (operation-journal-length registry)))
             (multiple-value-bind (disposition line code replayed)
                 (registry-operation-cancel registry "op-a" :request "req-cancel-1"
                                                            :author "stella"
                                                            :stamp "2026-09-19T12:00:07Z")
               (check-equal nil disposition "a changed actor has no disposition")
               (check-string= "OPERATION FAIL id=op-a op=- state=-: reused with a different payload"
                              line "the refusal carries the kernel's own reason text")
               (check-equal 2 code "the refusal exits 2")
               (check-equal nil replayed "a refusal is not a replay"))
             (check-equal before (operation-journal-length registry)
                          "a changed actor writes no record"))
           ;; The same semantic payload with a LATER observation timestamp is a
           ;; replay, not a new payload.
           (let ((before (operation-journal-length registry)))
             (multiple-value-bind (disposition line code replayed)
                 (registry-operation-cancel registry "op-a" :request "req-cancel-1"
                                                            :author "rowan"
                                                            :stamp "2026-09-19T23:59:59Z")
               (check-equal nil line "a stamp-only retry is acknowledged, not refused")
               (check-equal 0 code "a stamp-only retry exits 0")
               (ok replayed "a stamp-only retry says it is a replay")
               (check-equal :cancelled (getf disposition :state)
                            "and answers the original durable disposition"))
             (check-equal before (operation-journal-length registry)
                          "a stamp-only retry writes no second record"))
           ;; A cancel request id colliding with another record KIND: `op-a` is
           ;; the id of an ACCEPT record on this same journal.
           (let ((before (operation-journal-length registry))
                 (before-b (funcall state-of "op-b")))
             (multiple-value-bind (disposition line code replayed)
                 (registry-operation-cancel registry "op-b" :request "op-a"
                                                            :author "rowan"
                                                            :stamp "2026-09-19T12:00:08Z")
               (check-equal nil disposition "a cross-record-kind collision has no disposition")
               (check-string= "OPERATION FAIL id=op-b op=- state=-: reused with a different payload"
                              line "the refusal carries the kernel's own reason text")
               (check-equal 2 code "the refusal exits 2")
               (check-equal nil replayed "a refusal is not a replay"))
             (check-equal before (operation-journal-length registry)
                          "a cross-record-kind collision writes no record")
             (check-equal before-b (funcall state-of "op-b")
                          "and leaves the operation it named untouched")
             (let ((accept (accept-record-of (operation-registry-journal registry) "op-a")))
               (check-equal nil (cancel-record-p accept)
                            "the record under the colliding id is still the accept record")
               (check-string= "capture" (getf accept :kind)
                              "with the kind it was accepted under"))))
      (close-durable-operation-registry registry))))
