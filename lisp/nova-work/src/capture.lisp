;;;; capture.lisp --- source capture and import staging as one long operation
;;;; (SPEC-WORK.md:2721-2760).
;;;;
;;;; A source capture and an import staging are long operations of the same
;;;; shape as an export or a clip: the engine draws a durable operation id,
;;;; stages the inputs and results *outside* the mutation loop, and only the
;;;; owning engine admits a validated result at an expected revision, so a
;;;; concurrent capture never becomes a second writer. The staged inputs,
;;;; staged bytes and retained results are bounded by explicit limits.
;;;;
;;;; The durable accept record is the scheduler's (src/scheduler.lisp): the id
;;;; is appended to the recovery journal and made durable before the id is
;;;; answered, so a crash between accepting and acknowledging leaves no caller
;;;; holding an id the restart never heard of.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The bounded staging area
;;; ------------------------------------------------------------------

(defparameter *capture-stage-limits* '(:staged-inputs 8 :staged-bytes 4096
                                       :retained-results 4)
  "Staged inputs, staged bytes and retained results are bounded by explicit
limits; a queue that would grow past one refuses rather than growing
(SPEC-WORK.md:2753).")

(defstruct (capture-input (:constructor make-capture-input
                             (&key id kind expected-revision bytes records
                                   source-pin)))
  id
  kind
  expected-revision
  bytes
  records
  source-pin)

(defstruct (capture-stage (:constructor %make-capture-stage))
  registry
  (inputs '())
  (results '())
  (limits *capture-stage-limits*))

(defun make-capture-stage (&key registry (limits *capture-stage-limits*))
  "The in-process staging area over the operation registry. When no registry is
given the scheduler's in-process durable-accept journal is used."
  (%make-capture-stage :registry (or registry (make-operation-registry))
                       :inputs '() :results '() :limits limits))

(defun capture-wire-op (kind)
  "The wire operation name a long source operation reports
(SPEC-WORK.md:2722-2723)."
  (ecase kind
    (:capture "source.capture")
    (:import "import.stage")))

;;; ------------------------------------------------------------------
;;; Beginning the long operation
;;; ------------------------------------------------------------------

(defun begin-source-operation (stage kind &key id request author stamp)
  "A long source operation returns an operation id and a state at once. The id
is durable before it is printed: the accept record is appended to the recovery
journal and made durable, then OPERATION OK is answered (SPEC-WORK.md:2721-2730)."
  (operation-accept (capture-stage-registry stage)
                    :id id :kind kind :request request
                    :author author :stamp stamp)
  (values id
          (format nil "OPERATION OK id=~A op=~A state=queued" id kind)
          0))

(defun begin-source-capture (stage &key (id "op-capture-1")
                                      (request "req-capture-1")
                                      (author "rowan")
                                      (stamp "2026-09-17T00:00:00Z"))
  "A source capture is one long operation (SPEC-WORK.md:2721-2723)."
  (begin-source-operation stage :capture :id id :request request
                                       :author author :stamp stamp))

(defun begin-import (stage &key (id "op-import-1")
                               (request "req-import-1")
                               (author "rowan")
                               (stamp "2026-09-17T00:00:00Z"))
  "An import staging is one long operation (SPEC-WORK.md:2721-2723)."
  (begin-source-operation stage :import :id id :request request
                                    :author author :stamp stamp))

;;; ------------------------------------------------------------------
;;; Staging inputs outside the mutation loop, bounded
;;; ------------------------------------------------------------------

(defun capture-input-count (stage)
  (length (capture-stage-inputs stage)))

(defun capture-stage-bytes (stage)
  "The exact running sum of the staged bytes (SPEC-WORK.md:2753)."
  (reduce #'+ (capture-stage-inputs stage) :key #'capture-input-bytes
          :initial-value 0))

(defun capture-stage-input (stage &key id kind (expected-revision 0)
                                      (bytes 0) (records 1) source-pin)
  "Stage one source record, read outside the mutation loop. The staged input set
and the staged-byte sum are bounded by the declared limits; a breach refuses
and stages nothing (SPEC-WORK.md:2748-2753)."
  (let ((limits (capture-stage-limits stage)))
    (cond
      ((>= (capture-input-count stage) (getf limits :staged-inputs))
       (values nil (format nil "STAGE FAIL: staged inputs at the bound ~D"
                           (getf limits :staged-inputs))))
      ((> (+ (capture-stage-bytes stage) bytes) (getf limits :staged-bytes))
       (values nil (format nil "STAGE FAIL: staged bytes over the bound ~D"
                           (getf limits :staged-bytes))))
      (t
       (push (make-capture-input :id id :kind kind
                                 :expected-revision expected-revision
                                 :bytes bytes :records records
                                 :source-pin source-pin)
             (capture-stage-inputs stage))
       (values t (format nil "STAGE OK id=~A revision=~D" id expected-revision))))))

;;; ------------------------------------------------------------------
;;; Admitting a validated result at an expected revision
;;; ------------------------------------------------------------------

(defun capture-admit-result (stage current-revision &key id result)
  "Only the owning engine admits a validated result, and only at the expected
revision: a stage whose source revision no longer matches the engine's is stale,
refuses, and writes nothing, so a concurrent capture never becomes a second
writer. Retained results are bounded (SPEC-WORK.md:2748-2750, :2753)."
  (let ((input (find id (capture-stage-inputs stage)
                     :key #'capture-input-id :test #'equal)))
    (cond
      ((null input)
       (values nil (format nil "ADMIT FAIL: no staged input ~A" id)))
      ((/= (capture-input-expected-revision input) current-revision)
       (values nil
               (format nil "ADMIT FAIL: stale stage for ~A (expected revision ~D, now ~D)"
                       id (capture-input-expected-revision input) current-revision)))
      ((>= (length (capture-stage-results stage))
           (getf (capture-stage-limits stage) :retained-results))
       (values nil "ADMIT FAIL: retained results at the bound"))
      (t
       (push (list :id id :revision current-revision :result result
                   :source-pin (capture-input-source-pin input))
             (capture-stage-results stage))
       (values t (format nil "ADMIT OK id=~A revision=~D" id current-revision))))))

(defun capture-result-of (stage id)
  "The retained result for ID, or NIL."
  (find id (capture-stage-results stage)
        :key (lambda (row) (getf row :id)) :test #'equal))
