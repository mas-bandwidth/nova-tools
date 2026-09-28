;;;; execution-reconcile.lisp --- `execution reconcile` and `execution status`
;;;; over the kernel's own controls.
;;;;
;;;; docs/SPEC-WORK.md:4002-4019 promises that `execution reconcile --control
;;;; <id> --from <manifest-id>` admits "a bounded, content-addressed manifest
;;;; whose records bind control, offer and attempt id, node generation, source
;;;; identity, observed handle, observation time, an outcome in `running`,
;;;; `paused`, `stopped`, `completed`, `not-started`, `unsupported`, `unknown`,
;;;; and result and usage references where present" (:4003-4006), that
;;;; "contradictory observations are preserved unresolved, never
;;;; last-write-wins" (:4009-4010), that "missing usage stays unknown" and that
;;;; "a stop report never synthesises zero cost" (:4010-4011), that
;;;; `not-started` "alone may release an unlaunched reservation" and only under
;;;; a durable launch rejection (:4011-4013), that "Confirmed termination
;;;; permits capacity reconciliation but bypasses no holder-only `release`"
;;;; (:4013-4016) and that "capacity held for an unknown execution is never
;;;; advertised free" (:4018). :3997-4000 gives `execution status --control
;;;; <id>` its bounded counts -- selected, pending-delivery, acknowledged,
;;;; confirmed, unsupported, unresolved -- "and one row per target under
;;;; `--max`". The output lines are the grammar's at :6020 (the mutation line,
;;;; whose `operation=` is "- for a release-hold and a reconcile"), :6021 (the
;;;; status counts), :6022 (`EXECUTION ROW`) and :6023 (`EXECUTION FAIL
;;;; control=<id> change=<c>: <reason>` -- exit 1, nothing written).
;;;;
;;;; What is new here over src/operations.lisp:1687 `reconcile-observations` --
;;;; the pure verdict model the `reconcile-preserves-contradiction` replay
;;;; drives -- is that the verb runs over real kernel state: the control is a
;;;; durable hold of src/control.lisp:173 `%install-control`, the targets are
;;;; that hold's own captured attempt identities, and the admitted manifest is
;;;; recorded in the kernel's journal BEFORE the verb acknowledges, exactly as a
;;;; hold's record is (SPEC-WORK.md:3962, src/control.lisp:15-18). The journal
;;;; is then the only store: what a reconcile retains is read back out of it, so
;;;; a close, a reopen and a replay answer from the record and never from a
;;;; survivor in the image, and "a later reconciliation records a new set and
;;;; keeps the earlier one, contradictions included" (:2879) is a property of
;;;; the append and not of a merge.
;;;;
;;;; The verdicts themselves are not re-derived: `stop-evidence-p`,
;;;; `not-started-qualifies-p`, `synthesised-usage` and `contradictory-attempts`
;;;; (src/operations.lisp:1644-1684) are called as they stand.
;;;;
;;;; Two readings this file takes where the paragraph does not spell one out,
;;;; stated here rather than buried:
;;;;
;;;;   1. A `running`, `paused` or `unknown` observation, and a `stopped` one
;;;;      that is not stop evidence, leave their target `unresolved`: :3999 says
;;;;      "an observation that times out prints unresolved, completes nothing",
;;;;      and none of those four settles the directive this control staged.
;;;;   2. `acknowledged=` is answered 0 by a reconcile alone. :3992 says
;;;;      "Delivery, acknowledgement and an observed pause or exit are three
;;;;      receipts"; this verb admits the third, and an acknowledgement is a
;;;;      receipt it never sees. A target with no record at all is
;;;;      `pending-delivery`.

(in-package #:nova-work)

(defparameter *observation-outcomes*
  '(:running :paused :stopped :completed :not-started :unsupported :unknown)
  "The seven outcomes a manifest record may carry (SPEC-WORK.md:4005-4006). An
eighth is refused rather than retained as evidence of something unnamed.")

(defparameter *manifest-bound* 64
  "The default bound on one manifest's records (SPEC-WORK.md:4003, \"a bounded,
content-addressed manifest\"). A manifest past its bound refuses before
anything is written, as an unrepresentable capture pin does at
src/control.lisp:185-191.")

(defparameter *observation-record-fields*
  '(:control :offer :attempt :generation :source :handle :observed-at :outcome
    :result :usage :evidence-quality :negative-lookup-p :identity-bound-p
    :launch-rejects-p :queue-miss-p)
  "The record's fields in one fixed order, so the content address of a manifest
is the records' bytes and not their plist order (SPEC-WORK.md:4003-4006). The
first eight are the bindings the paragraph names; `:result` and `:usage` are its
\"references where present\"; the rest is the provenance the verdicts at
src/operations.lisp:1644-1662 read.")

(defparameter *observation-required-fields*
  '(:control :attempt :outcome :observed-at :source)
  "What every record must bind to be admitted at all (SPEC-WORK.md:4004-4005).
An unbound record is refused; it is never retained as an anonymous fact.")

;;; ------------------------------------------------------------------
;;; The manifest: its canonical bytes and its content address.
;;; ------------------------------------------------------------------

(defun %canonical-field (value)
  "One field as restricted data. The printer of src/value.lisp:36 admits lists,
keywords, strings and integers only, so a record's booleans are written as the
keyword `:yes` and an absent field as `(:absent)` -- the three spellings of
SPEC-WORK.md:329-337 kept apart."
  (cond ((null value) +absent+)
        ((eq value t) :yes)
        (t value)))

(defun %plain-field (value)
  "The inverse of %CANONICAL-FIELD, so a record read back out of the journal is
the record that was admitted."
  (cond ((absentp value) nil)
        ((eq value :yes) t)
        (t value)))

(defun %observation-canonical (record)
  "RECORD's fields in *OBSERVATION-RECORD-FIELDS* order, so two manifests
carrying the same facts have the same bytes."
  (loop for field in *observation-record-fields*
        collect field
        collect (%canonical-field (getf record field))))

(defun %observation-plain (record)
  (loop for field in *observation-record-fields*
        collect field
        collect (%plain-field (getf record field))))

(defun observation-manifest-id (records)
  "The content address of a manifest: the sha256 of its records' canonical
bytes, in the order the manifest lists them (SPEC-WORK.md:4003)."
  (sha256-hex (canonical-string
               (list :manifest (mapcar #'%observation-canonical records)))))

(defun %record-unbound-field (record)
  "The first binding *OBSERVATION-REQUIRED-FIELDS* names that RECORD does not
carry, or NIL when the record is bound."
  (find-if (lambda (field) (null (getf record field))) *observation-required-fields*))

(defun %manifest-refusal (control records from bound)
  "The refusal a manifest takes before anything is written, or NIL. It is read
off the payload alone -- no control and no capacity is looked at here -- so the
answer is the same before and after any mutable precondition moves."
  (block refusal
    (flet ((fail (fmt &rest args)
             (return-from refusal
               (format nil "EXECUTION FAIL control=~A change=reconcile: ~A"
                       control (apply #'format nil fmt args)))))
      (when (null records)
        (fail "empty manifest"))
      (when (> (length records) bound)
        (fail "manifest span=~D exceeds bound=~D" (length records) bound))
      (loop for record in records
            for n from 1
            do (let ((missing (%record-unbound-field record)))
                 (when missing
                   (fail "record ~D unbound: no ~A" n
                         (string-downcase (symbol-name missing))))
                 (unless (member (observation-outcome record) *observation-outcomes*)
                   (fail "record ~D outcome=~A is not one of the seven" n
                         (let ((o (observation-outcome record)))
                           (if o (string-downcase (princ-to-string o)) "-"))))
                 (unless (equal (getf record :control) control)
                   (fail "record ~D control=~A is not this control" n
                         (getf record :control)))))
      (let ((address (observation-manifest-id records)))
        (when (and from (not (equal from address)))
          (fail "manifest=~A is not the content address of these records (~A)"
                from address)))
      nil)))

(defun %manifest-payload-digest (control manifest)
  "The payload a retry is deduplicated on: this control and the content address
of the records. It names nothing mutable, so the same request with the same
manifest answers `already applied` however far the controls have moved
(SPEC-WORK.md:315)."
  (sha256-hex (canonical-string (list :control control :manifest manifest))))

;;; ------------------------------------------------------------------
;;; The durable record. The journal is the store; the image keeps nothing.
;;; ------------------------------------------------------------------

(defun %reconcile-record-line (control manifest rev records)
  "One journal line per admitted manifest. `records=` is last and carries the
canonical s-expression, so the fielded head stays readable by the same `%field`
reader src/control.lisp:115 uses."
  (format nil "RECONCILE control=~A manifest=~A rev=~D records=~A"
          control manifest rev
          (canonical-string (mapcar #'%observation-canonical records))))

(defun %parse-reconcile-line (line)
  "The plist a `RECONCILE` journal line carries, or NIL when LINE is not one."
  (let ((head-marker " records="))
    (when (and (stringp line)
               (>= (length line) 10)
               (string= "RECONCILE " line :end2 10))
      (let ((marker (search head-marker line)))
        (when marker
          (let ((head (subseq line 0 marker))
                (body (subseq line (+ marker (length head-marker)))))
            (list :control (%field head "control")
                  :manifest (%field head "manifest")
                  :rev (parse-integer (%field head "rev"))
                  :records (mapcar #'%observation-plain (read-restricted body)))))))))

(defun control-reconciliations (kernel control)
  "Every manifest this control has admitted, oldest first, read back out of the
kernel's own journal. Nothing is merged and nothing is dropped: a later
reconciliation records a new set and keeps the earlier one, contradictions
included (SPEC-WORK.md:2879, :4009-4010)."
  (let ((out '()))
    (dolist (request (journal-order (kernel-journal kernel)))
      (multiple-value-bind (found digest line) (journal-lookup (kernel-journal kernel) request)
        (declare (ignore digest))
        (when (eq found t)
          (let ((record (%parse-reconcile-line line)))
            (when (and record (equal control (getf record :control)))
              (push record out))))))
    (nreverse out)))

(defun control-observations (kernel control)
  "The records every manifest of CONTROL retained, in acceptance order. This is
the retained evidence itself, read from the journal, so a close, a reopen and a
replay answer the same (SPEC-WORK.md:4002, \"retained with provenance\")."
  (loop for manifest in (control-reconciliations kernel control)
        append (getf manifest :records)))

(defun %records-for (retained attempt)
  "RETAINED's records about ATTEMPT, in acceptance order."
  (remove-if-not (lambda (o) (equal attempt (observation-attempt o))) retained))

;;; ------------------------------------------------------------------
;;; The dispositions the retained evidence supports.
;;;
;;; Each reader takes RETAINED, so one read of the journal answers a whole
;;; status and the cost of a status does not grow with the targets times the
;;; history (SPEC-WORK.md:6316-6321, the bounded-read promise).
;;; ------------------------------------------------------------------

(defun %settling-record-p (record)
  "True when RECORD settles its target: an observed stop that is stop evidence,
a completion, or a qualifying `not-started` (SPEC-WORK.md:4008-4013). A stop
that is silence, an expired lease, an elapsed estimate or an unbound negative
lookup settles nothing."
  (or (stop-evidence-p record)
      (eq (observation-outcome record) :completed)
      (not-started-qualifies-p record)))

(defun target-disposition (retained attempt &optional (contradictions
                                                        (contradictory-attempts retained)))
  "The disposition of one target under everything this control has retained
(SPEC-WORK.md:3997-3999, grammar :6022). Contradiction wins over every other
answer: it is preserved unresolved and never settled by the newest record."
  (let ((mine (%records-for retained attempt)))
    (cond
      ((null mine) :pending-delivery)
      ((member attempt contradictions :test #'equal) :unresolved)
      ((some #'%settling-record-p mine) :confirmed)
      ((some (lambda (o) (eq (observation-outcome o) :unsupported)) mine) :unsupported)
      (t :unresolved))))

(defun target-observed (retained attempt)
  "The outcome the latest record for ATTEMPT reports, or NIL where none does.
A contradiction is not settled by this: the disposition stays `unresolved` and
this is only what the row prints."
  (let ((mine (%records-for retained attempt)))
    (and mine (observation-outcome (car (last mine))))))

(defun target-observed-at (retained attempt)
  "The observation time the latest record for ATTEMPT carries."
  (let ((mine (%records-for retained attempt)))
    (and mine (getf (car (last mine)) :observed-at))))

(defun target-usage (retained attempt)
  "The usage the retained records carry for ATTEMPT, or `:unknown`. Missing
usage stays unknown and a stop report never synthesises zero cost
(SPEC-WORK.md:4010-4011)."
  (let* ((mine (%records-for retained attempt))
         (known (find-if (lambda (o) (not (eq (synthesised-usage o) :absent))) mine)))
    (if known (synthesised-usage known) :unknown)))

(defun %control-hold (kernel control)
  (find control (ctl-holds (kernel-controls kernel)) :key #'hold-id :test #'equal))

(defun %ctl-attempt (kernel attempt)
  (find attempt (ctl-attempts (kernel-controls kernel))
        :key #'ctl-attempt-id :test #'equal))

(defun %attempt-offer (kernel attempt)
  (find attempt (ctl-offers (kernel-controls kernel)) :key #'of-attempt :test #'equal))

(defun control-target-ids (kernel control &optional (retained (control-observations kernel control)))
  "The targets a status or a reconcile answers for: the identities the control
captured, plus any attempt a retained record names that the capture does not. A
late record about an earlier attempt attaches there (SPEC-WORK.md:4006-4007)
rather than being dropped for lying outside the pin."
  (let* ((hold (%control-hold kernel control))
         (captured (and hold (copy-list (hold-targets hold))))
         (extra (sort (remove-duplicates
                       (loop for o in retained
                             for attempt = (observation-attempt o)
                             unless (member attempt captured :test #'equal)
                               collect attempt)
                       :test #'equal)
                      #'string<)))
    (append captured extra)))

;;; ------------------------------------------------------------------
;;; Capacity: what a reconcile may and may not release.
;;; ------------------------------------------------------------------

(defun %releasable-attempts (kernel control)
  "The attempts a reconcile may release: a qualifying `not-started` alone, and
only where nothing contradicts it and the reservation was never launched
(SPEC-WORK.md:4011-4013). A confirmed termination is not here -- it permits
capacity reconciliation but bypasses no holder-only `release` (:4013-4016) --
and neither is an `unknown`, whose capacity is never advertised free (:4018)."
  (let ((retained (control-observations kernel control)))
    (remove-duplicates
     (loop for o in retained
           for attempt = (observation-attempt o)
           when (and (not-started-qualifies-p o)
                     (not (member attempt (contradictory-attempts retained) :test #'equal))
                     (let ((offer (%attempt-offer kernel attempt)))
                       (and offer (not (of-launched-p offer)))))
             collect attempt)
     :test #'equal)))

(defun %release-reservations (kernel control)
  "Release exactly the unlaunched reservations `not-started` qualifies, and
nothing else. No attempt is invented: only an offer that already exists is
touched, and every other offer's effect, lease and launch flags are left as they
were."
  (let ((released '()))
    (dolist (attempt (%releasable-attempts kernel control))
      (let ((offer (%attempt-offer kernel attempt)))
        (when (and offer (not (eq (of-effect offer) :released)))
          (setf (of-effect offer) :released)
          (push attempt released))))
    (nreverse released)))

;;; ------------------------------------------------------------------
;;; `execution reconcile`
;;; ------------------------------------------------------------------

(defun %reconcile-ok-line (kernel control rid manifest)
  "The mutation line of the grammar at :6020, less its `emitted=`, which is the
CLI's count of what it printed and there is no CLI in this slice
(src/kernel.lisp:16-19). `operation=` is `-`: :6020 says so for a reconcile."
  (declare (ignorable manifest))
  (format nil "EXECUTION OK id=~A request=~A control=~A change=reconcile scope=~A operation=- selected=~D rev=~D pushed=-"
          rid rid control
          (let ((hold (%control-hold kernel control)))
            (if hold (%scope-string (hold-scope hold)) "-"))
          (length (control-target-ids kernel control))
          (ctl-revision (kernel-controls kernel))))

(defun execution-reconcile (kernel &key control from records request
                                        (bound *manifest-bound*))
  "Admit one observation manifest for CONTROL. Answer (values OK-P LINE
EXIT-CODE ANSWER).

The order is the kernel's: the payload is validated and deduplicated before any
mutable precondition is read, the one receipt is recorded durably before
anything is installed, and a refusal writes nothing at all. FROM is the manifest
id, which is its content address; a FROM that is not these records' address
refuses (SPEC-WORK.md:4003)."
  (let* ((rid (or request (format nil "~A-reconcile-1" control)))
         (refusal (%manifest-refusal control records from bound)))
    ;; 1. The payload, alone.
    (when refusal
      (return-from execution-reconcile (values nil refusal 1 nil)))
    (let* ((manifest (observation-manifest-id records))
           (digest (%manifest-payload-digest control manifest)))
      ;; 2. Dedup, still before any mutable precondition (SPEC-WORK.md:315).
      (multiple-value-bind (found recorded-digest) (journal-lookup (kernel-journal kernel) rid)
        (cond
          ((eq found :unavailable)
           (return-from execution-reconcile
             (values nil (format nil "EXECUTION FAIL control=~A change=reconcile: dedup unavailable"
                                 control)
                     1 nil)))
          (found
           (return-from execution-reconcile
             (if (equal digest recorded-digest)
                 (values t (%reconcile-ok-line kernel control rid manifest) 0
                         (list :control control :request rid :manifest manifest
                               :replayed t :released '()))
                 (values nil (format nil "EXECUTION FAIL control=~A change=reconcile: request=~A reused with a different payload"
                                     control rid)
                         1 nil))))))
      ;; 3. The mutable precondition.
      (let ((hold (%control-hold kernel control)))
        (unless hold
          (return-from execution-reconcile
            (values nil (format nil "EXECUTION FAIL control=~A change=reconcile: no such control" control)
                    1 nil)))
        (let* ((rev (ctl-revision (kernel-controls kernel)))
               (line (%reconcile-record-line control manifest rev records)))
          ;; 4. One receipt, durably recorded before anything is installed.
          (multiple-value-bind (accepted reason)
              (journal-accept (kernel-journal kernel)
                              (list :request rid :digest digest :line line
                                    :rev rev :events '()))
            (unless accepted
              (return-from execution-reconcile
                (values nil (format nil "EXECUTION FAIL control=~A change=reconcile: ~A"
                                    control reason)
                        1 nil))))
          (journal-record (kernel-journal kernel) rid digest line rev))
        ;; 5. Install: the only capacity a reconcile moves.
        (let ((released (%release-reservations kernel control)))
          (values t (%reconcile-ok-line kernel control rid manifest) 0
                  (list :control control :request rid :manifest manifest
                        :selected (length (control-target-ids kernel control))
                        :retained (control-observations kernel control)
                        :released released)))))))

;;; ------------------------------------------------------------------
;;; `execution status --control <id> [--max <n>]`
;;; ------------------------------------------------------------------

(defun %status-row (kernel control attempt retained contradictions)
  (let ((offer (%attempt-offer kernel attempt))
        (a (%ctl-attempt kernel attempt)))
    (format nil "EXECUTION ROW control=~A node=~A offer=~A attempt=~A generation=~A disposition=~A observed=~A at=~A"
            control
            (or (and a (ctl-attempt-node a)) "-")
            (if offer (of-id offer) "-")
            attempt
            (or (and a (ctl-attempt-generation a)) "-")
            (string-downcase (symbol-name (target-disposition retained attempt contradictions)))
            (let ((observed (target-observed retained attempt)))
              (if observed (string-downcase (symbol-name observed)) "-"))
            (or (target-observed-at retained attempt) "-"))))

(defun execution-status (kernel &key control (max 20))
  "The bounded counts of SPEC-WORK.md:3997-3999 and one `EXECUTION ROW` per
target under `--max`. Answer (values OK-P LINE EXIT-CODE ROWS). No event and no
`id=` (grammar :6021); `acknowledged=` is 0 because a reconcile admits the
observation receipt and never the acknowledgement one (:3992). The journal is
read once for the whole answer."
  (let ((hold (%control-hold kernel control)))
    (unless hold
      (return-from execution-status
        (values nil (format nil "EXECUTION FAIL control=~A change=status: no such control" control)
                1 nil)))
    (let* ((retained (control-observations kernel control))
           (contradictions (contradictory-attempts retained))
           (targets (control-target-ids kernel control retained))
           (dispositions (mapcar (lambda (a) (target-disposition retained a contradictions))
                                 targets))
           (shown (min max (length targets)))
           (rows (loop for attempt in targets
                       repeat shown
                       collect (%status-row kernel control attempt retained contradictions))))
      (flet ((tally (kind) (count kind dispositions)))
        (values t
                (format nil "EXECUTION OK control=~A change=status selected=~D pending-delivery=~D acknowledged=~D confirmed=~D unsupported=~D unresolved=~D rev=~D pushed=- shown=~D"
                        control (length targets)
                        (tally :pending-delivery)
                        0
                        (tally :confirmed)
                        (tally :unsupported)
                        (tally :unresolved)
                        (ctl-revision (kernel-controls kernel))
                        shown)
                0
                rows)))))
