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
