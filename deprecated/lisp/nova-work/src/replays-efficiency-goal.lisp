;;;; replays-efficiency-goal.lisp --- the pure part of the efficiency lessons
;;;; (docs/SPEC-WORK.md:4861-4919), the trial-adoption gate (:4780-4786) and the
;;;; goal reference a harness switch reads (:1449-1523, replays at :5938 and
;;;; :5975).
;;;;
;;;; Card 8644's five acceptance replays call these functions directly. The
;;;; live session, CLI, dispatch and snapshot wiring a later slice owns is not
;;;; here; the boundary is the same choice the seven applicable/delegation
;;;; replays made in src/replays-applicable-delegation.lisp.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; prime: the read-only projection under --max-bytes (:4877)
;;; ------------------------------------------------------------------

(defun prime (&key goal notes leases stop-requests (max-bytes 4096))
  "Render the projection -- the current goal, applicable notes, caller leases
and pending stop requests -- and bound it under MAX-BYTES. It is a read: it
creates and maintains no shadow state, so the same inputs print the same bytes."
  (let* ((text (format nil "goal=~A~%notes=~D~%leases=~D~%stop=~D~%"
                       (or goal "-") (length notes) (length leases)
                       (length stop-requests)))
         (limit (max 0 max-bytes)))
    (subseq text 0 (min (length text) limit))))

;;; ------------------------------------------------------------------
;;; decompose --pour: unpoured checklist items stay out of |O| (:4880)
;;; ------------------------------------------------------------------

(defstruct (checklist-item
             (:constructor make-checklist-item
                 (text &key independent-verification independent-worker
                             explicit-dependency isolated-recovery)))
  text independent-verification independent-worker explicit-dependency
  isolated-recovery)

(defun materializes-node-p (item)
  "A checklist item materialises a child node in O only for independent
verification, independent worker assignment, an explicit dependency edge or an
isolated recovery boundary. Every other item stays inline."
  (or (checklist-item-independent-verification item)
      (checklist-item-independent-worker item)
      (checklist-item-explicit-dependency item)
      (checklist-item-isolated-recovery item)))

(defun checklist-open-delta (items)
  "The number of open items a decompose --pour of ITEMS adds to |O|. Unpoured
checklist items never count in |O| and never count as verified."
  (count-if #'materializes-node-p items))

;;; ------------------------------------------------------------------
;;; :max-attempts and tripped= (:4884)
;;; ------------------------------------------------------------------

(defparameter *default-max-attempts* 3
  "Node attempts are bounded by :max-attempts, default 3.")

(defun attempts-tripped-p (attempts &key (max-attempts *default-max-attempts*))
  "tripped= is a status reading: the attempts have reached the bound."
  (>= attempts max-attempts))

(defun lease-admission (attempts &key reason (max-attempts *default-max-attempts*))
  "Taking a lease on a tripped node requires an explicit --reason. The reason
explains the operator's intent and grants no execution authority by itself."
  (if (and (attempts-tripped-p attempts :max-attempts max-attempts)
           (not (present-p reason)))
      (values nil "LEASE FAIL: tripped node requires --reason" 2)
      (values t "LEASE OK" 0)))

;;; ------------------------------------------------------------------
;;; delegate mode (:4887)
;;; ------------------------------------------------------------------

(defparameter *delegate-refused-actions* '(:edit :build)
  "A delegate role profile restricts the worker to designated verbs and
read-only git operations; file edits and build execution are refused below the
model by the sandbox and tool layer.")

(defun delegate-admission (role action)
  "Admit or refuse ACTION under ROLE. A declared delegate role refuses edits and
builds at exit 2; role transitions are configuration's, not this verb's."
  (if (and (eq role :delegate) (member action *delegate-refused-actions*))
      (values nil (format nil "DELEGATE FAIL: ~A refused below the model"
                          (string-downcase (symbol-name action)))
              2)
      (values t "DELEGATE OK" 0)))

;;; ------------------------------------------------------------------
;;; promote evidence, not enthusiasm (:4780)
;;; ------------------------------------------------------------------

(defstruct (trial-result
             (:constructor make-trial-result
                 (&key prospective baseline coverage quality tolerances-matched)))
  prospective baseline coverage quality tolerances-matched)

(defun adoption-verdict (result)
  "Automatic trial-to-adopt promotion requires a preregistered prospective
trial with a selected baseline, completed coverage, passing quality and the
predeclared tolerances with referenced results. A retrospective correlation
alone, or any missing qualification, cannot auto-promote."
  (cond
    ((not (trial-result-baseline result))
     (values nil "ADOPT FAIL: missing baseline" :missing-baseline))
    ((not (trial-result-coverage result))
     (values nil "ADOPT FAIL: incomplete coverage" :missing-coverage))
    ((not (trial-result-quality result))
     (values nil "ADOPT FAIL: unmatched quality" :unmatched-quality))
    ((not (trial-result-prospective result))
     (values nil "ADOPT FAIL: retrospective correlation alone" :retrospective))
    ((not (trial-result-tolerances-matched result))
     (values nil "ADOPT FAIL: predeclared tolerances not met" :tolerances))
    (t (values t "ADOPT OK" :promote))))

;;; ------------------------------------------------------------------
;;; Gas Town accounting: root-only step records and durable triggers
;;; (:4868-4873, replay :4915)
;;; ------------------------------------------------------------------

(defstruct (root-step-record (:constructor make-root-step-record (&key node steps)))
  node steps)

(defun root-step-record (task-id steps)
  "Root-only step records cut operational row counts: one record rooted at the
task, its steps inline, and no per-step node in O."
  (make-root-step-record :node task-id :steps (length steps)))

(defun step-record-count (records)
  (length records))

(defun step-record-node-explosion (records)
  "The number of new O nodes root-only step records create: none."
  (declare (ignore records))
  0)

(defstruct (next-trigger (:constructor make-next-trigger (&key kind due)))
  kind due)

(defstruct (waiting-item (:constructor make-waiting-item (&key id trigger)))
  id trigger)

(defun pulse-reexecutions (items)
  "A durable next-trigger re-executes only the waiting item whose trigger is
due. An empty pulse -- no due trigger -- causes zero model re-executions."
  (count-if (lambda (item) (next-trigger-due (waiting-item-trigger item))) items))

;;; ------------------------------------------------------------------
;;; the goal reference (:1441-1523; replays :5938, :5975)
;;; ------------------------------------------------------------------

(defstruct (goal-store
             (:constructor %make-goal-store)
             (:copier nil))
  scope goal node-state rev notes history dedup)

(defun make-goal-store (&key (scope "C") goal (node-state :doing) (rev 0)
                             notes history dedup)
  "The resident goal index for one scope, shared by every harness of the
session. DEDUP is the journal's own request-id answer, not a second ledger."
  (%make-goal-store :scope scope :goal goal :node-state node-state :rev rev
                    :notes (or notes '())
                    :history (or history '())
                    :dedup (or dedup (make-hash-table :test #'equal))))

(defun snapshot-goal-store (store)
  "The clipped snapshot a reader with no live session loads: the same goal,
revision, state and notes at the captured revision, and a dedup table of its
own so a read can never write the live journal."
  (let ((dedup (make-hash-table :test #'equal)))
    (maphash (lambda (k v) (setf (gethash k dedup) v))
             (goal-store-dedup store))
    (%make-goal-store :scope (goal-store-scope store)
                      :goal (goal-store-goal store)
                      :node-state (goal-store-node-state store)
                      :rev (goal-store-rev store)
                      :notes (copy-list (goal-store-notes store))
                      :history (copy-list (goal-store-history store))
                      :dedup dedup)))

(defun goal-stop-state (store)
  "stop= is derived from the node's state and is no second field."
  (case (goal-store-node-state store)
    (:cancel-requested :requested)
    (:cancelled :cancelled)
    (:deferred :deferred)
    (t :none)))

(defun goal-add-note (store note)
  "A delegation note is written under its own event and moves no goal count."
  (push note (goal-store-notes store))
  note)

(defun %goal-expect-check (store expect)
  "`--expect` is required on both goal writers. Absence is exit 2 naming the
flag; a stale expectation is exit 1 with the current value printed; nothing is
written either way. Answer (values OK LINE CODE)."
  (cond
    ((null expect)
     (values nil (format nil "GOAL FAIL scope=~A goal=~A: --expect is required"
                         (goal-store-scope store)
                         (or (goal-store-goal store) "-"))
             2))
    ((/= expect (goal-store-rev store))
     (values nil (format nil "GOAL FAIL scope=~A goal=~A expect=~D current=~D: stale"
                         (goal-store-scope store)
                         (or (goal-store-goal store) "-")
                         expect (goal-store-rev store))
             1))
    (t (values t nil 0))))

(defun %goal-record (store change request)
  "The one append of a goal write. A dry run never reaches here."
  (incf (goal-store-rev store))
  (push (list :change change :rev (goal-store-rev store))
        (goal-store-history store))
  (when request
    (setf (gethash request (goal-store-dedup store))
          (list :change change :rev (goal-store-rev store))))
  (goal-store-rev store))

(defun goal-set (store &key goal clear expect as reason dry-run request)
  "`goal set` writes the scope's goal reference. It is refused when the node's
disposition is closed, whatever the expectation, and a stale --expect is
refused before anything is written."
  (declare (ignore as reason))
  (multiple-value-bind (ok line code) (%goal-expect-check store expect)
    (unless ok (return-from goal-set (values nil line code))))
  (when (member (goal-store-node-state store)
                '(:done :cancelled :superseded :removed))
    (return-from goal-set
      (values nil (format nil "GOAL FAIL scope=~A goal=~A: disposition=~A"
                          (goal-store-scope store)
                          (or (goal-store-goal store) "-")
                          (string-downcase
                           (symbol-name (goal-store-node-state store))))
              1)))
  (let ((new-goal (if clear +absent+ goal))
        (rev (goal-store-rev store)))
    (unless dry-run
      (setf (goal-store-goal store) new-goal)
      (setf rev (%goal-record store (if clear :clear :set) request)))
    (values t (format nil "GOAL OK scope=~A goal=~A change=~A kind=goal rev=~D pushed=-"
                      (goal-store-scope store)
                      (if clear "-" (or goal "-"))
                      (if clear "clear" "set")
                      rev)
            0)))

(defun goal-update (store &key expect progress evidence criterion against
                              blocked-by reason stop dry-run request as)
  "`goal update` names the current goal node and writes the existing event kind
on it: --stop a :transition to :cancel-requested, --blocked-by a :transition to
:blocked, --progress with evidence an :evidence event, --progress alone a
:transition to :doing. A stop request is not stopped-worker evidence, and no
transition is written on a node already in :cancel-requested."
  (declare (ignore criterion against as))
  (multiple-value-bind (ok line code) (%goal-expect-check store expect)
    (unless ok (return-from goal-update (values nil line code))))
  (when (eq (goal-store-node-state store) :cancel-requested)
    (return-from goal-update
      (values nil (format nil "GOAL FAIL scope=~A goal=~A: stop requested"
                          (goal-store-scope store)
                          (or (goal-store-goal store) "-"))
              1)))
  (let* ((change (cond (stop :stop)
                       (blocked-by :blocked)
                       ((and progress evidence) :evidence)
                       (progress :progress)
                       (t nil))))
    (unless change
      (return-from goal-update
        (values nil (format nil "GOAL FAIL scope=~A goal=~A: no update form"
                            (goal-store-scope store)
                            (or (goal-store-goal store) "-"))
                2)))
    (when (and (eq change :progress)
               (eq (goal-store-node-state store) :doing))
      (return-from goal-update
        (values nil (format nil "GOAL FAIL scope=~A goal=~A: no edge"
                            (goal-store-scope store)
                            (or (goal-store-goal store) "-"))
                1)))
    (let ((new-state (case change
                       (:stop :cancel-requested)
                       (:blocked :blocked)
                       (:progress :doing)
                       (:evidence (goal-store-node-state store))))
          (rev (goal-store-rev store)))
      (unless dry-run
        (setf (goal-store-node-state store) new-state)
        (setf rev (%goal-record store change request)))
      (values t (format nil "GOAL OK scope=~A goal=~A change=~A kind=~A rev=~D pushed=-"
                        (goal-store-scope store)
                        (or (goal-store-goal store) "-")
                        (string-downcase (symbol-name change))
                        (if (eq change :evidence) "evidence" "transition")
                        rev)
              0))))

(defun goal-cancel (store &key evidence)
  "Confirmed cancellation is the one evidence-bearing operation and is admitted
from :cancel-requested alone."
  (unless (present-p evidence)
    (return-from goal-cancel
      (values nil "GOAL FAIL: cancel requires --evidence" 2)))
  (unless (eq (goal-store-node-state store) :cancel-requested)
    (return-from goal-cancel
      (values nil (format nil "GOAL FAIL scope=~A goal=~A: no edge"
                          (goal-store-scope store)
                          (or (goal-store-goal store) "-"))
              1)))
  (setf (goal-store-node-state store) :cancelled)
  (let ((rev (%goal-record store :cancelled nil)))
    (values t (format nil "GOAL OK scope=~A goal=~A change=cancelled kind=cancel rev=~D"
                      (goal-store-scope store)
                      (or (goal-store-goal store) "-")
                      rev)
            0)))

(defun goal-show (store &key as expect)
  "`goal show` answers at the revision it prints, with the goal, the derived
stop=, and the constraint rows `applicable` would carry before any capped row.
It takes no --expect."
  (when expect
    (return-from goal-show
      (values nil (format nil "GOAL FAIL scope=~A: show does not take --expect"
                          (goal-store-scope store))
              2)))
  (let* ((notes (remove-if-not (lambda (n) (note-scope-covers-p n as nil))
                               (goal-store-notes store)))
         (constraints (remove-if-not (lambda (n) (constraint-p (getf n :constraint)))
                                     notes))
         (rows (loop for n in notes
                     when (constraint-p (getf n :constraint))
                       collect (format nil "constraint ~A deny=~S"
                                       (getf n :id)
                                       (constraint-deny (getf n :constraint))))))
    (values (list :scope (goal-store-scope store)
                  :goal (goal-store-goal store)
                  :rev (goal-store-rev store)
                  :state (goal-store-node-state store)
                  :stop (goal-stop-state store)
                  :constraints (length constraints)
                  :notes rows)
            (format nil "GOAL OK scope=~A goal=~A rev=~D state=~A stop=~A constraints=~D notes=~D"
                    (goal-store-scope store)
                    (or (goal-store-goal store) "-")
                    (goal-store-rev store)
                    (string-downcase (symbol-name (goal-store-node-state store)))
                    (string-downcase (symbol-name (goal-stop-state store)))
                    (length constraints) (length notes))
            0)))
