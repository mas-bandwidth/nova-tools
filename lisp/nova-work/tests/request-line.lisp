;;;; request-line.lisp --- what a resident session answers the CLI.
;;;;
;;;; Every case here drives the REAL socket: a real session server on a real
;;;; Unix-domain endpoint, a real connection, the request line the Go client
;;;; spells (docs/SPEC-WORK.md, "The engine and its client"), and the line that
;;;; comes back read as the client reads it -- by the SECOND token and by
;;;; nothing else, which is the spec's own client rule.

(in-package #:nova-work/tests)

(defvar *request-line-socket-counter* 0)

(defun %request-line-dir ()
  "A fresh 0700 directory for one endpoint. The endpoint refuses a directory
whose mode is not 0700, which is the spec's own requirement, so the test makes
one rather than borrowing the bench's temp directory."
  (let* ((name (format nil "nova-work-reqline-~D-~D"
                       (sb-posix:getpid)
                       (incf *request-line-socket-counter*)))
         (dir (merge-pathnames (concatenate 'string name "/")
                               #p"/tmp/")))
    (ensure-directories-exist dir)
    (sb-posix:chmod (namestring dir) #o700)
    dir))

(defun call-with-served-session (fn &key (owner "rowan"))
  "Run FN on a live session server and its socket path, then stop it and
unlink. The socket path is short by construction: sun_path is capped at 104
bytes on darwin."
  (let* ((dir (%request-line-dir))
         (sock (namestring (merge-pathnames "s.sock" dir))))
    (multiple-value-bind (server line exit)
        (nova-work:session-start :path sock :owner owner
                                 :socket-path sock :serve t :foreground nil)
      (declare (ignore line exit))
      (unwind-protect (funcall fn server sock)
        (nova-work:session-server-stop server)
        (ignore-errors (sb-posix:rmdir (namestring dir)))))))

(defmacro with-served-session ((server socket &key (owner "rowan")) &body body)
  `(call-with-served-session (lambda (,server ,socket)
                               (declare (ignorable ,server ,socket))
                               ,@body)
                             :owner ,owner))

(defun ask-over-the-socket (socket request)
  "Write ONE request line to SOCKET and read the ONE line back, the way
internal/workclient does it. Answers the reply without its newline."
  (let ((s (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
    (unwind-protect
         (progn
           (sb-bsd-sockets:socket-connect s socket)
           (let ((stream (sb-bsd-sockets:socket-make-stream
                          s :input t :output t
                          :element-type 'character :external-format :utf-8)))
             (write-line request stream)
             (finish-output stream)
             (read-line stream nil "")))
      (ignore-errors (sb-bsd-sockets:socket-close s)))))

(defun reply-verdict (line)
  "The client's own rule: the answer is classified by the SECOND token and by
nothing else (docs/SPEC-WORK.md, \"The engine and its client\"). Answers the
second token, or NIL when the line has not got two."
  (let ((words (nova-work::%request-words line)))
    (second words)))

;;; ------------------------------------------------------------------
;;; 1. The verb the CLI spells reaches the session.
;;; ------------------------------------------------------------------

(deftest "session-status-over-the-socket-with-its-flags"
    "docs/SPEC-WORK.md:2642-2708"
    "the request line the CLI sends -- the verb AND its flags -- is answered, not only the bare verb"
  (with-served-session (server sock)
    (let ((reply (ask-over-the-socket
                  sock
                  (format nil "session status --session ~A" sock))))
      (check-string= "SESSION" (first (nova-work::%request-words reply))
                     "the first token is the verb's noun")
      (check-string= "OK" (reply-verdict reply)
                     (format nil "the second token of ~S" reply))
      (ok (search (format nil "session=~A" sock) reply)
          "the SESSION OK line names its own socket: ~S" reply))))

(deftest "the-status-answer-is-the-grammars-full-session-ok-line"
    "docs/SPEC-WORK.md, Output grammar"
    "SESSION OK is one shape, printed by session start, session status and session stop alike"
  (with-served-session (server sock)
    (let ((reply (ask-over-the-socket sock (format nil "session status --session ~A" sock))))
      ;; The fields the short identity line does not carry. Their absence is
      ;; what made the socket's answer a different shape from the one the
      ;; session prints on start and stop.
      (dolist (field '("session=" "file=" "journal=" "events=" "pending="
                       "pushed=" "nodes=" "edges=" "parses=" "replays="
                       "clip-every=" "clip-after=" "retain=" "boundary="
                       "findings=" "build=" "emitted="))
        (ok (search field reply)
            "the SESSION OK line is missing ~A: ~S" field reply)))))

(deftest "the-bare-in-process-spellings-still-answer"
    "docs/SPEC-WORK.md:2642-2708"
    "status, ping and session ping keep answering, so nothing that worked stops working"
  (with-served-session (server sock)
    (dolist (request '("status" "ping" "session ping" "session status"))
      (let ((reply (ask-over-the-socket sock request)))
        (check-string= "OK" (reply-verdict reply)
                       (format nil "~S answered ~S" request reply))))))

;;; ------------------------------------------------------------------
;;; 2. A verb the session does not answer is refused IN THE GRAMMAR.
;;; ------------------------------------------------------------------

(deftest "a-verb-the-session-does-not-answer-is-refused-by-name"
    "docs/SPEC-WORK.md, Output grammar"
    "SESSION FAIL session=<path> owner=<name> generation=<n>: <reason>, naming the verb"
  (with-served-session (server sock)
    (let ((reply (ask-over-the-socket
                  sock
                  (format nil "machine --session ~A --as rowan --register m1 --reason x" sock))))
      (check-string= "SESSION" (first (nova-work::%request-words reply))
                     "the first token is the verb's noun")
      (check-string= "FAIL" (reply-verdict reply)
                     (format nil "the second token of ~S" reply))
      (ok (search "machine" reply)
          "the refusal does not name the verb it refused: ~S" reply)
      (ok (search (format nil "session=~A" sock) reply)
          "the refusal does not name the session: ~S" reply)
      (ok (not (search "unsupported in-process request" reply))
          "the refusal still says the old unnameable thing: ~S" reply))))

(deftest "a-refused-verb-answers-exit-two"
    "docs/SPEC-WORK.md:2191-2207"
    "what could not run at all is exit 2, and what ran and said no is exit 1"
  (with-served-session (server sock)
    (multiple-value-bind (ok-p line code)
        (nova-work:serve-session-request server "render --session x --view r")
      (ok (not ok-p) "a verb the session does not answer was accepted: ~S" line)
      (check-equal 2 code "the exit code of an unanswered verb"))))

(deftest "a-request-with-no-verb-is-refused-saying-so"
    "docs/SPEC-WORK.md, Output grammar"
    "flags with no verb, and an empty line, are refused naming the missing verb"
  (with-served-session (server sock)
    (dolist (request (list "--session /run/work.sock" "" "   "))
      (let ((reply (ask-over-the-socket sock request)))
        (check-string= "FAIL" (reply-verdict reply)
                       (format nil "~S answered ~S" request reply))
        (ok (search "a verb is required" reply)
            "~S was refused as ~S" request reply)))))

;;; ------------------------------------------------------------------
;;; 3. The refusal is ONE line whatever the caller sent.
;;; ------------------------------------------------------------------

(deftest "a-refusal-echoes-a-callers-verb-capped-and-with-no-control-characters"
    "docs/SPEC-WORK.md, Output grammar"
    "one machine-scannable line per answer: nothing a caller sends can add a second line"
  (with-served-session (server sock)
    (let* ((long (make-string 4096 :initial-element #\x))
           (reply (ask-over-the-socket sock long)))
      (check-string= "FAIL" (reply-verdict reply)
                     (format nil "a 4096-byte verb answered ~S" reply))
      (ok (< (length reply) 512)
          "the refusal grew with the caller's verb: ~D bytes" (length reply)))
    (let ((reply (ask-over-the-socket sock (format nil "ma~Cchine --session x" #\Tab))))
      ;; A tab inside the verb would split the verb, so the word before it is
      ;; the verb; what matters is that no control character survives into the
      ;; answer line.
      (ok (not (find-if (lambda (c) (let ((n (char-code c)))
                                      (or (< n 32) (= n 127))))
                        reply))
          "a control character survived into the answer: ~S" reply))))

;;; ------------------------------------------------------------------
;;; 4. The verb reader itself, without a socket.
;;; ------------------------------------------------------------------

(deftest "the-verb-is-the-words-before-the-first-flag"
    "docs/SPEC-WORK.md:2191-2207"
    "a verb is one or two words and a flag ends it; a stray positional makes a verb no session knows"
  (check-string= "session status"
                 (nova-work::request-line-verb "session status --session /run/w.sock")
                 "a two-word verb")
  (check-string= "machine"
                 (nova-work::request-line-verb "machine --session s --register m1")
                 "a one-word verb")
  (check-string= "query"
                 (nova-work::request-line-verb "query --session s --ask fleet --branch open")
                 "query")
  (check-string= "status" (nova-work::request-line-verb "status") "a bare verb")
  (check-string= "" (nova-work::request-line-verb "--session s") "flags with no verb")
  (check-string= "" (nova-work::request-line-verb "") "an empty line")
  (check-string= "state load"
                 (nova-work::request-line-verb "state load --from x")
                 "a positional after the verb is part of the verb, never trimmed away")
  (check-string= "session status"
                 (nova-work::request-line-verb "  session   status   --session s ")
                 "runs of spaces are one separator"))

;;; ------------------------------------------------------------------
;;; TestE11F06FinalityRisesWithTierA     docs/SPEC-WORK.md:4592, :4653
;;;
;;; E11-F06-03: finality-rises-with-tier. "Finality rises with the tier and is
;;; never final below the seat: a child's `done` is a claim against the parent's
;;; acceptance until the parent verifies it itself, with evidence bound to the
;;; criteria and 'not merely a worker's success claim'". A child that settles
;;; `:done` naming evidence has made a success claim and nothing more: the
;;; dependent does not treat that need as met -- it does not move -- until the
;;; parent's own verification holds the raw fact the child's criterion names.
;;; ------------------------------------------------------------------

(deftest "TestE11F06FinalityRisesWithTierA"
    "docs/SPEC-WORK.md:4592"
    "a child's done is a claim; the parent moves only after its own verification with evidence bound to the child's criterion; a worker's success claim alone never moves a node below the seat"
  ;; The child N settles :done naming ev-1: that is the worker's success claim.
  ;; It must not move the dependent D (a node below the seat) on its own.
  (let* ((k (gate-kernel))
         (evidence (gate-job-evidence "acme/work/n"))
         (session (gate-session))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence)))))
    (gate-done k "acme/work/n")
    (multiple-value-bind (met reason)
        (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "a child's done (success claim) alone does not satisfy the need: ~A" reason)
      (check-equal :need-unverified reason
                   "a worker's success claim alone reads need-unverified, never met"))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 1 unmet "the dependent still counts one unmet need: the parent has not moved")
      (check-string= "acme/work/n" need "and names the claiming child")
      (check-equal :need-unverified reason "with the one token"))
    ;; The parent's own verification: the cache holds the raw fact, and only now
    ;; does the child's done move anything.
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n")
    (multiple-value-bind (met reason)
        (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok met "the need is met only once the parent verifies the evidence: ~A" reason))
    (multiple-value-bind (unmet)
        (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 0 unmet "and only now does the dependent read unmet=0"))))
