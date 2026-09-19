;;;; replays-e02-operations.lisp --- the operation scheduler's durable accept
;;;; record and its restart reconciliation (AUDIT row E02.2).
;;;;
;;;;   operation-accept-record-is-durable-before-the-id   SPEC-WORK.md:2728-2733
;;;;   restart-reconciles-every-operation-id-a-caller-holds
;;;;                                    SPEC-WORK.md:2731-2733, :2759-2760
;;;;   an-id-no-journal-holds-is-its-own-line   SPEC-WORK.md:2733-2735, :5982
;;;;
;;;; Every case here runs twice where the seam allows it: once over the
;;;; in-process implementation of the durable-accept seam (the fake) and once
;;;; over the real bounded filesystem journal of src/journal.lisp, which is the
;;;; one that writes and fsyncs the accept frame before answering the id. The
;;;; restart case closes the journal, reopens it from the bytes on disk and
;;;; reconciles, because "a crash between accepting the work and acknowledging
;;;; it can never leave a caller holding an id the restart never heard of"
;;;; (SPEC-WORK.md:2731-2733) is a claim about a second process, not about a
;;;; resident map.

(in-package #:nova-work/tests)

(defun operation-journal-text (path)
  "The bytes the journal has published, read back while the journal is still
open: this is what a second process would find after a crash."
  (with-open-file (in path :direction :input :element-type 'character
                           :external-format :utf-8)
    (let ((text (make-string (file-length in))))
      (subseq text 0 (read-sequence text in)))))

;;; ------------------------------------------------------------------
;;; operation-accept-record-is-durable-before-the-id  SPEC-WORK.md:2728-2733
;;; ------------------------------------------------------------------

(deftest "operation-accept-record-is-durable-before-the-id" "docs/SPEC-WORK.md:2728-2733"
    "expected=accept-record-on-disk-before-the-id-is-answered;id-kind-request-author-stamp-all-recorded"
  ;; The fake: the in-process implementation of the same seam.
  (let* ((fake (make-operation-registry))
         (fake-id (operation-accept fake :id "op-f1" :kind "capture"
                                         :request "req-f1" :author "rowan"
                                         :stamp "2026-09-19T12:00:00Z")))
    (check-string= "op-f1" fake-id "the fake seam answers the id it was handed")
    (check-equal 1 (operation-journal-length fake) "the fake holds one accept record")
    (let ((record (accept-record-of (operation-registry-journal fake) "op-f1")))
      (check-string= "capture" (getf record :kind) "the fake record carries the kind")
      (check-string= "req-f1" (getf record :request) "the fake record carries the request id")))
  ;; The real file journal twin.
  (let* ((path (test-journal-path "operation-accept"))
         (registry (open-durable-operation-registry path)))
    (unwind-protect
         (let ((before (operation-journal-text path)))
           (ok (null (search "op-r1" before))
               "the id is not on disk before it is accepted")
           (let ((id (operation-accept registry :id "op-r1" :kind "capture"
                                                :request "req-r1" :author "rowan"
                                                :stamp "2026-09-19T12:00:00Z")))
             (check-string= "op-r1" id "the real seam answers the id it was handed")
             ;; The journal is still open: these are the published, fsynced
             ;; bytes a second process would read, not a close-time flush.
             (let ((after (operation-journal-text path)))
               (ok (search "op-r1" after) "the accept record's id is on disk before the id is answered")
               (ok (search "req-r1" after) "the accept record's request id is on disk")
               (ok (search "capture" after) "the accept record's operation kind is on disk")
               (ok (search "rowan" after) "the accept record's author is on disk")
               (ok (search "2026-09-19T12:00:00Z" after) "the accept record's stamp is on disk"))
             (check-equal 1 (operation-journal-length registry)
                          "the real journal holds exactly one accept record")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; restart-reconciles-every-operation-id-a-caller-holds
;;;                               SPEC-WORK.md:2731-2733, :2759-2760
;;; ------------------------------------------------------------------

(deftest "restart-reconciles-every-operation-id-a-caller-holds" "docs/SPEC-WORK.md:2731-2733,2759-2760"
    "expected=reopened-registry-knows-both-ids;accept-record-fields-survive;reconcile-is-idempotent"
  (let ((path (test-journal-path "operation-restart")))
    (let ((first (open-durable-operation-registry path)))
      (unwind-protect
           (progn
             (operation-accept first :id "op-a" :kind "capture" :request "req-a"
                                     :author "rowan" :stamp "2026-09-19T12:00:00Z")
             (operation-accept first :id "op-b" :kind "export" :request "req-b"
                                     :author "stella" :stamp "2026-09-19T12:00:01Z")
             (check-equal 2 (operation-journal-length first) "two accept records are durable"))
        (close-durable-operation-registry first)))
    ;; The restart: a second process opening the same journal file.
    (let ((second (open-durable-operation-registry path)))
      (unwind-protect
           (progn
             (check-equal '("op-a" "op-b")
                          (sort (mapcar (lambda (row) (getf row :id))
                                        (registry-operation-list second :max 16))
                                #'string<)
                          "the reopened registry knows every id a caller was told about")
             ;; Neither id is the "no such operation" case any more.
             (dolist (id '("op-a" "op-b"))
               (multiple-value-bind (state line code) (registry-operation-state second id)
                 (declare (ignore state))
                 (check-equal nil line (format nil "~A is not answered with a refusal line after the restart" id))
                 (check-equal 0 code (format nil "~A answers exit 0 after the restart" id))))
             ;; The accept record itself survived, field by field.
             (let ((a (accept-record-of (operation-registry-journal second) "op-a"))
                   (b (accept-record-of (operation-registry-journal second) "op-b")))
               (check-string= "capture" (getf a :kind) "op-a's kind survives the restart")
               (check-string= "req-a" (getf a :request) "op-a's request id survives the restart")
               (check-string= "rowan" (getf a :author) "op-a's author survives the restart")
               (check-string= "2026-09-19T12:00:00Z" (getf a :stamp) "op-a's stamp survives the restart")
               (check-string= "export" (getf b :kind) "op-b's kind survives the restart")
               (check-string= "stella" (getf b :author) "op-b's author survives the restart"))
             ;; Reconciliation happens once, before anything is retried: a second
             ;; pass recovers nothing and registers no duplicate.
             (check-equal '() (reconcile-operation-registry second)
                          "a second reconciliation recovers nothing")
             (check-equal 2 (length (registry-operation-list second :max 16))
                          "reconciling twice registers no duplicate"))
        (close-durable-operation-registry second)))))

;;; ------------------------------------------------------------------
;;; an-id-no-journal-holds-is-its-own-line  SPEC-WORK.md:2733-2735, :5982
;;; ------------------------------------------------------------------

(deftest "an-id-no-journal-holds-is-its-own-line" "docs/SPEC-WORK.md:2733-2735,5982"
    "expected=OPERATION-FAIL-no-such-operation;exit=2;never-an-invented-queued"
  (let* ((path (test-journal-path "operation-unknown"))
         (registry (open-durable-operation-registry path)))
    (unwind-protect
         (progn
           (operation-accept registry :id "op-known" :kind "clip" :request "req-known"
                                      :author "rowan" :stamp "2026-09-19T12:00:00Z")
           (multiple-value-bind (state line code) (registry-operation-state registry "op-missing")
             (check-equal nil state "an id no journal holds has no state, never an invented queued")
             (check-string= "OPERATION FAIL id=op-missing op=- state=-: no such operation" line
                            "the refusal is the line the output grammar fixes")
             (check-equal 2 code "the refusal exits 2")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; an-id-the-journal-cannot-hold-is-never-answered
;;;                               SPEC-WORK.md:2728-2733, :2759
;;; ------------------------------------------------------------------

(deftest "an-id-the-journal-cannot-hold-is-never-answered" "docs/SPEC-WORK.md:2728-2733,2759"
    "expected=accept-past-the-bound-signals;no-id-answered;journal-holds-the-bound;restart-agrees"
  (let* ((path (test-journal-path "operation-bound"))
         (registry (open-durable-operation-registry path :capacity 2)))
    (unwind-protect
         (progn
           (operation-accept registry :id "op-1" :kind "capture" :request "req-1"
                                      :author "rowan" :stamp "2026-09-19T12:00:00Z")
           (operation-accept registry :id "op-2" :kind "export" :request "req-2"
                                      :author "rowan" :stamp "2026-09-19T12:00:01Z")
           ;; Past the explicit bound the accept is refused. It must signal
           ;; rather than answer an id the recovery journal does not hold: an
           ;; answered id the restart never heard of is the one thing this
           ;; paragraph forbids (SPEC-WORK.md:2731-2733).
           (let ((answered (handler-case
                               (operation-accept registry :id "op-3" :kind "clip"
                                                          :request "req-3" :author "rowan"
                                                          :stamp "2026-09-19T12:00:02Z")
                             (error () :refused))))
             (check-equal :refused answered "an accept past the bound answers no id"))
           (check-equal 2 (operation-journal-length registry)
                        "the journal holds its bound and no more")
           (ok (null (search "op-3" (operation-journal-text path)))
               "the refused id was never written to the recovery journal"))
      (close-durable-operation-registry registry))
    ;; The restart agrees: it heard of the two accepted ids and of no third.
    (let ((second (open-durable-operation-registry path :capacity 2)))
      (unwind-protect
           (check-equal '("op-1" "op-2") (operation-journal-ids
                                          (operation-registry-journal second))
                        "the restart heard of exactly the ids that were answered")
        (close-durable-operation-registry second)))))
