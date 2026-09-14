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

(defun check-keyword (keyword)
  "The printer downcases a keyword's name, so two names differing only in case
would print identical bytes. Only a name that is already upper case is admitted,
which makes the downcasing injective over what is admitted, and a lower-case
name can reach the reader only through the `|` escape it already refuses
(SPEC-WORK.md:678-681, and :337, which calls this the one deterministic printer)."
  (let ((name (symbol-name keyword)))
    (unless (string= name (string-upcase name))
      (error 'restricted-data-violation
             :value (format nil "keyword name ~S is not upper case and would collide when downcased"
                            name))))
  keyword)

(defun canonical-print (value stream)
  (typecase value
    (integer (format stream "~D" value))
    (keyword (check-keyword value)
             (write-char #\: stream)
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

(defun char-utf8-bytes (ch)
  (let ((code (char-code ch)))
    (cond ((<= code #x7f) 1)
          ((<= code #x7ff) 2)
          ((<= code #xffff) 3)
          (t 4))))

(defun utf8-bytes-up-to (text n)
  "UTF-8 octet length of the first N characters of TEXT."
  (loop for i from 0 below n
        summing (char-utf8-bytes (char text i))))

(defpackage #:nova-work.read (:use))

(defun refuse-evaluation-syntax (text)
  "Refuse every dispatch macro and every other form the source forbids as
evaluation, outside a string literal (SPEC-WORK.md:681). Comment text is text:
it changes no string or dispatch state, and each of its UTF-8 characters is
counted exactly once."
  (let ((in-string nil) (escaped nil) (byte-offset 0)
        (len (length text)) (i 0))
    (loop while (< i len)
          do (let ((ch (char text i)))
               (cond
                 ((and in-string escaped)
                  (setf escaped nil)
                  (incf byte-offset (char-utf8-bytes ch))
                  (incf i))
                 ((and in-string (char= ch #\\))
                  (setf escaped t)
                  (incf byte-offset (char-utf8-bytes ch))
                  (incf i))
                 ((char= ch #\")
                  (setf in-string (not in-string))
                  (incf byte-offset (char-utf8-bytes ch))
                  (incf i))
                 (in-string
                  (incf byte-offset (char-utf8-bytes ch))
                  (incf i))
                 ((char= ch #\;)
                  ;; Skip to the end of the line: a comment is opaque, its
                  ;; bytes count exactly once, and the newline that ends it is
                  ;; consumed by the ordinary arm on the next pass.
                  (loop while (and (< i len)
                                   (not (char= (char text i) #\Newline))
                                   (not (char= (char text i) #\Return)))
                        do (incf byte-offset (char-utf8-bytes (char text i)))
                           (incf i)))
                 ((char= ch #\#)
                  (error 'restricted-data-violation
                         :value (format nil "dispatch macro at byte ~D" byte-offset)))
                 ((char= ch #\|)
                  (error 'restricted-data-violation
                         :value (format nil "multiple escape at byte ~D" byte-offset)))
                 ((char= ch #\')
                  (error 'restricted-data-violation
                         :value (format nil "quote at byte ~D" byte-offset)))
                 ((char= ch #\`)
                  (error 'restricted-data-violation
                         :value (format nil "backquote at byte ~D" byte-offset)))
                 ((char= ch #\,)
                  (error 'restricted-data-violation
                         :value (format nil "unquote at byte ~D" byte-offset)))
                 ((char= ch #\\)
                  (error 'restricted-data-violation
                         :value (format nil "single escape at byte ~D" byte-offset)))
                 (t
                  (incf byte-offset (char-utf8-bytes ch))
                  (incf i)))))
    (when in-string
      (error 'restricted-data-violation :value "unterminated string"))))

(defun check-restricted (form)
  (typecase form
    (integer form)
    (keyword (check-keyword form))
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
    (with-input-from-string (in text)
      (flet ((offset () (utf8-bytes-up-to text (or (ignore-errors (file-position in)) 0))))
        (let ((form (handler-case (read in)
                      (restricted-data-violation (c) (error c))
                      (end-of-file ()
                        (error 'restricted-data-violation
                               :value (format nil "unbalanced form; input ended at byte ~D"
                                              (utf8-bytes-up-to text (length text)))))
                      (error ()
                        (error 'restricted-data-violation
                               :value (format nil "refused by the reader at byte ~D" (offset)))))))
          (let ((next (handler-case (read in nil :end-of-input)
                        (end-of-file () :end-of-input)
                        (error ()
                          ;; A stray closer is trailing bytes, not an end.
                          (error 'restricted-data-violation
                                 :value (format nil "trailing bytes after one form, at byte ~D"
                                                (offset)))))))
            (unless (eq next :end-of-input)
              (error 'restricted-data-violation
                     :value (format nil "trailing bytes after one form, at byte ~D" (offset)))))
          (incf *parses*)
          (check-restricted form))))))
