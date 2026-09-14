;;;; value.lisp --- restricted data and its one deterministic printer.
;;;;
;;;; docs/SPEC-WORK.md:678 — "A work file is a sequence of s-expressions made
;;;; only of lists, keywords, strings and integers". Nothing read is ever
;;;; evaluated (SPEC-WORK.md:681); every dispatch macro is refused.
;;;;
;;;; The printer is the one docs/SPEC-WORK.md:337 names: "each element printed
;;;; by the same deterministic printer a clip writes its snapshot with, one
;;;; space between elements, no comments and no other whitespace".
;;;;
;;;; Absent, empty and the empty string are three spellings and stay three
;;;; (SPEC-WORK.md:329-337, replay absent-empty-and-null-are-three-spellings):
;;;; `(:absent)`, `()` and `""`.

(in-package #:nova-work)

(defparameter +absent+ '(:absent)
  "The spelling of a field the caller did not give. A wire null serializes to
this too; the empty list is a value and is not this.")

(defun absentp (value) (equal value +absent+))

(defun canonical-print (value stream)
  (typecase value
    (integer (format stream "~D" value))
    (keyword (write-char #\: stream)
             (write-string (string-downcase (symbol-name value)) stream))
    (string (write-char #\" stream)
            (loop for ch across value
                  do (case ch
                       (#\" (write-string "\\\"" stream))
                       (#\\ (write-string "\\\\" stream))
                       (t (write-char ch stream))))
            (write-char #\" stream))
    (null (write-string "()" stream))
    (cons (write-char #\( stream)
          (loop for tail on value
                do (canonical-print (car tail) stream)
                   (let ((rest (cdr tail)))
                     (cond ((null rest))
                           ((consp rest) (write-char #\Space stream))
                           (t (error 'restricted-data-violation :value value)))))
          (write-char #\) stream))
    (t (error 'restricted-data-violation :value value)))
  value)

(defun canonical-string (value)
  (with-output-to-string (s) (canonical-print value s)))

;;; Reading back.

(defpackage #:nova-work.read (:use))

(defun refuse-evaluation-syntax (text)
  "Refuse every dispatch macro and every other form the source forbids as
evaluation, outside a string literal (SPEC-WORK.md:681)."
  (let ((in-string nil) (escaped nil))
    (loop for ch across text
          for i from 0
          do (cond (escaped (setf escaped nil))
                   ((and in-string (char= ch #\\)) (setf escaped t))
                   ((char= ch #\") (setf in-string (not in-string)))
                   (in-string)
                   ((char= ch #\#)
                    (error 'restricted-data-violation
                           :value (format nil "dispatch macro at byte ~D" i)))
                   ((char= ch #\|)
                    (error 'restricted-data-violation
                           :value (format nil "multiple escape at byte ~D" i)))
                   ((char= ch #\;)
                    (error 'restricted-data-violation
                           :value (format nil "comment at byte ~D" i)))
                   ((char= ch #\\)
                    (error 'restricted-data-violation
                           :value (format nil "single escape at byte ~D" i)))))
    (when in-string
      (error 'restricted-data-violation :value "unterminated string"))))

(defun check-restricted (form)
  (typecase form
    (integer form)
    (keyword form)
    (string form)
    (null form)
    (cons (loop for tail on form
                do (check-restricted (car tail))
                   (unless (listp (cdr tail))
                     (error 'restricted-data-violation :value form)))
          form)
    (t (error 'restricted-data-violation :value form))))

(defun read-restricted (text)
  "Read one restricted s-expression from TEXT. Counted in *PARSES*."
  (refuse-evaluation-syntax text)
  (let ((*read-eval* nil)
        (*package* (find-package '#:nova-work.read))
        (*read-base* 10))
    (multiple-value-bind (form position) (read-from-string text)
      (unless (every (lambda (ch) (member ch '(#\Space #\Tab #\Newline #\Return)))
                     (subseq text position))
        (error 'restricted-data-violation :value "trailing bytes after one form"))
      (incf *parses*)
      (check-restricted form))))
