;;;; replays-8645.lisp --- the pure part of the five acceptance replays of
;;;; nova-tools #362.
;;;;
;;;; docs/SPEC-WORK.md:1441-1528 makes the goal a scope-keyed reference and
;;;; `goal update` a thin verb writing the node's own `:transition` or
;;;; `:evidence` event; :5948-5974 fixes what the five `goal-` replays promise;
;;;; :5782 fixes the historic tick; :6248 fixes the hostile-data intake. Nothing
;;;; here starts a session, a socket or a CLI: this is the pure planning,
;;;; transition and intake layer the replays call, and the wiring a live
;;;; session owns is named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The goal world: nodes, the scope-keyed goal reference, the event log
;;; ------------------------------------------------------------------

(defstruct (goal-world (:constructor %make-goal-world))
  (rev 0 :type integer)
  nodes    ; id -> (:state <s> :branch <b>)
  goals    ; (scope . node-id), one goal reference per scope
  (events '())  ; newest first, each (:kind :node :fields :rev :request)
  (seen '())    ; request-id -> event rev, the journal's dedup answer
  (scope-rev 0))

(defun make-goal-world (&key (rev 0) nodes goals scope-rev)
  "A goal world at REV. NODES is an alist of (id . (:state :branch))."
  (%make-goal-world
   :rev rev
   :scope-rev (or scope-rev rev)
   :nodes (loop for (id . plist) in nodes
                collect (cons id (copy-list plist)))
   :goals (copy-list goals)))

(defun gw-node (world id)
  (cdr (assoc id (goal-world-nodes world) :test #'equal)))

(defun gw-state (world id)
  (getf (gw-node world id) :state))

(defun (setf gw-state) (value world id)
  (setf (getf (cdr (assoc id (goal-world-nodes world) :test #'equal)) :state) value))

(defun gw-branch (world id)
  (getf (gw-node world id) :branch :o))

(defun gw-goal (world scope)
  (cdr (assoc scope (goal-world-goals world) :test #'equal)))

(defun last-event (world)
  (first (goal-world-events world)))

(defun %goal-event (world kind node fields &key request)
  "Append one event and answer its revision. A request id is remembered with the
revision it produced, so a retry can replay the recorded answer."
  (let ((rev (incf (goal-world-rev world))))
    (push (list :kind kind :node node :fields fields :rev rev :request request)
          (goal-world-events world))
    (when request
      (push (cons request rev) (goal-world-seen world)))
    rev))

(defun event-fields-keys (event)
  (loop for (k v) on (getf event :fields) by #'cddr collect k))

(defun own-fields-ok-p (event)
  "An event of KIND must carry exactly its kind's ordered field list
(docs/SPEC-WORK.md:330-339)."
  (equal (event-fields-keys event) (kind-fields (getf event :kind))))

(defun stop-field (state)
  "stop= is derived from the state and no second field (docs/SPEC-WORK.md:1460)."
  (case state
    (:cancel-requested "requested")
    (:cancelled "cancelled")
    (:deferred "deferred")
    (t "none")))

(defun goal-world-show (world &key scope)
  "The GOAL OK fields a newly selected harness loads. Nothing here is the
conversation it came from."
  (let* ((goal (gw-goal world scope))
         (state (and goal (gw-state world goal))))
    (list :scope scope :goal goal :rev (goal-world-rev world)
          :state state :stop (if state (stop-field state) "none"))))

(defun goal-check (world)
  "no finding: a stop request is a legal transition and nothing more."
  (declare (ignore world))
  '())

;;; ------------------------------------------------------------------
;;; goal update and goal set
;;; ------------------------------------------------------------------

(defparameter *stop-edges* '(:todo :doing :blocked :unknown)
  "The table's own edge to :cancel-requested; :review and :done have none.")

(defparameter *progress-edges* '(:todo :blocked :review :unknown)
  "The progress-only edge to :doing, refused where the table refuses it.")

(defun %stale-line (scope goal expect current)
  (format nil "GOAL FAIL scope=~A goal=~A expect=~D current=~D: stale"
          scope goal expect current))

(defun goal-world-update (world &key scope expect (form :progress) reason
                          progress pointer criterion against request)
  "One `goal update` form, on the current goal node of SCOPE and no other node.
Answer (values OK LINE CODE). A stale --expect refuses at exit 1 and writes
nothing; a node in :cancel-requested refuses every update but a person's own
`state --to doing` `stop requested`."
  (when (null expect)
    (return-from goal-world-update (values nil "GOAL FAIL: --expect is required" 2)))
  (when (/= expect (goal-world-rev world))
    (return-from goal-world-update
      (values nil (%stale-line scope (gw-goal world scope) expect
                               (goal-world-rev world))
              1)))
  (let ((goal (gw-goal world scope)))
    (unless goal
      (return-from goal-world-update (values nil "GOAL FAIL: no goal in scope" 1)))
    (let ((state (gw-state world goal)))
      (ecase form
        (:stop
         (unless (member state *stop-edges*)
           (return-from goal-world-update (values nil "GOAL FAIL: no edge" 1)))
         ;; A stop request is a :transition carrying :reason and no :evidence.
         (let ((rev (%goal-event world :transition goal
                                 (list :to :cancel-requested
                                       :reason reason
                                       :blocked-by +absent+
                                       :evidence +absent+)
                                 :request request)))
           (setf (gw-state world goal) :cancel-requested)
           (values t
                   (format nil "GOAL OK scope=~A goal=~A change=stop kind=transition rev=~D"
                           scope goal rev)
                   0)))
        (:progress
         (when (eq state :cancel-requested)
           (return-from goal-world-update
             (values nil "GOAL FAIL: stop requested" 1)))
         (cond
          ;; --progress with the evidence triple writes the :evidence event
          ;; `evidence` writes, on the goal node.
          ((and pointer criterion against)
           (let ((rev (%goal-event world :evidence goal
                                   (list :pointer pointer
                                         :criterion criterion
                                         :against against
                                         :generation 1
                                         :attempt +absent+)
                                   :request request)))
             (values t
                     (format nil "GOAL OK scope=~A goal=~A change=evidence kind=evidence rev=~D"
                             scope goal rev)
                     0)))
          ;; A progress line on a node already :doing carries evidence or it is
          ;; not written.
          ((eq state :doing)
           (values nil "GOAL FAIL: no edge" 1))
          ((member state *progress-edges*)
           (let ((rev (%goal-event world :transition goal
                                   (list :to :doing
                                         :reason progress
                                         :blocked-by +absent+
                                         :evidence +absent+)
                                   :request request)))
             (setf (gw-state world goal) :doing)
             (values t
                     (format nil "GOAL OK scope=~A goal=~A change=progress kind=transition rev=~D"
                             scope goal rev)
                     0)))
          (t (values nil "GOAL FAIL: no edge" 1))))))))

(defun goal-world-set (world &key scope goal expect request reason clear)
  "`goal set` writes the one :goal event of the scope, whose :node is (:absent).
The disposition refusal is unconditional; a retried request replays its event."
  (let ((prior (assoc request (goal-world-seen world) :test #'equal)))
    (when prior
      (return-from goal-world-set
        (values t
                (format nil "GOAL OK scope=~A goal=~A change=~A kind=goal rev=~D"
                        scope (if clear "-" goal) (if clear "clear" "set")
                        (cdr prior))
                0))))
  (when (and (not clear) goal)
    (let ((state (gw-state world goal)))
      (cond ((null state)
             (return-from goal-world-set (values nil "GOAL FAIL: no such node" 1)))
            ((eq state :done)
             (return-from goal-world-set (values nil "GOAL FAIL: disposition=done" 1)))
            ((eq state :cancelled)
             (return-from goal-world-set (values nil "GOAL FAIL: disposition=cancelled" 1))))))
  (when (or (null expect) (/= expect (goal-world-rev world)))
    (return-from goal-world-set
      (if (null expect)
          (values nil "GOAL FAIL: --expect is required" 2)
          (values nil (%stale-line scope goal expect (goal-world-rev world)) 1))))
  (let ((rev (%goal-event world :goal +absent+
                          (list :change (if clear :clear :set)
                                :scope scope
                                :goal (if clear +absent+ goal)
                                :reason reason)
                          :request request)))
    (if clear
        (setf (goal-world-goals world)
              (remove scope (goal-world-goals world) :key #'car :test #'equal))
        (let ((cell (assoc scope (goal-world-goals world) :test #'equal)))
          (if cell
              (setf (cdr cell) goal)
              (push (cons scope goal) (goal-world-goals world)))))
    (values t
            (format nil "GOAL OK scope=~A goal=~A change=~A kind=goal rev=~D"
                    scope (if clear "-" goal) (if clear "clear" "set") rev)
            0)))

;;; ------------------------------------------------------------------
;;; The sibling verbs the goal replays compare against
;;; ------------------------------------------------------------------

(defun gw-state-to (world id &key to reason)
  "`state --to <to> --reason`, by id. The same :transition field list the goal
verb writes, so the withdrawal to :doing is a person's own act."
  (%goal-event world :transition id
               (list :to to :reason reason
                     :blocked-by +absent+ :evidence +absent+))
  (setf (gw-state world id) to)
  (last-event world))

(defun gw-cancel (world id &key evidence)
  "`event --kind cancel --evidence <pointer>`, admitted only from
:cancel-requested: the one evidence-bearing cancellation."
  (unless (eq (gw-state world id) :cancel-requested)
    (return-from gw-cancel (values nil "cancel refused: no edge" 1)))
  (unless (and (listp evidence) evidence)
    (return-from gw-cancel (values nil "cancel refused: no evidence" 1)))
  (%goal-event world :cancel id (list :evidence evidence))
  (setf (gw-state world id) :cancelled)
  (values t "EVENT OK kind=cancel" 0))

(defun gw-accept-add (world id)
  "`accept --add` on the node: it moves the scope revision; `goal update` never does."
  (declare (ignore id))
  (incf (goal-world-scope-rev world)))

;;; ------------------------------------------------------------------
;;; Historic delivery and current verification are two questions
;;; (docs/SPEC-WORK.md:4943-4951, :5782)
;;; ------------------------------------------------------------------

(defstruct (tick-record (:constructor make-tick-record
                            (&key id pinned-rev source-sha scope historic-tick
                                  current-verification)))
  id pinned-rev source-sha scope
  (historic-tick t)
  (current-verification :verified))

(defun source-change (tick &key new-source-sha new-criterion)
  "A changed source or criterion keeps the historic tick at its pinned revision
while the current view requires re-verification. Unrelated receipts are untouched
because this answers for one receipt only."
  (let* ((changed (or (and new-source-sha
                           (not (equal new-source-sha (tick-record-source-sha tick))))
                      new-criterion)))
    (make-tick-record
     :id (tick-record-id tick)
     :pinned-rev (tick-record-pinned-rev tick)
     :source-sha (tick-record-source-sha tick)
     :scope (tick-record-scope tick)
     :historic-tick (tick-record-historic-tick tick)
     :current-verification (if changed
                               :recheck-needed
                               (tick-record-current-verification tick)))))

;;; ------------------------------------------------------------------
;;; Hostile data (docs/SPEC-WORK.md:6248)
;;; ------------------------------------------------------------------

(defstruct (intake-limits (:constructor make-intake-limits
                              (&key (max-depth 64) (max-bytes 65536) (max-nodes 4096))))
  (max-depth 64) (max-bytes 65536) (max-nodes 4096))

(defvar *intake-visits* 0
  "Bytes examined by intake-scan. A deep or high-fan-out input must grow this
linearly in its own length, never quadratically.")

(defun intake-scan (text limits)
  "One linear pre-parse pass, counting peak nesting depth and atom nodes.
It never calls EVAL or READ, so reader evaluation is disabled by construction."
  (declare (ignore limits))
  (let ((depth 0) (peak 0) (nodes 0) (in-token nil))
    (loop for ch across text
          do (incf *intake-visits*)
             (cond ((char= ch #\() (incf depth) (setf peak (max peak depth))
                                  (setf in-token nil))
                   ((char= ch #\)) (when (plusp depth) (decf depth))
                                  (setf in-token nil))
                   ((find ch " \t\r\n") (setf in-token nil))
                   (t (unless in-token (incf nodes) (setf in-token t)))))
    (values peak nodes)))

(defun hostile-intake (text &key (limits (make-intake-limits)))
  "Intake of untrusted text. Answer (values OK LINE WHY). A read-time eval form
is refused before any parse; the byte, depth and node limits refuse the input
whole rather than truncating it."
  (when (search "#." text)
    (return-from hostile-intake
      (values nil "INTAKE FAIL: reader evaluation disabled" :eval)))
  (when (> (length text) (intake-limits-max-bytes limits))
    (return-from hostile-intake
      (values nil "INTAKE FAIL: byte limit exceeded" :bytes)))
  (multiple-value-bind (depth nodes) (intake-scan text limits)
    (when (> depth (intake-limits-max-depth limits))
      (return-from hostile-intake
        (values nil "INTAKE FAIL: depth limit exceeded" :depth)))
    (when (> nodes (intake-limits-max-nodes limits))
      (return-from hostile-intake
        (values nil "INTAKE FAIL: node limit exceeded" :nodes))))
  (values t "INTAKE OK" nil))

(defun archive-path-safe-p (path)
  "An archive member path that stays inside the archive: no absolute path, no
traversal, no drive or home escape and no backslash escaping."
  (and (stringp path)
       (plusp (length path))
       (char/= (char path 0) #\/)
       (char/= (char path 0) #\~)
       (null (find #\\ path))
       (null (search ".." path))))

(defun imported-prose-effect (prose)
  "Imported prose is data: it can neither run a command nor alter authority."
  (declare (ignore prose))
  (list :command nil :authority nil))
