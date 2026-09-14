;;;; journal.lisp --- the acceptance interface, and nothing more.
;;;;
;;;; docs/SPEC-WORK.md:307 — the session "appends it with its request id to the
;;;; local recovery journal, acknowledges only after the journal is durable,
;;;; then applies it". The kernel therefore asks the journal to accept an
;;;; envelope BEFORE any of it is applied, and a refusal applies nothing.
;;;;
;;;; What is here is the interface and a fake. THE FAKE TESTS ORDERING ONLY,
;;;; NOT DURABILITY: it holds no file, does no fsync, survives no restart, and
;;;; the `atomic-mutation` and `recovery` suites (SPEC-WORK.md:3346, :3357) are
;;;; out of this slice and are not claimed green by anything here.
;;;;
;;;; The dedup lookup lives behind this interface on purpose. SPEC-WORK.md:2117
;;;; forbids "an unbounded resident map of request ids" as production dedup; the
;;;; kernel keeps no such map, and the fake's own store is bounded and answers
;;;; `dedup unavailable` past its capacity, which is the shape of
;;;; SPEC-WORK.md:517 rather than a production implementation of it.

(in-package #:nova-work)

(defgeneric journal-accept (journal envelope)
  (:documentation "Answer (values T NIL) to accept, or (values NIL reason) to
refuse. Called before any part of ENVELOPE is applied."))

(defgeneric journal-record (journal request digest line rev)
  (:documentation "Record the accepted request's id, the digest of its payload
and the OK line it was answered with."))

(defgeneric journal-lookup (journal request)
  (:documentation "Answer (values FOUND-P digest line) for REQUEST, or
(values :unavailable page NIL) where the bounded store cannot say."))

(defclass ordering-journal ()
  ((records :initform (make-hash-table :test #'equal) :reader journal-records)
   (order :initform '() :accessor journal-order-slot)
   (capacity :initarg :capacity :initform 64 :reader journal-capacity)
   (evicted :initform nil :accessor journal-evicted-p)))

(defun make-ordering-journal (&key (capacity 64))
  (make-instance 'ordering-journal :capacity capacity))

(defun journal-order (journal)
  "The accepted request ids in the order they were accepted. Ordering is the
only property this fake carries."
  (reverse (journal-order-slot journal)))

(defmethod journal-accept ((journal ordering-journal) envelope)
  (declare (ignore envelope))
  (values t nil))

(defmethod journal-record ((journal ordering-journal) request digest line rev)
  (setf (gethash request (journal-records journal)) (list digest line rev))
  (push request (journal-order-slot journal))
  ;; Bounded: the oldest record leaves rather than the store growing without
  ;; end. A lookup of a request that has left answers :unavailable.
  (when (> (length (journal-order-slot journal)) (journal-capacity journal))
    (let ((evicted (car (last (journal-order-slot journal)))))
      (remhash evicted (journal-records journal))
      (setf (journal-evicted-p journal) t)
      (setf (journal-order-slot journal)
            (butlast (journal-order-slot journal)))))
  request)

(defmethod journal-lookup ((journal ordering-journal) request)
  (multiple-value-bind (record found) (gethash request (journal-records journal))
    (cond (found (values t (first record) (second record)))
          ;; Past the bound the answer is a refusal and never "this is new"
          ;; (SPEC-WORK.md:517): a request whose record has left the store
          ;; cannot be shown to be a first arrival.
          ((journal-evicted-p journal) (values :unavailable "journal-page-0" nil))
          (t (values nil nil nil)))))

(defclass rejecting-journal (ordering-journal)
  ((reject-on :initarg :reject-on :initform nil :reader journal-reject-on)))

(defun make-rejecting-journal (&key (capacity 64) reject-on)
  (make-instance 'rejecting-journal :capacity capacity :reject-on reject-on))

(defmethod journal-accept ((journal rejecting-journal) envelope)
  (let ((request (getf envelope :request))
        (reject (journal-reject-on journal)))
    (if (and reject (equal request reject))
        (values nil "injected acceptance failure")
        (values t nil))))
