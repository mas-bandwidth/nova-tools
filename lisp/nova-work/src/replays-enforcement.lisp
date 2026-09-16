;;;; replays-enforcement.lisp --- pure enforcement logic for the efficiency and
;;;; delegation replays of docs/SPEC-WORK.md's "Required enforcement replays".
;;;;
;;;; This file holds the pure functions and records the acceptance replays call.
;;;; It owns no state and no I/O. Where a replay names behaviour that belongs in
;;;; the resident kernel, session or state slices, the pure shape is proven here
;;;; and the wiring owed to those slices is listed in RESULT.md, one line each.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; bounds-are-not-prompts                     SPEC-WORK.md:4702
;;; ------------------------------------------------------------------

(defstruct launcher
  "What real bounds a launcher records support for. Every slot defaults NIL, so
an unconfigured launcher supports nothing and a required limit it lacks is named
rather than silently treated as an enforced one."
  input-bound output-bound deadline attempt-limit)

(defparameter *limit-kinds* '(:input-bound :output-bound :deadline :attempt-limit)
  "The four hard limits a launcher may support. A limit is a configured hard
bound, not a word in an instruction.")

(defun limit-support (launcher kind)
  (ecase kind
    (:input-bound  (launcher-input-bound launcher))
    (:output-bound (launcher-output-bound launcher))
    (:deadline     (launcher-deadline launcher))
    (:attempt-limit (launcher-attempt-limit launcher))))

(defun missing-hard-limits (launcher required)
  "The kinds in REQUIRED the launcher does not support. An empty list means the
launcher can enforce every required hard limit."
  (remove-if (lambda (kind) (limit-support launcher kind)) required))

(defun auto-dispatch-allowed-p (launcher required)
  "Automatic dispatch is allowed only when no required hard limit lacks launcher
support. A missing limit is a refusal, not a prompt the caller may ignore."
  (null (missing-hard-limits launcher required)))

(defun deadline-handle (deadline-supported-p passed-p)
  "A supported deadline yields :terminal once it has passed, else an :unresolved
handle. An unsupported deadline stays :unresolved: the deadline is a hard limit
that asserts nothing about termination when the adapter cannot stop a worker."
  (if deadline-supported-p
      (if passed-p :terminal :unresolved)
      :unresolved))

(defun admit-execution (request-id active)
  "Answer (values handle started-p). ACTIVE is an alist of request-id ->
handle-status. The handle is the request id; started-p is T only when the id is
not already present, so an unresolved handle is rejoined and never re-executed."
  (if (assoc request-id active :test #'equal)
      (values request-id nil)
      (values request-id t)))

;;; ------------------------------------------------------------------
;;; batch-with-bounds-and-urgency              SPEC-WORK.md:4706
;;; ------------------------------------------------------------------

(defstruct batch-config
  "CONFIG's per-batch bounds: bytes, records and a maximum delay in milliseconds."
  max-bytes max-records max-delay-ms)

(defstruct packet-fragment
  "One fragment the packet builder may admit. KIND is one of :normal,
:urgent-correction, :stop-request, :lease-change or :deadline. DEPENDENCIES and
RETRY-OF are preserved through coalescing."
  id bytes kind dependencies retry-of)

(defstruct packet
  "A manifest (alist of fragment id -> (bytes digest)) and the fragments it
references. The validator admits only referenced fragments."
  manifest fragments)

(defparameter *urgent-kinds*
  '(:urgent-correction :stop-request :lease-change :deadline)
  "Kinds that bypass the maximum delay bound (rule 4 of the efficiency policy).")

(defun total-bytes (fragments)
  (reduce #'+ fragments :key #'packet-fragment-bytes :initial-value 0))

(defun exhausted-bound (fragments config)
  "The bound names the batch has exceeded: :bytes, :records, or both."
  (let ((out '()))
    (when (> (total-bytes fragments) (batch-config-max-bytes config))
      (push :bytes out))
    (when (> (length fragments) (batch-config-max-records config))
      (push :records out))
    (nreverse out)))

(defun within-bounds-p (fragments config)
  (null (exhausted-bound fragments config)))

(defun fragment-form (fragment)
  (list (packet-fragment-id fragment)
        (packet-fragment-bytes fragment)
        (packet-fragment-kind fragment)
        (packet-fragment-dependencies fragment)
        (packet-fragment-retry-of fragment)))

(defun batch-unchanged-p (before after)
  "Two batches are unchanged when every fragment is structurally identical, so
an empty or unchanged batch triggers zero model calls."
  (equal (mapcar #'fragment-form before)
         (mapcar #'fragment-form after)))

(defun fragment-referenced-p (fragment manifest)
  (assoc (packet-fragment-id fragment) manifest :test #'equal))

(defun validate-packet (packet config)
  "NIL when the packet is admissible, else a list of problems: unreferenced
padding and over-bound fragments. It cannot judge semantic necessity, only that
every included fragment is referenced and inside the bounds."
  (let ((problems '()))
    (dolist (f (packet-fragments packet))
      (unless (fragment-referenced-p f (packet-manifest packet))
        (push (list :unreferenced (packet-fragment-id f)) problems)))
    (let ((over (exhausted-bound (packet-fragments packet) config)))
      (when over (push (list :over-bound over) problems)))
    (nreverse problems)))

(defun urgent-p (fragment)
  (member (packet-fragment-kind fragment) *urgent-kinds*))

(defun delay-applies-p (fragment)
  "The maximum delay bound holds only non-urgent fragments."
  (not (urgent-p fragment)))

(defun dependencies-of (fragments)
  (loop for f in fragments
        collect (list (packet-fragment-id f) (packet-fragment-dependencies f))))

(defun retry-identity-of (fragments)
  (loop for f in fragments
        collect (list (packet-fragment-id f) (packet-fragment-retry-of f))))

;;; ------------------------------------------------------------------
;;; evidence-before-adoption                  SPEC-WORK.md:4708
;;; ------------------------------------------------------------------

(defstruct adoption-claim
  "The evidence a trial presents for automatic adoption. PROSPECTIVE-P means the
trial was preregistered; RETROSPECTIVE-ONLY-P marks a historical comparison that
cannot be relabelled."
  baseline coverage-complete quality-match prospective retrospective-only)

(defun auto-promotion-supported-p (claim)
  "Automatic trial-to-adopt promotion requires a baseline, complete coverage,
matching quality and a prospective result. A retrospective correlation alone is
never enough, whatever else it satisfies."
  (and (adoption-claim-baseline claim)
       (adoption-claim-coverage-complete claim)
       (adoption-claim-quality-match claim)
       (adoption-claim-prospective claim)
       (not (adoption-claim-retrospective-only claim))))

;;; ------------------------------------------------------------------
;;; gas-town-efficiency-accounting            SPEC-WORK.md:4768
;;; ------------------------------------------------------------------

(defstruct step-record
  "A step emission. DEPTH 0 is the root-only record; a fine-grained emitter adds
one record per child (depth 1), which is the node explosion the root-only shape
avoids."
  node depth)

(defun root-only-record-count (records)
  (count-if (lambda (r) (zerop (step-record-depth r))) records))

(defun fine-grained-record-count (records)
  (length records))

(defparameter *pour-reasons*
  '(:independent-verification :independent-worker :dependency-edge :recovery-boundary)
  "The only decompose --pour reasons that materialise a child node in O.
An inline checklist (no reason) materialises nothing and counts nothing in |O|.")

(defun materialized-child-count (subtasks)
  (count-if (lambda (s) (member (getf s :reason) *pour-reasons*)) subtasks))

(defstruct waiting-item
  "A waiting item with its durable next-trigger: :owner-delivery, :job-handle,
:review-completion or :due-checkpoint. NIL means the item is polled instead."
  id next-trigger)

(defun durable-next-trigger-p (item)
  (member (waiting-item-next-trigger item)
          '(:owner-delivery :job-handle :review-completion :due-checkpoint)))

(defun model-reexecutions (items fired-triggers)
  "How many re-executions a pulse causes. With a durable next-trigger on every
waiting item, only fired triggers re-execute (an empty pulse reruns nothing).
Without one, an empty pulse re-runs every waiting item, the poll bottleneck."
  (if (every #'durable-next-trigger-p items)
      (length fired-triggers)
      (length items)))

;;; ------------------------------------------------------------------
;;; efficiency-lessons-gate                   SPEC-WORK.md:4769
;;; ------------------------------------------------------------------

(defun projection-within-bytes-p (bytes max-bytes)
  "prime is a read-only projection bounded under --max-bytes."
  (<= bytes max-bytes))

(defun open-count-excluding-unpoured (items)
  "|O| counts only materialised (poured) items; an unpoured checklist item never
enters the open count."
  (count-if (lambda (it) (and (getf it :materialized) (getf it :open))) items))

(defun tripped-lease-allowed-p (tripped-p reason)
  "A tripped node refuses a lease without an explicit --reason; the reason names
intent and grants no execution authority by itself."
  (if tripped-p (and reason (plusp (length reason))) t))

(defun delegate-verb-allowed-p (mode verb edit-p)
  "Delegate mode restricts the worker to designated verbs and read-only
operations; edits below the model are refused by the sandbox and tool layer."
  (declare (ignore verb))
  (if (eq mode :delegate) (not edit-p) t))

(defun packet-effort-present-p (packet)
  "Every card and delegated packet carries an explicit :effort bound; a packet
lacking it is refused."
  (not (null (getf packet :effort))))

(defun spend-ceiling-refusal (dispatch-cost local-spend ceiling)
  "NIL when a dispatch stays under the daily fleet spend ceiling; a refusal names
the configured ceiling and the bench's own local-spend."
  (if (> (+ local-spend dispatch-cost) ceiling)
      (list :refused t :ceiling ceiling :local-spend local-spend)
      nil))
