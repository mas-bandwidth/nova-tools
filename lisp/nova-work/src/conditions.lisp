;;;; conditions.lisp --- the refusals. Nothing here is a silent bypass.

(in-package #:nova-work)

(define-condition nova-work-error (error) ())

(define-condition restricted-data-violation (nova-work-error)
  ((value :initarg :value :reader restricted-data-violation-value))
  (:report (lambda (c s)
             (format s "restricted data refuses ~S" (restricted-data-violation-value c)))))

(define-condition unsupported-input (nova-work-error)
  ((what :initarg :what :reader unsupported-input-what))
  (:report (lambda (c s) (format s "~A" (unsupported-input-what c)))))

(define-condition not-implemented (nova-work-error)
  ((what :initarg :what :reader not-implemented-what))
  (:report (lambda (c s) (format s "not implemented: ~A" (not-implemented-what c)))))

(define-condition journal-error (nova-work-error) ())

(define-condition journal-corrupt-data (journal-error)
  ((path :initarg :path :reader journal-corrupt-data-path)
   (offset :initarg :offset :initform nil :reader journal-corrupt-data-offset)
   (reason :initarg :reason :reader journal-corrupt-data-reason))
  (:report (lambda (c s)
             (format s "journal ~A corrupt~@[ at offset ~A~]: ~A"
                     (journal-corrupt-data-path c)
                     (journal-corrupt-data-offset c)
                     (journal-corrupt-data-reason c)))))

(define-condition journal-mismatch (journal-error)
  ((path :initarg :path :reader journal-mismatch-path)
   (what :initarg :what :reader journal-mismatch-what))
  (:report (lambda (c s)
             (format s "journal ~A mismatch: ~A"
                     (journal-mismatch-path c)
                     (journal-mismatch-what c)))))

;;; Instrumentation. `open-count-is-read-not-computed` (SPEC-WORK.md:3229) asks
;;; for zero visits, zero parses and zero replays on a resident current-revision
;;; |O| query, so each of the three has a counter and every path that does one
;;; increments it.

(defvar *visits* 0 "Node accesses. `query --ask size` must make none.")
(defvar *parses* 0 "Restricted-data reads. `query --ask size` must make none.")
(defvar *replays* 0 "History replays. `query --ask size` must make none.")

(defmacro with-instrumentation (&body body)
  `(let ((*visits* 0) (*parses* 0) (*replays* 0)) ,@body))
