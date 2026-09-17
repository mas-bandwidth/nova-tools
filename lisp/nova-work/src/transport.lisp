;;;; transport.lisp --- the framed length-prefixed JSON wire codec.
;;;;
;;;; SPEC-WORK.md:2657-2689. One message is a 4-byte big-endian unsigned length
;;;; followed by that many bytes of one UTF-8 JSON object. Every integer the
;;;; protocol carries is a JSON string of decimal digits, never a JSON number;
;;;; a reader that meets a JSON number refuses the frame. An absent key and a
;;;; JSON null both mean not given, while an empty string and an empty array
;;;; are values. A frame past --max-frame-bytes is refused with one framed
;;;; error and the connection is then closed, never truncated and never
;;;; partially applied. This file is the codec only: the socket, the resident
;;;; session and the CLI are other rows of epic E01.

(in-package #:nova-work)

;;;; ------------------------------------------------------------------
;;;; UTF-8 (SPEC-WORK.md:2662-2663)
;;;; ------------------------------------------------------------------

#+sbcl
(defun wire-utf8-octets (string)
  "Encode STRING as UTF-8 octets, the bytes a frame carries."
  (sb-ext:string-to-octets string :external-format :utf-8))

#+sbcl
(defun wire-utf8-string (octets)
  "Decode UTF-8 OCTETS back to a Lisp string."
  (sb-ext:octets-to-string octets :external-format :utf-8))

#-sbcl
(defun wire-utf8-octets (string)
  (declare (ignore string))
  (error 'not-implemented :what "wire-utf8-octets needs an SBCL UTF-8 backend"))

#-sbcl
(defun wire-utf8-string (octets)
  (declare (ignore octets))
  (error 'not-implemented :what "wire-utf8-string needs an SBCL UTF-8 backend"))

;;;; ------------------------------------------------------------------
;;;; JSON encode (SPEC-WORK.md:2663-2672): integers are strings.
;;;; ------------------------------------------------------------------

(defun wire-json-escape (string)
  "The JSON body of STRING, without the surrounding quotes."
  (with-output-to-string (out)
    (loop for ch across string
          do (case ch
               (#\" (write-string "\\\"" out))
               (#\\ (write-string "\\\\" out))
               (#\Newline (write-string "\\n" out))
               (#\Return (write-string "\\r" out))
               (#\Tab (write-string "\\t" out))
               (#\Backspace (write-string "\\b" out))
               (#\Page (write-string "\\f" out))
               (t (if (< (char-code ch) 32)
                      (format out "\\u~4,'0X" (char-code ch))
                      (write-char ch out)))))))

(defun wire-json-encode (value)
  "One JSON value. An integer is a JSON string of decimal digits and never a
JSON number; `+absent+` and `:null` are `null`; `:true`/`:false` are the JSON
booleans; an empty list is an empty array, `(:array ...)` a non-empty one, and
an alist `((key . value) ...)` an object (:2663-2672)."
  (cond
    ((stringp value) (format nil "\"~A\"" (wire-json-escape value)))
    ((integerp value) (wire-encode-integer value))
    ((eq value :true) "true")
    ((eq value :false) "false")
    ((or (eq value :null) (absentp value)) "null")
    ((null value) "[]")
    ((and (consp value) (eq (car value) :array))
     (format nil "[~{~A~^, ~}]" (mapcar #'wire-json-encode (cdr value))))
    ((listp value)
     (format nil "{~{~A~^, ~}}"
             (mapcar (lambda (pair)
                       (format nil "\"~A\": ~A"
                               (wire-json-escape (car pair))
                               (wire-json-encode (cdr pair))))
                     value)))
    (t (error 'unsupported-input
              :what (format nil "wire value is not encodable: ~S" value)))))

;;;; ------------------------------------------------------------------
;;;; JSON decode (SPEC-WORK.md:2663-2672): a number refuses the frame.
;;;; ------------------------------------------------------------------

(defstruct (wire-json-parser (:constructor %make-wire-json-parser (text)))
  (text "" :type string)
  (pos 0 :type fixnum))

(defun wire-json-fail (parser why)
  (error 'unsupported-input
         :what (format nil "~A at offset ~D" why (wire-json-parser-pos parser))))

(defun wire-json-skip-ws (parser)
  (let ((text (wire-json-parser-text parser)))
    (loop while (and (< (wire-json-parser-pos parser) (length text))
                     (find (char text (wire-json-parser-pos parser))
                           '(#\Space #\Tab #\Newline #\Return)))
          do (incf (wire-json-parser-pos parser)))))

(defun wire-json-peek (parser)
  (let ((text (wire-json-parser-text parser))
        (i (wire-json-parser-pos parser)))
    (when (< i (length text)) (char text i))))

(defun wire-json-parse-string (parser)
  (let ((text (wire-json-parser-text parser)))
    (unless (char= (wire-json-peek parser) #\")
      (wire-json-fail parser "expected a JSON string"))
    (incf (wire-json-parser-pos parser))
    (with-output-to-string (out)
      (loop
        (let* ((i (wire-json-parser-pos parser))
               (ch (when (< i (length text)) (char text i))))
          (cond
            ((null ch) (wire-json-fail parser "unterminated JSON string"))
            ((char= ch #\") (incf (wire-json-parser-pos parser)) (return))
            ((char= ch #\\)
             (incf (wire-json-parser-pos parser))
             (let* ((e (wire-json-peek parser))
                    (code (case e
                            (#\" #\") (#\\ #\\) (#\/ #\/)
                            (#\b #\Backspace) (#\f #\Page) (#\n #\Newline)
                            (#\r #\Return) (#\t #\Tab))))
               (cond
                 (code (write-char code out) (incf (wire-json-parser-pos parser)))
                 ((char= e #\u)
                  (incf (wire-json-parser-pos parser))
                  (let ((start (wire-json-parser-pos parser))
                        (end (+ 4 (wire-json-parser-pos parser))))
                    (when (> end (length text))
                      (wire-json-fail parser "short \\u escape"))
                    (let ((n (parse-integer text :start start :end end :radix 16)))
                      (write-char (code-char n) out)
                      (setf (wire-json-parser-pos parser) end))))
                 (t (wire-json-fail parser "bad JSON escape")))))
            (t (write-char ch out) (incf (wire-json-parser-pos parser)))))))))

(defun wire-json-literal (parser word value)
  (let ((start (wire-json-parser-pos parser))
        (text (wire-json-parser-text parser)))
    (when (or (> (+ start (length word)) (length text))
              (string/= word text :start1 0 :end1 (length word)
                                :start2 start :end2 (+ start (length word))))
      (wire-json-fail parser (format nil "expected ~A" word)))
    (incf (wire-json-parser-pos parser) (length word))
    value))

(defun wire-json-parse-object (parser)
  (incf (wire-json-parser-pos parser))
  (wire-json-skip-ws parser)
  (let ((pairs '()))
    (if (char= (or (wire-json-peek parser) #\Nul) #\})
        (incf (wire-json-parser-pos parser))
        (loop
          (wire-json-skip-ws parser)
          (let ((key (wire-json-parse-string parser)))
            (wire-json-skip-ws parser)
            (unless (char= (wire-json-peek parser) #\:)
              (wire-json-fail parser "expected a colon after an object key"))
            (incf (wire-json-parser-pos parser))
            (push (cons key (wire-json-parse-value parser)) pairs))
          (wire-json-skip-ws parser)
          (case (wire-json-peek parser)
            (#\, (incf (wire-json-parser-pos parser)))
            (#\} (incf (wire-json-parser-pos parser)) (return))
            (t (wire-json-fail parser "expected a comma or the closing brace")))))
    (nreverse pairs)))

(defun wire-json-parse-array (parser)
  (incf (wire-json-parser-pos parser))
  (wire-json-skip-ws parser)
  (if (char= (or (wire-json-peek parser) #\Nul) #\])
      (progn (incf (wire-json-parser-pos parser)) '())
      (let ((out '()))
        (loop
          (push (wire-json-parse-value parser) out)
          (wire-json-skip-ws parser)
          (case (wire-json-peek parser)
            (#\, (incf (wire-json-parser-pos parser)) (wire-json-skip-ws parser))
            (#\] (incf (wire-json-parser-pos parser)) (return))
            (t (wire-json-fail parser "expected a comma or the closing bracket"))))
        (nreverse out))))

(defun wire-json-parse-value (parser)
  (wire-json-skip-ws parser)
  (let ((ch (wire-json-peek parser)))
    (cond
      ((null ch) (wire-json-fail parser "unexpected end of JSON"))
      ((char= ch #\{) (wire-json-parse-object parser))
      ((char= ch #\[) (wire-json-parse-array parser))
      ((char= ch #\") (wire-json-parse-string parser))
      ((char= ch #\t) (wire-json-literal parser "true" :true))
      ((char= ch #\f) (wire-json-literal parser "false" :false))
      ((char= ch #\n) (wire-json-literal parser "null" +absent+))
      ((or (char= ch #\-) (digit-char-p ch))
       (wire-json-fail parser "wire frame carries a JSON number"))
      (t (wire-json-fail parser "unexpected JSON token")))))

(defun wire-parse-json (text)
  "Parse one JSON value. Integer fields stay JSON strings (the schema knows
which are integers); a JSON number anywhere refuses the whole frame
(:2667-2668). `true`/`false` are `:true`/`:false` and `null` is `+absent+`."
  (let ((parser (%make-wire-json-parser text)))
    (let ((value (wire-json-parse-value parser)))
      (wire-json-skip-ws parser)
      (unless (>= (wire-json-parser-pos parser) (length text))
        (wire-json-fail parser "trailing bytes after the JSON value"))
      value)))

;;;; ------------------------------------------------------------------
;;;; Framing: a 4-byte big-endian unsigned length, then the payload
;;;; (SPEC-WORK.md:2661-2665, :5767).
;;;; ------------------------------------------------------------------

(defun wire-frame (text)
  "One frame: a 4-byte big-endian unsigned length then TEXT's UTF-8 bytes."
  (let* ((payload (wire-utf8-octets text))
         (n (length payload))
         (frame (make-array (+ 4 n) :element-type '(unsigned-byte 8))))
    (setf (aref frame 0) (ldb (byte 8 24) n)
          (aref frame 1) (ldb (byte 8 16) n)
          (aref frame 2) (ldb (byte 8 8) n)
          (aref frame 3) (ldb (byte 8 0) n))
    (replace frame payload :start1 4)
    frame))

(defun wire-frame-length (frame)
  "The big-endian unsigned byte count of FRAME's payload."
  (when (< (length frame) 4)
    (error 'unsupported-input :what "a frame shorter than its 4-byte prefix"))
  (logior (ash (aref frame 0) 24) (ash (aref frame 1) 16)
          (ash (aref frame 2) 8) (aref frame 3)))

(defun wire-frame-complete-p (frame)
  "True when FRAME holds its whole declared payload."
  (and (>= (length frame) 4)
       (>= (length frame) (+ 4 (wire-frame-length frame)))))

(defun wire-frame-payload (frame)
  "The UTF-8 payload string of a complete FRAME."
  (let ((n (wire-frame-length frame)))
    (unless (>= (length frame) (+ 4 n))
      (error 'unsupported-input :what "an incomplete frame has no payload"))
    (wire-utf8-string (subseq frame 4 (+ 4 n)))))

(defun wire-frame-oversized-p (frame max-frame-bytes)
  "A frame whose declared payload is past MAX-FRAME-BYTES is refused whole."
  (> (wire-frame-length frame) max-frame-bytes))

(defun wire-frame-error (reason)
  "One framed error, carrying a null request id and ok=false, before the close
(:2663-2665, :2705-2706)."
  (wire-frame
   (wire-json-encode
    (list (cons "request" +absent+)
          (cons "ok" :false)
          (cons "exit" "2")
          (cons "lines" (list :array (format nil "FAIL request=null: ~A" reason)))))))

;;;; ------------------------------------------------------------------
;;;; A streaming reader: fragments buffer, one payload at a time.
;;;; ------------------------------------------------------------------

(defstruct (wire-frame-reader (:constructor %make-wire-frame-reader))
  (max-frame-bytes 65536 :type integer)
  (buffer (make-array 64 :element-type '(unsigned-byte 8)
                         :adjustable t :fill-pointer 0)))

(defun make-wire-frame-reader (&key (max-frame-bytes 65536))
  (%make-wire-frame-reader :max-frame-bytes max-frame-bytes))

(defun wire-frame-reader-buffered (reader)
  "The bytes of the unfinished frame still held, or NIL when none are."
  (let ((buffer (wire-frame-reader-buffer reader)))
    (if (zerop (fill-pointer buffer))
        nil
        (subseq buffer 0 (fill-pointer buffer)))))

(defun wire-frame-reader-feed (reader octets)
  "Append OCTETS to READER and answer the complete payload strings now
available. Bytes of an unfinished frame stay buffered, so a response delivered
in fragments dispatches nothing until its last fragment arrives. A frame past
the reader's bound refuses whole, before any payload is answered (:2663-2665)."
  (let ((buffer (wire-frame-reader-buffer reader)))
    (loop for octet across octets do (vector-push-extend octet buffer))
    (let ((out '()))
      (loop
        (when (< (fill-pointer buffer) 4) (return))
        (let ((n (logior (ash (aref buffer 0) 24) (ash (aref buffer 1) 16)
                         (ash (aref buffer 2) 8) (aref buffer 3))))
          (when (> n (wire-frame-reader-max-frame-bytes reader))
            (error 'unsupported-input
                   :what (format nil "wire frame of ~D bytes exceeds max-frame-bytes ~D"
                                 n (wire-frame-reader-max-frame-bytes reader))))
          (when (< (fill-pointer buffer) (+ 4 n)) (return))
          (push (wire-utf8-string (subseq buffer 4 (+ 4 n))) out)
          (let ((remaining (- (fill-pointer buffer) (+ 4 n))))
            (replace buffer buffer :start1 0 :start2 (+ 4 n)
                                    :end2 (fill-pointer buffer))
            (setf (fill-pointer buffer) remaining))))
      (nreverse out))))
