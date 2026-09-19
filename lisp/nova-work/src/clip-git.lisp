;;;; clip-git.lisp --- the clip transport over a real git remote
;;;; (AUDIT row E02.6, SPEC-WORK.md:2744-2750, :5955-5957).
;;;;
;;;; "**The clip's transport is that shape and is not a second synchronous
;;;; one**: `clip` prints `OPERATION OK id=<id> op=clip state=<queued|running>`
;;;; at once and exits; the `CLIP OK` line, carrying `operation=<id>`, is what
;;;; `operation wait --id <id>` prints when the transport settles, and `CLIP
;;;; RACED` and `CLIP FAIL` arrive the same way; and **`session stop` is the one
;;;; caller that waits for its own clip**, and it waits by that same `operation
;;;; wait`, inside its `--git-timeout`" (:2744-2750).
;;;;
;;;; src/operations.lisp already declared the seam -- CLIP-REMOTE-TIP and
;;;; CLIP-REMOTE-PUSH, with an in-process implementation whose tip is a slot.
;;;; This is the other implementation: a real local bare repository. The tip is
;;;; read with `git rev-parse`, and the push is `git update-ref <ref> <new>
;;;; <expected-old>`, which is a compare-and-swap performed by git itself. That
;;;; IS the base predicate of :2746-2748 -- `tip == base` is not checked by this
;;;; file and then acted on, which would be a race; it is checked and acted on
;;;; in one operation by the thing that owns the ref, and a refusal is CLIP
;;;; RACED.
;;;;
;;;; THIS IS THE FIRST SUBPROCESS CALL IN lisp/nova-work, and it is deliberate:
;;;; the clip's transport is git, and there is no way to make the row real
;;;; without invoking it. Every call goes through GIT-RUN, which passes
;;;; --git-dir explicitly, never inherits a working directory, never runs a
;;;; shell, and answers the exit code and both streams so a failure is reported
;;;; rather than guessed.
;;;;
;;;; A local bare repository reached by path is a real remote for everything
;;;; this row promises: a tip that another writer can move under us, an
;;;; atomic ref update, and a push that either lands or refuses.

(in-package #:nova-work)

(defparameter *clip-git-identity*
  '("-c" "user.name=nova-work" "-c" "user.email=nova-work@localhost")
  "The identity `git commit-tree` writes with. It is passed per invocation so
this never reads or depends on the bench's git configuration.")

(defparameter *clip-git-payload-path* "clip"
  "The one path in the clip's tree. The clip publishes its boundary and its
snapshot digest; it is not a checkout.")

(defstruct (git-clip-remote (:constructor %make-git-clip-remote))
  (directory "")
  (ref "refs/heads/main"))

;;; ------------------------------------------------------------------
;;; Running git
;;; ------------------------------------------------------------------

(defun git-run (directory args &key input)
  "Run git against the bare repository at DIRECTORY. Answers
(values EXIT-CODE STDOUT STDERR). No shell is involved and no working directory
is inherited."
  (let ((out (make-string-output-stream))
        (err (make-string-output-stream)))
    (let ((process (sb-ext:run-program
                    "git" (append (list "--git-dir" directory) args)
                    :search t :wait t
                    :input (when input (make-string-input-stream input))
                    :output out :error err)))
      (values (sb-ext:process-exit-code process)
              (string-right-trim '(#\Newline #\Return)
                                 (get-output-stream-string out))
              (string-right-trim '(#\Newline #\Return)
                                 (get-output-stream-string err))))))

(defun open-git-clip-remote (directory &key (ref "refs/heads/main"))
  "The clip's remote: a real bare repository at DIRECTORY, created when it is
not there. No branch name is assumed of git's defaults -- the ref this remote
clips to is named outright and created by the first push."
  (let ((out (make-string-output-stream))
        (err (make-string-output-stream)))
    (unless (probe-file (concatenate 'string directory "/HEAD"))
      (ensure-directories-exist (concatenate 'string directory "/"))
      (let ((process (sb-ext:run-program "git" (list "init" "--bare" directory)
                                         :search t :wait t
                                         :output out :error err)))
        (unless (eql 0 (sb-ext:process-exit-code process))
          (error 'unsupported-input
                 :what (format nil "git init --bare failed for ~A: ~A"
                               directory (get-output-stream-string err))))))
    (%make-git-clip-remote :directory directory :ref ref)))

;;; ------------------------------------------------------------------
;;; The seam, over the real repository
;;; ------------------------------------------------------------------

(defmethod clip-remote-tip ((remote git-clip-remote))
  "The upstream tip. An empty remote answers \"genesis\", the same word the
in-process implementation starts at, so a first clip's base predicate reads the
same either way."
  (multiple-value-bind (code sha)
      (git-run (git-clip-remote-directory remote)
               (list "rev-parse" "--verify" "--quiet"
                     (git-clip-remote-ref remote)))
    (if (and (eql 0 code) (plusp (length sha))) sha "genesis")))

(defun git-clip-write-payload (remote base commit)
  "Write the clip's payload into the repository as a blob, a tree and a commit
whose parent is BASE. Answers the new commit's sha, or NIL with a reason."
  (let ((directory (git-clip-remote-directory remote))
        (payload (format nil "nova-work clip~%base=~A~%commit=~A~%" base commit)))
    (multiple-value-bind (code blob err)
        (git-run directory (list "hash-object" "-w" "-t" "blob" "--stdin")
                 :input payload)
      (if (not (eql 0 code))
          (values nil (format nil "hash-object: ~A" err))
          (multiple-value-bind (code tree err)
              (git-run directory (list "mktree")
                       :input (format nil "100644 blob ~A~C~A~%"
                                      blob #\Tab *clip-git-payload-path*))
            (if (not (eql 0 code))
                (values nil (format nil "mktree: ~A" err))
                (multiple-value-bind (code sha err)
                    (git-run directory
                             (append *clip-git-identity*
                                     (list "commit-tree" tree)
                                     (when (and base (not (equal base "genesis")))
                                       (list "-p" base))
                                     (list "-m" (format nil "nova-work clip ~A" commit))))
                  (if (eql 0 code)
                      (values sha nil)
                      (values nil (format nil "commit-tree: ~A" err))))))))))

(defmethod clip-remote-push ((remote git-clip-remote) base commit)
  "Push the clip. The base predicate and the publication are ONE operation:
`git update-ref <ref> <new> <expected-old>` moves the ref only when it is still
BASE, so nothing here reads the tip and then acts on it. A refusal answers NIL
and moves nothing, which the caller reports as CLIP RACED (SPEC-WORK.md:2746-2748).
Answers (values NEW-SHA NIL) on success, or (values NIL REASON)."
  (multiple-value-bind (sha reason) (git-clip-write-payload remote base commit)
    (if (null sha)
        (values nil reason)
        (multiple-value-bind (code out err)
            (git-run (git-clip-remote-directory remote)
                     (list "update-ref" (git-clip-remote-ref remote) sha
                           (if (equal base "genesis") "" base)))
          (declare (ignore out))
          (if (eql 0 code)
              (values sha nil)
              (values nil (or (and (plusp (length err)) err) "the base moved")))))))

(defun split-lines (text)
  "TEXT split on newlines, empty lines dropped. A local splitter so nothing in
src/ depends on uiop."
  (let ((lines (list)) (start 0))
    (loop for i from 0 below (length text)
          when (char= (char text i) #\Newline)
            do (push (subseq text start i) lines) (setf start (1+ i)))
    (push (subseq text start) lines)
    (remove "" (nreverse lines) :test #'equal)))

(defun git-clip-remote-history (remote)
  "Every commit on the clipped ref, newest first. Nothing in the engine needs
this; the acceptance cases read the remote with it."
  (multiple-value-bind (code out)
      (git-run (git-clip-remote-directory remote)
               (list "log" "--format=%H" (git-clip-remote-ref remote)))
    (if (eql 0 code)
        (split-lines out)
        '())))

(defun git-clip-commit-message (remote sha)
  (multiple-value-bind (code out)
      (git-run (git-clip-remote-directory remote)
               (list "log" "-1" "--format=%s" sha))
    (if (eql 0 code) out "")))

;;; ------------------------------------------------------------------
;;; The clip as one long operation (SPEC-WORK.md:2744-2750, :5955-5957)
;;; ------------------------------------------------------------------

(defun clip-ok-line (&key session operation boundary events base commit pushed
                          (attempts 1) (emitted 0))
  (format nil "CLIP OK session=~A operation=~A boundary=~A events=~D base=~A commit=~A pushed=~D attempts=~D emitted=~D"
          session operation boundary events base commit pushed attempts emitted))

(defun clip-raced-line (&key session operation boundary (generation 1) expected found)
  (format nil "CLIP RACED session=~A operation=~A boundary=~A generation=~D expected=~A found=~A"
          session operation boundary generation (clip-sha12 expected) (clip-sha12 found)))

(defun clip-fail-line (&key session operation boundary events base (pushed "-")
                            (attempts 1) reason)
  (format nil "CLIP FAIL session=~A operation=~A boundary=~A events=~D base=~A pushed=~A attempts=~D: ~A"
          session operation boundary events base pushed attempts reason))

(defun run-clip-transport (registry remote &key id session boundary (events 0)
                                                base commit revision stamp)
  "The clip's transport, run on whatever thread is doing the work -- never the
caller's. It pushes, then SETTLES the operation with the line the caller will
read out of `operation wait`: CLIP OK, CLIP RACED or CLIP FAIL
(SPEC-WORK.md:2744-2750). Answers the line.

`clip` itself has already printed OPERATION OK and exited; this is what happens
afterwards, and the only way anyone learns of it is the wait."
  (let* ((base (or base (clip-remote-tip remote)))
         (line nil)
         (state :done))
    (multiple-value-bind (sha reason) (clip-remote-push remote base commit)
      (cond
        (sha
         (setf line (clip-ok-line :session session :operation id :boundary boundary
                                  :events events :base base :commit commit
                                  :pushed (or revision 0))))
        ;; A refused push is the race exactly when the tip is no longer the
        ;; base. That is read from the remote rather than from git's wording,
        ;; so the classification does not depend on a message that may change
        ;; between git versions.
        (t
         (setf state :failed)
         (let ((tip (clip-remote-tip remote)))
           (if (not (equal tip base))
               (setf line (clip-raced-line :session session :operation id
                                           :boundary boundary :expected base
                                           :found tip))
               (setf line (clip-fail-line :session session :operation id
                                          :boundary boundary :events events
                                          :base base
                                          :reason (or reason "push refused"))))))))
    (operation-settle registry id :state state :result line :stamp stamp
                                  :kind :clip-settled)
    line))

(defun session-stop-wait-for-clip (registry id &key (git-timeout "30s"))
  "`session stop` is the one caller that waits for its own clip, and it waits by
that same `operation wait`, inside its `--git-timeout` (SPEC-WORK.md:2748-2750,
:6091-6095). Answers (values LINE STATE CODE): the clip's own settled line when
it settles in time, or the wait's NOTE line when the git timeout passes, which
leaves the transport running."
  (multiple-value-bind (rows state cursor line code)
      (durable-operation-wait registry id :timeout git-timeout :after 0)
    (declare (ignore rows cursor))
    (if (terminal-operation-state-p state)
        (values (registry-operation-result registry id) state 0)
        (values line state code))))
