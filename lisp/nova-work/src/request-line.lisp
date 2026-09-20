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

(defparameter *status-verbs* '("session status" "status" "session ping" "ping")
  "The spellings that ask a session who it is. `session status` is the verb of
the spec's block; the other three are the in-process spellings the seam's first
handler already answered, kept so that nothing that worked stops working.")

(defun serve-request-line (server request)
  "Answer ONE request line against SERVER, as (values OK LINE EXIT).

`session status` answers the FULL `SESSION OK` line -- the one shape `session
start`, `session status` and `session stop` print alike (docs/SPEC-WORK.md,
*Output grammar*) -- and not the shorter identity line the seam's first handler
served, which carries no `session=`, no `journal=`, no counts and no
`emitted=`, and so is not a line of the grammar at all.

Every other verb is refused BY NAME in the grammar's own `SESSION FAIL` shape
at exit 2: the session says which verb it does not answer, rather than saying
that something unnamed was unsupported. A request with no verb -- flags alone,
or nothing -- is refused the same way and says so."
  (let* ((session (session-server-session server))
         (verb (request-line-verb request)))
    (cond
      ((member verb *status-verbs* :test #'string=)
       (values t (session-status-line session) 0))
      ((zerop (length verb))
       (values nil (session-fail-line session "a verb is required") 2))
      (t
       (values nil
               (session-fail-line
                session
                (format nil "this session does not answer the verb ~A"
                        (%echo-safely verb)))
               2)))))

;;; The seam, bound. src/transport.lisp's own docstring for
;;; *SESSION-REQUEST-HANDLER* is "The serve seam: NIL selects
;;; DEFAULT-SESSION-REQUEST-HANDLER, the one in-process implementation. A later
;;; wire slice binds the framed protocol here." This is that binding, for the
;;; line protocol the CLI speaks today; the framed one replaces this one line
;;; when it lands. DEFAULT-SESSION-REQUEST-HANDLER is left exactly as it is, so
;;; a caller that wants it binds *SESSION-REQUEST-HANDLER* back to it.
(setf *session-request-handler* #'serve-request-line)
