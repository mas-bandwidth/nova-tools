;;;; verify-resolver.lisp --- a resolver is a real command, run directly
;;;; (docs/SPEC-WORK.md:1251-1266).
;;;;
;;;; SPEC-WORK.md:1254-1263 fixes the wire: `verify` executes the operator's
;;;; resolver command directly and never through a shell -- the pointer is
;;;; caller text, and an `sh -c` here is an injection -- passing two arguments,
;;;; the pointer and the criterion's subject, and reading one line on stdout
;;;; `<fact> <stamp>`. Exit 0 the fact holds, 1 it does not (a negative is a
;;;; fact and is cached like any other), 2 it could not be established; any
;;;; other exit, a `<fact>` disagreeing with the exit, no line, or a line past
;;;; `--max-bytes` is *unreachable*, and an unreachable answer is never cached.
;;;;
;;;; The command string is the resolver's identity (`--resolver
;;;; <scheme>=<command>`, verbatim and unnormalized, SPEC-WORK.md:1295-1301), so
;;;; it is handed to the process runner as the program itself and is never
;;;; split on whitespace or otherwise normalised.
;;;;
;;;; This run is bounded three ways and owns its child. The command runs under a
;;;; finite deadline (`--fetch-timeout <seconds>`, or the slot's default when
;;;; the session set none); stdout is read as the child runs, never after the
;;;; whole of it has been buffered, and the reader stops the instant the
;;;; retained answer would pass `--max-bytes` in UTF-8 octets (plus the one
;;;; allowed trailing newline). A deadline expiry or an over-long answer kills
;;;; and reaps the child and is *unreachable*: the child gets SIGTERM, SIGKILL
;;;; after a grace, and is waited for unconditionally, so no unbounded process
;;;; wait precedes enforcement and no zombie survives the call. The answer is
;;;; exactly one line -- a newline followed by any further byte is a refusal,
;;;; not a truncation -- and the bound is measured in encoded bytes, not Lisp
;;;; characters. Stderr is not captured: the spec's wire is stdout only, so it
;;;; goes to the null device and can neither exhaust memory nor block the child
;;;; on a full pipe.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The command resolver (SPEC-WORK.md:1251-1266)
;;; ------------------------------------------------------------------

(defconstant +resolver-default-timeout-seconds+ 30
  "The resolver execution deadline in seconds when no `--fetch-timeout' was
set (SPEC-WORK.md:1257-1258, :1292). There is no path with no deadline.")

(defconstant +resolver-kill-grace-seconds+ 0.25
  "Seconds a resolver gets between SIGTERM and SIGKILL when its deadline
expires or it overruns `--max-bytes'.")

(defstruct (command-resolver
            (:include verification-resolver)
            (:constructor make-command-resolver
                (scheme command &key max-bytes timeout)))
  ;; the most UTF-8 bytes of the resolver's first stdout line this run accepts
  (max-bytes 4096)
  ;; the resolver's own execution deadline in seconds; NIL means the default
  (timeout +resolver-default-timeout-seconds+))

(defun %resolver-whitespace-p (char)
  (member char '(#\Space #\Tab #\Return #\Newline)))

(defun %resolver-tokens (line)
  "LINE split into its non-empty whitespace-separated tokens."
  (let ((tokens '())
        (start 0)
        (end (length line)))
    (loop
      (let ((sep (position-if #'%resolver-whitespace-p line :start start)))
        (when (> (or sep end) start)
          (push (subseq line start (or sep end)) tokens))
        (unless (and sep (< sep end))
          (return))
        (setf start (1+ sep))))
    (nreverse tokens)))

(defun parse-resolver-output (text &key max-bytes)
  "The single answer TEXT must be (SPEC-WORK.md:1251-1266): exactly one line,
exactly two whitespace-separated tokens, the first `holds' or `absent', with at
most one trailing newline. A newline followed by any further byte is a refusal
-- never a silent truncation. MAX-BYTES bounds the line's UTF-8 octet length,
excluding the one allowed final newline and measured before the trailing
whitespace trim. Returns (values FACT STAMP), where FACT is `:holds' or
`:absent', or NIL when there is no acceptable answer -- no line, one or three
tokens, an unknown first token, output past the one line, or a line whose UTF-8
octets pass MAX-BYTES."
  (when (stringp text)
    (let* ((newline (position #\Newline text))
           (raw (if newline (subseq text 0 newline) text)))
      (when (or (null newline) (= newline (1- (length text))))
        (when (or (null max-bytes)
                  (<= (utf8-bytes-up-to raw (length raw)) max-bytes))
          (let* ((line (string-trim '(#\Space #\Tab #\Return) raw))
                 (tokens (%resolver-tokens line)))
            (when (= 2 (length tokens))
              (let ((fact (cond ((string= (first tokens) "holds") :holds)
                                ((string= (first tokens) "absent") :absent)
                                (t nil))))
                (when fact
                  (values fact (second tokens)))))))))))

(defun %kill-and-reap-resolver-process (process)
  "Own the resolver PROCESS to the end. SIGTERM it; if it is still alive after
+RESOLVER-KILL-GRACE-SECONDS+, SIGKILL it; then WAIT for it unconditionally so
no zombie survives, and close its output stream. Safe on an already-exited
process, so an UNWIND-PROTECT cleanup may always call it."
  (when process
    (when (sb-ext:process-alive-p process)
      (ignore-errors (sb-ext:process-kill process sb-posix:sigterm))
      (let ((grace-end (+ (get-internal-real-time)
                          (round (* +resolver-kill-grace-seconds+
                                    internal-time-units-per-second)))))
        (loop while (and (sb-ext:process-alive-p process)
                         (< (get-internal-real-time) grace-end))
              do (sleep 0.005)))
      (when (sb-ext:process-alive-p process)
        (ignore-errors (sb-ext:process-kill process sb-posix:sigkill))))
    (ignore-errors (sb-ext:process-wait process))
    (let ((stream (ignore-errors (sb-ext:process-output process))))
      (when stream (ignore-errors (close stream))))))

(defun run-resolver-command (command pointer subject
                             &key (timeout +resolver-default-timeout-seconds+)
                                  (max-bytes 4096))
  "Run the operator's resolver COMMAND as a real process, directly and never
through a shell, passing exactly two arguments, POINTER and SUBJECT, and read
exactly one line of stdout `<fact> <stamp>' (SPEC-WORK.md:1251-1266). The child
runs under TIMEOUT seconds -- a positive real, NIL resolving to
+RESOLVER-DEFAULT-TIMEOUT-SECONDS+ -- and stdout is read as it runs, never after
it has all been buffered; the reader stops the instant the retained answer would
pass MAX-BYTES UTF-8 octets plus the one allowed trailing newline. A deadline
expiry or an over-long answer kills and reaps the child and signals
VERIFICATION-UNREACHABLE. Exit 0 holds, 1 absent, 2 could not establish; any
other exit, a fact disagreeing with the exit, no line, output after the one
line, or a line past MAX-BYTES signals VERIFICATION-UNREACHABLE. COMMAND is the
program itself, verbatim and unsplit; POINTER and SUBJECT are never
interpolated into a command line. Stderr is never captured."
  #+sbcl
  (let* ((deadline-seconds (or timeout +resolver-default-timeout-seconds+))
         (limit (and max-bytes (1+ max-bytes)))
         (deadline (+ (get-internal-real-time)
                      (round (* deadline-seconds internal-time-units-per-second))))
         (process (sb-ext:run-program command (list pointer subject)
                                      :search t
                                      :input nil
                                      :output :stream
                                      :error nil
                                      :wait nil)))
    (unwind-protect
         (progn
           (unless process
             (error 'verification-unreachable
                    :what (format nil "resolver ~A did not start" command)))
           (let* ((stream (sb-ext:process-output process))
                  (answer (make-string-output-stream))
                  (bytes 0)
                  (overflow nil)
                  (expired nil))
             (loop
               (let ((ch (read-char-no-hang stream nil :eof)))
                 (cond
                   ((eq ch :eof)
                    (if (sb-ext:process-alive-p process)
                        (if (>= (get-internal-real-time) deadline)
                            (progn (setf expired t) (return))
                            (sleep 0.005))
                        (return)))
                   ((null ch)
                    (when (>= (get-internal-real-time) deadline)
                      (setf expired t)
                      (return))
                    (sleep 0.005))
                   (t
                    (incf bytes (char-utf8-bytes ch))
                    (when (and limit (> bytes limit))
                      (setf overflow t)
                      (return))
                    (write-char ch answer)))))
             (cond
               (overflow
                (error 'verification-unreachable
                       :what (format nil "resolver ~A answered past ~D bytes"
                                     command max-bytes)))
               (expired
                (error 'verification-unreachable
                       :what (format nil "resolver ~A did not answer within ~A seconds"
                                     command deadline-seconds)))
               (t
                (sb-ext:process-wait process)
                (let ((exit (sb-ext:process-exit-code process))
                      (text (get-output-stream-string answer)))
                  (cond
                    ((eql exit 2)
                     (error 'verification-unreachable
                            :what (format nil "resolver ~A exited 2: could not establish"
                                          command)))
                    ((not (member exit '(0 1)))
                     (error 'verification-unreachable
                            :what (format nil "resolver ~A exited ~A, not 0, 1 or 2"
                                          command exit)))
                    (t
                     (multiple-value-bind (fact stamp)
                         (parse-resolver-output text :max-bytes max-bytes)
                       (unless fact
                         (error 'verification-unreachable
                                :what (format nil "resolver ~A did not answer one `<fact> <stamp>` line"
                                              command)))
                       (when (or (and (eql exit 0) (eq fact :absent))
                                 (and (eql exit 1) (eq fact :holds)))
                         (error 'verification-unreachable
                                :what (format nil "resolver ~A answered ~A on exit ~A"
                                              command
                                              (string-downcase (symbol-name fact))
                                              exit)))
                       (values fact stamp)))))))))
      (%kill-and-reap-resolver-process process)))
  #-sbcl
  (error 'unsupported-input
         :what "no process runner outside SBCL; the engine's platform is pinned"))

(defmethod fetch-resolver-fact ((resolver command-resolver) pointer subject
                                &key timeout)
  (run-resolver-command (verification-resolver-command resolver)
                        pointer subject
                        :timeout (or timeout (command-resolver-timeout resolver))
                        :max-bytes (command-resolver-max-bytes resolver)))
