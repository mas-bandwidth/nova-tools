;;;; replays-operation-records.lisp --- the durable long-operation record, its
;;;; status, its cancellation and the recovery reconciliation
;;;; (docs/SPEC-WORK.md:2725-2760, output grammar :5978-5982). The subject is
;;;; src/operation-records.lisp.
;;;;
;;;; Every case runs over a real kernel and its journal: the accept record is
;;;; the journal's, and the restart is a brand-new kernel over the same journal
;;;; with no memory of anything. Each fake-journal case has a real-file-journal
;;;; twin that closes the journal, reopens it and replays.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; The id is durable before it is printed. SPEC-WORK.md:2728-2733.
;;; ------------------------------------------------------------------

(deftest "operation-id-is-durable-before-it-is-printed" "docs/SPEC-WORK.md:2728"
    "expected=accept-record-holds-id-kind-request-author-stamp;a-restart-has-heard-of-it"
  (let* ((journal (make-ordering-journal))
         (k (fresh :journal journal)))
    (multiple-value-bind (okp line code record)
        (kernel-operation-accept k :id "op-1" :kind :clip :request "req-op-1"
                                   :author "rowan" :stamp "2026-09-19T12:00:00Z")
      (ok okp "accept refused: ~A" line)
      (check-equal 0 code "accept exit code")
      (check-string= "OPERATION OK id=op-1 op=clip state=queued" line "the accept line")
      ;; The record carries exactly what :2729-2731 names.
      (check-string= "op-1" (getf record :id) "the record's id")
      (check-equal :clip (getf record :kind) "the record's kind")
      (check-string= "req-op-1" (getf record :request) "the record's request id")
      (check-string= "rowan" (getf record :author) "the record's author")
      (check-string= "2026-09-19T12:00:00Z" (getf record :stamp) "the record's stamp"))
    ;; A crash between accepting the work and acknowledging it can never leave a
    ;; caller holding an id the restart never heard of.
    (let ((k2 (fresh :journal journal)))
      (multiple-value-bind (okp line code) (kernel-operation-status k2 :id "op-1")
        (ok okp "the restart never heard of the id: ~A" line)
        (check-equal 0 code "status exit code")
        (ok (search "state=queued" line) "the restart invented a state: ~A" line)))
    ;; A retry of the same request draws no second id and answers the same.
    (multiple-value-bind (okp line) (kernel-operation-accept k :id "op-1" :kind :clip
                                                               :request "req-op-1"
                                                               :author "rowan"
                                                               :stamp "2026-09-19T12:00:00Z")
      (ok okp "the retry was refused: ~A" line)
      (check-string= "OPERATION OK id=op-1 op=clip state=queued" line "the retry's line"))
    (check-equal '("op-1") (kernel-operation-ids k) "the retry drew a second id")
    ;; The same request id under a different payload is a conflict.
    (multiple-value-bind (okp line code)
        (kernel-operation-accept k :id "op-2" :kind :clip :request "req-op-1"
                                   :author "rowan" :stamp "2026-09-19T12:00:00Z")
      (ok (not okp) "a reused request id drew a new operation: ~A" line)
      (check-equal 2 code "conflict exit code")
      (ok (search "reused with a different payload" line) "the conflict is not named: ~A" line))
    ;; A kind outside the grammar's five, and a missing id, refuse.
    (multiple-value-bind (okp line code)
        (kernel-operation-accept k :id "op-3" :kind :teleport :request "req-op-3")
      (ok (not okp) "an unknown operation kind was accepted: ~A" line)
      (check-equal 2 code "refusal exit code")
      (ok (search "is not an operation kind" line) "the refusal is not named: ~A" line))))

;;; ------------------------------------------------------------------
;;; An id no journal holds has a line of its own. SPEC-WORK.md:2733-2735.
;;; ------------------------------------------------------------------

(deftest "an-id-no-journal-holds-has-a-line-of-its-own" "docs/SPEC-WORK.md:2734"
    "expected=OPERATION FAIL id=<id> op=- state=-: no such operation;exit=2;never-an-invented-queued"
  (let ((k (fresh)))
    (multiple-value-bind (okp line code) (kernel-operation-status k :id "op-nope")
      (ok (not okp) "an unknown id answered: ~A" line)
      (check-equal 2 code "the exit code of an unknown id")
      (check-string= "OPERATION FAIL id=op-nope op=- state=-: no such operation" line
                     "the line of an unknown id"))
    ;; A cancel and a completion of an unknown id take the same line.
    (multiple-value-bind (okp line code)
        (kernel-operation-cancel k :id "op-nope" :request "req-c-1")
      (ok (not okp) "an unknown id was cancelled: ~A" line)
      (check-equal 2 code "cancel exit code")
      (check-string= "OPERATION FAIL id=op-nope op=- state=-: no such operation" line
                     "the cancel's line"))
    (multiple-value-bind (okp line) (kernel-operation-complete k :id "op-nope")
      (ok (not okp) "an unknown id was completed: ~A" line))))

;;; ------------------------------------------------------------------
;;; A cancel is a request with its own disposition, and erases nothing.
;;; SPEC-WORK.md:2740-2744.
;;; ------------------------------------------------------------------

(deftest "a-cancel-replayed-twice-cancels-once" "docs/SPEC-WORK.md:2741"
    "expected=one-record;erases-no-accepted-mutation;no-revision-moved"
  (let ((k (fresh)))
    (kernel-operation-accept k :id "op-c" :kind :export :request "req-acc-c"
                               :author "rowan" :stamp "2026-09-19T12:00:00Z")
    ;; An accepted mutation, before the cancel.
    (ok (submit k (close-request :node "acme/work/f1/t1" :request "close-before-cancel"))
        "the mutation before the cancel was refused")
    (let ((closed (state-closed-count (kernel-state k)))
          (digest (root-digest (kernel-state k)))
          (rev (state-revision (kernel-state k))))
      (multiple-value-bind (okp line code) (kernel-operation-cancel k :id "op-c"
                                                                     :request "req-cancel-c")
        (ok okp "the cancel was refused: ~A" line)
        (check-equal 0 code "cancel exit code")
        (check-string= "OPERATION OK id=op-c op=export state=cancelling" line "the cancel line"))
      ;; Replayed: cancels once.
      (multiple-value-bind (okp line) (kernel-operation-cancel k :id "op-c"
                                                                 :request "req-cancel-c")
        (ok okp "the replayed cancel was refused: ~A" line)
        (check-string= "OPERATION OK id=op-c op=export state=cancelling" line
                       "the replayed cancel's line"))
      (check-equal 2 (length (kernel-operation-records k "op-c"))
                   "the replayed cancel wrote a second record")
      ;; It erases no accepted mutation and moves no revision.
      (check-equal closed (state-closed-count (kernel-state k)) "the cancel moved |C|")
      (check-string= digest (root-digest (kernel-state k)) "the cancel moved the state bytes")
      (check-equal rev (state-revision (kernel-state k)) "the cancel moved the revision")
      ;; A cancellation stays a request until its outcome is known.
      (check-equal :uncertain (getf (kernel-operation-record k "op-c") :external)
                   "a cancel claimed a known external outcome"))))

;;; ------------------------------------------------------------------
;;; Recovery reconciles interrupted ids before anything is retried.
;;; SPEC-WORK.md:2759-2760.
;;; ------------------------------------------------------------------

(deftest "recovery-reconciles-interrupted-operations" "docs/SPEC-WORK.md:2759"
    "expected=interrupted-ids-reported-once;state-unmoved;external=uncertain;terminal-ids-left-alone"
  (let* ((journal (make-ordering-journal))
         (k (fresh :journal journal)))
    (kernel-operation-accept k :id "op-done" :kind :export :request "req-a-1"
                               :author "rowan" :stamp "2026-09-19T12:00:00Z")
    (kernel-operation-accept k :id "op-lost" :kind :clip :request "req-a-2"
                               :author "rowan" :stamp "2026-09-19T12:01:00Z")
    (kernel-operation-complete k :id "op-done" :result '(:revision 7))
    ;; The crash: a brand-new kernel over the same journal.
    (let ((k2 (fresh :journal journal)))
      (check-equal '("op-lost") (kernel-operation-interrupted k2)
                   "the wrong set of ids was called interrupted")
      (let ((digest (root-digest (kernel-state k2)))
            (rev (state-revision (kernel-state k2))))
        (multiple-value-bind (okp line code ids) (kernel-operation-reconcile k2)
          (ok okp "the reconcile refused: ~A" line)
          (check-equal 0 code "reconcile exit code")
          (check-equal '("op-lost") ids "the reconcile answered the wrong ids")
          (ok (search "reconciled=1" line) "the reconcile does not count: ~A" line))
        ;; It erases no accepted mutation.
        (check-string= digest (root-digest (kernel-state k2)) "the reconcile moved the state")
        (check-equal rev (state-revision (kernel-state k2)) "the reconcile moved the revision"))
      ;; The state is not moved and the external outcome is not claimed.
      (let ((record (kernel-operation-record k2 "op-lost")))
        (check-equal :queued (getf record :state) "the reconcile moved the operation's state")
        (check-equal :uncertain (getf record :external)
                     "the reconcile claimed an external outcome"))
      ;; The completed one keeps its result and is not reported again.
      (check-equal '(:revision 7) (kernel-operation-result k2 "op-done")
                   "the completed result did not survive the restart")
      ;; A second restart does not report it again: nothing is retried twice.
      (check-equal '() (kernel-operation-interrupted k2)
                   "a reconciled id was reported interrupted again")
      (multiple-value-bind (okp line code ids) (kernel-operation-reconcile k2)
        (ok okp "the second reconcile refused: ~A" line)
        (check-equal 0 code "second reconcile exit code")
        (check-equal '() ids "the second reconcile reported an id again")))))

;;; ------------------------------------------------------------------
;;; `operation list` is bounded and capped. SPEC-WORK.md:2735-2737.
;;; ------------------------------------------------------------------

(deftest "operation-list-is-bounded-and-capped" "docs/SPEC-WORK.md:2735"
    "expected=shown<=max;one-row-per-shown-id"
  (let ((k (fresh)))
    (loop for n from 1 to 3
          do (kernel-operation-accept k :id (format nil "op-~D" n) :kind :capture
                                        :request (format nil "req-l-~D" n)
                                        :author "rowan" :stamp "2026-09-19T12:00:00Z"))
    (multiple-value-bind (okp line code rows) (kernel-operation-list k :max 2)
      (ok okp "list refused: ~A" line)
      (check-equal 0 code "list exit code")
      (ok (search "shown=2" line) "the listing is not capped: ~A" line)
      (check-equal 2 (length rows) "--max did not bound the rows")
      (ok (search "OPERATION ROW id=op-1 op=capture state=queued" (first rows))
          "the row shape: ~A" (first rows))
      (ok (search "external=none" (first rows))
          "an operation with no external effect is not none: ~A" (first rows)))))

;;; ------------------------------------------------------------------
;;; The real-file-journal twin: close, reopen, replay.
;;; ------------------------------------------------------------------

(deftest "operation-records-survive-a-real-file-journal" "docs/SPEC-WORK.md:2728"
    "expected=close-reopen-replay-answers-the-same-records-and-the-same-interrupted-set"
  (let* ((path (test-journal-path "operation-records"))
         (init (root-digest (make-seed-state *seed*)))
         (j1 (open-file-journal path :initial-state-hash init)))
    (unwind-protect
         (let ((k (fresh :journal j1)))
           (multiple-value-bind (okp line)
               (kernel-operation-accept k :id "op-file" :kind :clip :request "req-f-1"
                                          :author "rowan" :stamp "2026-09-19T12:00:00Z")
             (ok okp "accept over a real journal refused: ~A" line))
           (multiple-value-bind (okp line)
               (kernel-operation-accept k :id "op-file-2" :kind :export :request "req-f-2"
                                          :author "rowan" :stamp "2026-09-19T12:02:00Z")
             (ok okp "the second accept refused: ~A" line))
           (kernel-operation-complete k :id "op-file-2" :result '(:revision 9)))
      (close-file-journal j1))
    (let ((j2 (open-file-journal path :initial-state-hash init)))
      (unwind-protect
           (let ((k2 (fresh :journal j2)))
             (replay-journal j2 k2)
             (check-equal '("op-file" "op-file-2") (kernel-operation-ids k2)
                          "the ids did not survive the file")
             (multiple-value-bind (okp line) (kernel-operation-status k2 :id "op-file")
               (ok okp "the reopened status refused: ~A" line)
               (ok (search "op=clip" line) "the reopened record lost its kind: ~A" line))
             (check-equal '(:revision 9) (kernel-operation-result k2 "op-file-2")
                          "the result did not survive the file")
             (check-equal '("op-file") (kernel-operation-interrupted k2)
                          "the reopened interrupted set is wrong")
             (multiple-value-bind (okp line code ids) (kernel-operation-reconcile k2)
               (ok okp "the reconcile after reopen refused: ~A" line)
               (check-equal 0 code "reconcile exit code")
               (check-equal '("op-file") ids "the reconcile after reopen answered wrong"))
             (check-equal '() (kernel-operation-interrupted k2)
                          "the reconcile after reopen was not durable"))
        (close-file-journal j2)))))
