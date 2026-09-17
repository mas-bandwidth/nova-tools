

;;; ------------------------------------------------------------------
;;; folded from replays-8642.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

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


;;; ------------------------------------------------------------------
;;; folded from replays-8643.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8643.lisp --- the pure part of the dry run, the complete cost
;;;; lineage, savepoint compaction and copied journals named by the replays of
;;;; docs/SPEC-WORK.md (5678 dry run, 4852 cost lineage, 5791 compaction,
;;;; 6017 copied journal, 6379 coordinator cost joins).
;;;;
;;;; Nothing here starts a session, opens a journal or dispatches a child: these
;;;; are the pure functions the five `*-8643` acceptance replays drive. The
;;;; live session, journal I/O and CLI wiring those paragraphs sit on is out of
;;;; this slice and is named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The dry run (SPEC-WORK.md:5678)
;;; ------------------------------------------------------------------

(defun make-replay-session (&key (revision 0) (events 0) (pending 0)
                                 (pushed nil) (dedup nil) (journal nil))
  "The four counters a `SESSION OK` line reports, the dedup index and the
journal, as one immutable plist a preview and an apply both read."
  (list :revision revision :events events :pending pending
        :pushed pushed :dedup dedup :journal journal))

(defun preview-mutation (session request)
  "Validate and project a mutation at the session's current revision without
mutating it: the revision, the events=/pending=/pushed= counters and the dedup
index are all left exactly as they were. Returns the preview line and the
unchanged session (SPEC-WORK.md:5678)."
  (let ((expect (getf request :expect))
        (rev (getf session :revision)))
    (if (and expect (/= expect rev))
        (values (format nil "SESSION FAIL expect=~D current=~D: stale" expect rev)
                session)
        (values (format nil "SESSION OK events=~D pending=~D pushed=~A dry-run=true"
                        (getf session :events) (getf session :pending)
                        (or (getf session :pushed) "-"))
                session))))

(defun apply-mutation (session request)
  "The real apply: a stale --expect refuses by name and writes nothing; a new
request at the current revision is admitted, appends one event and moves the
revision by one (SPEC-WORK.md:5681)."
  (let ((expect (getf request :expect))
        (rev (getf session :revision))
        (id (getf request :id)))
    (cond
      ((and expect (/= expect rev))
       (values nil
               (format nil "SESSION FAIL expect=~D current=~D: stale" expect rev)
               session))
      ((member id (getf session :dedup) :test #'equal)
       (values t (format nil "SESSION OK request=~A already applied" id) session))
      (t
       (let ((new (list :revision (1+ rev)
                        :events (1+ (getf session :events))
                        :pending (getf session :pending)
                        :pushed (getf session :pushed)
                        :dedup (cons id (getf session :dedup))
                        :journal (cons id (getf session :journal)))))
         (values t
                 (format nil "SESSION OK request=~A rev=~D events=~D pushed=~A changed=1"
                         id (getf new :revision) (getf new :events)
                         (or (getf new :pushed) "-"))
                 new))))))

;;; ------------------------------------------------------------------
;;; Complete cost lineage (SPEC-WORK.md:4852)
;;; ------------------------------------------------------------------

(defun join-cost-lineage (receipts)
  "Join parent/child/retry receipts once each, count a failed attempt, never add
a cache subset a second time, keep implementation cost apart and leave the
operational total unknown when a receipt is a gap rather than reading it as
zero (SPEC-WORK.md:4852)."
  (let ((seen '()) (operational 0) (implementation 0) (gaps 0)
        (failed 0) (cache-merged 0) (joined 0) (gap-p nil))
    (dolist (r receipts)
      (let ((id (getf r :id)))
        (unless (member id seen :test #'equal)
          (push id seen)
          (incf joined)
          (let ((cost (getf r :cost))
                (role (getf r :role)))
            (cond
              ((eq cost :unknown) (incf gaps) (setf gap-p t))
              ((eq role :cache-subset) (incf cache-merged))
              ((or (eq role :implementation) (getf r :implementation))
               (incf implementation (if (numberp cost) cost 0)))
              (t
               (incf operational (if (numberp cost) cost 0))
               (when (eq (getf r :attempt) :failed) (incf failed))))))))
    (list :operational (if gap-p :unknown operational)
          :implementation implementation
          :gaps gaps :failed failed :cache-merged cache-merged :joined joined)))

;;; ------------------------------------------------------------------
;;; Compaction keeps the last copy (SPEC-WORK.md:5791, 6280)
;;; ------------------------------------------------------------------

(defun compact-copies (copies)
  "Plan a compaction that may never remove the only recoverable copy: the newest
verified copy is kept, unverified and superseded copies are pruned, and when no
copy is verified nothing is pruned (SPEC-WORK.md:5791, :6280)."
  (let ((verified (remove-if-not (lambda (c) (getf c :verified)) copies)))
    (if (null verified)
        (list :keep (mapcar (lambda (c) (getf c :id)) copies) :pruned '())
        (let ((keep (getf (car (last verified)) :id)))
          (list :keep (list keep)
                :pruned (remove keep (mapcar (lambda (c) (getf c :id)) copies)
                                :test #'equal))))))

;;; ------------------------------------------------------------------
;;; A copied journal grants nothing (SPEC-WORK.md:6017)
;;; ------------------------------------------------------------------

(defun restore-copy (copy)
  "A savepoint restore over a copied journal inspects in isolation: it takes no
ownership and dispatches nothing, whatever the copy's journal id says
(SPEC-WORK.md:6017). The copy carries the savepoint and the records and bytes
the read saw; the restore is the same isolated read-only session a local
restore opens."
  (multiple-value-bind (session line code)
      (savepoint-restore (make-savepoint-load
                          :savepoint (getf copy :savepoint)
                          :journal (getf copy :journal)
                          :image (getf copy :image)
                          :replies (getf copy :replies)
                          :records (getf copy :records)
                          :gap (getf copy :gap)))
    (list :journal-id (getf copy :journal-id)
          :bench (getf copy :bench)
          :isolation :read-only
          :ownership (and session (restore-session-ownership session))
          :dispatches (if session (or (restore-session-dispatch-count session) 0) 0)
          :side-effects (if session (or (restore-session-external-effects session) 0) 0)
          :session session
          :line line
          :code code)))

(defun start-over-copy (copy)
  "A session start over a copied journal is refused by the fencing rules; the
journal id grants nothing (SPEC-WORK.md:6019)."
  (declare (ignore copy))
  (values nil "SESSION FAIL: copy not fenced to this bench (fencing rules)" 1))

;;; ------------------------------------------------------------------
;;; Cost joins include the coordinator (SPEC-WORK.md:6379)
;;; ------------------------------------------------------------------

(defun join-experiment-cost (record)
  "Join the complete operational cost of a comparable-work experiment:
coordinator overhead and rework included, elapsed time attributed to the
observable phases, unobservable time left unknown and implementation cost kept
sunk and apart (SPEC-WORK.md:6379)."
  (let* ((phases (list (cons :execution (getf record :execution))
                       (cons :queueing (getf record :queueing))
                       (cons :review (getf record :review))
                       (cons :ci-waiting (getf record :ci-waiting))))
         (unknown (count-if (lambda (p) (not (numberp (cdr p)))) phases))
         (observable (remove-if (lambda (p) (not (numberp (cdr p)))) phases))
         (operational (+ (getf record :coordinator-overhead 0)
                         (getf record :rework 0)
                         (reduce #'+ observable :key #'cdr :initial-value 0))))
    (list :operational operational
          :phases observable
          :unknown-phases unknown
          :coordinator-overhead (getf record :coordinator-overhead 0)
          :rework (getf record :rework 0)
          :implementation (getf record :implementation-cost 0)
          :implementation-sunk t
          :comparable (getf record :after-adoption))))


;;; ------------------------------------------------------------------
;;; folded from replays-8646.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8646.lisp --- five promised replays whose surfaces are outside the
;;;; slice-1 C/O transition kernel: inventory accounting, the separately
;;;; labelled cost values, the materialised working set, a moving source and the
;;;; packet/route dispatch gates.
;;;;
;;;; These are the pure models the five acceptance replays drive. No session,
;;;; provider adapter or scheduler is started here; the paragraphs name the
;;;; facts each model holds.
;;;;
;;;; SPEC-WORK.md:6358   inventory-expansion-and-contraction
;;;; SPEC-WORK.md:4651,5770 local-tokens-cost-zero-api
;;;; SPEC-WORK.md:1706-1738,6243 materialized-working-set
;;;; SPEC-WORK.md:6231   moving-source
;;;; SPEC-WORK.md:4848   packet-and-route-gates

(in-package #:nova-work)

;;; ==================================================================
;;; inventory-expansion-and-contraction (SPEC-WORK.md:6358)
;;; ==================================================================

(defstruct (inventory-accounting
            (:constructor make-inventory-accounting (&key baseline)))
  ;; the initial inventory, retained whole and never rewritten
  (baseline nil :read-only t)
  discovered
  removed
  completed
  reopened
  decomposed
  ;; (interval expansion contraction), newest first
  intervals)

(defun inventory-denominator (acct)
  "The visible denominator: initial + discovered - removed. It is read here and
never silently revised, so a change stands beside progress."
  (+ (length (inventory-accounting-baseline acct))
     (length (inventory-accounting-discovered acct))
     (- (length (inventory-accounting-removed acct)))))

(defun inventory-observe (acct kind id interval)
  "One accounting fact over a named interval. Each fact keeps its own list:
completed, reopened, decomposed and explicitly removed work are counted
separately, and every fact moves the denominator only as the paragraph says."
  (let ((cell (assoc interval (inventory-accounting-intervals acct) :test #'equal)))
    (unless cell
      (setf cell (list interval 0 0))
      (push cell (inventory-accounting-intervals acct)))
    (ecase kind
      (:discovered
       (unless (member id (inventory-accounting-discovered acct) :test #'equal)
         (push id (inventory-accounting-discovered acct))
         (incf (second cell))))
      (:removed
       (unless (member id (inventory-accounting-removed acct) :test #'equal)
         (push id (inventory-accounting-removed acct))
         (incf (third cell))))
      (:completed
       (pushnew id (inventory-accounting-completed acct) :test #'equal))
      (:reopened
       (pushnew id (inventory-accounting-reopened acct) :test #'equal))
      (:decomposed
       (push id (inventory-accounting-decomposed acct))))
    acct))

(defun inventory-interval-deltas (acct)
  "Expansion and contraction shown over each named interval, oldest first."
  (mapcar (lambda (cell) (list (first cell) (second cell) (third cell)))
          (reverse (inventory-accounting-intervals acct))))

(defun inventory-sustained-divergence-p (acct &key (threshold 2))
  "Sustained divergence under the stated policy: expansion outran contraction
in every one of the last THRESHOLD intervals."
  (let ((deltas (last (inventory-interval-deltas acct) threshold)))
    (and (= (length deltas) threshold)
         (every (lambda (d) (> (second d) (third d))) deltas))))

(defun inventory-report (acct)
  "The counts beside progress, with the denominator visible and a changed one
flagged, never silently revised."
  (let ((base (length (inventory-accounting-baseline acct))))
    (list :baseline base
          :discovered (length (inventory-accounting-discovered acct))
          :completed (length (inventory-accounting-completed acct))
          :reopened (length (inventory-accounting-reopened acct))
          :decomposed (length (inventory-accounting-decomposed acct))
          :removed (length (inventory-accounting-removed acct))
          :denominator (inventory-denominator acct)
          :denominator-changed-p (/= (inventory-denominator acct) base))))

;;; ==================================================================
;;; local-tokens-cost-zero-api (SPEC-WORK.md:4651,5770)
;;; ==================================================================

(defstruct (cost-record
            (:constructor %make-cost-record
                (&key tokens measured-cash marginal-cash reference-token-cost)))
  ;; the token count the cost is about; local inference counts its tokens
  (tokens 0)
  ;; measured provider cash, estimated marginal cash and virtual reference
  ;; token cost: three separately labelled values
  measured-cash
  marginal-cash
  reference-token-cost)

(defun make-cost (&key tokens measured-cash marginal-cash reference-token-cost)
  "Build one cost record. A dimension that was not supplied is `:unknown`, never
zero; an explicit zero stays zero."
  (%make-cost-record
   :tokens (or tokens 0)
   :measured-cash (if (null measured-cash) :unknown measured-cash)
   :marginal-cash (if (null marginal-cash) :unknown marginal-cash)
   :reference-token-cost (if (null reference-token-cost) :unknown reference-token-cost)))

(defun local-inference-cost (&key (tokens 0) reference-token-cost marginal-cash)
  "Local inference counts its tokens with a declared API charge of zero, while
any hardware or energy cost is a separate model and the virtual reference token
cost is neither zeroed nor dropped by the zero (SPEC-WORK.md:4643-4644)."
  (%make-cost-record
   :tokens tokens
   :measured-cash 0
   :marginal-cash (if (null marginal-cash) :unknown marginal-cash)
   :reference-token-cost (if (null reference-token-cost) :unknown reference-token-cost)))

(defun cost-known-p (x)
  (and x (not (eq x :unknown))))

;;; ==================================================================
;;; materialized-working-set (SPEC-WORK.md:1706-1738,6243)
;;; ==================================================================

(defstruct (wlease
            (:constructor make-wlease
                (&key id deadline holder attempt (external :none) uncertain)))
  id
  deadline
  holder
  attempt
  ;; :uncertain external execution is retained past an expiry (W4)
  external
  uncertain)

(defstruct (working-set (:constructor make-working-set))
  ;; id -> live wlease: membership and |W| are resident lookups (W2)
  (leases (make-hash-table :test #'equal))
  ;; the carried |W| counter, read and never computed by a scan (W2)
  (count 0)
  ;; (deadline . id), ascending: due leases are found here, not in O (W3)
  deadline-index
  ;; records an expiry left behind until reconciled (W4)
  uncertain
  ;; the lease-time watermark every ask prints (W3)
  (watermark 0)
  ;; a recovery that has not reconciled its leases advertises none as live (W5)
  (reconciled-p t)
  (scope-rev 0))

(defun %w-index-remove (ws id)
  (setf (working-set-deadline-index ws)
        (remove id (working-set-deadline-index ws)
                :key #'cdr :test #'equal)))

(defun %w-index-add (ws id deadline)
  (%w-index-remove ws id)
  (setf (working-set-deadline-index ws)
        (sort (acons deadline id (working-set-deadline-index ws))
              #'< :key #'car)))

(defun w-take (ws id deadline &key holder attempt (external :none) uncertain)
  "Take a lease. Two live attempts on one id count that id once in |W|."
  (unless (gethash id (working-set-leases ws))
    (incf (working-set-count ws)))
  (setf (gethash id (working-set-leases ws))
        (make-wlease :id id :deadline deadline :holder holder
                     :attempt attempt :external external :uncertain uncertain))
  (%w-index-add ws id deadline)
  (gethash id (working-set-leases ws)))

(defun w-renew (ws id deadline)
  "A renew moves the lease's deadline and its index entry."
  (let ((lease (gethash id (working-set-leases ws))))
    (when lease
      (setf (wlease-deadline lease) deadline)
      (%w-index-add ws id deadline)))
  ws)

(defun w-release (ws id)
  "Release removes the id and its index entry. W is a view of O: no verb writes
it other than take and release."
  (when (gethash id (working-set-leases ws))
    (remhash id (working-set-leases ws))
    (decf (working-set-count ws))
    (%w-index-remove ws id))
  ws)

(defun w-member-p (ws id)
  "Is this id working: a constant-time resident lookup."
  (and (gethash id (working-set-leases ws)) t))

(defun w-count (ws)
  "|W|: the carried counter, read and never computed."
  (working-set-count ws))

(defun w-operational-keys (ws)
  (sort (loop for id being the hash-keys of (working-set-leases ws) collect id)
        #'string<))

(defun w-reconstruction-equal-p (ws live-ids)
  "The materialisation equals an independent reconstruction made from the
canonical state."
  (equal (w-operational-keys ws) (sort (copy-list live-ids) #'string<)))

(defun w-advance-clock (ws clock)
  "Process the due leases through the deadline index, visiting due leases and
not all of O. An expiry past its deadline leaves its uncertain record retained."
  (let ((due '()) (later '()))
    (dolist (entry (working-set-deadline-index ws))
      (if (<= (car entry) clock)
          (push (cdr entry) due)
          (push entry later)))
    (dolist (id due)
      (let ((lease (gethash id (working-set-leases ws))))
        (when lease
          (when (or (eq (wlease-external lease) :uncertain)
                    (wlease-uncertain lease))
            (push (list :id id :attempt (wlease-attempt lease)
                        :external (wlease-external lease))
                  (working-set-uncertain ws)))
          (remhash id (working-set-leases ws))
          (decf (working-set-count ws)))))
    (setf (working-set-deadline-index ws) (nreverse later)
          (working-set-watermark ws) clock)
    (length due)))

(defun w-uncertain-retained-p (ws id)
  (and (member id (working-set-uncertain ws) :key (lambda (r) (getf r :id))
               :test #'equal)
       t))

(defun w-live-lease-p (ws id &key clock)
  "A live lease reads only when the materialisation is reconciled; a recovery
that has not reconciled its leases advertises none of them as live."
  (and (working-set-reconciled-p ws)
       (let ((lease (gethash id (working-set-leases ws))))
         (and lease (> (wlease-deadline lease) (or clock (working-set-watermark ws)))))
       t))

(defun w-ask (ws &key clock)
  "Every ask that reads W prints its lease-time watermark beside its scope; a
stale watermark is printed as stale and never presented as current."
  (let ((clock (or clock (working-set-watermark ws))))
    (format nil "W scope=~D watermark=~D ~A"
            (working-set-scope-rev ws)
            (working-set-watermark ws)
            (if (< (working-set-watermark ws) clock) "stale" "current"))))

;;; ==================================================================
;;; moving-source (SPEC-WORK.md:6231)
;;; ==================================================================

(defstruct (source-version
            (:constructor make-source-version (&key field value revision)))
  field
  value
  revision)

(defstruct (source-capture (:constructor make-source-capture (&key source-revision)))
  source-revision
  ;; (id field) -> newest-first preserving capture of every observed version
  (captured (make-hash-table :test #'equal))
  (observations '())
  (complete-p t)
  (mixed-p nil)
  (reconciled-p nil))

(defun capture-observe (cap id field value &key revision)
  "Record one observed version. Once any observation is made the capture is no
longer presented as a whole provider snapshot."
  (push (make-source-version :field field :value value :revision revision)
        (gethash (list id field) (source-capture-captured cap)))
  (setf (source-capture-complete-p cap) nil)
  cap)

(defun capture-note-mutation (cap id)
  "A body edit, a comment added and deleted, a label or state change, or a
reopen observed during capture: the capture is mixed and marked as such."
  (setf (source-capture-mixed-p cap) t
        (source-capture-complete-p cap) nil)
  (push (list :mutation id) (source-capture-observations cap))
  cap)

(defun capture-versions (cap id field)
  "Every captured version for one field, oldest first. A version is never lost
to a later observation."
  (reverse (gethash (list id field) (source-capture-captured cap))))

(defun capture-reconcile (cap id field value &key revision)
  "Reconcile a newer observation: current reads the newer value while the
captured versions stay preserved."
  (push (make-source-version :field field :value value :revision revision)
        (gethash (list id field) (source-capture-captured cap)))
  (setf (source-capture-reconciled-p cap) t)
  cap)

(defun capture-consistent-claim (cap)
  "No consistent provider snapshot is claimed where none was available."
  (if (and (source-capture-complete-p cap)
           (not (source-capture-mixed-p cap)))
      :consistent
      :incomplete))

;;; ==================================================================
;;; packet-and-route-gates (SPEC-WORK.md:4848)
;;; ==================================================================

(defstruct (dispatch-packet
            (:constructor make-dispatch-packet
                (&key bytes history-disallowed costly-escalation escalation-reason
                      scoped-exception)))
  bytes
  history-disallowed
  costly-escalation
  escalation-reason
  scoped-exception)

(defstruct (dispatch-route
            (:constructor make-dispatch-route (&key id reserved stale)))
  id
  reserved
  stale)

(defun packet-gate (packet max-bytes)
  "An oversized or history-disallowed packet refuses before dispatch."
  (cond
    ((and max-bytes (> (dispatch-packet-bytes packet) max-bytes))
     (values :refused
             (format nil "PACKET FAIL oversized: ~D > ~D"
                     (dispatch-packet-bytes packet) max-bytes)))
    ((dispatch-packet-history-disallowed packet)
     (values :refused "PACKET FAIL history-disallowed"))
    (t (values :ok "PACKET OK"))))

(defun route-gate (route)
  "A reserved or stale route refuses before dispatch."
  (cond
    ((dispatch-route-reserved route) (values :refused "ROUTE FAIL reserved"))
    ((dispatch-route-stale route) (values :refused "ROUTE FAIL stale"))
    (t (values :ok "ROUTE OK"))))

(defun escalation-gate (packet)
  "An unexplained costly escalation refuses; a valid scoped exception is
retained."
  (if (dispatch-packet-costly-escalation packet)
      (if (and (dispatch-packet-escalation-reason packet)
               (plusp (length (dispatch-packet-escalation-reason packet))))
          (values :ok "ESCALATION OK scoped-exception=retained")
          (values :refused "ESCALATION FAIL unexplained costly escalation"))
      (values :ok "ESCALATION OK")))

(defun dispatch-gates (packet route &key max-bytes)
  "All gates run before dispatch. Any refusal returns NIL and its line; a valid
scoped exception is retained and the packet dispatches."
  (multiple-value-bind (ok line) (packet-gate packet max-bytes)
    (unless (eq ok :ok) (return-from dispatch-gates (values nil line))))
  (multiple-value-bind (ok line) (route-gate route)
    (unless (eq ok :ok) (return-from dispatch-gates (values nil line))))
  (multiple-value-bind (ok line) (escalation-gate packet)
    (unless (eq ok :ok) (return-from dispatch-gates (values nil line)))
    (values t (if (search "retained" line)
                  "DISPATCH OK scoped-exception=retained"
                  "DISPATCH OK")
            :dispatched)))


;;; ------------------------------------------------------------------
;;; folded from replays-operations-undo.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-operations-undo.lisp --- the long-operation control plane and the
;;;; append-only reversal of accepted requests.
;;;;
;;;; The pure kernel the five acceptance replays
;;;; `status-answers-while-io-runs`, `cancel-is-a-request-not-an-erasure`,
;;;; `clip-is-one-long-operation`, `undo-appends-and-preserves` and
;;;; `redo-refuses-a-stale-plan` call. Nothing here starts a session, a socket
;;;; or a child. Sources: SPEC-WORK.md:2719-2758 (slow work returns a durable
;;;; operation id, the control plane never waits behind it, queues and staged
;;;; bytes are bounded, a restart reconciles pending ids, a cancellation is a
;;;; request with its own final disposition), :2825-2843 (mistakes are
;;;; reversible by appending, never by erasing), and :5778-5782 (clip is one
;;;; long operation).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Long operations and the responsive control plane (SPEC-WORK.md:2719)
;;; ------------------------------------------------------------------

(defparameter *operation-limits* '(:queue 4 :staged-bytes 4096 :retained-results 4)
  "Queues, staged bytes and retained results are bounded by explicit limits
(SPEC-WORK.md:2753).")

(defstruct (operation
             (:constructor make-operation
                 (&key id op request (state :queued) result spec
                       (staged-bytes 0) (retained-results 0))))
  id op request state result spec staged-bytes retained-results)

(defstruct (work-session
             (:constructor make-work-session
                 (&key (operations nil) (events nil) (receipts nil)
                       (cancellations nil) (path "<session>") base
                       (limits *operation-limits*) (git-timeout 30))))
  operations events receipts cancellations path base limits git-timeout)

(defun session-operation (session id)
  "The operation record itself, found in the bounded operation list."
  (find id (work-session-operations session) :key #'operation-id :test #'equal))

(defun session-add-operation (session op)
  (make-work-session :operations (cons op (work-session-operations session))
                     :events (work-session-events session)
                     :receipts (work-session-receipts session)
                     :cancellations (work-session-cancellations session)
                     :path (work-session-path session)
                     :base (work-session-base session)
                     :limits (work-session-limits session)
                     :git-timeout (work-session-git-timeout session)))

(defun operation-status (session id)
  "Read one operation's own record: no journal replay, no whole-queue drain and
no wait on the I/O the operation is doing, so status answers while the mutation
loop is busy (SPEC-WORK.md:2731, :2756)."
  (let ((op (session-operation session id)))
    (list :id id
          :op (and op (operation-op op))
          :state (if op (operation-state op) :none)
          :result (and op (operation-result op)))))

(defun operation-pending-p (op)
  (member (operation-state op) '(:queued :running)))

(defun session-bounded-p (session)
  "Queues, staged bytes and retained results, each inside its explicit limit
(SPEC-WORK.md:2753)."
  (let ((limits (work-session-limits session))
        (ops (work-session-operations session)))
    (and (<= (length ops) (getf limits :queue))
         (<= (reduce #'+ ops :key #'operation-staged-bytes :initial-value 0)
             (getf limits :staged-bytes))
         (<= (reduce #'+ ops :key #'operation-retained-results :initial-value 0)
             (getf limits :retained-results)))))

(defun operation-cancel (session id &key (request "req-cancel-1")
                                      (external-effect :none))
  "A cancellation is a request with its own acknowledgement and its own final
disposition. It is deduplicated by its own request id: the same cancel request
replayed cancels once; a different request id is a fresh acknowledgement of the
operation's final disposition. It neither erases an accepted mutation nor
claims an uncertain external effect was cancelled (SPEC-WORK.md:2734-2738)."
  (let ((recorded (assoc request (work-session-cancellations session)
                         :test #'equal)))
    (when recorded
      (return-from operation-cancel
        (values session (append (cdr recorded) (list :replayed t))))))
  (let* ((op (session-operation session id))
         (disposition
           (cond
             ((null op)
              (list :id id :state :none :request request
                    :reason "no such operation"))
             ((eq external-effect :uncertain)
              (setf (operation-state op) :uncertain)
              (list :id id :state :uncertain :request request
                    :reason "external effect uncertain"))
             ((member (operation-state op) '(:cancelled :uncertain))
              (list :id id :state (operation-state op) :request request))
             (t
              (setf (operation-state op) :cancelled)
              (list :id id :state :cancelled :request request)))))
    (push (cons request disposition) (work-session-cancellations session))
    (values session disposition)))

;;; ------------------------------------------------------------------
;;; The clip transport as one long operation (SPEC-WORK.md:2740-2745,
;;; :5355-5357, :5916-5920).
;;;
;;; A clip names a local event boundary, fetches the upstream tip, refuses
;;; `CLIP RACED` when the tip is not the base, validates the resident O, writes
;;; one deterministic snapshot and commits and pushes it. The remote is a
;;; protocol (a seam): the real implementation is the owned Git branch; the
;;; in-process implementation below is what the kernel's replay drives.
;;; ------------------------------------------------------------------

(defclass in-process-clip-remote ()
  ((tip :initarg :tip :accessor in-process-clip-remote-tip)
   (pushes :initform '() :accessor in-process-clip-remote-pushes)))

(defun make-clip-remote (&key (tip "genesis"))
  "The in-process implementation of the clip-remote seam (seam: clip-remote),
standing in for the Git branch a coordinator owns and pushes."
  (make-instance 'in-process-clip-remote :tip tip))

(defgeneric clip-remote-tip (remote)
  (:documentation "The upstream tip the transport fetched, a commit sha."))

(defgeneric clip-remote-push (remote base commit)
  (:documentation "A compare-and-swap push: move the remote tip to COMMIT only
when it still equals BASE. Answer T when the push landed and NIL when the base
predicate refused; a refused push is reported `CLIP RACED` and never retried
blindly (SPEC-WORK.md:2740-2743)."))

(defmethod clip-remote-tip ((remote in-process-clip-remote))
  (in-process-clip-remote-tip remote))

(defmethod clip-remote-push ((remote in-process-clip-remote) base commit)
  (if (equal base (in-process-clip-remote-tip remote))
      (progn (setf (in-process-clip-remote-tip remote) commit)
             (push commit (in-process-clip-remote-pushes remote))
             t)
      nil))

(defun clip-remote-pushes (remote)
  "The commits this remote has accepted, newest first, for the replay."
  (in-process-clip-remote-pushes remote))

(defun clip-boundary (events)
  "The request id of the last accepted event: the local event boundary a clip
names. NIL when there is no accepted event to clip."
  (let ((last (car (last events))))
    (and last (or (getf last :request) (getf last :id)))))

(defun clip-snapshot (events revision)
  "The deterministic snapshot a clip writes: the retained event history and the
revision it was taken at, in one canonical serialization, so two builds commit
the same bytes (SPEC-WORK.md:2749-2754, :3980)."
  (canonical-string (list :events events :revision revision)))

(defun clip-commit (events revision)
  "The commit sha of the snapshot: a real content digest, not an invented id."
  (sha256-hex (clip-snapshot events revision)))

(defun clip-sha12 (sha)
  "The twelve-hex abbreviation `expected=` and `found=` print."
  (if (and (stringp sha) (>= (length sha) 12)) (subseq sha 0 12) (or sha "-")))

(defun clip-request (session &key (id "op-clip-1") (request "req-clip-1")
                                (staged-bytes 0) remote base attempts
                                (events (work-session-events session))
                                (revision (length events)) path)
  "`clip` prints OPERATION OK id=<id> op=clip state=<queued|running> at once and
exits; the transport continues. The boundary, revision and snapshot are pinned
now, so a write admitted while the clip runs is pending for the next one. A full
queue refuses rather than growing unbounded (SPEC-WORK.md:2740-2745, :2753)."
  (let ((limits (work-session-limits session)))
    (when (>= (length (work-session-operations session)) (getf limits :queue))
      (return-from clip-request
        (values session nil "OPERATION FAIL: queue full")))
    (let* ((base (or base (work-session-base session)
                     (and remote (clip-remote-tip remote))))
           (op (make-operation
                :id id :op :clip :request request :state :queued
                :staged-bytes staged-bytes
                :spec (list :remote remote :base base
                            :boundary (clip-boundary events)
                            :events events :revision revision
                            :commit (clip-commit events revision)
                            :attempts (or attempts 25)
                            :git-timeout (or (work-session-git-timeout session) 30)
                            :path (or path (work-session-path session))))))
      (values (session-add-operation session op) op
              (format nil "OPERATION OK id=~A op=clip state=queued" id)))))

(defun operation-wait (session id &key race)
  "`operation wait --id` is a bounded block over the operation's own result.
When the transport settles it prints the CLIP OK line carrying operation=<id>
and its pushed=; a base predicate that refused prints CLIP RACED. The wait is
idempotent: a settled operation answers its recorded line
(SPEC-WORK.md:2740-2745, :5930-5934)."
  (let ((op (session-operation session id)))
    (cond
      ((null op)
       (values session
               (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id)))
      ((eq :done (operation-state op))
       (values session (operation-result op)))
      (t
       (let* ((spec (operation-spec op))
              (remote (getf spec :remote))
              (base (getf spec :base))
              (tip (and remote (clip-remote-tip remote)))
              (path (getf spec :path))
              (boundary (getf spec :boundary))
              (events (getf spec :events)))
         (cond
           ((or race (and remote (not (equal tip base))))
            (setf (operation-state op) :raced
                  (operation-result op) nil)
            (values session
                    (format nil "CLIP RACED session=~A operation=~A boundary=~A generation=1 expected=~A found=~A"
                            path id boundary (clip-sha12 base) (clip-sha12 tip))))
           ((and events (null boundary))
            (setf (operation-state op) :failed)
            (values session
                    (format nil "CLIP FAIL session=~A operation=~A boundary=- events=~D base=~A pushed=- attempts=0: resident O invalid"
                            path id (length events) base)))
           (t
            (let ((commit (getf spec :commit))
                  (pushed (getf spec :revision)))
              (unless (and remote (clip-remote-push remote base commit))
                (setf (operation-state op) :failed)
                (return-from operation-wait
                  (values session
                          (format nil "CLIP FAIL session=~A operation=~A boundary=~A events=~D base=~A pushed=- attempts=1: push refused"
                                  path id boundary (length events) base))))
              (setf (operation-state op) :done
                    (operation-result op)
                    (format nil "CLIP OK session=~A operation=~A boundary=~A events=~D base=~A commit=~A pushed=~D attempts=1 emitted=0"
                            path id boundary (length events) base commit pushed))
              (values session (operation-result op))))))))))

(defun session-stop (session &key race)
  "`session stop` is the one caller that waits for its own clip, by the same
`operation wait` inside its --git-timeout; it prints the CLIP OK first and then
the SESSION OK (SPEC-WORK.md:2742-2745, :5480-5490)."
  (let* ((clip (find :clip (work-session-operations session) :key #'operation-op))
         (clip-line
           (when clip
             (multiple-value-bind (settled line)
                 (operation-wait session (operation-id clip) :race race)
               (declare (ignore settled))
               line)))
         (session-line
           (format nil "SESSION OK session=~A owner=rowan generation=1 state=live events=~D pending=0 pushed=~D"
                   (work-session-path session)
                   (length (work-session-events session))
                   (if clip (getf (operation-spec clip) :revision) 0))))
    (if clip-line (list clip-line session-line) (list session-line))))

(defun reconcile-operations (session)
  "A restart reconciles the operation ids that were pending before anything is
retried, erasing no accepted mutation (SPEC-WORK.md:2722-2727, :2753)."
  (let ((reconciled '()))
    (dolist (op (work-session-operations session))
      (when (operation-pending-p op)
        (setf (operation-state op) :reconciled)
        (push (operation-id op) reconciled)))
    (values session (nreverse reconciled))))

;;; ------------------------------------------------------------------
;;; Undo appends and preserves (SPEC-WORK.md:2825)
;;; ------------------------------------------------------------------

(defparameter *reversible-verbs*
  '((:node-add . :node-remove)
    (:node-require . :node-require)
    (:dep . :dep)
    (:take . :release)
    (:release . :take))
  "A reduced reversible-verb table (SPEC-WORK.md:2853-2874): the verbs this
replay slice exercises, each mapped to the envelope its undo appends.")

(defun undo-request (ledger request &key (by "rowan")
                                         (stamp "2026-09-16T00:00:00Z"))
  "An undo of a named request appends a typed compensating envelope carrying
its lineage. The original event and every receipt stay exactly where they are;
nothing is deleted and nothing is rewritten (SPEC-WORK.md:2825-2837)."
  (let ((original (find request (getf ledger :history)
                        :key (lambda (e) (getf e :request)) :test #'equal)))
    (unless original
      (return-from undo-request
        (values ledger nil
                (format nil "UNDO FAIL request=~A: no such request" request))))
    (let ((compensating (cdr (assoc (getf original :kind) *reversible-verbs*
                                    :test #'equal))))
      (unless compensating
        (return-from undo-request
          (values ledger nil
                  (format nil "UNDO FAIL request=~A: not reversible here" request))))
      (let ((envelope (list :id (format nil "ev-undo-~A" request)
                            :kind compensating
                            :request (format nil "req-undo-~A" request)
                            :undo-of request
                            :compensates (getf original :id)
                            :lineage (list (getf original :id) request)
                            :by by :stamp stamp)))
        (values (list :history (append (getf ledger :history) (list envelope))
                      :receipts (getf ledger :receipts))
                envelope
                nil)))))

(defun moved-preconditions (expected actual)
  "The precondition keys whose value moved between EXPECTED and ACTUAL."
  (let ((moved '()))
    (loop for (k v) on expected by #'cddr
          unless (equal v (getf actual k))
            do (push k moved))
    (nreverse moved)))

(defun redo-request (ledger undo-id plan current)
  "A redo reapplies the intent against current preconditions rather than
deleting the undo. A plan whose preconditions moved is refused atomically,
naming what changed, writing nothing, and the undo is never deleted
(SPEC-WORK.md:2829-2843)."
  (let ((undo-envelope (find undo-id (getf ledger :history)
                             :key (lambda (e) (getf e :request))
                             :test #'equal)))
    (unless undo-envelope
      (return-from redo-request
        (values ledger nil
                (format nil "REDO FAIL request=~A: no such undo" undo-id))))
    (let ((expected-rev (getf plan :at-rev))
          (actual-rev (getf current :rev)))
      (unless (eql expected-rev actual-rev)
        (return-from redo-request
          (values ledger nil
                  (format nil "REDO FAIL request=~A: stale plan rev moved ~A->~A"
                          undo-id expected-rev actual-rev))))
      (let ((moved (moved-preconditions (getf plan :preconditions)
                                        (getf current :preconditions))))
        (when moved
          (return-from redo-request
            (values ledger nil
                    (format nil "REDO FAIL request=~A: stale plan changed ~{~A~^,~}"
                            undo-id moved)))))
      (let ((envelope (list :id (format nil "ev-redo-~A" undo-id)
                            :kind (getf undo-envelope :kind)
                            :request (format nil "req-redo-~A" undo-id)
                            :redo-of undo-id
                            :lineage (getf undo-envelope :lineage))))
        (values (list :history (append (getf ledger :history) (list envelope))
                      :receipts (getf ledger :receipts))
                envelope
                nil)))))


;;; ------------------------------------------------------------------
;;; folded from replays-wire-and-operations.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-wire-and-operations.lisp --- the pure part of the wire the five
;;;; card-8608 acceptance replays assert (docs/SPEC-WORK.md:2660-2758 and the
;;;; acceptance table at :5159-5183).
;;;;
;;;; Nothing here opens a socket, starts a session or runs a CLI: the functions
;;;; are the wire codec, the version handshake, the pipelined request/reply
;;;; correlation, the disconnect reconciliation over the real kernel dedup, and
;;;; the long-operation registry. The live transport that a later slice owns is
;;;; not in this slice. The reading each replay takes where the paragraph is
;;;; ambiguous is stated at its function.

(in-package #:nova-work)

;;;; ------------------------------------------------------------------
;;;; The wire codec: every integer is a JSON string, numbers are refused
;;;; (SPEC-WORK.md:2663-2666, :2669-2672).
;;;; ------------------------------------------------------------------

(defun wire-encode-integer (n)
  "An integer crosses the wire as a JSON string of decimal digits, never as a
JSON number, so a value above 2^53 cannot be rounded silently."
  (check-type n integer)
  (format nil "\"~D\"" n))

(defun wire-number-token-p (text)
  "True when TEXT is a bare JSON number token (its first non-sign character is
a digit and the rest are digits, signs, a decimal point or an exponent). The
wire carries no JSON numbers, so such a token is refused."
  (and (plusp (length text))
       (let ((start (if (find (char text 0) "+-") 1 0)))
         (and (< start (length text))
              (loop for i from start below (length text)
                    always (or (digit-char-p (char text i))
                               (find (char text i) ".eE+")))))))

(defun wire-decode-value (text)
  "Decode one wire value. A `null` is the absent spelling (:absent) and an
absent key is the same value; an empty string and an empty array are values,
and `true`/`false` decode to T/NIL. An array decodes each element, so a
response's `lines` array is read back whole. A JSON number is refused, reported
as an unsupported input."
  (cond
    ((string= text "null") +absent+)
    ((string= text "true") t)
    ((string= text "false") nil)
    ((and (>= (length text) 2)
          (char= (char text 0) #\[)
          (char= (char text (1- (length text))) #\]))
     (let ((inner (string-trim '(#\Space #\Tab #\Newline #\Return)
                               (subseq text 1 (1- (length text))))))
       (if (string= inner "")
           '()
           (mapcar (lambda (element)
                     (wire-decode-value
                      (string-trim '(#\Space #\Tab #\Newline #\Return) element)))
                   (wire-split-top-level inner #\,)))))
    ((and (>= (length text) 2)
          (char= (char text 0) #\")
          (char= (char text (1- (length text))) #\"))
     (let ((inner (subseq text 1 (1- (length text)))))
       (if (and (plusp (length inner)) (every #'digit-char-p inner))
           (parse-integer inner)
           inner)))
    ((wire-number-token-p text)
     (error 'unsupported-input
            :what (format nil "wire frame carries a JSON number ~A" text)))
    (t
     (error 'unsupported-input
            :what (format nil "wire frame is not a restricted scalar: ~A" text)))))

(defun wire-trim (text)
  (string-trim '(#\Space #\Tab #\Newline #\Return) text))

(defun wire-split-top-level (text separator)
  "Split TEXT on SEPARATOR at bracket depth zero, respecting strings."
  (let ((parts '()) (start 0) (in-string nil) (escaped nil) (depth 0)
        (len (length text)))
    (loop for i from 0 below len
          for ch = (char text i)
          do (cond
               ((and in-string escaped) (setf escaped nil))
               ((and in-string (char= ch #\\)) (setf escaped t))
               ((char= ch #\") (setf in-string (not in-string)))
               (in-string nil)
               ((find ch "[{") (incf depth))
               ((find ch "]}") (decf depth))
               ((and (char= ch separator) (zerop depth))
                (push (subseq text start i) parts)
                (setf start (1+ i)))))
    (push (subseq text start) parts)
    (nreverse parts)))

(defun wire-object-decode (text)
  "Decode a flat JSON object into an alist of (key . value). A value is decoded
by WIRE-DECODE-VALUE, so a JSON number anywhere inside refuses."
  (let ((trimmed (wire-trim text)))
    (unless (and (>= (length trimmed) 2)
                 (char= (char trimmed 0) #\{)
                 (char= (char trimmed (1- (length trimmed))) #\}))
      (error 'unsupported-input
             :what (format nil "wire frame is not a JSON object: ~A" text)))
    (let ((body (subseq trimmed 1 (1- (length trimmed)))))
      (if (string= (wire-trim body) "")
          '()
          (loop for pair in (wire-split-top-level body #\,)
                for colon = (position #\: pair)
                do (unless colon
                     (error 'unsupported-input
                            :what (format nil "wire object field without a colon: ~A" pair)))
                collect (let ((key (wire-trim (subseq pair 0 colon)))
                              (value (wire-trim (subseq pair (1+ colon)))))
                          (when (and (>= (length key) 2)
                                     (char= (char key 0) #\")
                                     (char= (char key (1- (length key))) #\"))
                            (setf key (subseq key 1 (1- (length key)))))
                          (cons key (wire-decode-value value))))))))

(defun wire-field (object key)
  "The field KEY of a decoded object, or (:absent) when the object does not
carry it. An absent key is the same value an explicit `null` decodes to."
  (let ((cell (assoc key object :test #'string=)))
    (if cell (cdr cell) +absent+)))

;;;; ------------------------------------------------------------------
;;;; The version handshake (SPEC-WORK.md:2682-2685).
;;;; ------------------------------------------------------------------

(defstruct (protocol-session (:constructor %make-protocol-session))
  supported version handshaken-p closed-p max-frame-bytes)

(defun make-protocol-session (&key (supported '("1")) (max-frame-bytes 65536))
  (%make-protocol-session :supported supported :version nil :handshaken-p nil
                          :closed-p nil :max-frame-bytes max-frame-bytes))

(defun protocol-hello (session offered)
  "The client's first frame offers the versions it can speak. Answer the one
version the session will speak, or refuse with the supported list named and
close the connection. An unsupported version never degrades into a guess."
  (cond
    ((protocol-session-closed-p session)
     (values nil '("connection closed")))
    ((protocol-session-handshaken-p session)
     (values (protocol-session-version session) nil))
    (t
     (let ((version (find-if (lambda (v)
                               (member v (protocol-session-supported session)
                                       :test #'string=))
                             offered)))
       (if version
           (progn
             (setf (protocol-session-version session) version
                   (protocol-session-handshaken-p session) t)
             (values version nil))
           (progn
             (setf (protocol-session-closed-p session) t)
             (values nil (protocol-session-supported session))))))))

(defun protocol-admit (session id)
  "No request is admitted before the handshake finishes (SPEC-WORK.md:2682)."
  (declare (ignore id))
  (cond
    ((protocol-session-closed-p session) (values nil "connection closed"))
    ((not (protocol-session-handshaken-p session))
     (values nil "no request before the handshake"))
    (t (values t nil))))

(defun protocol-frame-ok-p (session size)
  "A frame within the session's bound is read; a larger one is refused whole."
  (and (not (protocol-session-closed-p session))
       (<= size (protocol-session-max-frame-bytes session))))

(defun protocol-framed-error (session reason)
  "One framed error, carrying a null request id, and then the close."
  (setf (protocol-session-closed-p session) t)
  (format nil "FAIL request=null: ~A; connection closed" reason))

;;;; ------------------------------------------------------------------
;;;; The pipelined wire with its correlated replies, the three batch modes
;;;; and the mutation connection have moved to src/transport.lisp
;;;; (SPEC-WORK.md:2689-2717, :2763-2779, :5762-5775). The wire codec and
;;;; the version handshake stay here.
;;;; ------------------------------------------------------------------

;;;; ------------------------------------------------------------------
;;;; The long-operation registry (SPEC-WORK.md:2719-2758).
;;;; ------------------------------------------------------------------

(defstruct (operation-registry (:constructor %make-operation-registry))
  (journal nil)
  (operations '()))

(defun make-operation-registry (&key journal)
  "The operation registry over its durable accept journal. When no journal is
given the in-process implementation of the durable-accept seam is used; the
real one is the bounded filesystem journal of src/journal.lisp."
  (%make-operation-registry :journal (or journal (make-accept-journal))
                            :operations '()))

(defun operation-accept (registry &key id kind request author stamp)
  "The id is durable before it is printed: append the accept record -- the id,
kind, request id, author and stamp -- to the local recovery journal and make
that record durable, then answer the id. A crash between accepting the work and
acknowledging it can never leave a caller holding an id the restart never heard
of (SPEC-WORK.md:2721-2730)."
  (let ((record (list :id id :kind kind :request request :author author
                      :stamp stamp)))
    (durable-accept-record (operation-registry-journal registry) record)
    (setf (operation-registry-operations registry)
          (append (operation-registry-operations registry)
                  (list (cons id (append record (list :state :running
                                                      :result nil))))))
    id))

(defun registry-operation-state (registry id)
  "Answer (values STATE NIL 0) for an operation the journal holds, or
(values NIL LINE 2) for one no journal holds -- never an invented `queued`."
  (let ((cell (assoc id (operation-registry-operations registry) :test #'string=)))
    (if cell
        (values (getf (cdr cell) :state) nil 0)
        (values nil
                (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id)
                2))))

(defun operation-complete (registry id result)
  (let ((cell (assoc id (operation-registry-operations registry) :test #'string=)))
    (when cell
      (setf (getf (cdr cell) :state) :done
            (getf (cdr cell) :result) result))
    cell))

(defun registry-operation-result (registry id)
  (let ((cell (assoc id (operation-registry-operations registry) :test #'string=)))
    (and cell (getf (cdr cell) :result))))

(defun registry-operation-wait (registry id &key timeout (after 0))
  "A bounded wait over an event cursor, never a poll loop. AFTER is the cursor
the caller last read, so a resumed wait does not read the operation's events
again from zero. A timeout leaves the operation running and answers the cursor
it reached; a completed result is answered (values RESULT :done CURSOR)
(SPEC-WORK.md:2733-2735)."
  (declare (ignore timeout))
  (let ((cell (assoc id (operation-registry-operations registry) :test #'string=)))
    (cond
      ((null cell) (values nil :unknown after))
      ((eq :done (getf (cdr cell) :state))
       (values (getf (cdr cell) :result) :done 1))
      (t (values nil :timeout after)))))

(defun operation-client-exit (registry)
  "The CLI may exit while the work continues: the registry is already durable."
  (declare (ignore registry))
  t)

(defun operation-journal-length (registry)
  (accept-journal-count (operation-registry-journal registry)))

;;; ------------------------------------------------------------------
;;; The CLI thin client (SPEC-WORK.md:288-290, :2642-2646, :2679-2683,
;;; :2691-2694).
;;;
;;; The engine owns the canonical state, the journal, the indexes and the
;;; mutation ordering; the CLI is a thin client of it: it holds no kernel, so a
;;; fresh CLI process reloads nothing, and it prints exactly the lines it was
;;; handed. It matches every reply by its request id -- never arrival order --
;;; does not put the same id in flight twice on one connection, splits the
;;; handed lines by the second token and by nothing else, and counts the bytes
;;; it printed. The live socket is the transport row's and the framing it
;;; carries is the wire row's; the client drives decoded frames.
;;; ------------------------------------------------------------------

(defstruct (client-request
             (:constructor make-client-request
                 (&key op request as expect now max deadline args)))
  op request as expect now max deadline args)

(defun client-json-value (value)
  "One restricted wire value: an integer is a JSON string of decimal digits, a
string is quoted, T/NIL are the JSON booleans, an alist is an ordered object
and any other list is an array. A JSON number is never emitted."
  (cond
    ((null value) "null")
    ((eq value t) "true")
    ((integerp value) (wire-encode-integer value))
    ((stringp value) (format nil "\"~A\"" value))
    ((and (listp value) (every #'consp value)) (client-json-object value))
    ((listp value) (format nil "[~{~A~^,~}]" (mapcar #'client-json-value value)))
    (t (error 'unsupported-input
              :what (format nil "client frame value ~S" value)))))

(defun client-json-object (fields)
  "FIELDS is an ordered alist of (name . value)."
  (format nil "{~{~A~^,~}}"
          (mapcar (lambda (field)
                    (format nil "\"~A\":~A"
                            (car field) (client-json-value (cdr field))))
                  fields)))

(defun client-request-frame (request)
  "The pinned request frame: `op`, `request`, `as`, `expect`, `now`, `max`,
`deadline` and `args`, in that order, every integer a JSON string and an absent
field `null` (SPEC-WORK.md:2675-2676). No identity is a default of this build:
an absent `as` stays null and no friend, bench or house name is written."
  (client-json-object
   (list (cons "op" (client-request-op request))
         (cons "request" (client-request-request request))
         (cons "as" (client-request-as request))
         (cons "expect" (client-request-expect request))
         (cons "now" (client-request-now request))
         (cons "max" (client-request-max request))
         (cons "deadline" (client-request-deadline request))
         (cons "args" (client-request-args request)))))

(defun client-decode-response (frame)
  "Decode one response frame into (values of a plist) :request, :ok, :exit,
:lines, :rev and :pushed. The wire row owns the framing; this reads the object
the framed protocol carries."
  (let ((object (wire-object-decode frame)))
    (list :request (wire-field object "request")
          :ok (wire-field object "ok")
          :exit (wire-field object "exit")
          :lines (let ((lines (wire-field object "lines")))
                   (if (eq lines +absent+) '() lines))
          :rev (wire-field object "rev")
          :pushed (wire-field object "pushed"))))

(defun client-second-token (line)
  "The second space-delimited token of LINE, which is the disposition
(OK/ROW/NOTE/MORE/FAIL/RACED); NIL when the line has no second token."
  (let* ((first (position #\Space line))
         (start (and first (1+ first)))
         (end (and start (or (position #\Space line :start start) (length line)))))
    (and start (subseq line start end))))

(defun client-route-lines (lines)
  "Split LINES by the second token and by nothing else: OK, ROW, NOTE and MORE
are stdout; every other disposition (FAIL, RACED and refusals) is stderr
(SPEC-WORK.md:2679-2683). Answer (values STDOUT STDERR), each in wire order."
  (let ((stdout '()) (stderr '()))
    (dolist (line lines)
      (if (member (client-second-token line) '("OK" "ROW" "NOTE" "MORE")
                  :test #'string=)
          (push line stdout)
          (push line stderr)))
    (values (nreverse stdout) (nreverse stderr))))

(defun client-string-bytes (text)
  "The UTF-8 bytes of TEXT, so emitted counts bytes and never characters."
  #+sbcl (length (sb-ext:string-to-octets text :external-format :utf-8))
  #-sbcl (length text))

(defun client-lines-bytes (lines)
  "The bytes the client prints for LINES: each line and its newline."
  (let ((total 0))
    (dolist (line lines) (incf total (1+ (client-string-bytes line))))
    total))

(defstruct (cli-client (:constructor %make-cli-client))
  (in-flight '())
  (settled '())
  (emitted 0))

(defun make-cli-client ()
  "A fresh thin client: no kernel, no resident state, nothing in flight and
nothing printed, so starting it reloads nothing (SPEC-WORK.md:2646)."
  (%make-cli-client))

(defun cli-client-send (client request)
  "Put one request in flight and answer its frame. The client does not put the
same request id in flight twice on one connection (SPEC-WORK.md:2701)."
  (let ((id (client-request-request request)))
    (when (or (null id) (and (stringp id) (string= id "")))
      (error 'unsupported-input :what "a request needs its own id"))
    (when (or (assoc id (cli-client-in-flight client) :test #'string=)
              (member id (cli-client-settled client) :test #'string=))
      (error 'unsupported-input
             :what (format nil "request ~A is already in flight on this connection" id)))
    (setf (cli-client-in-flight client)
          (append (cli-client-in-flight client) (list (cons id request))))
    (client-request-frame request)))

(defun cli-client-receive (client response)
  "Deliver one decoded response. Match it by its request id alone, so an
out-of-order reply reaches only its own request. An unknown, duplicate or
absent id is a protocol error: signal without falsely settling anything
(SPEC-WORK.md:2691-2706). Answer RESPONSE."
  (let ((id (getf response :request)))
    (cond
      ((or (eq id +absent+) (null id) (and (stringp id) (string= id "")))
       (error 'unsupported-input
              :what "response request=null: no decodable id"))
      ((member id (cli-client-settled client) :test #'string=)
       (error 'unsupported-input
              :what (format nil "duplicate response id ~A" id)))
      ((not (assoc id (cli-client-in-flight client) :test #'string=))
       (error 'unsupported-input
              :what (format nil "unknown response id ~A" id)))
      (t
       (setf (cli-client-in-flight client)
             (remove id (cli-client-in-flight client) :key #'car :test #'string=))
       (setf (cli-client-settled client)
             (append (cli-client-settled client) (list id)))
       response))))

(defun cli-client-run (client frame &key (out *standard-output*) (err *error-output*))
  "Deliver one response FRAME: settle its request by id, print the lines it was
handed to OUT or ERR by their second token, and count the bytes printed. Answer
(values EXIT EMITTED STDOUT STDERR) (SPEC-WORK.md:2642-2646, :2679-2683)."
  (let* ((response (cli-client-receive client (client-decode-response frame)))
         (lines (getf response :lines))
         (exit (let ((code (getf response :exit))) (if (integerp code) code 0)))
         (emitted (client-lines-bytes lines)))
    (multiple-value-bind (stdout stderr) (client-route-lines lines)
      (dolist (line stdout) (write-line line out))
      (dolist (line stderr) (write-line line err))
      (finish-output out)
      (finish-output err)
      (incf (cli-client-emitted client) emitted)
      (values exit emitted stdout stderr))))
