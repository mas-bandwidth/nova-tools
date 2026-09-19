;;;; replays-e02-wait.lisp --- `operation wait` as an event cursor and a bounded
;;;; block, over the same concurrency the resident session's kernel runs on
;;;; (AUDIT row E02.1, SPEC-WORK.md:2736-2740, :2759).
;;;;
;;;;   a-wait-is-a-bounded-block-that-wakes-on-an-event   :2736-2738
;;;;   a-wait-that-times-out-leaves-the-operation-running :2738-2739, :5980
;;;;   a-completed-result-is-retrievable-by-its-id-afterwards  :2739-2740
;;;;   a-resumed-wait-does-not-read-the-events-again      :2735-2737
;;;;   the-event-cursor-is-bounded-by-an-explicit-limit   :2759
;;;;   a-cancellation-wakes-a-wait                        :2740-2743
;;;;   waiting-on-an-id-no-journal-holds-is-its-own-line  :2733-2735, :5982
;;;;   a-session-keeps-its-operations-beside-its-own-journal   :2729-2733
;;;;
;;;; Every case here uses a REAL second thread as the producer, because the
;;;; claim is about blocking: "Waiting is an event cursor and a bounded block,
;;;; never a model asking again in a loop". A model that answers at once cannot
;;;; be told from a block that works, so each timing case asserts both bounds --
;;;; that the wait did not return before the producer emitted, and that it did
;;;; not sit until its own deadline.

(in-package #:nova-work/tests)

(defun elapsed-seconds (start)
  (/ (- (get-internal-real-time) start) internal-time-units-per-second))

(defvar *wait-test-counter* 0)

(defun test-session-root (name)
  "A fresh empty directory to hold a session and its recovery journal."
  (let* ((base (uiop:default-temporary-directory))
         (dir (merge-pathnames (format nil "nova-work-test-sessions/~A-~D-~D/"
                                       name (get-universal-time)
                                       (incf *wait-test-counter*))
                               base)))
    (ensure-directories-exist dir)
    (string-right-trim "/" (namestring (truename dir)))))

(defun wait-test-registry (path)
  (let ((registry (open-durable-operation-registry path)))
    (operation-accept registry :id "op-a" :kind "capture" :request "req-a"
                               :author "rowan" :stamp "2026-09-19T12:00:00Z")
    registry))

;;; ------------------------------------------------------------------
;;; a-wait-is-a-bounded-block-that-wakes-on-an-event      :2736-2738
;;; ------------------------------------------------------------------

(deftest "a-wait-is-a-bounded-block-that-wakes-on-an-event" "docs/SPEC-WORK.md:2736-2738"
    "expected=the-wait-blocks-until-the-producer-emits;wakes-on-the-broadcast-not-the-deadline;cursor-advances"
  (let* ((path (test-journal-path "operation-wait"))
         (registry (wait-test-registry path)))
    (unwind-protect
         (let* ((start (get-internal-real-time))
                (producer (sb-thread:make-thread
                           (lambda ()
                             (sleep 0.20)
                             (operation-emit-event registry "op-a" :kind :staged
                                                   :stamp "2026-09-19T12:00:01Z"))
                           :name "nova-work-test-producer")))
           (multiple-value-bind (rows state cursor line code)
               (durable-operation-wait registry "op-a" :timeout "10s" :after 0)
             (let ((took (elapsed-seconds start)))
               (sb-thread:join-thread producer)
               (check-equal nil line "the wait is answered, not refused")
               (check-equal 0 code "the wait exits 0")
               (check-equal 1 (length rows) "the wait answers the one event past the cursor")
               (check-equal 1 (getf (first rows) :cursor) "the cursor advanced to 1")
               (check-equal :staged (getf (first rows) :kind) "the event is the producer's")
               (check-equal 1 cursor "the wait answers the cursor it reached")
               (check-equal :running state "the operation is still running")
               (ok (>= took 0.19)
                   "the wait really blocked until the producer emitted (took ~,3Fs)" took)
               (ok (< took 5)
                   "the wait woke on the broadcast, not on its 10s deadline (took ~,3Fs)" took))))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-wait-that-times-out-leaves-the-operation-running    :2738-2739, :5980
;;; ------------------------------------------------------------------

(deftest "a-wait-that-times-out-leaves-the-operation-running" "docs/SPEC-WORK.md:2738-2739,5980"
    "expected=OPERATION-NOTE-waiting;cursor-unchanged;the-operation-is-still-running"
  (let* ((path (test-journal-path "operation-wait-timeout"))
         (registry (wait-test-registry path)))
    (unwind-protect
         (let ((start (get-internal-real-time)))
           (multiple-value-bind (rows state cursor line code)
               (durable-operation-wait registry "op-a" :timeout "200ms" :after 0)
             (let ((took (elapsed-seconds start)))
               (check-equal '() rows "a wait that times out answers no row")
               (check-equal :timeout state "the wait reports its timeout")
               (check-equal 0 cursor "the cursor it reached is the one it started from")
               (check-string= "OPERATION NOTE waiting id=op-a timeout=200ms after=0" line
                              "the note is the line the output grammar fixes")
               (check-equal 0 code "a timeout is not a refusal")
               (ok (>= took 0.19) "the wait blocked for its whole bound (took ~,3Fs)" took))))
           ;; A wait that times out leaves the operation running.
           (check-equal :running
                        (getf (cdr (assoc "op-a" (operation-registry-operations registry)
                                          :test #'equal))
                              :state)
                        "the operation is still running after the wait timed out")
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-completed-result-is-retrievable-by-its-id-afterwards  :2739-2740
;;; ------------------------------------------------------------------

(deftest "a-completed-result-is-retrievable-by-its-id-afterwards" "docs/SPEC-WORK.md:2739-2740"
    "expected=a-wait-after-the-settle-answers-at-once;state-done;the-result-is-still-there"
  (let* ((path (test-journal-path "operation-wait-done"))
         (registry (wait-test-registry path)))
    (unwind-protect
         (progn
           (operation-settle registry "op-a" :state :done
                                             :result (list :emitted 128)
                                             :stamp "2026-09-19T12:00:02Z")
           (let ((start (get-internal-real-time)))
             (multiple-value-bind (rows state cursor line code)
                 (durable-operation-wait registry "op-a" :timeout "10s" :after 0)
               (let ((took (elapsed-seconds start)))
                 (check-equal nil line "a settled operation is answered, not refused")
                 (check-equal 0 code "it exits 0")
                 (check-equal :done state "the wait answers the final disposition")
                 (check-equal 1 cursor "the settle appended its own event")
                 (check-equal 1 (length rows) "and the wait answers it")
                 (ok (< took 2) "a settled operation answers at once, never after its timeout (took ~,3Fs)" took))))
           (check-equal (list :emitted 128) (registry-operation-result registry "op-a")
                        "the completed result is retrievable by its id afterwards"))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-resumed-wait-does-not-read-the-events-again         :2735-2737
;;; ------------------------------------------------------------------

(deftest "a-resumed-wait-does-not-read-the-events-again" "docs/SPEC-WORK.md:2735-2737"
    "expected=after-is-a-cursor-not-a-restart;the-page-is-capped;OPERATION-ROW-matches-the-grammar"
  (let* ((path (test-journal-path "operation-wait-cursor"))
         (registry (wait-test-registry path)))
    (unwind-protect
         (progn
           (dolist (n '(1 2 3))
             (operation-emit-event registry "op-a" :kind :staged
                                   :stamp (format nil "2026-09-19T12:00:0~DZ" n)))
           ;; Bounded and capped like every other listing.
           (multiple-value-bind (rows state cursor) (durable-operation-wait
                                                     registry "op-a" :timeout "10s"
                                                     :after 0 :max 2)
             (declare (ignore state))
             (check-equal 2 (length rows) "the page is capped at max")
             (check-equal '(1 2) (mapcar (lambda (r) (getf r :cursor)) rows)
                          "the page is the oldest events past the cursor")
             (check-equal 3 cursor "the cursor answered is the stream's, not the page's"))
           ;; A resumed wait does not read the operation's events again from zero.
           (multiple-value-bind (rows) (durable-operation-wait registry "op-a"
                                                               :timeout "10s" :after 2)
             (check-equal 1 (length rows) "a resumed wait answers only what is new")
             (check-equal 3 (getf (first rows) :cursor) "and it starts after the cursor it was given")
             (check-string= "OPERATION ROW id=op-a op=capture state=staged started=2026-09-19T12:00:00Z updated=2026-09-19T12:00:03Z external=none"
                            (operation-row-line "op-a" "capture" "2026-09-19T12:00:00Z"
                                                (first rows))
                            "one OPERATION ROW per event of a wait's cursor")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; the-event-cursor-is-bounded-by-an-explicit-limit      :2759
;;; ------------------------------------------------------------------

(deftest "the-event-cursor-is-bounded-by-an-explicit-limit" "docs/SPEC-WORK.md:2759"
    "expected=an-emit-past-the-bound-refuses;the-stream-does-not-grow;exit=2"
  (let* ((path (test-journal-path "operation-wait-bound"))
         (registry (wait-test-registry path)))
    (unwind-protect
         (let ((stream (ensure-operation-event-stream (operation-cell registry "op-a") :cap 3)))
           (dotimes (n 3)
             (multiple-value-bind (cursor line code)
                 (operation-emit-event registry "op-a" :kind :staged :stamp "s")
               (check-equal (1+ n) cursor (format nil "event ~D is appended" (1+ n)))
               (check-equal nil line "an event inside the bound is not refused")
               (check-equal 0 code "and exits 0")))
           (multiple-value-bind (cursor line code)
               (operation-emit-event registry "op-a" :kind :staged :stamp "s")
             (check-equal nil cursor "an emit past the bound answers no cursor")
             (check-equal 2 code "it refuses at exit 2")
             (ok (search "the event cursor is at its bound" line)
                 "the refusal names the bound: ~A" line))
           (check-equal 3 (operation-event-stream-count stream)
                        "the stream refuses rather than growing"))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-cancellation-wakes-a-wait                           :2740-2743
;;; ------------------------------------------------------------------

(deftest "a-cancellation-wakes-a-wait" "docs/SPEC-WORK.md:2740-2743"
    "expected=the-wait-returns-on-the-cancellation;state-cancelled;before-its-deadline"
  (let* ((path (test-journal-path "operation-wait-cancel"))
         (registry (wait-test-registry path)))
    (unwind-protect
         (let* ((start (get-internal-real-time))
                (canceller (sb-thread:make-thread
                            (lambda ()
                              (sleep 0.20)
                              (registry-operation-cancel registry "op-a"
                                                         :request "req-cancel-1"
                                                         :author "rowan"
                                                         :stamp "2026-09-19T12:00:05Z"))
                            :name "nova-work-test-canceller")))
           (multiple-value-bind (rows state cursor line code)
               (durable-operation-wait registry "op-a" :timeout "10s" :after 0)
             (declare (ignore rows cursor))
             (let ((took (elapsed-seconds start)))
               (sb-thread:join-thread canceller)
               (check-equal nil line "the wait is answered, not refused")
               (check-equal 0 code "it exits 0")
               (check-equal :cancelled state "a waiter does not block behind a durable disposition")
               (ok (>= took 0.19) "the wait blocked until the cancellation (took ~,3Fs)" took)
               (ok (< took 5) "and woke on it, not on its deadline (took ~,3Fs)" took))))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; waiting-on-an-id-no-journal-holds-is-its-own-line   :2733-2735, :5982
;;; ------------------------------------------------------------------

(deftest "waiting-on-an-id-no-journal-holds-is-its-own-line" "docs/SPEC-WORK.md:2733-2735,5982"
    "expected=OPERATION-FAIL-no-such-operation;exit=2;no-block-at-all"
  (let* ((path (test-journal-path "operation-wait-unknown"))
         (registry (wait-test-registry path)))
    (unwind-protect
         (let ((start (get-internal-real-time)))
           (multiple-value-bind (rows state cursor line code)
               (durable-operation-wait registry "op-missing" :timeout "10s" :after 0)
             (let ((took (elapsed-seconds start)))
               (check-equal '() rows "an id no journal holds answers no row")
               (check-equal nil state "and no state")
               (check-equal 0 cursor "and the cursor it was given")
               (check-string= "OPERATION FAIL id=op-missing op=- state=-: no such operation" line
                              "the refusal is the line the output grammar fixes")
               (check-equal 2 code "the refusal exits 2")
               (ok (< took 1) "and it refuses at once rather than blocking (took ~,3Fs)" took))))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-session-keeps-its-operations-beside-its-own-journal  :2729-2733
;;; ------------------------------------------------------------------

(deftest "a-session-keeps-its-operations-beside-its-own-journal" "docs/SPEC-WORK.md:2729-2733"
    "expected=the-recovery-journal-is-beside-the-session;a-restart-of-that-session-finds-its-operations"
  (let* ((root (test-session-root "session-operations"))
         (path (format nil "~A/sess" root))
         (session (session-start :owner "emma" :path path :base "abc123")))
    (check-string= (format nil "~A.operations" path)
                   (session-recovery-journal-path session)
                   "the local recovery journal is beside the session it belongs to")
    (let ((registry (open-session-operation-registry session)))
      (unwind-protect
           (progn
             (operation-accept registry :id "op-s" :kind "export" :request "req-s"
                                        :author "emma" :stamp "2026-09-19T12:00:00Z")
             (ok (probe-file (session-recovery-journal-path session))
                 "the session's recovery journal is a real file on disk"))
        (close-durable-operation-registry registry)))
    ;; The session restarts: the same path, a second registry, nothing resident.
    (let* ((restarted (session-start :owner "emma" :path path :base "abc123"))
           (registry (open-session-operation-registry restarted)))
      (unwind-protect
           (progn
             (check-equal '("op-s")
                          (mapcar (lambda (row) (getf row :id))
                                  (registry-operation-list registry :max 16))
                          "a restart of that session finds the operation it accepted")
             (multiple-value-bind (state line code) (registry-operation-state registry "op-s")
               (declare (ignore state))
               (check-equal nil line "and does not call it unknown")
               (check-equal 0 code "and answers exit 0")))
        (close-durable-operation-registry registry)))))
