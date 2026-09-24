;;;; operation-events.lisp --- the operation's event cursor and the bounded
;;;; block `operation wait` is (SPEC-WORK.md:2736-2740, :2759).
;;;;
;;;; "**Waiting is an event cursor and a bounded block, never a model asking
;;;; again in a loop** (a poll loop is a whole agent per tick, which the record
;;;; already paid for)" (:2736-2738), and "**A wait that times out leaves the
;;;; operation running**; a completed result is retrievable by its id
;;;; afterwards" (:2738-2740).
;;;;
;;;; So a wait here is not a model. It is the same concurrency the resident
;;;; session's kernel already runs on (src/command-thread.lisp): one mutex, one
;;;; waitqueue, SB-THREAD:CONDITION-WAIT with the caller's remaining timeout.
;;;; The waiter holds no CPU between events; the producer -- whatever thread is
;;;; actually doing the long work -- appends an event and broadcasts, and every
;;;; waiter wakes on that broadcast and not on a tick. A wait that reaches its
;;;; deadline answers the NOTE line the grammar fixes at :5980 and leaves the
;;;; operation running.
;;;;
;;;; The cursor is a count, so AFTER is what a resumed wait passes to avoid
;;;; reading the operation's events again from zero, and the page it answers is
;;;; bounded and capped like every other listing. The stream itself is bounded
;;;; by an explicit limit: past it an emit REFUSES rather than growing, which is
;;;; what ":2759" asks of every queue here.

(in-package #:nova-work)

(defparameter *operation-event-cap* 64
  "The explicit bound on one operation's event cursor. Past it an emit refuses
rather than growing (SPEC-WORK.md:2759).")

(defparameter *operation-wait-page* 32
  "The default page a wait answers, capped like every other listing
(SPEC-WORK.md:2735-2736).")

(defparameter *operation-terminal-states* '(:done :cancelled :failed :uncertain)
  "The states a wait may return on without a new event: an operation that has
reached its final disposition.")

(defun terminal-operation-state-p (state)
  (and (member state *operation-terminal-states*) t))

;;; ------------------------------------------------------------------
;;; The stream: one mutex and one waitqueue per operation
;;; ------------------------------------------------------------------

(defstruct (operation-event-stream (:constructor %make-operation-event-stream))
  (lock (sb-thread:make-mutex :name "nova-work-operation-events"))
  (cvar (sb-thread:make-waitqueue))
  ;; newest first, so an append is a push and the page reverses what it takes
  (entries '())
  (count 0)
  (cap *operation-event-cap*))

(defvar *operation-stream-lock* (sb-thread:make-mutex :name "nova-work-operation-streams")
  "Guards the creation of an operation's stream, so two threads reaching a
brand-new operation at once cannot end up waiting on two different waitqueues.")

(defun operation-cell (registry id)
  "The registry's cell for ID, recovering it from the durable journal first when
this process does not hold it. NIL when no journal holds ID."
  (or (assoc id (operation-registry-operations registry) :test #'equal)
      (progn (recover-operation registry id)
             (assoc id (operation-registry-operations registry) :test #'equal))))

(defun ensure-operation-event-stream (cell &key (cap *operation-event-cap*))
  (sb-thread:with-mutex (*operation-stream-lock*)
    (or (getf (cdr cell) :stream)
        (setf (getf (cdr cell) :stream)
              (%make-operation-event-stream :cap cap)))))

(defun no-such-operation-line (id)
  (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id))

;;; ------------------------------------------------------------------
;;; Appending to the cursor, and settling the operation
;;; ------------------------------------------------------------------

(defun operation-emit-event (registry id &key kind stamp (external :none))
  "Append one event to ID's cursor and wake every wait blocked on it. Answers
(values CURSOR NIL 0), or (values NIL LINE 2) for an id no journal holds and for
an emit past the stream's explicit bound (SPEC-WORK.md:2736-2738, :2759)."
  (let ((cell (operation-cell registry id)))
    (if (null cell)
        (values nil (no-such-operation-line id) 2)
        (let ((stream (ensure-operation-event-stream cell)))
          (sb-thread:with-mutex ((operation-event-stream-lock stream))
            (if (>= (operation-event-stream-count stream)
                    (operation-event-stream-cap stream))
                (values nil
                        (format nil "OPERATION FAIL id=~A op=~A state=~(~A~): ~A"
                                id (or (getf (cdr cell) :kind) "-")
                                (or (getf (cdr cell) :state) :running)
                                "the event cursor is at its bound")
                        2)
                (let ((cursor (1+ (operation-event-stream-count stream))))
                  (push (list :cursor cursor :kind kind :stamp stamp
                              :external external)
                        (operation-event-stream-entries stream))
                  (setf (operation-event-stream-count stream) cursor)
                  (sb-thread:condition-broadcast (operation-event-stream-cvar stream))
                  (values cursor nil 0))))))))

(defun operation-settle (registry id &key (state :done) result stamp
                                          (external :none) (kind :settled))
  "Give ID its final disposition and wake every wait on it. The state and the
result are installed under the same lock the waiters block on, so no waiter can
observe a settled operation without its result (SPEC-WORK.md:2738-2740)."
  (let ((cell (operation-cell registry id)))
    (if (null cell)
        (values nil (no-such-operation-line id) 2)
        (let ((stream (ensure-operation-event-stream cell)))
          (sb-thread:with-mutex ((operation-event-stream-lock stream))
            (setf (getf (cdr cell) :state) state)
            (setf (getf (cdr cell) :result) result)
            (let ((cursor (if (< (operation-event-stream-count stream)
                                 (operation-event-stream-cap stream))
                              (let ((next (1+ (operation-event-stream-count stream))))
                                (push (list :cursor next :kind kind :stamp stamp
                                            :external external)
                                      (operation-event-stream-entries stream))
                                (setf (operation-event-stream-count stream) next))
                              (operation-event-stream-count stream))))
              (sb-thread:condition-broadcast (operation-event-stream-cvar stream))
              (values cursor nil 0)))))))

(defun operation-notify (registry id)
  "Wake every wait on ID without appending an event. A cancellation uses this:
its disposition is already installed and its record is already durable, and a
waiter must not go on blocking behind it (SPEC-WORK.md:2740-2743)."
  (let ((cell (operation-cell registry id)))
    (when cell
      (let ((stream (ensure-operation-event-stream cell)))
        (sb-thread:with-mutex ((operation-event-stream-lock stream))
          (sb-thread:condition-broadcast (operation-event-stream-cvar stream))))
      t)))

;;; ------------------------------------------------------------------
;;; The page a wait answers, and the wait itself
;;; ------------------------------------------------------------------

(defun operation-event-page (stream after max)
  "The events strictly after cursor AFTER, oldest first, capped at MAX. The
cursor is a count, so a resumed wait does not read the events again from zero
(SPEC-WORK.md:2735-2737)."
  (let ((rows (loop for entry in (reverse (operation-event-stream-entries stream))
                    when (> (getf entry :cursor) after)
                      collect entry)))
    (if (and max (> (length rows) max))
        (subseq rows 0 max)
        rows)))

(defun operation-row-line (id kind started row)
  "One OPERATION ROW line per event of a wait's cursor (SPEC-WORK.md:5979)."
  (format nil "OPERATION ROW id=~A op=~A state=~(~A~) started=~A updated=~A external=~(~A~)"
          id (or kind "-") (or (getf row :kind) "-")
          (or started "-") (or (getf row :stamp) "-")
          (or (getf row :external) :none)))

(defun durable-operation-wait (registry id &key (timeout "30s") (after 0) max)
  "`operation wait --id <id> --timeout <duration> [--after <cursor>]`.

Answers (values ROWS STATE CURSOR LINE CODE). A bounded block on the
operation's waitqueue: the caller sleeps until an event past AFTER arrives, or
the operation settles, or the deadline passes. It is never a loop that asks
again -- the only wakeups are the producer's broadcast and the deadline
(SPEC-WORK.md:2736-2738).

A wait that times out leaves the operation running and answers the cursor it
reached, with the NOTE line the grammar fixes at :5980. A settled operation
answers at once with its rows and its state, so a completed result is
retrievable by its id afterwards. An id no journal holds is the line at :5982,
exit 2."
  (let ((cell (operation-cell registry id)))
    (when (null cell)
      (return-from durable-operation-wait
        (values '() nil after (no-such-operation-line id) 2)))
    (let* ((stream (ensure-operation-event-stream cell))
           (seconds (parse-duration timeout))
           (deadline (+ (get-internal-real-time)
                        (round (* seconds internal-time-units-per-second))))
           (page (or max *operation-wait-page*)))
      (sb-thread:with-mutex ((operation-event-stream-lock stream))
        (loop
          (let ((count (operation-event-stream-count stream))
                (state (getf (cdr cell) :state)))
            (when (or (> count after) (terminal-operation-state-p state))
              (return (values (operation-event-page stream after page)
                              state count nil 0)))
            (let ((remaining (/ (- deadline (get-internal-real-time))
                                internal-time-units-per-second)))
              (when (<= remaining 0)
                ;; The operation is still running, and this line says so.
                (return (values '() :timeout after
                                (format nil "OPERATION NOTE waiting id=~A timeout=~A after=~D"
                                        id timeout after)
                                0)))
              (or (sb-thread:condition-wait (operation-event-stream-cvar stream)
                                            (operation-event-stream-lock stream)
                                            :timeout remaining)
                  ;; NIL: the mutex is not held, answer the same note.
                  (return (values '() :timeout after
                                  (format nil "OPERATION NOTE waiting id=~A timeout=~A after=~D"
                                          id timeout after)
                                  0))))))))))

;;; ------------------------------------------------------------------
;;; The resident session's own recovery journal (SPEC-WORK.md:2729-2731)
;;; ------------------------------------------------------------------

(defun session-recovery-journal-path (session)
  "The local recovery journal beside the resident session it belongs to. A
session with no path has no local journal to put it beside, which is a refusal
and not a temporary file somewhere else."
  (let ((path (session-path session)))
    (if (or (null path) (string= path ""))
        (error 'unsupported-input
               :what "a session with no path has no local recovery journal")
        (concatenate 'string path ".operations"))))
