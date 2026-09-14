;;;; stubs.lisp --- red-first skeleton.
;;;;
;;;; Every operation the slice-1 acceptance suite names is declared here and
;;;; signals NOT-IMPLEMENTED. The suite is written against these signatures and
;;;; is expected to be red at this commit. The implementation replaces this file.

(in-package #:nova-work)

(define-condition nova-work-error (error) ())

(define-condition not-implemented (nova-work-error)
  ((what :initarg :what :reader not-implemented-what))
  (:report (lambda (c s) (format s "not implemented: ~A" (not-implemented-what c)))))

(define-condition restricted-data-violation (nova-work-error)
  ((value :initarg :value :reader restricted-data-violation-value))
  (:report (lambda (c s)
             (format s "restricted data refuses ~S" (restricted-data-violation-value c)))))

(define-condition unsupported-input (nova-work-error)
  ((what :initarg :what :reader unsupported-input-what))
  (:report (lambda (c s) (format s "unsupported: ~A" (unsupported-input-what c)))))

(defmacro %stub (name lambda-list)
  `(defun ,name ,lambda-list
     (declare (ignorable ,@(remove-if (lambda (s) (member s lambda-list-keywords))
                                      lambda-list)))
     (error 'not-implemented :what ',name)))

(defparameter +absent+ '(:absent))

(defvar *visits* 0)
(defvar *parses* 0)
(defvar *replays* 0)

(defmacro with-instrumentation (&body body)
  `(let ((*visits* 0) (*parses* 0) (*replays* 0)) ,@body))

(%stub absentp (value))
(%stub canonical-string (value))
(%stub canonical-print (value stream))
(%stub read-restricted (text))
(%stub sha256-hex (input))
(%stub payload-digest (events))
(%stub event-digest-form (event))
(%stub event-record-form (event))
(%stub event-id (event))
(%stub closed-row-key (event))
(%stub make-seed-state (nodes))
(%stub state-open-count (state))
(%stub state-closed-count (state))
(%stub state-revision (state))
(%stub state-history (state))
(%stub state-closed-rows (state))
(%stub node-open-count (state id))
(%stub node-branch (state id))
(%stub node-state (state id))
(%stub root-digest (state))
(%stub state-canonical-form (state))
(%stub reconstruct-state (text))
(%stub make-ordering-journal (&key capacity))
(%stub make-rejecting-journal (&key capacity reject-on))
(%stub journal-order (journal))
(%stub journal-accept (journal envelope))
(%stub journal-record (journal request digest line rev))
(%stub journal-lookup (journal request))
(%stub make-kernel (&key state journal rev-base))
(%stub kernel-state (kernel))
(%stub kernel-journal (kernel))
(%stub submit (kernel request))
(%stub ask-size (kernel))
(%stub open-issue-count (kernel))
(%stub open-leaf-count (kernel))
