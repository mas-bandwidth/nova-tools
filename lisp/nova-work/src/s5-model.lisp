;;;; s5-model.lisp --- the models index: register, rate, evidence, and query.
;;;;
;;;; S5, in its own file, wired into the command thread by a later card. The
;;;; models index has no node kind of its own (SPEC-WORK.md:1048-1053): a model
;;;; event names a model identity rather than a :node, and no count, roadmap or
;;;; required set moves when one is written.
;;;;
;;;; A model keeps three fields apart (SPEC-WORK.md:4507-4509): the declared
;;;; vendor capability (register), a pricing assessment (rate, immutable by
;;;; content identity), and a measured result (evidence). Unknown or outdated
;;;; evidence never hardens into a strength (SPEC-WORK.md:4508-4509).

(in-package #:nova-work)

(defstruct (model-record
            (:conc-name model-)
            (:constructor %make-model-record))
  id provider route billing
  (capabilities '())
  (assessments '())
  (measurements '()))

(defstruct (rate-record
            (:conc-name rate-)
            (:constructor make-rate-record (&key pricing-id pricing effective source)))
  pricing-id pricing effective source)

(defstruct (measurement-record
            (:conc-name measurement-)
            (:constructor make-measurement-record (&key task-class result samples source)))
  task-class result samples source)

(defun make-model-index ()
  "An empty model index: an alist of (id . model-record), keyed by stable model
  identity (SPEC-WORK.md:4503)."
  '())

(defun index-get (index id)
  (cdr (assoc id index :test #'string=)))

(defun index-put (index id record)
  (cons (cons id record)
        (remove id index :test #'string= :key #'car)))

(defun model-register (index id provider route billing)
  "register a model identity and its declared vendor capability (provider,
  route, billing). Re-registering updates the identity fields and preserves the
  assessment and measurement fields."
  (let* ((existing (index-get index id))
         (record (or existing (%make-model-record))))
    (setf (model-id record) id
          (model-provider record) provider
          (model-route record) route
          (model-billing record) billing)
    (index-put index id record)))

(defun model-rate (index id rate)
  "Append a pricing assessment. A pricing record is immutable by content
  identity; revising a rate appends and never rewrites (SPEC-WORK.md:4520-4534)."
  (let* ((existing (index-get index id))
         (record (or existing (%make-model-record :id id))))
    (setf (model-assessments record)
          (append (model-assessments record) (list rate)))
    (index-put index id record)))

(defun model-evidence (index id measurement)
  "Append a measured result. A failed or unresolved attempt is evidence and not
  a verdict on the model's worth (SPEC-WORK.md:4514-4516)."
  (let* ((existing (index-get index id))
         (record (or existing (%make-model-record :id id))))
    (setf (model-measurements record)
          (append (model-measurements record) (list measurement)))
    (index-put index id record)))

(defun model-three-fields (model)
  "The declared capability, the assessments and the measured results, as three
  separate lists that never collapse into one."
  (list :capabilities (model-capabilities model)
        :assessments (model-assessments model)
        :measurements (model-measurements model)))

(defun query-models (index)
  "Every known model record, in stable id order."
  (sort (mapcar #'cdr index) #'string< :key (lambda (m) (model-id m))))
