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
  (limits *capture-stage-limits*)
  (absorb-allowed nil)
  (intake-mode :link)
  (absorb-repositories '())
  (absorb-authors '())
  (absorb-authority nil))

(defun %non-empty-string-list-p (x)
  (and (consp x)
       (every (lambda (s) (and (stringp s) (plusp (length s)))) x)))

(defun validate-absorb-selection (absorb-allowed intake-mode repositories
                                  authors authority)
  "Signal an error unless the intake selection is one SPEC-WORK.md:7586-7617
permits. The intake mode is :LINK (the default) or :ABSORB. Absorb is separate
from link: :ABSORB-ALLOWED T needs the explicit :ABSORB intake mode, a non-empty
scope of applicable repositories and participating authors, and a named grant
AUTHORITY (its source reference, recorded as provenance). The :ABSORB mode
without :ABSORB-ALLOWED T is refused, as is a scope given to a link stage.
Author identity alone never selects absorption."
  (unless (member intake-mode '(:link :absorb))
    (error "capture intake mode ~S is not :link or :absorb" intake-mode))
  (cond
    (absorb-allowed
     (unless (eq intake-mode :absorb)
       (error "absorb allowed but intake mode is ~S; absorb needs explicit :absorb"
              intake-mode))
     (unless (%non-empty-string-list-p repositories)
       (error "absorb needs a selected scope: :absorb-repositories is ~S"
              repositories))
     (unless (%non-empty-string-list-p authors)
       (error "absorb needs a selected scope: :absorb-authors is ~S" authors))
     (unless (and (stringp authority) (plusp (length authority)))
       (error "absorb needs a selected authority: :absorb-authority is ~S"
              authority)))
    ((eq intake-mode :absorb)
     (error "intake mode :absorb requires :absorb-allowed t"))
    ((or repositories authors authority)
     (error "absorb scope/authority given to a link stage (absorb not allowed)")))
  t)

(defun make-capture-stage (&key registry (limits *capture-stage-limits*)
                              (absorb-allowed nil) (intake-mode :link)
                              absorb-repositories absorb-authors
                              absorb-authority)
  "The in-process staging area over the operation registry. When no registry is
given the scheduler's in-process durable-accept journal is used.
By default, absorb is disabled and link is the default mode (SPEC-WORK.md:7614).
Absorb is enabled only with :ABSORB-ALLOWED T, :INTAKE-MODE :ABSORB and a
selected scope (:ABSORB-REPOSITORIES, :ABSORB-AUTHORS) and authority
(:ABSORB-AUTHORITY); see VALIDATE-ABSORB-SELECTION."
  (validate-absorb-selection absorb-allowed intake-mode absorb-repositories
                             absorb-authors absorb-authority)
  (%make-capture-stage :registry (or registry (make-operation-registry))
                       :inputs '() :results '() :limits limits
                       :absorb-allowed (and absorb-allowed t)
                       :intake-mode intake-mode
                       :absorb-repositories (copy-list absorb-repositories)
                       :absorb-authors (copy-list absorb-authors)
                       :absorb-authority absorb-authority))

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

(defun capture-absorb-allowed-p (stage)
  "Absorb is disabled by default; link is the default mode. Absorb requires
explicit scope and authority selection (SPEC-WORK.md:7586-7617, E09-F04-01):
true only for a stage whose absorb intake mode, repositories, authors and
authority were all selected."
  (and (capture-stage-absorb-allowed stage)
       (eq (capture-stage-intake-mode stage) :absorb)
       (consp (capture-stage-absorb-repositories stage))
       (consp (capture-stage-absorb-authors stage))
       (stringp (capture-stage-absorb-authority stage))
       t))

(defun capture-result-of (stage id)
  "The retained result for ID, or NIL."
  (find id (capture-stage-results stage)
        :key (lambda (row) (getf row :id)) :test #'equal))
