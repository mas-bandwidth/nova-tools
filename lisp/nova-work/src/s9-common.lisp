;;;; s9-common.lisp --- helpers the fleet, routes and slots share.
;;;;
;;;; Slice 9 (The fleet, the routes and the slots, #678/#687/#691) is implemented
;;;; as pure functions and records in its own s9-*.lisp files, beside slice 1.
;;;; None of it is wired into the command thread; a later card connects it.
;;;;
;;;; Every stamp this slice compares is a UTC `YYYY-MM-DDTHH:MM:SSZ`, so the
;;;; lexicographic order of the strings is their chronological order. Nothing
;;;; here reads a clock: a caller names the "now" it asks about.

(in-package #:nova-work)

(defun s9-stamp< (a b)
  "True when the UTC stamp A is strictly earlier than B. NIL stamps are never
  earlier than anything and never compared against a missing other."
  (and a b (string< a b)))

(defun s9-stamp<= (a b)
  (and a b (string<= a b)))

(defun s9-join-names (values)
  "Comma-join a list of keywords or strings for the `roles=` / `capabilities=`
  columns of a QUERY ROW: names, downcased, in order."
  (let ((names (mapcar (lambda (v)
                         (if (keywordp v)
                             (string-downcase (symbol-name v))
                             (princ-to-string v)))
                       values)))
    (if names (format nil "~{~A~^,~}" names) "-")))

(defun s9-plist->two-lines (plist)
  "Not used by the slice; a reminder that facts and limits stay plists and are
  rendered field by field, never flattened into a single opaque value."
  (declare (ignore plist))
  nil)
