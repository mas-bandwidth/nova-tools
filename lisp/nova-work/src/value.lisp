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

(defun reader-whitespace-p (ch)
  (find ch '(#\Space #\Tab #\Newline #\Return #\Page)))

(defun reader-token-boundary-p (ch)
  (or (reader-whitespace-p ch)
      (find ch '(#\( #\) #\; #\" #\# #\| #\' #\` #\, #\\))))

(defun decimal-integer-token-p (text start end)
  (let* ((digit-start (if (and (< start end)
                               (find (char text start) "+-"))
                          (1+ start)
                          start))
         ;; Common Lisp reads a terminal decimal point as an integer marker:
         ;; 1., +1. and -1. are integers, while 1.0 remains a float.
         (digit-end (if (and (< digit-start end)
                             (char= (char text (1- end)) #\.))
                        (1- end)
                        end)))
    (and (< digit-start digit-end)
         (loop for i from digit-start below digit-end
               always (find (char text i) "0123456789")))))

(defun keyword-token-p (text start end)
  (and (< (1+ start) end)
       (char= (char text start) #\:)
       (loop for i from (1+ start) below end
             never (char= (char text i) #\:))))

(defun refuse-evaluation-syntax (text)
  "Lex the restricted grammar before the Common Lisp reader can intern a
forbidden token. Report the token's UTF-8 start byte. Comment text is opaque;
each of its UTF-8 characters is counted exactly once."
  (let ((in-string nil) (escaped nil) (string-start-byte nil) (byte-offset 0)
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
                  (if in-string
                      (setf in-string nil
                            string-start-byte nil)
                      (setf in-string t
                            string-start-byte byte-offset))
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
                 ((or (reader-whitespace-p ch) (find ch "()"))
                  (incf byte-offset (char-utf8-bytes ch))
                  (incf i))
                 (t
                  (let ((start i) (start-byte byte-offset))
                    (loop while (and (< i len)
                                     (not (reader-token-boundary-p (char text i))))
                          do (incf byte-offset (char-utf8-bytes (char text i)))
                             (incf i))
                    (unless (or (decimal-integer-token-p text start i)
                                (keyword-token-p text start i))
                      (error 'restricted-data-violation
                             :value (format nil "forbidden token at byte ~D" start-byte))))))))
    (when in-string
      (error 'restricted-data-violation
             :value (format nil "unterminated string at byte ~D" string-start-byte)))))

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

(defun read-restricted (text &key max-bytes max-depth max-nodes)
  "Read one restricted s-expression from TEXT. Counted in *PARSES*.
When MAX-BYTES, MAX-DEPTH or MAX-NODES are given, refuse inputs that exceed them."
  (refuse-evaluation-syntax text)
  (when (and max-bytes (> (length text) max-bytes))
    (error 'restricted-data-violation
           :value (format nil "input ~D bytes exceeds max-bytes ~D" (length text) max-bytes)))
  (let ((*read-eval* nil)
        (*package* (find-package '#:nova-work.read))
        (*read-base* 10)
        (end-of-input (gensym "END-OF-INPUT-")))
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
          (let ((next (handler-case (read in nil end-of-input)
                         (error ()
                           ;; A stray closer is trailing bytes, not an end.
                           (error 'restricted-data-violation
                                  :value (format nil "trailing bytes after one form, at byte ~D"
                                                 (offset)))))))
            (unless (eq next end-of-input)
              (error 'restricted-data-violation
                     :value (format nil "trailing bytes after one form, at byte ~D" (offset)))))
          (incf *parses*)
          (let ((form (check-restricted form)))
            (when (and max-depth (plusp max-depth))
              (let ((peak (scan-depth form 0)))
                (when (> peak max-depth)
                  (error 'restricted-data-violation
                         :value (format nil "depth ~D exceeds max-depth ~D" peak max-depth)))))
            (when (and max-nodes (plusp max-nodes))
              (let ((nodes (count-nodes form)))
                (when (> nodes max-nodes)
                  (error 'restricted-data-violation
                         :value (format nil "nodes ~D exceeds max-nodes ~D" nodes max-nodes)))))
            form))))))

(defun scan-depth (form current)
  "Return the peak nesting depth of FORM, where CURRENT is the depth already reached."
  (typecase form
    (cons (let ((child-depth (1+ current)))
            (loop for sub in form
                  maximize (scan-depth sub child-depth))))
    (t current)))

(defun count-nodes (form)
  "Count the total number of cons cells in FORM."
  (typecase form
    (cons (loop for sub in form
                summing (count-nodes sub)
                into total
                finally (return (1+ total))))
    (t 1)))
