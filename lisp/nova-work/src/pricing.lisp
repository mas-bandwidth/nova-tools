

;;; ------------------------------------------------------------------
;;; folded from replays-8647.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8647.lisp --- the pure kernel support for five acceptance replays
;;;; of docs/SPEC-WORK.md: the validated efficiency policy round trip
;;;; (:4699-4741, :4847), the revision-pinned price (:4636-4651, :5769), the
;;;; quiet-until-actionable pulse (:4760-4763, :4820-4832, :4850), the
;;;; read-only intake adapter (:6229) and trial regression recovery
;;;; (:4787-4790, :4856).
;;;;
;;;; Nothing here starts a session, dispatches a model or opens a network
;;;; connection: each replay drives these pure functions directly, so the
;;;; live-session and CLI wiring a later slice owns is not claimed.

(in-package #:nova-work)


;;; ------------------------------------------------------------------
;;; Validated efficiency policy (SPEC-WORK.md:4701)
;;; ------------------------------------------------------------------

(defparameter *policy-required-groups* '(:quality :routing :bounds :measurement :trial)
  "The typed groups a versioned :efficiency-policy must carry (SPEC-WORK.md:4706).")

(defun policy-present-p (v)
  (and v (not (and (stringp v) (string= "" v)))))

(defun policy-positive-integer-p (v) (and (integerp v) (plusp v)))

(defun policy-bounds-complete-p (bounds)
  "`:bounds` is four positive integer limits and a full-history boolean
(SPEC-WORK.md:4710)."
  (and (listp bounds)
       (policy-positive-integer-p (getf bounds :input-packet-bytes))
       (policy-positive-integer-p (getf bounds :report-bytes))
       (policy-positive-integer-p (getf bounds :attempt-count))
       (policy-positive-integer-p (getf bounds :execution-limit-ms))
       (member (getf bounds :full-history) '(:true :false))))

(defun efficiency-policy-complete-p (policy)
  "An invalid or incomplete policy is refused whole; the previous revision is
left intact (SPEC-WORK.md:4721)."
  (and (policy-present-p (getf policy :id))
       (policy-positive-integer-p (getf policy :schema-version))
       (policy-present-p (getf policy :revision))
       (policy-present-p (getf policy :scope))
       (policy-present-p (getf policy :author))
       (every (lambda (group) (policy-present-p (getf policy group)))
              *policy-required-groups*)
       (policy-bounds-complete-p (getf policy :bounds))))

(defun make-efficiency-policy (&key id schema-version scope author
                                    quality routing bounds measurement trial)
  "The revision is the content hash of every field, so two policies with the
same content carry the same revision and a changed one does not."
  (let ((body (list :schema-version (or schema-version 1)
                    :id id :scope scope :author author
                    :quality (or quality '())
                    :routing (or routing '())
                    :bounds bounds
                    :measurement (or measurement '())
                    :trial (or trial '()))))
    (list* :revision (sha256-hex (canonical-string body)) body)))

(defstruct (policy-store (:constructor %make-policy-store))
  policy history manifests executions rev)

(defun make-policy-store (&key policy (history '()) (manifests '()) (executions '()) (rev 0))
  (%make-policy-store :policy policy :history history :manifests manifests
                      :executions executions :rev rev))

(defun policy-store-form (store)
  (list :policy (policy-store-policy store)
        :history (policy-store-history store)
        :manifests (policy-store-manifests store)
        :executions (policy-store-executions store)
        :rev (policy-store-rev store)))

(defun policy-store-export (store)
  (canonical-string (policy-store-form store)))

(defun policy-store-import (text)
  "A load is read-only and reproduces the exported fields exactly."
  (let ((form (read-restricted text)))
    (make-policy-store :policy (getf form :policy)
                       :history (getf form :history)
                       :manifests (getf form :manifests)
                       :executions (getf form :executions)
                       :rev (getf form :rev))))

(defun policy-intake (store candidate)
  "`config --intake` owns policy admission and replacement (SPEC-WORK.md:4718).
A complete policy replaces the stored one under the existing revision guard; an
incomplete one is refused and leaves the previous revision and every other field
exactly as they were, so malformed intake has no partial effect."
  (if (efficiency-policy-complete-p candidate)
      (values
       (make-policy-store
        :policy candidate
        :history (if (policy-store-policy store)
                     (cons (policy-store-policy store) (policy-store-history store))
                     (policy-store-history store))
        :manifests (policy-store-manifests store)
        :executions (policy-store-executions store)
        :rev (1+ (policy-store-rev store)))
       t
       (format nil "POLICY OK revision=~A" (getf candidate :revision))
       0)
      (values store nil "POLICY FAIL: incomplete or malformed policy intake" 2)))

(defun policy-undo (store)
  "The prior policy is restored and the reference lists survive; an empty
history has nothing to undo."
  (let ((prior (first (policy-store-history store))))
    (if prior
        (values (make-policy-store :policy prior
                                   :history (rest (policy-store-history store))
                                   :manifests (policy-store-manifests store)
                                   :executions (policy-store-executions store)
                                   :rev (1+ (policy-store-rev store)))
                t "POLICY UNDO OK")
        (values store nil "POLICY UNDO FAIL: no previous revision"))))

(defun policy-replay (candidates)
  "Fold a revision's intake sequence from an empty store. Replay reproduces the
live policy and its intake history, minting nothing new."
  (let ((store (make-policy-store)))
    (dolist (candidate candidates store)
      (multiple-value-setq (store) (policy-intake store candidate)))))

(defun make-trial-manifest (&key hypothesis workload source-revision acceptance-revision
                                 baseline changed-variables procedure sample-stop
                                 tolerances (stage :prospective))
  "An experiment manifest pins its hypothesis, workload, revisions, baseline,
changed variables, procedure, sample/stop rule and tolerances before a
prospective trial (SPEC-WORK.md:4725); its id is its immutable content."
  (let ((body (list :stage stage :hypothesis hypothesis :workload workload
                    :source-revision source-revision
                    :acceptance-revision acceptance-revision
                    :baseline baseline :changed-variables changed-variables
                    :procedure procedure :sample-stop sample-stop
                    :tolerances tolerances)))
    (list* :id (sha256-hex (canonical-string body)) body)))

(defun trial-manifest-id (manifest) (getf manifest :id))

(defun make-execution-reference (&key policy-revision task-generation attempt
                                     parent-execution packet-digest packet-bytes
                                     requested-model observed-model harness bench
                                     execution-limit-ms started-at expires-at
                                     stop-outcome stop-observed-at checkpoint
                                     result usage)
  "An ACTIVE execution references its policy revision, generation, attempt and
parent, packet digest/bytes, requested and observed model, harness, bench, the
execution limit, start/expiry, stop outcome/time, checkpoint, result and usage
pointers (SPEC-WORK.md:4733)."
  (let ((body (list :policy-revision policy-revision
                    :task-generation task-generation :attempt attempt
                    :parent-execution parent-execution
                    :packet-digest packet-digest :packet-bytes packet-bytes
                    :requested-model requested-model :observed-model observed-model
                    :harness harness :bench bench
                    :execution-limit-ms execution-limit-ms
                    :started-at started-at :expires-at expires-at
                    :stop-outcome stop-outcome :stop-observed-at stop-observed-at
                    :checkpoint checkpoint :result result :usage usage)))
    (list* :id (sha256-hex (canonical-string body)) body)))

(defun execution-reference-policy-revision (reference)
  (getf reference :policy-revision))


;;; ------------------------------------------------------------------
;;; A price pinned by its revision (SPEC-WORK.md:4636)
;;; ------------------------------------------------------------------

(defun make-pricing-record (&key id currency unit-scale input output cache-write
                                 cache-read reasoning tiers batch source effective-time)
  "A pricing record is immutable by content identity and states currency, unit
scale and separate input/output/cache-write/cache-read rates; a missing
dimension is :unknown and never zero (SPEC-WORK.md:4636)."
  (let ((body (list :currency (or currency "usd")
                    :unit-scale (or unit-scale 1000000)
                    :input (if input input :unknown)
                    :output (if output output :unknown)
                    :cache-write (if cache-write cache-write :unknown)
                    :cache-read (if cache-read cache-read :unknown)
                    :reasoning (if reasoning reasoning :unknown)
                    :tiers (or tiers '())
                    :batch (or batch '())
                    :source (or source +absent+)
                    :effective-time (or effective-time +absent+))))
    (list* :id (or id (sha256-hex (canonical-string body))) body)))

(defun pricing-record-id (record) (getf record :id))
(defun pricing-record-input (record) (getf record :input))
(defun pricing-record-effective-time (record) (getf record :effective-time))

(defstruct (pricing-registry (:constructor %make-pricing-registry))
  records)

(defun make-pricing-registry ()
  (%make-pricing-registry :records (make-hash-table :test #'equal)))

(defun register-pricing (registry record)
  "A refresh adds a revision; it never rewrites one already registered, so an
old estimate stays reproducible."
  (setf (gethash (pricing-record-id record) (pricing-registry-records registry)) record)
  record)

(defun lookup-pricing (registry id)
  (gethash id (pricing-registry-records registry)))

(defun pricing-refresh (record &key input output cache-write cache-read source effective-time)
  "A price refresh is a meaningful config change with its source and effective
time; it produces a new content identity and leaves the historical record
unchanged (SPEC-WORK.md:4648)."
  (make-pricing-record
   :currency (getf record :currency)
   :unit-scale (getf record :unit-scale)
   :input (if input input (getf record :input))
   :output (if output output (getf record :output))
   :cache-write (if cache-write cache-write (getf record :cache-write))
   :cache-read (if cache-read cache-read (getf record :cache-read))
   :reasoning (getf record :reasoning)
   :tiers (getf record :tiers)
   :batch (getf record :batch)
   :source source
   :effective-time effective-time))

(defun pricing-rate (record category)
  (let ((value (getf record category)))
    (if (integerp value) value :unknown)))

(defun estimate-cost (registry revision usage &key (cash 0) (virtual-weight 1))
  "Compute cost from the pricing record pinned at REVISION. A used category with
a missing rate makes the total :unknown, never zero. Measured provider cash,
estimated marginal cash and virtual reference token cost stay three separately
labelled values (SPEC-WORK.md:4640)."
  (let* ((record (lookup-pricing registry revision))
         (scale (and record (getf record :unit-scale)))
         (total 0)
         (unknown nil))
    (dolist (category '(:input :output :cache-write :cache-read))
      (let ((tokens (getf usage category 0))
            (rate (if record (pricing-rate record category) :unknown)))
        (cond ((not (integerp tokens)) (setf unknown t))
              ((zerop tokens) nil)
              ((not (integerp rate)) (setf unknown t))
              (t (incf total (floor (* tokens rate) scale))))))
    (let* ((total (if (or (null record) unknown) :unknown total))
           (marginal total)
           (virtual-cost (if (eq total :unknown) :unknown (* total virtual-weight)))
           (cash-charge (if (eq total :unknown) :unknown cash)))
      (list :pricing-revision revision :usage usage
            :total-cost total :cash cash-charge :marginal marginal
            :virtual virtual-cost))))


;;; ------------------------------------------------------------------
;;; Quiet until actionable (SPEC-WORK.md:4760)
;;; ------------------------------------------------------------------

(defstruct (dispatch-pulse (:constructor %make-dispatch-pulse))
  record-bound byte-bound last pending pending-bytes dispatches)

(defun make-dispatch-pulse (&key (record-bound 4) (byte-bound 4096))
  (%make-dispatch-pulse :record-bound record-bound :byte-bound byte-bound
                        :last nil :pending '() :pending-bytes 0 :dispatches 0))

(defun observation-urgent-p (observation)
  "Corrections, stop requests, lease changes and deadlines bypass the delay
(SPEC-WORK.md:4762)."
  (member (getf observation :kind) '(:correction :stop :lease-change :deadline)))

(defun dispatch-pulse-flush (pulse)
  (when (dispatch-pulse-pending pulse)
    (setf (dispatch-pulse-pending pulse) '()
          (dispatch-pulse-pending-bytes pulse) 0)
    (incf (dispatch-pulse-dispatches pulse))
    t))

(defun observe-pulse (pulse observation)
  "An unchanged observation dispatches nothing. A changed urgent one dispatches
at once. Routine deltas queue until a configured record or byte bound is
reached, then go as one focused packet (SPEC-WORK.md:4822). Answers the number
of dispatches this observation caused."
  (let ((digest (canonical-string observation)))
    (if (equal digest (dispatch-pulse-last pulse))
        0
        (progn
          (setf (dispatch-pulse-last pulse) digest)
          (cond
            ((observation-urgent-p observation)
             (incf (dispatch-pulse-dispatches pulse))
             1)
            (t
             (push observation (dispatch-pulse-pending pulse))
             (incf (dispatch-pulse-pending-bytes pulse)
                   (or (getf observation :bytes) 0))
             (if (or (>= (length (dispatch-pulse-pending pulse))
                         (dispatch-pulse-record-bound pulse))
                     (>= (dispatch-pulse-pending-bytes pulse)
                         (dispatch-pulse-byte-bound pulse)))
                 (progn (dispatch-pulse-flush pulse) 1)
                 0)))))))


;;; ------------------------------------------------------------------
;;; Read-only intake (SPEC-WORK.md:6229)
;;; ------------------------------------------------------------------

(defstruct (recording-adapter (:constructor %make-recording-adapter))
  inventory reads mutations)

(defun make-recording-adapter (&key inventory)
  (%make-recording-adapter :inventory inventory :reads '() :mutations '()))

(defun adapter-inventory (adapter) (recording-adapter-inventory adapter))
(defun adapter-read-calls (adapter) (reverse (recording-adapter-reads adapter)))
(defun adapter-mutation-calls (adapter) (reverse (recording-adapter-mutations adapter)))

(defun adapter-read (adapter key)
  (push key (recording-adapter-reads adapter))
  (getf (recording-adapter-inventory adapter) key))

(defun adapter-mutate (adapter method endpoint &key body)
  "The recording adapter fails on every source mutation method, so a stray call
could never pass silently."
  (push (list method endpoint body) (recording-adapter-mutations adapter))
  (error 'unsupported-input
         :what (format nil "read-only adapter refused ~A ~A" method endpoint)))

(defun dry-run-capture (adapter)
  "A dry-run capture reads and records nothing on the source."
  (list :kind :capture
        :issues (adapter-read adapter :issues)
        :comments (adapter-read adapter :comments)
        :source-issues (adapter-read adapter :issues)))

(defun initial-import (adapter)
  "A normal initial import reads only."
  (list :kind :work
        :issue-count (adapter-read adapter :issues)
        :comment-count (adapter-read adapter :comments)))

(defun apply-plan (source destination plan)
  "Revalidate against the source, then apply to the destination. A plan whose
source moved is refused and the destination is untouched; the source itself is
never written."
  (let ((now (adapter-read source :issues)))
    (if (equal now (getf plan :source-issues))
        (progn (setf (getf destination :applied)
                     (+ (or (getf destination :applied) 0) (getf plan :issues)))
               destination)
        (values nil "INTAKE FAIL: stale plan, source changed since capture"))))


;;; ------------------------------------------------------------------
;;; Regression and recovery (SPEC-WORK.md:4787)
;;; ------------------------------------------------------------------

(defun trial-stage (policy) (getf (getf policy :trial) :stage))
(defun trial-tolerances (policy) (getf (getf policy :trial) :tolerances))
(defun trial-fallback (policy) (getf (getf policy :trial) :fallback))

(defun trial-tolerance-breach (policy observed)
  "The first regressed axis, or NIL. Quality below its tolerance breaches;
tokens, cost and wall time above theirs breach (SPEC-WORK.md:4787)."
  (let ((tol (trial-tolerances policy)))
    (flet ((limit (key) (getf tol key))
           (got (key) (getf observed key)))
      (cond
        ((and (limit :quality) (got :quality)
              (< (got :quality) (limit :quality))) :quality)
        ((and (limit :tokens) (got :tokens)
              (> (got :tokens) (limit :tokens))) :tokens)
        ((and (limit :cost) (got :cost)
              (> (got :cost) (limit :cost))) :cost)
        ((and (limit :wall) (got :wall)
              (> (got :wall) (limit :wall))) :wall)
        (t nil)))))

(defun role-limits-within-p (fallback trial)
  "No role limit of the fallback may exceed the trial's agreed limit."
  (let ((wanted (getf (getf fallback :routing) :role-limits))
        (agreed (getf (getf trial :routing) :role-limits)))
    (loop for (role limit) on wanted by #'cddr
          always (let ((ceiling (getf agreed role)))
                   (or (null ceiling) (<= limit ceiling))))))

(defun fallback-eligible-p (fallback trial)
  "An eligible fallback is an approved policy that preserves role limits."
  (and fallback
       (member (trial-stage fallback) '(:observe :adopt :approved))
       (role-limits-within-p fallback trial)))

(defstruct (assignment-control (:constructor make-assignment-control
                                (&key policy attempts handles suspended blocker)))
  policy attempts handles suspended blocker)

(defun automatic-assignment-allowed-p (control)
  (not (assignment-control-suspended control)))

(defun regression-recover (control observed)
  "A measured tolerance breach suspends new automatic routing under the trial
and returns to an eligible approved policy, recording the reason. Failed
attempts and uncertain live handles are preserved; with no eligible fallback an
explicit scheduling blocker is left for the coordinator (SPEC-WORK.md:4787)."
  (let ((policy (assignment-control-policy control)))
    (let ((breach (trial-tolerance-breach policy observed)))
      (if (not breach)
          (values control (list :breached nil))
          (let ((fallback (trial-fallback policy)))
            (if (fallback-eligible-p fallback policy)
                (values
                 (make-assignment-control
                  :policy fallback
                  :attempts (assignment-control-attempts control)
                  :handles (assignment-control-handles control)
                  :suspended t :blocker nil)
                 (list :breached breach :fallback (getf fallback :id)
                       :reason (format nil "trial breached ~A tolerance" breach)))
                (values
                 (make-assignment-control
                  :policy policy
                  :attempts (assignment-control-attempts control)
                  :handles (assignment-control-handles control)
                  :suspended t
                  :blocker (format nil "no eligible fallback after ~A breach" breach))
                 (list :breached breach :fallback nil))))))))

(defun assign-automatic (control candidate)
  "A suspended trial accepts no new automatic assignment."
  (if (automatic-assignment-allowed-p control)
      (values (make-assignment-control
               :policy (assignment-control-policy control)
               :attempts (cons candidate (assignment-control-attempts control))
               :handles (assignment-control-handles control)
               :suspended nil :blocker (assignment-control-blocker control))
              t "ASSIGN OK")
      (values control nil
              "ASSIGN FAIL: trial suspended, no new automatic assignment")))


;;; ------------------------------------------------------------------
;;; folded from replays-8660.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8660.lisp --- the pure duty-tier model behind the five replays of
;;;; docs/SPEC-WORK.md:5547 and the amendment #500 replay list at
;;;; docs/SPEC-WORK.md:6067-6079.
;;;;
;;;; The amendment says of itself "nothing here is built" (docs/SPEC-WORK.md:6444)
;;;; and the resident session, the provider dispatch and the CLI it names are
;;;; outside the slice-1 C/O transition kernel (README.md, "What is out"). So,
;;;; exactly as src/replays-applicable-delegation.lisp does for the
;;;; applicable/delegation replays, this file is the pure part the tests call:
;;;; the approved policy record and its execution, the escalation row and the
;;;; stale pass, the wait table's four presence columns, and the quiet pulse.
;;;; The revive replay itself is kernel behaviour and uses src/kernel.lisp.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; duty-tier-executes-the-policy             SPEC-WORK.md:6067-6070
;;; ------------------------------------------------------------------

(defstruct (policy-rule (:constructor make-policy-rule (&key id by task-class models)))
  "One rule of an approved policy. :by is the word the rule is; a rule with no
:by is refused at load (docs/SPEC-WORK.md:2574)."
  id by task-class models)

(defstruct (duty-policy (:constructor %make-duty-policy (&key version rules)))
  "A versioned approved finite policy record in C."
  version rules)

(defun load-policy (&key version rules)
  "Load the approved policy record; every rule must carry :by. A rule with no
:by refuses at load, because an approved policy is a record of whose word every
rule is (docs/SPEC-WORK.md:2572-2576)."
  (dolist (rule rules)
    (unless (and (stringp (policy-rule-by rule)) (plusp (length (policy-rule-by rule))))
      (error 'unsupported-input
             :what (format nil "policy rule ~A has no :by" (policy-rule-id rule)))))
  (%make-duty-policy :version version :rules (copy-list rules)))

(defun author-policy (&rest args)
  "The duty tier executes a policy it never authors (docs/SPEC-WORK.md:2569-2574)."
  (declare (ignore args))
  (error 'unsupported-input :what "a duty session never authors policy"))

(defun policy-rule-for (policy task-class)
  "The approved rule that decides TASK-CLASS, or NIL."
  (find task-class (duty-policy-rules policy)
        :key #'policy-rule-task-class :test #'equal))

(defun cheapest-qualified (models price-table)
  "The cheapest model among MODELS that the price table knows, or NIL. Ties go
to the name that sorts first, so the choice is deterministic."
  (let ((qualified (remove-if-not (lambda (m) (assoc m price-table :test #'equal))
                                  models)))
    (when qualified
      (first (sort (copy-list qualified) #'<
                   :key (lambda (m) (cdr (assoc m price-table :test #'equal))))))))

(defun execute-policy (policy task-class price-table)
  "Answer (values DISPOSITION MODEL-OR-RULE): :execute with the cheapest qualified
model, :none with the rule when no named model is qualified, or :escalate when no
rule could decide. Every judgment it cannot make becomes an escalation."
  (let ((rule (policy-rule-for policy task-class)))
    (unless rule
      (return-from execute-policy (values :escalate nil)))
    (let ((model (cheapest-qualified (policy-rule-models rule) price-table)))
      (if model
          (values :execute model)
          (values :none rule)))))

;;; ------------------------------------------------------------------
;;; escalation-carries-rule-default-age       SPEC-WORK.md:6071-6073
;;; ------------------------------------------------------------------

(defstruct (escalation-row
             (:constructor make-escalation-row (&key rule default age)))
  "An escalation row carries the policy rule that could not decide it, the
default that fires on silence, and its age (docs/SPEC-WORK.md:2579-2586)."
  rule default age)

(defun stale-pass (rows)
  "The coordinator's stale pass reads the three and reassigns nothing: answer the
same rows and a receipt of what it read."
  (values rows
          (format nil "STALE OK rows=~D reassigned=0" (length rows))))

;;; ------------------------------------------------------------------
;;; wait-table-four-presence-columns          SPEC-WORK.md:6074-6076
;;; ------------------------------------------------------------------

(defparameter *wait-presence-columns*
  '(:process-alive :beat-written :delivery-handled :parent-woke)
  "The four presence facts the per-harness wait table gains, beside :wait-source
(docs/SPEC-WORK.md:2589-2591).")

(defstruct (wait-row
             (:constructor make-wait-row
                 (&key harness process-alive beat-written delivery-handled parent-woke)))
  "One harness's wait row: the wait source plus the four presence columns."
  harness process-alive beat-written delivery-handled parent-woke)

(defun wait-presence (row)
  "The row's four presence facts, in column order."
  (list :process-alive (wait-row-process-alive row)
        :beat-written (wait-row-beat-written row)
        :delivery-handled (wait-row-delivery-handled row)
        :parent-woke (wait-row-parent-woke row)))

(defun wait-holds-resident-p (row)
  "A harness with the fourth column unproven cannot hold a resident session
(docs/SPEC-WORK.md:2591-2596)."
  (every (lambda (column) (getf (wait-presence row) column))
         *wait-presence-columns*))

(defun wait-holds-duty-p (row)
  "A harness that has proven the first three may hold a duty session driven by
notes (docs/SPEC-WORK.md:2594-2596)."
  (every (lambda (column) (getf (wait-presence row) column))
         '(:process-alive :beat-written :delivery-handled)))

;;; ------------------------------------------------------------------
;;; quiet-time-calls-nothing                  SPEC-WORK.md:6077-6079
;;; ------------------------------------------------------------------

(defstruct (pulse-result (:constructor %make-pulse-result
                              (&key model-calls notes published cost)))
  "One pulse's outcome: the calls it made, the notes it sent, what it published
mechanically, and the spend of the one call the event caused."
  model-calls notes published cost)

(defun quiet-pulse (&key changed decision price-table)
  "A resident or duty session with nothing changed makes no model call and sends
no note; state is published mechanically. When something changed, one call is
made and the cost of the event is the measured spend of that one call."
  (if (not changed)
      (%make-pulse-result :model-calls 0 :notes 0
                          :published '(:clip :beat :projection) :cost 0)
      (let* ((model (getf decision :model))
             (cost (or (cdr (assoc model price-table :test #'equal)) 0)))
        (%make-pulse-result :model-calls 1 :notes 0
                            :published '(:clip :beat :projection) :cost cost))))
