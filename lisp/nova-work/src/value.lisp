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

(defun read-restricted (text)
  "Read one restricted s-expression from TEXT. Counted in *PARSES*."
  (refuse-evaluation-syntax text)
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
          (check-restricted form))))))

;;; ------------------------------------------------------------------
;;; Intake limits and pre-parse bounds scanning
;;; (SPEC-WORK.md:837-845, :6248)
;;; ------------------------------------------------------------------

(defstruct (intake-limits (:constructor make-intake-limits
                              (&key (max-depth 64) (max-bytes 65536) (max-nodes 4096))))
  (max-depth 64) (max-bytes 65536) (max-nodes 4096))

(defvar *intake-visits* 0
  "Bytes examined by intake-scan. A deep or high-fan-out input must grow this
linearly in its own length, never quadratically.")

(defun intake-scan (text &optional limits)
  "One linear pre-parse pass, counting peak nesting depth and atom nodes.
Respects Common Lisp lexical rules: ignores parentheses and whitespace inside
double-quoted strings (handling escaped quotes \") and line comments (;...\n).
Recognizes all Common Lisp whitespace characters: space (#\Space), tab (#\Tab),
newline (#\Newline), return (#\Return), and page (#\Page).
It never calls EVAL or READ, so reader evaluation is disabled by construction."
  (declare (ignore limits))
  (let ((depth 0)
        (peak 0)
        (nodes 0)
        (in-token nil)
        (in-string nil)
        (escaped nil)
        (in-comment nil))
    (loop for ch across text
          do (incf *intake-visits*)
             (cond
               (in-comment
                (when (or (char= ch #\Newline) (char= ch #\Return))
                  (setf in-comment nil)))
               (in-string
                (cond
                  (escaped
                   (setf escaped nil))
                  ((char= ch #\\)
                   (setf escaped t))
                  ((char= ch #\")
                   (setf in-string nil))))
               ((char= ch #\;)
                (setf in-comment t
                      in-token nil))
               ((char= ch #\")
                (setf in-string t
                      escaped nil
                      in-token nil)
                (incf nodes))
               ((char= ch #\()
                (incf depth)
                (setf peak (max peak depth)
                      in-token nil))
               ((char= ch #\))
                (when (plusp depth) (decf depth))
                (setf in-token nil))
               ((reader-whitespace-p ch)
                (setf in-token nil))
               (t
                (unless in-token
                  (incf nodes)
                  (setf in-token t)))))
    (values peak nodes)))

(defgeneric session-bounds (sess)
  (:documentation "Return the bounds governing SESS as (values max-bytes max-depth max-nodes)."))

(defmethod session-bounds ((sess t))
  (values nil nil nil))

(defun octets-to-utf8-string (bytes)
  "Convert a byte vector into a UTF-8 string."
  #+sbcl (sb-ext:octets-to-string bytes :external-format :utf-8)
  #-sbcl (map 'string #'code-char bytes))

(defun %read-bounded-octets-from-stream (stream &key max-bytes (file "input") (signal-error t))
  "Incrementally read octets from STREAM up to MAX-BYTES without reading or allocating beyond limit + 1."
  (let* ((chunk-size 8192)
         (buf (make-array chunk-size :element-type '(unsigned-byte 8)))
         (total 0)
         (acc (make-array 0 :element-type '(unsigned-byte 8) :adjustable t :fill-pointer 0)))
    (loop
      (let ((to-read chunk-size))
        (when max-bytes
          (setf to-read (min chunk-size (- (+ max-bytes 1) total))))
        (when (and max-bytes (<= to-read 0))
          (if signal-error
              (error 'read-bounds-exceeded :file file :bound "--max-bytes" :limit max-bytes :observed total)
              (return-from %read-bounded-octets-from-stream
                (values nil (format nil "read ~A: ~D bytes exceeds --max-bytes ~D" file total max-bytes) 2))))
        (let ((n (read-sequence buf stream :end to-read)))
          (when (zerop n)
            (return))
          (let ((new-total (+ total n)))
            (when (and max-bytes (> new-total max-bytes))
              (if signal-error
                  (error 'read-bounds-exceeded :file file :bound "--max-bytes" :limit max-bytes :observed new-total)
                  (return-from %read-bounded-octets-from-stream
                    (values nil (format nil "read ~A: ~D bytes exceeds --max-bytes ~D" file new-total max-bytes) 2))))
            (let ((old-len (length acc)))
              (adjust-array acc (+ old-len n) :fill-pointer (+ old-len n))
              (replace acc buf :start1 old-len :start2 0 :end2 n))
            (setf total new-total)))))
    (values acc (format nil "READ OK file=~A bytes=~D" file total) 0)))

(defun read-bounded-octets (source &key max-bytes (file nil) (signal-error t))
  "Read octets from SOURCE (stream, pathname, or string path) bounded by MAX-BYTES.
Enforces MAX-BYTES incrementally during read: as soon as MAX-BYTES + 1 bytes are
read, stops reading immediately and refuses, avoiding unbounded allocation.
Returns (values octets line exit-code)."
  (let ((file-str (or file
                      (cond ((stringp source) (namestring (merge-pathnames source)))
                            ((pathnamep source) (namestring (merge-pathnames source)))
                            (t "stream")))))
    (if (streamp source)
        (%read-bounded-octets-from-stream source :max-bytes max-bytes :file file-str :signal-error signal-error)
        (progn
          (unless (probe-file source)
            (if signal-error
                (error 'unsupported-input :what (format nil "file not found: ~A" file-str))
                (return-from read-bounded-octets (values nil (format nil "file not found: ~A" file-str) 2))))
          (with-open-file (in source :direction :input :element-type '(unsigned-byte 8))
            (%read-bounded-octets-from-stream in :max-bytes max-bytes :file file-str :signal-error signal-error))))))

(defun read-bounded-file (source &key max-bytes max-depth max-nodes session (require-all t) (signal-error t))
  "Read a file under uniform read bounds: max-bytes, max-depth, and max-nodes.
If SESSION is supplied, its bounds govern the read (SPEC-WORK.md:839-845).
If REQUIRE-ALL is true and session is omitted, missing bounds refuse exit 2 ('refusing to guess').
If any bound is exceeded, refuses exit 2 naming which bound and which file, never truncated.
Reads incrementally in a single open descriptor, preventing check/read races."
  (multiple-value-bind (sess-mb sess-md sess-mn)
      (if session (session-bounds session) (values nil nil nil))
    (let* ((mb (or max-bytes sess-mb))
           (md (or max-depth sess-md))
           (mn (or max-nodes sess-mn))
           (file-str (cond ((stringp source) (namestring (merge-pathnames source)))
                           ((pathnamep source) (namestring (merge-pathnames source)))
                           (t "stream"))))
      (when require-all
        (unless mb
          (if signal-error
              (error 'missing-read-bounds :bound "--max-bytes")
              (return-from read-bounded-file (values nil "missing --max-bytes: refusing to guess" 2))))
        (unless md
          (if signal-error
              (error 'missing-read-bounds :bound "--max-depth")
              (return-from read-bounded-file (values nil "missing --max-depth: refusing to guess" 2))))
        (unless mn
          (if signal-error
              (error 'missing-read-bounds :bound "--max-nodes")
              (return-from read-bounded-file (values nil "missing --max-nodes: refusing to guess" 2)))))
      (multiple-value-bind (octets line code)
          (read-bounded-octets source :max-bytes mb :file file-str :signal-error signal-error)
        (unless octets
          (return-from read-bounded-file (values nil line code)))
        (let ((text (octets-to-utf8-string octets)))
          (multiple-value-bind (depth nodes) (intake-scan text)
            (when (and md (> depth md))
              (if signal-error
                  (error 'read-bounds-exceeded :file file-str :bound "--max-depth" :limit md :observed depth)
                  (return-from read-bounded-file
                    (values nil (format nil "read ~A: depth ~D exceeds --max-depth ~D" file-str depth md) 2))))
            (when (and mn (> nodes mn))
              (if signal-error
                  (error 'read-bounds-exceeded :file file-str :bound "--max-nodes" :limit mn :observed nodes)
                  (return-from read-bounded-file
                    (values nil (format nil "read ~A: nodes ~D exceeds --max-nodes ~D" file-str nodes mn) 2)))))
          (values text (format nil "READ OK file=~A bytes=~D" file-str (length octets)) 0))))))

(defun check-read-bounds (text &key max-bytes max-depth max-nodes session (file "input") (require-all nil) (signal-error t))
  "Check that TEXT obeys max-bytes, max-depth, and max-nodes.
When SESSION is supplied, its bounds govern the check.
When REQUIRE-ALL is true, missing bounds refuse with 'refusing to guess'.
When bounds are exceeded, refuse naming which bound and which file (SPEC-WORK.md:837-845)."
  (multiple-value-bind (sess-mb sess-md sess-mn)
      (if session (session-bounds session) (values nil nil nil))
    (let ((mb (or max-bytes sess-mb))
          (md (or max-depth sess-md))
          (mn (or max-nodes sess-mn)))
      (when require-all
        (unless mb
          (if signal-error
              (error 'missing-read-bounds :bound "--max-bytes")
              (return-from check-read-bounds (values nil "missing --max-bytes: refusing to guess" 2))))
        (unless md
          (if signal-error
              (error 'missing-read-bounds :bound "--max-depth")
              (return-from check-read-bounds (values nil "missing --max-depth: refusing to guess" 2))))
        (unless mn
          (if signal-error
              (error 'missing-read-bounds :bound "--max-nodes")
              (return-from check-read-bounds (values nil "missing --max-nodes: refusing to guess" 2)))))
      (let ((byte-count (utf8-bytes-up-to text (length text))))
        (when (and mb (> byte-count mb))
          (if signal-error
              (error 'read-bounds-exceeded :file file :bound "--max-bytes" :limit mb :observed byte-count)
              (return-from check-read-bounds
                (values nil (format nil "read ~A: ~D bytes exceeds --max-bytes ~D" file byte-count mb) 2)))))
      (multiple-value-bind (depth nodes) (intake-scan text)
        (when (and md (> depth md))
          (if signal-error
              (error 'read-bounds-exceeded :file file :bound "--max-depth" :limit md :observed depth)
              (return-from check-read-bounds
                (values nil (format nil "read ~A: depth ~D exceeds --max-depth ~D" file depth md) 2))))
        (when (and mn (> nodes mn))
          (if signal-error
              (error 'read-bounds-exceeded :file file :bound "--max-nodes" :limit mn :observed nodes)
              (return-from check-read-bounds
                (values nil (format nil "read ~A: nodes ~D exceeds --max-nodes ~D" file nodes mn) 2)))))
      (values t (format nil "BOUNDS OK file=~A" file) 0))))
