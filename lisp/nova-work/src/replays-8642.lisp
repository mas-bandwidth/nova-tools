;;;; replays-8642.lisp --- pure records and functions for the five named
;;;; acceptance replays of nova-tools #362, each naming its paragraph of
;;;; docs/SPEC-WORK.md:
;;;;
;;;;   bounds-are-not-prompts               4849  launcher hard limits
;;;;   batch-with-bounds-and-urgency        4853  batch coalescing
;;;;   async-operations                     6239  operation launch/cancel
;;;;   batches-and-pipelines                6240  atomic and independent batches
;;;;   cache-aware-context-choice           4854  priced context policy
;;;;
;;;; This is the internal C/O kernel slice: there is no session, CLI, socket,
;;;; provider or launcher here. Each replay's pure shape is implemented and
;;;; exercised directly; the live wiring owed to those later slices is named
;;;; in RESULT.md, one line per replay. Nothing here invents a new protocol:
;;;; the batch functions are the existing read-bundle / independent-batch /
;;;; atomic-batch semantics stated as pure data.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; bounds-are-not-prompts                    SPEC-WORK.md:4849
;;; ------------------------------------------------------------------
;;; A launcher lacking a required hard limit refuses automatic dispatch; a
;;; supported deadline returns a terminal or unresolved handle without
;;; duplicate execution.

(defstruct launcher
  "What hard limits a launcher records support for. Every slot defaults NIL, so
an unconfigured launcher supports nothing and a required limit it lacks is named
rather than silently treated as an enforced one."
  input-bound output-bound deadline attempt-limit)

(defun limit-support (launcher kind)
  (ecase kind
    (:input-bound   (launcher-input-bound launcher))
    (:output-bound  (launcher-output-bound launcher))
    (:deadline      (launcher-deadline launcher))
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
;;; batch-with-bounds-and-urgency             SPEC-WORK.md:4853
;;; ------------------------------------------------------------------
;;; Independent results coalesce within byte, record and delay bounds;
;;; unchanged batches cause no call; unreferenced padding refuses; urgent
;;; corrections bypass delay; dependencies and partial retry identities
;;; survive.

(defstruct batch-config
  "CONFIG's per-batch bounds: bytes, records and a maximum delay in
milliseconds."
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
  "Kinds that bypass the maximum delay bound.")

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
;;; async-operations                          SPEC-WORK.md:6239
;;; ------------------------------------------------------------------
;;; Status, wait and cancel under a busy import, export and clip: no double
;;; launch and no false cancellation success. The bounded queues, the
;;; restart with operations pending and the uncertain external effect are owed
;;; to the session slice; the launch/cancel dispositions are pure here.

(defun operation-launch (op)
  "Pure. A pending operation enters :running and counts one attempt. Launching
a :running, :done or :cancelled operation launches nothing (no double launch)."
  (if (eq (getf op :state) :pending)
      (list :id (getf op :id) :kind (getf op :kind) :state :running
            :attempts (1+ (getf op :attempts 0)))
      op))

(defun async-operation-cancel (op)
  "Pure. Cancelling a finished operation is never a false success: it reports
:already-complete and no cancellation. Something not finished cancels to
:cancelled with disposition :cancelled."
  (if (eq (getf op :state) :done)
      (list :id (getf op :id) :kind (getf op :kind) :state :done
            :attempts (getf op :attempts 0) :disposition :already-complete)
      (list :id (getf op :id) :kind (getf op :kind) :state :cancelled
            :attempts (getf op :attempts 0) :disposition :cancelled)))

;;; ------------------------------------------------------------------
;;; batches-and-pipelines                     SPEC-WORK.md:6240
;;; ------------------------------------------------------------------
;;; An atomic batch publishes all or none; an independent batch preserves its
;;; exact accepted prefix and marks the remainder not attempted. Same-id retry
;;; and changed-payload refusal, oversize refusal and control-plane bounds are
;;; owed to the session/dispatch slice; both batch dispositions are pure here.

(defun apply-atomic-batch (acc entries validator)
  "Pure, all-or-none. If every entry satisfies VALIDATOR, answer
(values acc+ids :applied). If any entry fails, nothing is applied:
(values acc :refused)."
  (if (every (lambda (e) (funcall validator e)) entries)
      (values (append acc (mapcar (lambda (e) (getf e :id)) entries)) :applied)
      (values acc :refused)))

(defun apply-independent-batch (acc entries validator)
  "Pure. Stop at the first refusal: the exact accepted prefix is applied and the
remaining entries are marked :not-attempted; no entry after the refusal is
applied. Answers (values new-acc accepted-ids not-attempted-ids)."
  (let ((accepted '()) (not-attempted '()) (new-acc acc) (after-failure nil))
    (dolist (e entries)
      (let ((id (getf e :id)))
        (cond (after-failure (push id not-attempted))
              ((funcall validator e) (push id accepted)
               (setf new-acc (append new-acc (list id))))
              (t (setf after-failure t)))))
    (values new-acc (reverse accepted) (reverse not-attempted))))

;;; ------------------------------------------------------------------
;;; cache-aware-context-choice                SPEC-WORK.md:4854
;;; ------------------------------------------------------------------
;;; Cache reads and writes and tier thresholds price separately; a reset
;;; includes its rebuild cost and refuses a missing decision, adapter or
;;; evidence; a lower hit rate can still win when total matched-work cost
;;; falls. Hit rate is reported, never the decision.

(defstruct cache-price
  "Separate rates for fresh input, cache reads and cache writes, so a read and
a write are never one charge."
  input-rate cache-read-rate cache-write-rate)

(defun cache-cost (price read-tokens write-tokens)
  "The priced cost of cache READS and WRITES, each at its own rate."
  (+ (* read-tokens (cache-price-cache-read-rate price))
     (* write-tokens (cache-price-cache-write-rate price))))

(defun tiered-input-cost (tokens threshold under-rate over-rate)
  "Price tokens below THRESHOLD at UNDER-RATE and those at or above it at
OVER-RATE, so a long-context threshold prices separately."
  (let ((over (max 0 (- tokens threshold))))
    (+ (* (- tokens over) under-rate)
       (* over over-rate))))

(defstruct context-plan
  "One context policy's matched-work shape. HIT-RATE is reported, never the
decision. RETENTION-TOKENS is the per-call cached volume read; NEW-PREFIX-TOKENS
is the fresh input a refresh writes; REBUILD-TOKENS is the checkpoint and
new-prefix work a reset charges on top."
  hit-rate retention-tokens new-prefix-tokens rebuild-tokens)

(defun plan-cost (plan price)
  "The total matched-work cost of a plan under PRICE: cache reads and writes
priced separately, and a reset's rebuild charged as cache writes. A reset is
never inferred to save money without including its rebuild."
  (+ (cache-cost price (context-plan-retention-tokens plan) 0)
     (cache-cost price 0 (context-plan-new-prefix-tokens plan))
     (cache-cost price 0 (context-plan-rebuild-tokens plan))))

(defstruct refresh-admission
  "The revision-bound decision artifact, the adapter capability reference and
the applicable trial/adoption evidence an automatic refresh requires."
  decision adapter evidence)

(defun refresh-refusal (admission)
  "NIL when an automatic refresh may proceed; else the names of the missing
inputs, in :decision :adapter :evidence order."
  (remove nil
          (list (unless (refresh-admission-decision admission) :decision)
                (unless (refresh-admission-adapter admission) :adapter)
                (unless (refresh-admission-evidence admission) :evidence))))

(defun refresh-allowed-p (admission)
  (null (refresh-refusal admission)))

(defun choose-context-plan (retained refreshed price)
  "Choose the plan with the lower total matched-work cost; ties keep RETAINED.
Hit rate never decides: a lower-hit refresh wins when its total cost is lower."
  (if (<= (plan-cost retained price) (plan-cost refreshed price))
      retained
      refreshed))
