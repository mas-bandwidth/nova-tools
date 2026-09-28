;;;; command-thread.lisp --- the single writer.
;;;;
;;;; docs/SPEC-WORK.md:2603-2616 (SPEC-AHEAD #500, rule 6): the kernel that owns
;;;; O and C is one command thread, like redis, or it corrupts. Every mutation
;;;; is a command applied in order by that thread and journaled in the same
;;;; order -- the sequence number is the order; readers, network, journal fsync
;;;; and clip may run elsewhere but never touch the structure. `kernel:submit`
;;;; becomes an enqueue that waits for its receipt. Validator rule: a mutation
;;;; outside the command loop is a defect.

(in-package #:nova-work)

(defun start-command-thread (kernel)
  "Spawn the kernel's single command thread and return it."
  (sb-thread:make-thread (lambda () (command-loop kernel))
                         :name "nova-work-kernel"))

(defun command-loop (kernel)
  "The single command thread. It drains the mailbox one command at a time and
applies each against O and C in the order it arrived. The journal's sequence
numbers follow from that order, so the sequence number is the total order."
  (unwind-protect
       (loop
         (let ((cmd nil))
           (sb-thread:with-mutex ((kernel-q-lock kernel))
             (loop while (and (null (kernel-queue kernel))
                              (not (kernel-closed-p kernel)))
                   do (sb-thread:condition-wait (kernel-q-cvar kernel)
                                                (kernel-q-lock kernel)))
             (when (and (kernel-closed-p kernel) (null (kernel-queue kernel)))
               (return))
             (setf cmd (pop (kernel-queue kernel))))
           (run-command kernel cmd)))
    ;; On unwind (shutdown) close the mailbox so no further command is admitted.
    (sb-thread:with-mutex ((kernel-q-lock kernel))
      (setf (kernel-closed-p kernel) t)
      (sb-thread:condition-broadcast (kernel-q-cvar kernel)))))

(defun run-command (kernel cmd)
  "Run one command on the command thread, then reply to the waiter. This is the
only place a mutation runs: *BEFORE-APPLY-HOOK*, whose binding is thread-local,
is restored from what submit captured."
  (let ((*before-apply-hook* (cmd-before-apply-hook cmd)))
    (handler-case
        (multiple-value-bind (okp line code env) (%dispatch kernel (cmd-request cmd))
          (finish-command cmd (list okp line code env) nil))
      (error (c)
        (finish-command cmd nil c)))))

(defun finish-command (cmd results error)
  (sb-thread:with-mutex ((cmd-lock cmd))
    (setf (cmd-results cmd) results
          (cmd-error cmd) error
          (cmd-done-p cmd) t)
    (sb-thread:condition-notify (cmd-cvar cmd))))

(defun submit (kernel request)
  "Answer (values OK-P LINE EXIT-CODE ENVELOPE). The request is enqueued onto the
kernel's one command thread and the caller waits for its receipt; readers of O
and C never touch that thread."
  (let ((cmd (make-kernel-command
              :request request
              :before-apply-hook *before-apply-hook*
              :lock (sb-thread:make-mutex)
              :cvar (sb-thread:make-waitqueue))))
    (sb-thread:with-mutex ((kernel-q-lock kernel))
      (when (kernel-closed-p kernel)
        (error 'unsupported-input :what "the kernel's command thread is closed"))
      (setf (kernel-queue kernel) (nconc (kernel-queue kernel) (list cmd)))
      (sb-thread:condition-notify (kernel-q-cvar kernel)))
    (sb-thread:with-mutex ((cmd-lock cmd))
      (loop until (cmd-done-p cmd)
            do (sb-thread:condition-wait (cmd-cvar cmd) (cmd-lock cmd))))
    (if (cmd-error cmd)
        (error (cmd-error cmd))
        (values-list (cmd-results cmd)))))
