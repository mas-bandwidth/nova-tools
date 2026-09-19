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

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The command resolver (SPEC-WORK.md:1251-1266)
;;; ------------------------------------------------------------------

(defstruct (command-resolver
            (:include verification-resolver)
            (:constructor make-command-resolver (scheme command &key max-bytes)))
  ;; the most bytes of the resolver's first stdout line this run accepts
  (max-bytes 4096))

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

(defun parse-resolver-line (text &key max-bytes)
  "The fact and stamp on the FIRST line of TEXT (SPEC-WORK.md:1251-1266):
exactly two whitespace-separated tokens, the first `holds' or `absent'. Returns
(values FACT STAMP) where FACT is `:holds' or `:absent', or NIL when there is no
acceptable line -- no line, one or three tokens, an unknown first token, or a
first line past MAX-BYTES."
  (when (stringp text)
    (let* ((end (or (position #\Newline text) (length text)))
           (raw (subseq text 0 end)))
      (when (or (null max-bytes) (<= (length raw) max-bytes))
        (let* ((line (string-trim '(#\Space #\Tab #\Return) raw))
               (tokens (%resolver-tokens line)))
          (when (= 2 (length tokens))
            (let ((fact (cond ((string= (first tokens) "holds") :holds)
                              ((string= (first tokens) "absent") :absent)
                              (t nil))))
              (when fact
                (values fact (second tokens))))))))))

(defun run-resolver-command (command pointer subject &key timeout (max-bytes 4096))
  "Run the operator's resolver COMMAND as a real process, directly and never
through a shell, passing exactly two arguments, POINTER and SUBJECT, and read
one line of stdout `<fact> <stamp>' (SPEC-WORK.md:1251-1266). Exit 0 holds, 1
absent, 2 could not establish; any other exit, a fact disagreeing with the exit,
no line, or a first line past MAX-BYTES signals VERIFICATION-UNREACHABLE. COMMAND
is the program itself, verbatim and unsplit; POINTER and SUBJECT are never
interpolated into a command line."
  #+sbcl
  (let* ((out (make-string-output-stream))
         (process (sb-ext:run-program command (list pointer subject)
                                      :search t
                                      :input nil
                                      :output out
                                      :error nil
                                      :wait t))
         (exit (and process (sb-ext:process-exit-code process)))
         (text (get-output-stream-string out)))
    (declare (ignore timeout))
    (cond
      ((eql exit 2)
       (error 'verification-unreachable
              :what (format nil "resolver ~A exited 2: could not establish" command)))
      ((not (member exit '(0 1)))
       (error 'verification-unreachable
              :what (format nil "resolver ~A exited ~A, not 0, 1 or 2"
                            command exit)))
      (t
       (multiple-value-bind (fact stamp) (parse-resolver-line text :max-bytes max-bytes)
         (unless fact
           (error 'verification-unreachable
                  :what (format nil "resolver ~A did not answer one `<fact> <stamp>` line"
                                command)))
         (when (or (and (eql exit 0) (eq fact :absent))
                   (and (eql exit 1) (eq fact :holds)))
           (error 'verification-unreachable
                  :what (format nil "resolver ~A answered ~A on exit ~A"
                                command (string-downcase (symbol-name fact)) exit)))
         (values fact stamp)))))
  #-sbcl
  (error 'unsupported-input
         :what "no process runner outside SBCL; the engine's platform is pinned"))

(defmethod fetch-resolver-fact ((resolver command-resolver) pointer subject
                                &key timeout)
  (run-resolver-command (verification-resolver-command resolver)
                        pointer subject
                        :timeout timeout
                        :max-bytes (command-resolver-max-bytes resolver)))
