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

;;; ------------------------------------------------------------------
;;; The response object's own request field.
;;; ------------------------------------------------------------------

(defun %wire-object-body (frame)
  "The inside of one JSON object FRAME, or an error when it is not an object."
  (let ((trimmed (wire-trim frame)))
    (unless (and (>= (length trimmed) 2)
                 (char= (char trimmed 0) #\{)
                 (char= (char trimmed (1- (length trimmed))) #\}))
      (error 'unsupported-input
             :what (format nil "wire frame is not a JSON object: ~A" frame)))
    (subseq trimmed 1 (1- (length trimmed)))))

(defun %wire-key (pair)
  "The key of one `"key": value` top-level pair, or NIL when it has no colon or
its key is not a quoted string."
  (let* ((colon (position #\: pair))
         (key (and colon (wire-trim (subseq pair 0 colon)))))
    (when (and key (>= (length key) 2)
               (char= (char key 0) #\")
               (char= (char key (1- (length key))) #\"))
      (subseq key 1 (1- (length key))))))

(defun wire-object-field (frame key)
  "Read KEY from the flat JSON object FRAME. Answer the string value, (:absent)
for a JSON null, or (:missing) when the object does not carry the key. A JSON
number in the field refuses, because the protocol carries no numbers."
  (loop with body = (%wire-object-body frame)
        for pair in (wire-split-top-level body #\,)
        for field-key = (%wire-key pair)
        when (and field-key (string= field-key key))
          do (let* ((colon (position #\: pair))
                    (value (wire-trim (subseq pair (1+ colon)))))
               (return
                 (cond
                   ((string= value "null") +absent+)
                   ((and (>= (length value) 2)
                         (char= (char value 0) #\")
                         (char= (char value (1- (length value))) #\"))
                    (subseq value 1 (1- (length value))))
                   ((wire-number-token-p value)
                    (error 'unsupported-input
                           :what (format nil "wire frame carries a JSON number ~A"
                                         value)))
                   (t
                    (error 'unsupported-input
                           :what (format nil "wire ~A field is not a string: ~A"
                                         key value))))))
        finally (return :missing)))

(defun wire-request-id (frame)
  "The request id a frame echoes, read from the object's own `request` field,
or (:absent) for a JSON null and (:missing) when the key is not there."
  (wire-object-field frame "request"))

(defun wire-request-op (frame)
  "The typed operation name a request frame carries, or (:missing)."
  (wire-object-field frame "op"))

;;; ------------------------------------------------------------------
;;; Building a frame: no JSON number is ever written.
;;; ------------------------------------------------------------------

(defun wire-json-string (text)
  "TEXT as one JSON string token, escaped and quoted."
  (with-output-to-string (out)
    (write-char #\" out)
    (loop for ch across text
          do (case ch
               (#\" (write-string "\\\"" out))
               (#\\ (write-string "\\\\" out))
               (#\Newline (write-string "\\n" out))
               (t (write-char ch out))))
    (write-char #\" out)))

(defun wire-json-lines (lines)
  "LINES as a JSON array of string tokens -- the wire's `lines` field, exactly
the one-line answers of the output grammar, never reformatted."
  (format nil "[~{~A~^,~}]" (mapcar #'wire-json-string lines)))

(defun wire-frame-request (op request &key (as "client") (fields '()))
  "One request frame. FIELDS is an alist of (key . pre-encoded-json-value)."
  (format nil "{\"op\": ~A, \"request\": ~A, \"as\": ~A~{, ~A: ~A~}}"
          (wire-json-string (string op))
          (wire-json-string request)
          (wire-json-string as)
          (loop for (key . value) in fields
                append (list (wire-json-string key) value))))

(defun wire-frame-response (request &key (ok "true") (exit "0") lines
                                          (rev "0") (pushed "-") operation)
  "One response frame. The request id is echoed first; `ok` and `exit` are the
only JSON booleans/number-free spellings, `rev` is the local durable revision
and `pushed` the last shared Git revision, and a long operation's durable id is
carried beside -- never in place of -- the request id that asked for it."
  (format nil "{\"request\": ~A, \"ok\": ~A, \"exit\": ~A, \"lines\": ~A, \"rev\": ~A, \"pushed\": ~A~@[, \"operation\": ~A~]}"
          (wire-json-string request)
          ok
          (wire-json-string exit)
          (wire-json-lines lines)
          (wire-json-string rev)
          (wire-json-string pushed)
          (and operation (wire-json-string operation))))

;;; ------------------------------------------------------------------
;;; The pipelined connection: request ids in flight, replies correlated.
;;; (SPEC-WORK.md:2689-2708, :2763-2766).
;;; ------------------------------------------------------------------

(defstruct (wire-connection (:constructor %make-wire-connection))
  protocol outstanding settled buffer)

(defun make-wire-connection (&key (supported '("1")) (max-frame-bytes 65536))
  (%make-wire-connection
   :protocol (make-protocol-session :supported supported
                                    :max-frame-bytes max-frame-bytes)
   :outstanding '() :settled '() :buffer ""))

(defun wire-connection-closed-p (connection)
  (protocol-session-closed-p (wire-connection-protocol connection)))

(defun wire-close (connection)
  (setf (protocol-session-closed-p (wire-connection-protocol connection)) t)
  connection)

(defun wire-pipeline-request (connection request-frame)
  "Put a request frame in flight without waiting for an earlier reply. The id
is the frame's own `request` field. The client does not put the same request id
in flight twice on one connection: a second is a protocol error that closes the
connection and admits nothing. Answer the id, or NIL when it is not admitted."
  (when (wire-connection-closed-p connection)
    (return-from wire-pipeline-request nil))
  (handler-case
      (let ((id (wire-request-id request-frame)))
        (cond
          ((eq id +absent+)
           (wire-close connection)
           nil)
          ((eq id :missing)
           (wire-close connection)
           nil)
          (t
           (multiple-value-bind (admitted why)
               (protocol-admit (wire-connection-protocol connection) id)
             (declare (ignore why))
             (unless admitted (return-from wire-pipeline-request nil)))
           (when (assoc id (wire-connection-outstanding connection) :test #'string=)
             (wire-close connection)
             (return-from wire-pipeline-request nil))
           (setf (wire-connection-outstanding connection)
                 (append (wire-connection-outstanding connection)
                         (list (cons id (wire-request-op request-frame)))))
           id)))
    (unsupported-input ()
      (wire-close connection)
      nil)))

(defun wire-feed (connection text)
  "Append TEXT to the connection's receive buffer and answer the complete
frames a newline terminates. The bytes of an unfinished frame stay buffered, so
a response delivered in fragments dispatches nothing until its last fragment
arrives."
  (let ((buffer (concatenate 'string (wire-connection-buffer connection) text)))
    (loop with start = 0
          for newline = (position #\Newline buffer :start start)
          while newline
          collect (subseq buffer start newline)
          do (setf start (1+ newline))
          finally (setf (wire-connection-buffer connection)
                        (subseq buffer start)))))

(defun wire-dispatch (connection frame)
  "Deliver one complete response frame. Answer (values REQUEST-ID FRAME) only
when the frame's own request field names exactly one outstanding request. A
null, missing, unknown or duplicate id is a protocol error: close the
connection, settle no outstanding request, and answer (values NIL REASON)."
  (when (wire-connection-closed-p connection)
    (return-from wire-dispatch (values nil "connection closed")))
  (let ((id (handler-case (wire-request-id frame)
              (unsupported-input (c)
                (wire-close connection)
                (return-from wire-dispatch
                  (values nil (format nil "FAIL request=null: ~A" c)))))))
    (cond
      ((eq id +absent+)
       (wire-close connection)
       (values nil "FAIL request=null: response frame carries a null request id"))
      ((eq id :missing)
       (wire-close connection)
       (values nil "FAIL request=null: response frame is missing its request id"))
      ((assoc id (wire-connection-settled connection) :test #'string=)
       (wire-close connection)
       (values nil (format nil "FAIL request=~A: duplicate response id" id)))
      ((not (assoc id (wire-connection-outstanding connection) :test #'string=))
       (wire-close connection)
       (values nil (format nil "FAIL request=~A: unknown response id" id)))
      (t
       (setf (wire-connection-outstanding connection)
             (remove id (wire-connection-outstanding connection)
                     :key #'car :test #'string=))
       (setf (wire-connection-settled connection)
             (append (wire-connection-settled connection) (list (cons id frame))))
       (values id frame)))))

(defun wire-reconcile-outstanding (connection)
  "The request ids still outstanding on a connection whose protocol error just
closed it: the client reconciles these mutation ids rather than guessing which
request succeeded (SPEC-WORK.md:2702-2706)."
  (mapcar #'car (wire-connection-outstanding connection)))

;;; ------------------------------------------------------------------
;;; The read bundle's three batch spells stay over the same envelope.
;;; (SPEC-WORK.md:2762-2779).
;;; ------------------------------------------------------------------

(defun independent-batch-results (entries)
  "An independent batch is ordered entries with their own request ids. The
default is to stop at the first refusal and mark every remaining entry
`:not-attempted` (SPEC-WORK.md:2777-2779)."
  (let ((stopped nil) (out '()))
    (dolist (entry entries)
      (cond
        (stopped (push (list (car entry) :not-attempted) out))
        ((cdr entry) (push (list (car entry) (cdr entry)) out))
        (t (push (list (car entry) :refused) out)
           (setf stopped t))))
    (nreverse out)))

(defun atomic-batch-validate (entries)
  "An atomic batch validates whole. A NIL disposition is a validation failure:
answer (values NIL FAILING-REQUEST-ID) and write nothing. Otherwise answer
(values T NIL)."
  (let ((failed (find-if (lambda (entry) (null (cdr entry))) entries)))
    (if failed
        (values nil (car failed))
        (values t nil))))

;;; ------------------------------------------------------------------
;;; A mutation connection: disconnect is not a rollback
;;; (SPEC-WORK.md:2708-2717).
;;; ------------------------------------------------------------------

(defstruct (wire-session (:constructor %make-wire-session))
  kernel pushed client-alive-p operations)

(defun make-wire-session (&key kernel (pushed "-") (client-alive-p t)
                               (operations nil))
  (%make-wire-session :kernel kernel :pushed pushed
                      :client-alive-p client-alive-p
                      :operations (or operations (make-operation-registry))))

(defun wire-session-mutate (session request)
  "Enter the mutation through the kernel's single writer and answer
(values OK LINE EXIT RESPONSE). The response carries rev= (the local durable
revision) and pushed= (the last shared Git revision) as two fields. The same
request id and body on a reconnected client returns the journal's recorded
disposition and applies no second event; the same id with different arguments
is refused by the kernel's dedup predicate."
  (multiple-value-bind (ok line code envelope)
      (submit (wire-session-kernel session) request)
    (declare (ignore envelope))
    (values ok line code
            (list :request (getf request :request)
                  :ok ok
                  :rev (state-revision (kernel-state (wire-session-kernel session)))
                  :pushed (wire-session-pushed session)))))

;;;; transport.lisp --- the socket endpoint, the local listener and the
;;;; resident session server daemon.
;;;;
;;;; SPEC-WORK.md:2642-2655: the engine is the resident session; the Go CLI is
;;;; a thin client over one explicitly named local endpoint (a Unix-domain
;;;; socket, or the Windows named pipe under its platform's spelling and never
;;;; a second transport). The socket and the directory that holds it belong to
;;;; the running account: the directory created 0700 and the socket 0600, both
;;;; owned by that account, and there is no network listener and no remote
;;;; evaluation protocol anywhere in this scope (replay
;;;; `endpoint-is-local-and-private`, prose :5879-5882).
;;;;
;;;; SPEC-WORK.md:268-310, :2256-2267: `session start` is the launcher and not
;;;; the session process; the process it starts is a supervised, long-lived
;;;; session that serves reads against its resident objects while a fresh CLI
;;;; process is never a fresh parse.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The endpoint: a local socket path in a directory owned 0700.
;;; ------------------------------------------------------------------

(defstruct (session-endpoint
             (:constructor %make-session-endpoint
                 (directory socket-path directory-mode socket-mode owner socket-family)))
  directory socket-path directory-mode socket-mode owner socket-family)

(defun session-endpoint-dir-mode (endpoint) (session-endpoint-directory-mode endpoint))
(defun session-endpoint-file-mode (endpoint) (session-endpoint-socket-mode endpoint))

#+sbcl
(defun %file-mode (path)
  (logand (sb-posix:stat-mode (sb-posix:stat path)) #o777))

#+sbcl
(defun local-socket-family ()
  "The family of a local socket, for the endpoint assertion."
  (sb-bsd-sockets:socket-family (make-instance 'sb-bsd-sockets:local-socket :type :stream)))

#-sbcl
(defun local-socket-family () :local)

#+sbcl
(defun current-account-uid () (sb-posix:getuid))

#-sbcl
(defun current-account-uid () 0)

(defun make-session-endpoint (directory socket-path)
  "Create or reuse DIRECTORY at 0700 and validate SOCKET-PATH. A pre-existing
directory or socket with wider modes refuses rather than being reused. The
endpoint is a name for the local socket; MAKE-LOCAL-LISTENER does the binding,
so a validated endpoint is not itself a listener."
  (unless (probe-file directory)
    (sb-posix:mkdir directory #o700))
  (let ((dmode (%file-mode directory)))
    (unless (eql dmode #o700)
      (error 'unsupported-input
             :what (format nil "endpoint: pre-existing directory ~A has mode ~O, not 0700"
                           directory dmode))))
  (when (probe-file socket-path)
    (let ((smode (%file-mode socket-path)))
      (unless (eql smode #o600)
        (error 'unsupported-input
               :what (format nil "endpoint: pre-existing socket ~A has mode ~O, not 0600"
                             socket-path smode)))))
  (%make-session-endpoint directory socket-path #o700 #o600 (sb-posix:getuid)
                          (local-socket-family)))

(defun endpoint-network-listener-p (endpoint)
  "True iff the endpoint's socket family is a network family. The listener's
family is AF_UNIX, so this is false; an AF_INET family would make it true."
  (let ((family (session-endpoint-socket-family endpoint)))
    (and family (not (eql family (local-socket-family))))))

;;; ------------------------------------------------------------------
;;; The local listener: bind, listen, accept.
;;; ------------------------------------------------------------------

(defstruct (local-listener (:constructor %make-local-listener))
  endpoint socket (open-p t))

(defun make-local-listener (endpoint)
  "Bind and listen on ENDPOINT's local socket, chmod the bound path 0600 and
answer a LISTENER. A bound local socket is never a network listener."
  (let* ((socket (make-instance 'sb-bsd-sockets:local-socket :type :stream))
         (path (session-endpoint-socket-path endpoint)))
    (sb-bsd-sockets:socket-bind socket path)
    (sb-bsd-sockets:socket-listen socket 16)
    (sb-posix:chmod path #o600)
    (%make-local-listener :endpoint endpoint :socket socket :open-p t)))

(defun listener-open-p (listener)
  (and listener (local-listener-open-p listener)))

(defun listener-socket-family (listener)
  "The family of the listening socket: AF_UNIX for a local endpoint."
  (sb-bsd-sockets:socket-family (local-listener-socket listener)))

(defun listener-accept (listener)
  "Accept one connection on LISTENER and answer the connected socket."
  (values (sb-bsd-sockets:socket-accept (local-listener-socket listener))))

(defun listener-close (listener)
  "Close LISTENER's socket and unlink its path."
  (when (local-listener-open-p listener)
    (setf (local-listener-open-p listener) nil)
    (ignore-errors (sb-bsd-sockets:socket-close (local-listener-socket listener))))
  (ignore-errors (sb-posix:unlink (session-endpoint-socket-path
                                   (local-listener-endpoint listener))))
  listener)

;;; ------------------------------------------------------------------
;;; The resident session server daemon.
;;; ------------------------------------------------------------------

(defstruct (session-server (:constructor %make-session-server))
  session listener thread (running-p t) (path "") (owner "") (foreground t))

(defun session-build-identity ()
  "The build= field: the running binary says which build it is
(SPEC-WORK.md:302-303)."
  (format nil "~A-~A" (lisp-implementation-type) (lisp-implementation-version)))

(defun session-kernel-state (session)
  (let ((kernel (session-kernel session)))
    (and kernel (kernel-state kernel))))

(defun session-status-line (session)
  "The full `SESSION OK` identity line `session start`, `session status` and
`session stop` print alike: identity, state, journal path, base revision, clip
cadence and every bound, each read rather than remembered
(SPEC-WORK.md:304-308, output grammar :5340-5346)."
  (let* ((state (session-kernel-state session))
         (base (session-base session))
         (nodes (if state (hash-table-count (wstate-nodes state)) 0))
         (edges (if state
                    (loop for n being the hash-values of (wstate-nodes state)
                          count (and (wnode-parent n) t))
                    0))
         (events (if state (length (wstate-history state)) 0)))
    (format nil "SESSION OK session=~A owner=~A generation=~D state=~(~A~) until=~A file=~A base=~A journal=~A events=~D pending=~D pushed=~A nodes=~D edges=~D parses=~D replays=~D every=~A skew=~A clip-every=~A clip-after=~D retain=~A index-cache=~D page-bytes=~D page-records=~D closed-window=~A max-bytes=~D max-depth=~D max-nodes=~D boundary=~D findings=~D build=~A emitted=~D"
            (session-path session)
            (session-owner session)
            (session-generation session)
            (session-state session)
            (session-until session)
            (session-file session)
            (if (or (null base) (zerop (length base))) "-" base)
            (session-journal session)
            events
            (session-pending session)
            (session-pushed session)
            nodes edges
            (session-parses session)
            (session-replays session)
            (session-every session)
            (session-skew session)
            (session-clip-every session)
            (session-clip-after session)
            (session-retain session)
            (session-index-cache session)
            (session-page-bytes session)
            (session-page-records session)
            (session-closed-window session)
            (session-max-bytes session)
            (session-max-depth session)
            (session-max-nodes session)
            (session-boundary session)
            (session-findings session)
            (session-build-identity)
            (session-emitted session))))

(defun session-identity-line (session)
  "The `SESSION OK` identity line a running session prints and serves."
  (session-status-line session))

;;; ------------------------------------------------------------------
;;; `session stop` and `session handoff` (SPEC-WORK.md:814-828).
;;;
;;; Both clip under the same guard and then publish one owner record: a stop
;;; releases it with `until` at the stop's stamp and no successor, a handoff
;;; writes the same generation, `until` at the handoff's stamp and
;;; `successor=<name>`, and exits fenced. The clip's git push is the seam
;;; below: the local event boundary and the lines are real, the remote CAS
;;; push is out of this slice.
;;; ------------------------------------------------------------------

(defun session-owning-record (session)
  "The ownership record the resident session currently holds."
  (make-ownership-record :owner (session-owner session)
                         :generation (session-generation session)
                         :token (session-token session)
                         :stamp (session-until session)
                         :until (session-until session)
                         :bench ""))

(defun %released-ownership-record (record &key (now "") successor)
  "RECORD released: the generation is kept, `until` is the stopping stamp, and
SUCCESSOR is written only by a handoff (SPEC-WORK.md:817-826)."
  (make-ownership-record :owner (if successor (owner-owner record) "")
                         :generation (owner-generation record)
                         :token (owner-token record)
                         :stamp (owner-stamp record)
                         :until now
                         :bench (owner-bench record)
                         :successor successor))

(defun session-clip-line (session &key race (operation-id "op-clip")
                                         (request "req-clip") (commit "cafef00d"))
  "The CLIP OK line a stop waits for; CLIP RACED when the base predicate
refused the push (SPEC-WORK.md:2740-2747, output grammar :5362-5363)."
  (let* ((state (session-kernel-state session))
         (events (if state (length (wstate-history state)) 0)))
    (if race
        (format nil "CLIP RACED session=~A operation=~A boundary=~A generation=~D expected=~A found=~A"
                (session-path session) operation-id request (session-generation session)
                (session-base session) "deadbeef")
        (format nil "CLIP OK session=~A operation=~A boundary=~A events=~D base=~A commit=~A pushed=~A attempts=1 emitted=0"
                (session-path session) operation-id request events
                (session-base session) commit (session-pushed session)))))

(defun session-stop-lifecycle (session &key no-clip race
                                           (now (format-rfc3339 (get-universal-time))))
  "`session stop` is the same sequence as a handoff without a successor: clip
unless NO-CLIP, then release the owner with `until` at the stop's stamp, so a
taker after a planned stop waits `--skew` (SPEC-WORK.md:824-826). Answers
(values T LINES RECORD); LINES is the CLIP OK line (unless NO-CLIP) followed by
the SESSION OK identity line."
  (let ((clip-line (unless no-clip (session-clip-line session :race race)))
        (status-line (session-status-line session))
        (released (%released-ownership-record (session-owning-record session)
                                              :now now :successor nil)))
    (setf (session-state session) :fenced
          (session-until session) now
          (session-owner session) ""
          (session-token session) "")
    (values t (if clip-line (list clip-line status-line) (list status-line))
            released)))

(defun session-handoff (session &key to commit (pushed (session-pushed session))
                                        (now (format-rfc3339 (get-universal-time))))
  "`session handoff` fences the session from admission, clips under the same
guard, then publishes one owner record with the same generation, `until` at the
handoff's stamp and `successor=<name>`, and exits fenced
(SPEC-WORK.md:814-822). Answers (values T LINE RECORD)."
  (let* ((released (%released-ownership-record (session-owning-record session)
                                               :now now :successor to)))
    (setf (session-state session) :fenced
          (session-until session) now)
    (values t
            (format nil "HANDOFF OK session=~A generation=~D to=~A commit=~A pushed=~A emitted=0"
                    (session-path session) (session-generation session) to commit pushed)
            released)))

(defun session-identity-line (session)
  "The `SESSION OK` identity line a running session prints and serves: identity,
bounds and clip cadence are explicit and read, never remembered
(SPEC-WORK.md:304-308)."
  (let ((base (session-base session)))
    (format nil "SESSION OK owner=~A generation=~D token=~A state=~(~A~) until=~A base=~A every=~A skew=~A max-bytes=~D max-depth=~D max-nodes=~D index-cache=~D page-bytes=~D page-records=~D closed-window=~A"
            (session-owner session)
            (session-generation session)
            (session-token session)
            (session-state session)
            (session-until session)
            (if (or (null base) (zerop (length base))) "nil" base)
            (session-every session)
            (session-skew session)
            (session-max-bytes session)
            (session-max-depth session)
            (session-max-nodes session)
            (session-index-cache session)
            (session-page-bytes session)
            (session-page-records session)
            (session-closed-window session))))

(defun endpoint-directory-for (socket-path)
  "The directory that holds SOCKET-PATH, as a directory namestring."
  (namestring (make-pathname :name nil :type nil :defaults (pathname socket-path))))

(defun default-session-request-handler (server request)
  "The one in-process implementation of the serve seam. It answers the
read-only session requests from the resident session; mutations and the wire
protocol arrive in a later slice through *SESSION-REQUEST-HANDLER*."
  (cond
    ((member request '("status" "session status" "ping" "session ping")
             :test #'string=)
     (values t (session-identity-line (session-server-session server)) 0))
    (t
     (values nil (format nil "FAIL request=~A: unsupported in-process request"
                         request)
             2))))

(defvar *session-request-handler* nil
  "The serve seam: NIL selects DEFAULT-SESSION-REQUEST-HANDLER, the one
in-process implementation. A later wire slice binds the framed protocol here.")

(defun serve-session-request (server request)
  "Answer (values OK LINE EXIT) for one request line against SERVER."
  (funcall (or *session-request-handler* #'default-session-request-handler)
           server request))

(defun %serve-connection (server connection)
  "Serve every request line on one accepted CONNECTION until the peer closes."
  (unwind-protect
       (handler-case
           (let ((stream (sb-bsd-sockets:socket-make-stream
                          connection :input t :output t
                          :element-type 'character :external-format :utf-8)))
             (loop for request = (read-line stream nil :eof)
                   until (eq request :eof)
                   do (multiple-value-bind (ok answer code)
                          (serve-session-request server request)
                        (declare (ignore ok code))
                        (write-line answer stream)
                        (finish-output stream))))
         (error () nil))
    (ignore-errors (sb-bsd-sockets:socket-close connection))))

(defun %session-server-loop (server)
  "The daemon's accept loop: one thread per accepted local connection, until
the server is stopped."
  (loop while (session-server-running-p server)
        do (handler-case
               (multiple-value-bind (connection peer)
                   (sb-bsd-sockets:socket-accept
                    (local-listener-socket (session-server-listener server)))
                 (declare (ignore peer))
                 (sb-thread:make-thread
                  (lambda () (%serve-connection server connection))
                  :name "nova-work-session-connection"))
             (error () (return)))))

(defun start-session-server (session &key socket-path (foreground t))
  "Make SESSION the resident process: bind the local listener at SOCKET-PATH
and spawn the accept thread. Answer (values SERVER LINE EXIT). With FOREGROUND
NIL the caller is the launcher: it receives the SESSION OK line and returns
while the daemon stays up and serves (SPEC-WORK.md:268-280)."
  (let* ((endpoint (make-session-endpoint (endpoint-directory-for socket-path)
                                          socket-path))
         (listener (make-local-listener endpoint))
         (server (%make-session-server :session session
                                       :listener listener
                                       :running-p t
                                       :path socket-path
                                       :owner (session-owner session)
                                       :foreground foreground)))
    (setf (session-server-thread server)
          (sb-thread:make-thread (lambda () (%session-server-loop server))
                                 :name "nova-work-session-server"))
    (values server (session-identity-line session) 0)))

(defun session-server-stop (server)
  "Stop the daemon: close the listener, unlink its path and join the accept
thread. The session is stopped explicitly and inspectably (SPEC-WORK.md:308)."
  (when (session-server-running-p server)
    (setf (session-server-running-p server) nil)
    (listener-close (session-server-listener server))
    (let ((thread (session-server-thread server)))
      (when thread
        (ignore-errors (sb-thread:join-thread thread :timeout 5 :default nil)))))
  t)
;;;; transport.lisp --- the read bundle, the independent batch and the atomic
;;;; mutation batch (docs/SPEC-WORK.md:2762-2814, the B2/B3/B4/B5 paragraphs).
;;;;
;;;; The three batch modes ride the accepted mutation envelope and the framed
;;;; protocol the wire codec of src/operations.lisp already defines. Here they
;;;; are made real over the kernel: the read bundle captures one revision and
;;;; one lease-time watermark and answers the asks it names from that snapshot;
;;;; an independent batch enters each entry through the kernel's one writer and
;;;; stops at the first refusal by default; an atomic batch validates every
;;;; entry against the expected revision before one of them is published.
;;;;
;;;; The one thing the slice-1 kernel cannot do is commit several entries as a
;;;; single journal envelope; the atomic batch therefore validates the whole
;;;; batch on a private reconstruction and only then applies the entries through
;;;; the one writer. That is noted in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; B2 -- a read bundle is one revision and one watermark.
;;; ------------------------------------------------------------------

(defstruct (read-bundle (:conc-name read-bundle-)
                        (:constructor %make-read-bundle))
  "A captured revision and its lease-time watermark. Every ask is evaluated
against REVISION, so a mutation after the capture cannot move what the bundle
answers; the work set is never read."
  revision watermark state)

(defun make-read-bundle (kernel &key (watermark 0))
  "Capture the kernel's current revision and the lease-time watermark in force
when the bundle was opened. One bundle is one snapshot identity."
  (%make-read-bundle :revision (state-revision (kernel-state kernel))
                     :watermark watermark
                     :state (kernel-state kernel)))

(defun read-bundle-ask (bundle ask)
  "Evaluate one bounded ask against the bundle's captured revision and answer
the fields and aggregates the ask names. `:size` reads the |O| and |C| counters
the bundle's own revision maintains; `:watermark` reads the lease-time
watermark. The work set (W) is never read."
  (ecase ask
    (:size (list :open (state-open-count (read-bundle-state bundle))
                 :closed (state-closed-count (read-bundle-state bundle))))
    (:watermark (read-bundle-watermark bundle))))

(defun read-bundle-page (bundle floor &key (ask :size))
  "A later page keeps the bundle's snapshot identity; asked at or above FLOOR
the snapshot has expired and the page refuses `page expired`, the cursor rule of
Retention rather than a second one. Answers (values FIELDS LINE)."
  (if (< (read-bundle-revision bundle) floor)
      (values nil (format nil "BUNDLE FAIL page expired rev=~D floor=~D"
                          (read-bundle-revision bundle) floor))
      (values (read-bundle-ask bundle ask)
              (format nil "BUNDLE OK rev=~D watermark=~D"
                      (read-bundle-revision bundle)
                      (read-bundle-watermark bundle)))))

;;; ------------------------------------------------------------------
;;; B3/B4 -- the two batch modes over the kernel.
;;; ------------------------------------------------------------------

(defparameter *long-operation-verbs* '(:capture :import :export :clip)
  "The verbs whose acceptance returns a durable operation id. An atomic
mutation batch runs no external I/O inside it, so an entry naming one of these
is refused by its own entry id before anything is staged (SPEC-WORK.md:2791-2797).")

(defun independent-batch-run (kernel entries &key continue (batch "batch-1"))
  "An independent batch is ordered entries with outcomes of their own. Submit
each entry through the kernel's one writer, in order; other requests may
interleave because the writer is shared. The default is to stop at the first
refusal and mark every remaining entry `:not-attempted`; a continuation past a
refusal is the explicit CONTINUE flag and never the default. The mode promises
no rollback and no single shared revision. An entry that accepts a long
operation answers with an operation id and no completed result, so a later entry
cannot read it as one. Answers (values RESULTS APPLIED REFUSED NOT-ATTEMPTED
REGISTRY), each result an (id request state rev/line) plist."
  (declare (ignore batch))
  (let ((results '()) (applied 0) (refused 0) (not-attempted 0)
        (stopped nil) (registry nil))
    (dolist (entry entries)
      (let ((id (getf entry :id))
            (request (getf entry :request)))
        (cond
          ((and stopped (not continue))
           (incf not-attempted)
           (push (list :id id :request request :state :not-attempted) results))
          ((getf entry :op)
           ;; A long operation: acceptance is durable and answers an operation
           ;; id at once; it is never a completed result.
           (unless registry (setf registry (make-operation-registry)))
           (let ((opid (operation-accept registry
                                         :id (format nil "op-~A" id)
                                         :kind (getf entry :op)
                                         :request request
                                         :author (getf request :by)
                                         :stamp (getf request :stamp))))
             (incf applied)
             (push (list :id id :request request :state :operation
                         :operation-id opid :result nil)
                   results)))
          (t
           (multiple-value-bind (okp line code envelope)
               (submit kernel request)
             (declare (ignore code envelope))
             (if okp
                 (progn
                   (incf applied)
                   (push (list :id id :request request :state :applied
                               :rev (state-revision (kernel-state kernel))
                               :line line)
                         results))
                 (progn
                   (incf refused)
                   (setf stopped t)
                   (push (list :id id :request request :state :refused
                               :line line)
                         results))))))))
    (values (nreverse results) applied refused not-attempted registry)))

(defun atomic-batch-run (kernel entries &key expect)
  "An atomic mutation batch is one envelope, all or none. Every entry is
validated against EXPECT (defaulting to the kernel's current revision) before
anything is published. An entry that would accept a long operation is refused
by its own entry id before anything is staged; any entry the verb grammar
refuses fails the whole batch and nothing is written. Only once every entry has
validated is each applied through the kernel's one writer. Answers (values OK-P
FAILING-ID APPLIED REV); FAILING-ID is the entry id to name, or :expect when the
batch was taken at a stale revision."
  (let ((rev (state-revision (kernel-state kernel))))
    (let ((long (find-if (lambda (e) (member (getf e :op) *long-operation-verbs*))
                         entries)))
      (when long
        (return-from atomic-batch-run (values nil (getf long :id) 0 rev))))
    (when (and expect (not (equal expect rev)))
      (return-from atomic-batch-run (values nil :expect 0 rev)))
    ;; Validate the batch whole on a private reconstruction of the state; a
    ;; single refusal publishes nothing and names its entry id.
    (let* ((state (kernel-state kernel))
           (clone (make-kernel
                   :state (reconstruct-state
                           (canonical-string (state-canonical-form state))))))
      (dolist (entry entries)
        (multiple-value-bind (okp line code) (submit clone (getf entry :request))
          (declare (ignore line code))
          (unless okp
            (return-from atomic-batch-run (values nil (getf entry :id) 0 rev))))))
    ;; Every entry validated; publish them through the one writer.
    (dolist (entry entries)
      (submit kernel (getf entry :request)))
    (values t nil (length entries) (state-revision (kernel-state kernel)))))

;;; ------------------------------------------------------------------
;;; The request bundle: the bus between two owners.
;;;
;;; SPEC-WORK.md:423-440 and :2210-2251: a fenced session's accepted events
;;; since its base are written as a request bundle -- each request with its
;;; request id, its required `--expect` (the one clipped revision) and its
;;; payload -- which the owning coordinator applies with `session replay
;;; --from`, one request at a time, validated fresh against the live O. It is
;;; the one bus: no second transport is invented (SPEC-WORK.md:2240).
;;; ------------------------------------------------------------------

(defstruct (request-bundle
             (:constructor %make-request-bundle (base clipped-revision requests)))
  (base "-" :type string)
  (clipped-revision 0 :type integer)
  (requests '() :type list))

(defun make-request-bundle (&key (base "-") (clipped-revision 0) (requests '()))
  "One request bundle: the boundary commit sha, the single clipped revision
every request expects, and the ordered requests."
  (%make-request-bundle base clipped-revision requests))

(defun request-bundle-form (bundle)
  "The canonical order of a bundle: base=, the one clipped revision and the
ordered requests, each with its request id, its required --expect and its
payload (SPEC-WORK.md:2256-2262, :423-425)."
  (list :request-bundle
        :base (request-bundle-base bundle)
        :clipped-revision (request-bundle-clipped-revision bundle)
        :requests (request-bundle-requests bundle)))

(defun request-bundle-string (bundle)
  "The canonical bytes a bundle is written and read as."
  (canonical-string (request-bundle-form bundle)))

(defun read-request-bundle (text)
  "Read one restricted s-expression request bundle. The bundle is
`(:request-bundle :base <sha> :clipped-revision <n> :requests (<request> ...))`
where each request is a plist carrying `:request` and the verb's own fields. A
malformed or absent boundary refuses rather than being guessed."
  (let ((form (read-restricted text)))
    (unless (and (consp form) (eq (first form) :request-bundle))
      (error 'unsupported-input :what "not a request bundle"))
    (let ((base (getf (rest form) :base))
          (clipped (getf (rest form) :clipped-revision +absent+))
          (requests (getf (rest form) :requests +absent+)))
      (unless (and (stringp base) (plusp (length base)))
        (error 'unsupported-input :what "request bundle needs a :base sha"))
      (unless (and (integerp clipped) (not (minusp clipped)))
        (error 'unsupported-input
               :what "request bundle needs a nonnegative :clipped-revision"))
      (unless (listp requests)
        (error 'unsupported-input :what "request bundle needs a :requests list"))
      (dolist (request requests)
        (unless (and (consp request) (keywordp (first request)))
          (error 'unsupported-input :what "a bundle request is not a plist"))
        (unless (getf request :request)
          (error 'unsupported-input :what "a bundle request carries no :request id")))
      (make-request-bundle :base base :clipped-revision clipped :requests requests))))

;;; ------------------------------------------------------------------
;;; `session export`: read the accepted journal, write the bundle.
;;; ------------------------------------------------------------------

(defun journal-accepted-records (journal)
  "Every accepted record of JOURNAL in acceptance order, from its first record
forward: (:request id :rev n :events (...)). Reads no repository and validates
nothing; a bundle is requests."
  (let ((path (journal-path journal))
        (records '())
        (seq 0))
    (with-open-file (in path :direction :input
                             :element-type 'character :external-format :utf-8)
      (read-header in path (journal-initial-state-hash journal))
      (loop
        (let ((frame (read-record-frame in path (1+ seq))))
          (unless frame (return))
          (incf seq)
          (let ((record (getf (rest frame) :record)))
            (push (list :request (getf record :request)
                        :rev (getf record :rev)
                        :events (getf record :events))
                  records)))))
    (nreverse records)))

(defun export-request-bundle (journal &key into (clipped-revision nil) (base nil))
  "`session export --journal`: write JOURNAL's accepted events as a request
bundle -- each request with its request id, its required --expect and its
payload. Every request carries the one CLIPPED-REVISION as `--expect`, and BASE
is the journal's newest clip boundary commit sha; both are supplied by the
caller because a bundle is a file and the boundary record is what carries them
(SPEC-WORK.md:430-440). Starts no session, takes no ownership, reads no
repository and validates nothing. Answers the canonical bundle string; when
INTO names a path it is written there byte-identically."
  (let* ((records (journal-accepted-records journal))
         (clipped (or clipped-revision
                      (if records (getf (first records) :rev) 0)))
         (requests (mapcar (lambda (record)
                             (list :request (getf record :request)
                                   :expect clipped
                                   :payload (getf record :events)))
                           records))
         (bundle (make-request-bundle :base (or base "-")
                                      :clipped-revision clipped
                                      :requests requests))
         (text (request-bundle-string bundle)))
    (when into
      (with-open-file (out into :direction :output :if-exists :supersede
                                :if-does-not-exist :create
                                :element-type 'character :external-format :utf-8)
        (write-string text out)
        (write-char #\Newline out)
        (finish-output out)))
    text))

;;; ------------------------------------------------------------------
;;; `session replay --from`: the bundle intake.
;;;
;;; The verb-level replay applies each request through the session's single
;;; writer (SUBMIT), so the journal's dedup predicate answers a retry with its
;;; recorded disposition and the coordinator's `--as` is recorded in
;;; `:generation-owner` while the event's `:by` stays the bundle's author.
;;; The lower REPLAY-REQUEST-BUNDLE is the same intake against a bare kernel,
;;; used where there is no resident session (SPEC-WORK.md:423-427, :2214-2238).
;;; ------------------------------------------------------------------

(defun %bundle-request (request coordinator &key now)
  "The kernel request a bundle entry becomes. `--expect` is the replay's own
precondition and is not a kernel field, so it is removed; the replayer replaces
`:generation-owner`; the bundle's `:by` is left alone."
  (let ((req (copy-list request)))
    (remf req :expect)
    (setf (getf req :generation-owner) coordinator)
    (let ((stamp (getf req :stamp)))
      (cond
        (stamp (unless (getf req :clock) (setf (getf req :clock) :given)))
        (now (setf (getf req :stamp) now (getf req :clock) :given))
        (t (setf (getf req :stamp) "1970-01-01T00:00:00Z"
                 (getf req :clock) :tool))))
    req))

(defun %bundle-apply (session request coordinator &key now)
  "Apply one bundle request through the session's single writer and answer a
verdict line for it."
  (multiple-value-bind (okp line code)
      (wire-session-mutate session (%bundle-request request coordinator :now now))
    (if okp
        (values t (format nil "REPLAY OK request=~A node=~A"
                          (getf request :request) (getf request :node))
                0)
        (values nil (format nil "REPLAY FAIL request=~A node=~A: ~A"
                            (getf request :request) (getf request :node)
                            (or (and (stringp line) line)
                                (format nil "exit ~A" code)))
                1))))

(defun %replay-verdict (session request coordinator baseline &key now)
  "Answer (values OK-P LINE EXIT) for one bundle request. A recorded id is
answered by the journal's dedup predicate; a fresh request whose node has
accepted an event of its own after the revision `--expect` names is refused
`stale`; otherwise it is applied."
  (let* ((kernel (wire-session-kernel session))
         (node (getf request :node))
         (rid (getf request :request))
         (expect (getf request :expect)))
    (multiple-value-bind (found recorded-digest recorded-line)
        (journal-lookup (kernel-journal kernel) rid)
      (declare (ignore recorded-digest recorded-line))
      (cond
        ((eq found :unavailable)
         (values nil (format nil "REPLAY FAIL request=~A node=~A: dedup unavailable" rid node) 1))
        (found
         ;; The id and body are already recorded: the kernel's dedup predicate
         ;; returns the recorded disposition and applies no second event.
         (%bundle-apply session request coordinator :now now))
        ((null expect)
         (values nil (format nil "REPLAY FAIL request=~A node=~A: --expect is required on a bundle request"
                             rid node)
                 1))
        ((not (and (integerp expect) (not (minusp expect))))
         (values nil (format nil "REPLAY FAIL request=~A node=~A: --expect must be a nonnegative revision"
                             rid node)
                 1))
        ((let ((latest (gethash node baseline)))
           (and latest (> latest expect)))
         (values nil (format nil "REPLAY FAIL request=~A node=~A expect=~D current=~D: stale"
                             rid node expect (gethash node baseline))
                 1))
        (t
         (%bundle-apply session request coordinator :now now))))))

(defun session-replay (session bundle &key as max now)
  "Apply BUNDLE one request at a time through the session's kernel, bounded by
MAX (NIL or 0 means every request; a negative bound refuses). `--as` names the
coordinator applying the bundle and is recorded in `:generation-owner`, while
the event's `:by` stays the request's own author. Answer (values OK-P LINES
EXIT): one verdict line per request, exit 0 only when every one applied."
  (unless (and (stringp as) (plusp (length as)))
    (error 'unsupported-input :what "session replay needs --as <name>"))
  (when (and max (minusp max))
    (error 'unsupported-input :what "session replay: a negative --max is refused"))
  (let* ((kernel (wire-session-kernel session))
         (baseline (make-hash-table :test #'equal))
         (requests (request-bundle-requests bundle))
         (selected (if (or (null max) (zerop max))
                       requests
                       (subseq requests 0 (min max (length requests)))))
         (lines '())
         (exit 0))
    ;; The per-node accepted-revision baseline is read once, before the replay,
    ;; so a bundle's own earlier requests never stale its later ones.
    (dolist (record (state-history (kernel-state kernel)))
      (dolist (form (getf record :events))
        (let ((node (getf form :node))
              (rev (getf form :rev)))
          (when (and (stringp node) (integerp rev)
                     (or (null (gethash node baseline))
                         (> rev (gethash node baseline))))
            (setf (gethash node baseline) rev)))))
    (dolist (request selected)
      (multiple-value-bind (okp line code)
          (%replay-verdict session request as baseline :now now)
        (declare (ignore okp))
        (push line lines)
        (when (plusp code) (setf exit 1))))
    (values (zerop exit) (nreverse lines) exit)))

(defun replay-request-bundle (text target-kernel)
  "`session replay --from` against a bare TARGET-KERNEL: apply the bundle
TEXT's requests one at a time, validating each fresh. The bundle is one ordered
sequence from one session and every request carries the same clipped revision,
so the check is against the revision the target started at -- a target that
moved independently refuses the request `stale` and applies nothing. Answers
(values LINES APPLIED)."
  (let* ((bundle (read-request-bundle text))
         (clipped (request-bundle-clipped-revision bundle))
         (requests (request-bundle-requests bundle))
         (start (state-revision (kernel-state target-kernel)))
         (lines '())
         (applied 0))
    (dolist (request requests)
      (let ((rid (getf request :request))
            (expect (getf request :expect)))
        (if (/= (or expect clipped) start)
            (push (format nil "REPLAY FAIL request=~A expect=~D current=~D: stale"
                          rid (or expect clipped) start)
                  lines)
            (let* ((events (mapcar (lambda (e)
                                     (record-form->event
                                      e :session-written-p
                                      (member (getf e :kind) '(:settle :revive))))
                                   (getf request :payload)))
                   (last-event (car (last events)))
                   (envelope (list :request rid
                                   :digest (payload-digest events)
                                   :events events)))
              (setf (kernel-state target-kernel)
                    (apply-envelope (kernel-state target-kernel) envelope))
              (setf (kernel-next-rev target-kernel) (1+ (work-event-rev last-event)))
              (incf applied)
              (push (format nil "REPLAY OK request=~A rev=~D events=~D"
                            rid (state-revision (kernel-state target-kernel))
                            (length events))
                    lines)))))
    (values (nreverse lines) applied)))
