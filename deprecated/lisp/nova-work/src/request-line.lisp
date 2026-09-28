;;;; request-line.lisp --- what the resident session answers when the CLI
;;;; speaks to it.
;;;;
;;;; THE SEAM WAS NEVER FILLED. `*session-request-handler*` (src/transport.lisp)
;;;; is NIL everywhere in the tree, so every request on the socket reached
;;;; DEFAULT-SESSION-REQUEST-HANDLER, which answers a MEMBER test over four
;;;; bare strings -- "status", "session status", "ping", "session ping" -- and
;;;; nothing else. The CLI does not send bare strings. It sends the verb and
;;;; its flags, `session status --session /run/work.sock`, which is the spelling
;;;; its own wire paragraph pins, so the one verb the session implemented was
;;;; unreachable from the one client there is. Measured against a real daemon
;;;; on 2026-09-19: every verb the CLI sends, `session status` among them, came
;;;; back
;;;;
;;;;   FAIL request=session status --session /tmp/dogfood/work.sock: unsupported in-process request
;;;;
;;;; which is also not a line of *Output grammar*: its first token is not a
;;;; verb's noun, so the client -- reading the SECOND token, which is the
;;;; spec's own client rule -- could not classify it either and refused it as
;;;; ungrammatical. Two halves of one wire, each correct about the other being
;;;; wrong.
;;;;
;;;; This file fills the seam. It is deliberately ONE file and it touches
;;;; transport.lisp not at all: the seam's own docstring says "A later wire
;;;; slice binds the framed protocol here", so binding it is the documented way
;;;; in, and E01's file stays free for the lanes working in it.
;;;;
;;;; What it does NOT do, so that the next reader is not misled: it routes no
;;;; mutation and opens no journal. The verbs the CLI can now spell reach a
;;;; session that answers `session status` and refuses the rest BY NAME, in the
;;;; grammar, at exit 2. That is a smaller promise than the verbs block, and it
;;;; is a true one, where "unsupported in-process request" was neither.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Reading the verb off a request line.
;;; ------------------------------------------------------------------

(defun %request-words (request)
  "REQUEST split on runs of spaces and tabs, empty words dropped."
  (let ((words '()) (start nil))
    (dotimes (i (length request))
      (let ((c (char request i)))
        (if (or (char= c #\Space) (char= c #\Tab))
            (when start
              (push (subseq request start i) words)
              (setf start nil))
            (unless start (setf start i)))))
    (when start (push (subseq request start) words))
    (nreverse words)))

(defun request-line-verb (request)
  "The verb at the head of REQUEST: every word before the first one that opens
with a `-`. `session status --session /run/work.sock` is the verb `session
status`; `machine --session s --register m1` is `machine`. A request whose
first word is already a flag has no verb and answers the empty string.

The client spells the verb the spec's verbs block spells it (docs/SPEC-WORK.md,
\"The verbs\"), one or two words, so reading the leading non-flag words reads
the verb and a stray positional argument makes a verb no session knows --
which is a refusal naming it, and never a verb quietly trimmed to fit."
  (let ((out '()))
    (dolist (w (%request-words request))
      (when (char= (char w 0) #\-)
        (return))
      (push w out))
    (format nil "~{~A~^ ~}" (nreverse out))))

(defparameter *request-echo-cap* 64
  "How much of a caller's own text a refusal echoes back. A refusal is ONE
line, so an unbounded echo of whatever arrived on the socket is a line of
whatever length the caller chose.")

(defun %echo-safely (text)
  "TEXT rendered for a refusal line: capped, and with every control character
replaced, so nothing a caller sends can add a second line or repaint a
terminal. The same guarantee internal/oneline makes on the Go side; here it
is the one place a session repeats a caller's bytes."
  (let* ((capped (if (> (length text) *request-echo-cap*)
                     (concatenate 'string (subseq text 0 *request-echo-cap*) "...")
                     text))
         (clean (map 'string
                     (lambda (c)
                       (let ((code (char-code c)))
                         (if (or (< code 32) (= code 127)) #\? c)))
                     capped)))
    (if (zerop (length clean)) "-" clean)))

;;; ------------------------------------------------------------------
;;; The answer lines, in the output grammar and in no other shape.
;;; ------------------------------------------------------------------

(defun session-fail-line (session reason)
  "The grammar's own refusal for a session verb: `SESSION FAIL session=<path>
owner=<name> generation=<n>: <reason>` (docs/SPEC-WORK.md, *Output grammar*).
Its first token is the verb's noun and its second is FAIL, which is what the
client splits on and what it prints to stderr at exit 1."
  (format nil "SESSION FAIL session=~A owner=~A generation=~D: ~A"
          (let ((p (session-path session)))
            (if (or (null p) (zerop (length p))) "-" p))
          (let ((o (session-owner session)))
            (if (or (null o) (zerop (length o))) "-" o))
          (session-generation session)
          reason))

(defparameter *request-schema*
  '(("session status" :answers :status :source :spec
     :flags (("session" "<path>" :endpoint))
     :example "session status --session /run/work.sock")
    ("session ping" :answers :status :source :in-process
     :flags (("session" "<path>" :endpoint))
     :example "session ping --session /run/work.sock")
    ("status" :answers :status :source :in-process
     :flags (("session" "<path>" :endpoint))
     :example "status")
    ("ping" :answers :status :source :in-process
     :flags (("session" "<path>" :endpoint))
     :example "ping"))
  "THE ONE SCHEMA of the verbs this session answers (nova-work E08-F01-01: \"use
one schema for client validation, protocol, help and examples\"). Each entry is
(VERB :answers WHAT :source WHERE :flags ((NAME PLACEHOLDER KIND) ...) :example
REQUEST), and every reading of a verb is taken from it and from nowhere else:

- the protocol: SERVE-REQUEST-LINE answers exactly these verbs;
- validation: REQUEST-SCHEMA-PROBLEM refuses a flag the entry does not list,
  and a listed flag with no value;
- help: REQUEST-SCHEMA-USAGE renders the usage line, which for a :source :spec
  verb is the spec's own verbs-block line (docs/SPEC-WORK.md, \"The verbs\");
- examples: REQUEST-SCHEMA-EXAMPLE, which the same validator admits.

`session status` is the verb of the spec's block; the other three are the
in-process spellings the seam's first handler already answered, kept so that
nothing that worked stops working. An :endpoint flag names the socket the
request travels over: the client refuses a request without it and so the help
line prints it bare, while the session, which IS that socket, answers without it.")

(defun request-schema-verb (entry) (first entry))

(defun request-schema-entry (verb)
  "The schema entry for VERB, or NIL when the session does not answer it."
  (assoc verb *request-schema* :test #'string=))

(defun request-schema-in-spec-p (entry)
  (eq (getf (rest entry) :source) :spec))

(defun request-schema-example (verb)
  (getf (rest (request-schema-entry verb)) :example))

(defun request-schema-usage (verb)
  "VERB's help line, rendered from the schema: the verb, then each flag as
`--name <placeholder>`, bracketed when optional. An :endpoint flag prints bare,
because the client requires it."
  (let ((entry (request-schema-entry verb)))
    (when entry
      (format nil "~A~{ ~A~}" verb
              (mapcar (lambda (f)
                        (destructuring-bind (name placeholder &optional kind) f
                          (if (member kind '(:endpoint :required))
                              (format nil "--~A ~A" name placeholder)
                              (format nil "[--~A ~A]" name placeholder))))
                      (getf (rest entry) :flags))))))

(defun request-schema-problem (request)
  "NIL when REQUEST is a request line the schema admits, or else the one-phrase
reason it is not: a verb the schema does not list, a flag its entry does not
list, a listed flag with no value, or a stray word after the flags. Every value
is one word, because the client escapes whitespace inside a value
(internal/oneline Field) and the session validates the value itself."
  (let* ((verb (request-line-verb request))
         (entry (request-schema-entry verb)))
    (if (null entry)
        (format nil "this session does not answer the verb ~A" (%echo-safely verb))
        (let ((flags (mapcar #'first (getf (rest entry) :flags)))
              (words (nthcdr (length (%request-words verb)) (%request-words request))))
          (loop while words
                do (let ((w (pop words)))
                     (cond
                       ((not (and (> (length w) 2) (string= "--" w :end2 2)))
                        (return (format nil "~A takes no word ~A here" verb (%echo-safely w))))
                       ((not (member (subseq w 2) flags :test #'string=))
                        (return (format nil "~A does not take ~A" verb (%echo-safely w))))
                       ((null words)
                        (return (format nil "~A needs a value" (%echo-safely w))))
                       (t (pop words)))))))))

(defun serve-request-line (server request)
  "Answer ONE request line against SERVER, as (values OK LINE EXIT).

`session status` answers the FULL `SESSION OK` line -- the one shape `session
start`, `session status` and `session stop` print alike (docs/SPEC-WORK.md,
*Output grammar*) -- and not the shorter identity line the seam's first handler
served, which carries no `session=`, no `journal=`, no counts and no
`emitted=`, and so is not a line of the grammar at all.

Which verbs are answered, and which flags each takes, is *REQUEST-SCHEMA* and
nothing else: a request the schema does not admit is refused naming what it
does not admit, with the verb's usage line and example from the same schema.

Every other verb is refused BY NAME in the grammar's own `SESSION FAIL` shape
at exit 2: the session says which verb it does not answer, rather than saying
that something unnamed was unsupported. A request with no verb -- flags alone,
or nothing -- is refused the same way and says so."
  (let* ((session (session-server-session server))
         (verb (request-line-verb request)))
    (cond
      ((zerop (length verb))
       (values nil (session-fail-line session "a verb is required") 2))
      ((null (request-schema-entry verb))
       (values nil (session-fail-line session (request-schema-problem request)) 2))
      (t
       (let ((problem (request-schema-problem request)))
         (if problem
             (values nil
                     (session-fail-line
                      session
                      (format nil "~A; usage: ~A; example: ~A" problem
                              (request-schema-usage verb)
                              (request-schema-example verb)))
                     2)
             (values t (session-status-line session) 0)))))))

;;; The seam, bound. src/transport.lisp's own docstring for
;;; *SESSION-REQUEST-HANDLER* is "The serve seam: NIL selects
;;; DEFAULT-SESSION-REQUEST-HANDLER, the one in-process implementation. A later
;;; wire slice binds the framed protocol here." This is that binding, for the
;;; line protocol the CLI speaks today; the framed one replaces this one line
;;; when it lands. DEFAULT-SESSION-REQUEST-HANDLER is left exactly as it is, so
;;; a caller that wants it binds *SESSION-REQUEST-HANDLER* back to it.
(setf *session-request-handler* #'serve-request-line)
