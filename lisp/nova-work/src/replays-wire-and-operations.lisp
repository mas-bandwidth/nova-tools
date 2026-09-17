;;;; replays-wire-and-operations.lisp --- the pure part of the wire the five
;;;; card-8608 acceptance replays assert (docs/SPEC-WORK.md:2660-2758 and the
;;;; acceptance table at :5159-5183).
;;;;
;;;; Nothing here opens a socket, starts a session or runs a CLI: the functions
;;;; are the wire codec, the version handshake, the pipelined request/reply
;;;; correlation, the disconnect reconciliation over the real kernel dedup, and
;;;; the long-operation registry. The live transport that a later slice owns is
;;;; not in this slice. The reading each replay takes where the paragraph is
;;;; ambiguous is stated at its function.

(in-package #:nova-work)

;;;; ------------------------------------------------------------------
;;;; The wire codec: every integer is a JSON string, numbers are refused
;;;; (SPEC-WORK.md:2663-2666, :2669-2672).
;;;; ------------------------------------------------------------------

(defun wire-encode-integer (n)
  "An integer crosses the wire as a JSON string of decimal digits, never as a
JSON number, so a value above 2^53 cannot be rounded silently."
  (check-type n integer)
  (format nil "\"~D\"" n))

(defun wire-number-token-p (text)
  "True when TEXT is a bare JSON number token (its first non-sign character is
a digit and the rest are digits, signs, a decimal point or an exponent). The
wire carries no JSON numbers, so such a token is refused."
  (and (plusp (length text))
       (let ((start (if (find (char text 0) "+-") 1 0)))
         (and (< start (length text))
              (loop for i from start below (length text)
                    always (or (digit-char-p (char text i))
                               (find (char text i) ".eE+")))))))

(defun wire-decode-value (text)
  "Decode one wire scalar. A `null` is the absent spelling (:absent) and an
absent key is the same value; an empty string and an empty array are values.
A JSON number is refused, reported as an unsupported input."
  (cond
    ((string= text "null") +absent+)
    ((string= text "[]") '())
    ((and (>= (length text) 2)
          (char= (char text 0) #\")
          (char= (char text (1- (length text))) #\"))
     (let ((inner (subseq text 1 (1- (length text)))))
       (if (and (plusp (length inner)) (every #'digit-char-p inner))
           (parse-integer inner)
           inner)))
    ((wire-number-token-p text)
     (error 'unsupported-input
            :what (format nil "wire frame carries a JSON number ~A" text)))
    (t
     (error 'unsupported-input
            :what (format nil "wire frame is not a restricted scalar: ~A" text)))))

(defun wire-trim (text)
  (string-trim '(#\Space #\Tab #\Newline #\Return) text))

(defun wire-split-top-level (text separator)
  "Split TEXT on SEPARATOR at bracket depth zero, respecting strings."
  (let ((parts '()) (start 0) (in-string nil) (escaped nil) (depth 0)
        (len (length text)))
    (loop for i from 0 below len
          for ch = (char text i)
          do (cond
               ((and in-string escaped) (setf escaped nil))
               ((and in-string (char= ch #\\)) (setf escaped t))
               ((char= ch #\") (setf in-string (not in-string)))
               (in-string nil)
               ((find ch "[{") (incf depth))
               ((find ch "]}") (decf depth))
               ((and (char= ch separator) (zerop depth))
                (push (subseq text start i) parts)
                (setf start (1+ i)))))
    (push (subseq text start) parts)
    (nreverse parts)))

(defun wire-object-decode (text)
  "Decode a flat JSON object into an alist of (key . value). A value is decoded
by WIRE-DECODE-VALUE, so a JSON number anywhere inside refuses."
  (let ((trimmed (wire-trim text)))
    (unless (and (>= (length trimmed) 2)
                 (char= (char trimmed 0) #\{)
                 (char= (char trimmed (1- (length trimmed))) #\}))
      (error 'unsupported-input
             :what (format nil "wire frame is not a JSON object: ~A" text)))
    (let ((body (subseq trimmed 1 (1- (length trimmed)))))
      (if (string= (wire-trim body) "")
          '()
          (loop for pair in (wire-split-top-level body #\,)
                for colon = (position #\: pair)
                do (unless colon
                     (error 'unsupported-input
                            :what (format nil "wire object field without a colon: ~A" pair)))
                collect (let ((key (wire-trim (subseq pair 0 colon)))
                              (value (wire-trim (subseq pair (1+ colon)))))
                          (when (and (>= (length key) 2)
                                     (char= (char key 0) #\")
                                     (char= (char key (1- (length key))) #\"))
                            (setf key (subseq key 1 (1- (length key)))))
                          (cons key (wire-decode-value value))))))))

(defun wire-field (object key)
  "The field KEY of a decoded object, or (:absent) when the object does not
carry it. An absent key is the same value an explicit `null` decodes to."
  (let ((cell (assoc key object :test #'string=)))
    (if cell (cdr cell) +absent+)))

;;;; ------------------------------------------------------------------
;;;; The version handshake (SPEC-WORK.md:2682-2685).
;;;; ------------------------------------------------------------------

(defstruct (protocol-session (:constructor %make-protocol-session))
  supported version handshaken-p closed-p max-frame-bytes)

(defun make-protocol-session (&key (supported '("1")) (max-frame-bytes 65536))
  (%make-protocol-session :supported supported :version nil :handshaken-p nil
                          :closed-p nil :max-frame-bytes max-frame-bytes))

(defun protocol-hello (session offered)
  "The client's first frame offers the versions it can speak. Answer the one
version the session will speak, or refuse with the supported list named and
close the connection. An unsupported version never degrades into a guess."
  (cond
    ((protocol-session-closed-p session)
     (values nil '("connection closed")))
    ((protocol-session-handshaken-p session)
     (values (protocol-session-version session) nil))
    (t
     (let ((version (find-if (lambda (v)
                               (member v (protocol-session-supported session)
                                       :test #'string=))
                             offered)))
       (if version
           (progn
             (setf (protocol-session-version session) version
                   (protocol-session-handshaken-p session) t)
             (values version nil))
           (progn
             (setf (protocol-session-closed-p session) t)
             (values nil (protocol-session-supported session))))))))

(defun protocol-admit (session id)
  "No request is admitted before the handshake finishes (SPEC-WORK.md:2682)."
  (declare (ignore id))
  (cond
    ((protocol-session-closed-p session) (values nil "connection closed"))
    ((not (protocol-session-handshaken-p session))
     (values nil "no request before the handshake"))
    (t (values t nil))))

(defun protocol-frame-ok-p (session size)
  "A frame within the session's bound is read; a larger one is refused whole."
  (and (not (protocol-session-closed-p session))
       (<= size (protocol-session-max-frame-bytes session))))

(defun protocol-framed-error (session reason)
  "One framed error, carrying a null request id, and then the close."
  (setf (protocol-session-closed-p session) t)
  (format nil "FAIL request=null: ~A; connection closed" reason))

;;;; ------------------------------------------------------------------
;;;; Pipelined requests and their correlated replies
;;;; (SPEC-WORK.md:2689-2706, :2763-2764).
;;;; ------------------------------------------------------------------

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

(defun wire-pipeline-request (connection request-id kind)
  "Put a request in flight without waiting for an earlier reply; every request
carries its own id. Answer the id, or NIL when the request is not admitted."
  (multiple-value-bind (admitted why)
      (protocol-admit (wire-connection-protocol connection) request-id)
    (declare (ignore why))
    (when admitted
      (setf (wire-connection-outstanding connection)
            (append (wire-connection-outstanding connection)
                    (list (cons request-id kind))))
      request-id)))

(defun wire-feed (connection text)
  "Append TEXT to the connection's receive buffer and answer the complete frames
a newline terminates. Bytes of an unfinished frame stay buffered, so a response
delivered in fragments dispatches nothing until its last fragment arrives."
  (let ((buffer (concatenate 'string (wire-connection-buffer connection) text)))
    (loop with start = 0
          for newline = (position #\Newline buffer :start start)
          while newline
          collect (subseq buffer start newline)
          do (setf start (1+ newline))
          finally (setf (wire-connection-buffer connection)
                        (subseq buffer start)))))

(defun wire-dispatch (connection frame)
  "Deliver one complete response frame. Answer (values REQUEST-ID BODY) only
when the id names exactly one outstanding request. An unknown, duplicate or
undecodable id closes the connection, settles no outstanding request, and
answers (values NIL REASON)."
  (when (wire-connection-closed-p connection)
    (return-from wire-dispatch (values nil "connection closed")))
  (let* ((space (position #\Space frame))
         (id (if space (subseq frame 0 space) frame))
         (body (if space (subseq frame (1+ space)) "")))
    (cond
      ((or (string= id "") (string= id "null") (string= id "-"))
       (wire-close connection)
       (values nil "FAIL request=null: malformed frame with no decodable id"))
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
             (append (wire-connection-settled connection) (list (cons id body))))
       (values id body)))))

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

;;;; ------------------------------------------------------------------
;;;; A mutation connection: disconnect is not a rollback
;;;; (SPEC-WORK.md:2708-2717).
;;;; ------------------------------------------------------------------

(defstruct (wire-session (:constructor %make-wire-session))
  kernel pushed client-alive-p)

(defun make-wire-session (&key kernel (pushed "-") (client-alive-p t))
  (%make-wire-session :kernel kernel :pushed pushed :client-alive-p client-alive-p))

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

;;;; ------------------------------------------------------------------
;;;; The long-operation registry (SPEC-WORK.md:2719-2758).
;;;; ------------------------------------------------------------------

(defstruct (operation-registry (:constructor make-operation-registry))
  (journal '())
  (operations '()))

(defun operation-accept (registry &key id kind request author stamp)
  "The id is durable before it is printed: append the accept record -- the id,
kind, request id, author and stamp -- to the local recovery journal, then
answer the id. A crash between accepting the work and acknowledging it can
never leave a caller holding an id the restart never heard of."
  (let ((record (list :id id :kind kind :request request :author author
                      :stamp stamp :state :running :result nil)))
    (setf (operation-registry-operations registry)
          (append (operation-registry-operations registry) (list (cons id record))))
    (setf (operation-registry-journal registry)
          (append (operation-registry-journal registry) (list record)))
    id))

(defun registry-operation-state (registry id)
  "Answer (values STATE NIL 0) for an operation the journal holds, or
(values NIL LINE 2) for one no journal holds -- never an invented `queued`."
  (let ((cell (assoc id (operation-registry-operations registry) :test #'string=)))
    (if cell
        (values (getf (cdr cell) :state) nil 0)
        (values nil
                (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id)
                2))))

(defun operation-complete (registry id result)
  (let ((cell (assoc id (operation-registry-operations registry) :test #'string=)))
    (when cell
      (setf (getf (cdr cell) :state) :done
            (getf (cdr cell) :result) result))
    cell))

(defun registry-operation-result (registry id)
  (let ((cell (assoc id (operation-registry-operations registry) :test #'string=)))
    (and cell (getf (cdr cell) :result))))

(defun registry-operation-wait (registry id &key timeout)
  "A bounded wait. A timeout leaves the operation running; a completed result
is answered (values RESULT :done)."
  (declare (ignore timeout))
  (if (eq :done (registry-operation-state registry id))
      (values (registry-operation-result registry id) :done)
      (values nil :timeout)))

(defun operation-client-exit (registry)
  "The CLI may exit while the work continues: the registry is already durable."
  (declare (ignore registry))
  t)

(defun operation-journal-length (registry)
  (length (operation-registry-journal registry)))
