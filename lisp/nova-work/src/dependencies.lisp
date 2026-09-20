;;;; dependencies.lisp --- the needs/blocks edges (nova-tools #785).
;;;;
;;;; SPEC-WORK.md:850-886 -- `:deps` is a reference edge: needed, not owned,
;;;; not counted, and validated for existence and for dependency state. The
;;;; forward edge is a node's `:deps`; the reverse edge is its DEPENDENTS slot,
;;;; built once by rule 2 when the seed is published and updated by the same
;;;; write path that moves a need's branch. It is what `what does this block`
;;;; reads, so the answer is one bounded lookup and never a walk of O
;;;; (SPEC-WORK.md:709, the reverse-dependency index).
;;;;
;;;; SPEC-WORK.md:2110-2114 -- a node is ready only when every need is terminal
;;;; accepted, and a dependent whose need is open is not ready. The same gate
;;;; blocks the dependent's transition to doing (:1885) and the parent's
;;;; settlement to green (:1951, "A parent is green only when every required
;;;; child and every dependency gate is satisfied").

(in-package #:nova-work)

(defun node-dependents (state id)
  "The reverse-dependency index: the ids whose `:deps` name ID, in the order
they were seeded. One slot read -- SPEC-WORK.md:709, `what does this block` is
one bounded indexed access for the one id it needs, never a walk of the set."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (copy-list (wnode-dependents n))))

(defun %dependency-blocker (state id)
  "The first need of ID that is not terminal accepted, or NIL when every need is
settled. The dependency gate of SPEC-WORK.md:1885 and :2110: an unmet need
blocks the dependent, and the refusal names the blocker."
  (let ((n (%node-quiet state id)))
    (and n
         (find-if-not (lambda (dep) (%need-terminal-p state dep))
                      (wnode-deps n)))))

(defun %needs-settled-p (state id)
  "True when every need of ID is terminal accepted."
  (let ((n (%node-quiet state id)))
    (and n (every (lambda (dep) (%need-terminal-p state dep))
                  (wnode-deps n)))))

;;; ------------------------------------------------------------------
;;; `dep` -- the runtime dependency add/remove verb (E03-F03,
;;; SPEC-WORK.md:2929, :987).
;;; ------------------------------------------------------------------

(defun %dep-reachable-p (state from target)
  "True when TARGET is reachable from FROM by following :deps edges. Bounded by
the node count, so a cycle is detected rather than followed forever."
  (let ((visited (make-hash-table :test #'equal)))
    (labels ((walk (cur)
               (when (gethash cur visited) (return-from walk nil))
               (setf (gethash cur visited) t)
               (when (equal cur target) (return-from walk t))
               (let ((n (%node-quiet state cur)))
                 (and n (some (lambda (d) (walk d)) (wnode-deps n))))))
      (walk from))))

(defun dep (kernel id &key add remove (reason "deps") request)
  "`dep --add ... --remove ...` moves :deps reference edges (SPEC-WORK.md:987,
:850-886). Each added need must exist and must not close a cycle, each removed
need is dropped from the reverse index, and the whole change is validated before
anything is written. It owns the required-set sibling of `node require`: the
`:deps` field, which was set at seed time only. Answers (values OK-P LINE EXIT)."
  (declare (ignore reason))
  (let* ((state (kernel-state kernel))
         (node (%node-quiet state id)))
    (unless node
      (return-from dep (values nil (format nil "DEP FAIL node=~A: no such node" id) 1)))
    (let ((add (remove-if-not #'stringp (or add '())))
          (remove (remove-if-not #'stringp (or remove '()))))
      (dolist (d add)
        (unless (plusp (length d))
          (return-from dep (values nil (format nil "DEP FAIL node=~A: bad dependency id" id) 1)))
        (unless (%node-quiet state d)
          (return-from dep
            (values nil (format nil "DEP FAIL node=~A: no such dependency ~A" id d) 1)))
        (when (equal d id)
          (return-from dep (values nil (format nil "DEP FAIL node=~A: self-dependency" id) 1)))
        (when (%dep-reachable-p state d id)
          (return-from dep
            (values nil (format nil "DEP FAIL node=~A: dependency cycle through ~A" id d) 1))))
      (let* ((current (copy-list (wnode-deps node)))
             (new (copy-list current)))
        (dolist (d remove) (setf new (remove d new :test #'equal)))
        (dolist (d add) (unless (member d new :test #'equal) (setf new (append new (list d)))))
        ;; Maintain the reverse-dependency index for the edges that changed.
        (dolist (d (set-difference current new :test #'equal))
          (let ((tgt (%node-quiet state d)))
            (when tgt
              (setf (wnode-dependents tgt)
                    (remove id (wnode-dependents tgt) :test #'equal)))))
        (dolist (d (set-difference new current :test #'equal))
          (let ((tgt (%node-quiet state d)))
            (when (and tgt (not (member id (wnode-dependents tgt) :test #'equal)))
              (setf (wnode-dependents tgt)
                    (append (wnode-dependents tgt) (list id))))))
        (setf (wnode-deps node) new)
        (values t (format nil "DEP OK id=~A request=~A changed=~D deps=~D"
                          id (or request "-")
                          (if (equal current new) 0 1) (length new))
                0)))))


;;; ------------------------------------------------------------------
;;; folded from replays-8650.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8650.lisp --- the pure model behind the five #362 acceptance
;;;; replays: schema evolution (:6247), the shared prerequisite owned once
;;;; (:6365), the single-writer command loop (:2616, :6083), the source
;;;; inventory and its reconciliation (:6228), and the three separately
;;;; labelled cost values (:5770, :4642).
;;;;
;;;; Like src/replays-applicable-delegation.lisp, this is a pure layer: no
;;;; session, socket, provider or live import is started here.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; schema-evolution                            SPEC-WORK.md:6247
;;; ------------------------------------------------------------------

(defparameter *supported-source-schemas* '("work-v1"))
(defparameter *current-schema* "work-v2")

(defun migrate-state-schema (form)
  "Migrate FORM's declared source schema to the current one. The body is copied,
so the only source copy is never rewritten; an unsupported source version
refuses before anything is produced (SPEC-WORK.md:6247)."
  (let ((schema (getf form :schema)))
    (unless (and (stringp schema)
                 (member schema *supported-source-schemas* :test #'string=))
      (error 'unsupported-input
             :what (format nil "schema ~A is not a supported source schema"
                           (if schema schema "-"))))
    (list* :schema *current-schema*
           :migration (list :from schema :to *current-schema*)
           (copy-tree (cddr form)))))

;;; ------------------------------------------------------------------
;;; shared-prerequisite-owned-once              SPEC-WORK.md:6365
;;; ------------------------------------------------------------------

(defparameter *prerequisite-obligations*
  '(:merged-fix :verified-behaviour :published-distribution))

(defun declare-shared-prerequisite (id owner)
  "Register a shared prerequisite with one owner and the three evidence
obligations of :6365 beside feature completion."
  (list :id id :owner owner :cells '()
        :obligations (copy-list *prerequisite-obligations*)))

(defun own-shared-prerequisite (prereq owner)
  "A shared prerequisite is owned once: a second, different owner is refused and
the existing owner is left standing."
  (let ((current (getf prereq :owner)))
    (when (and current (not (string= current owner)))
      (error 'unsupported-input
             :what (format nil "~A is already owned by ~A"
                           (getf prereq :id) current))))
  prereq)

(defun reference-shared-prerequisite (prereq cell)
  "Reference the one prerequisite from an affected cell; the same cell is
counted once however often it references."
  (unless (getf prereq :owner)
    (error 'unsupported-input
           :what (format nil "~A has no owner" (getf prereq :id))))
  (let ((cells (getf prereq :cells)))
    (unless (member cell cells :test #'string=)
      (setf (getf prereq :cells) (append cells (list cell)))))
  prereq)

(defun prerequisite-owner (prereq) (getf prereq :owner))
(defun prerequisite-cells (prereq) (getf prereq :cells))
(defun prerequisite-obligations (prereq) (getf prereq :obligations))

;;; ------------------------------------------------------------------
;;; single-writer                               SPEC-WORK.md:6241, :6083
;;; ------------------------------------------------------------------

(defstruct (command-loop (:constructor make-command-loop (&key kernel)))
  kernel order sequence)

(defun command-loop-submit (loop request)
  "The one command thread applies REQUEST in order. Only an accepted command
enters the total order the journal shows."
  (multiple-value-bind (okp line code envelope) (submit (command-loop-kernel loop) request)
    (declare (ignore code))
    (when okp
      (unless (getf envelope :replayed)
        (setf (command-loop-order loop)
              (append (command-loop-order loop)
                      (list (getf request :request))))
        (setf (command-loop-sequence loop)
              (append (command-loop-sequence loop)
                      (list (1+ (length (command-loop-sequence loop))))))))
    (values okp line)))

(defun mutate-outside-command-loop (kernel request)
  "Validator rule of :2620 -- a mutation outside the command loop is a defect.
It is refused rather than applied behind the one writer's back."
  (declare (ignore kernel request))
  (error 'unsupported-input
         :what "a mutation outside the command loop is a defect"))

;;; ------------------------------------------------------------------
;;; source-inventory                            SPEC-WORK.md:6228
;;; ------------------------------------------------------------------

(defun inventory-record (id kind &key original mapping unresolved)
  "One captured source record: a preserved original plus a normalised mapping,
or an explicit unresolved entry, never neither."
  (list :id id :kind kind
        :original (if original original +absent+)
        :mapping (if mapping mapping +absent+)
        :unresolved (if unresolved unresolved +absent+)))

(defun record-resolved-p (record)
  (and (not (absentp (getf record :original)))
       (not (absentp (getf record :mapping)))))

(defun record-unresolved-p (record)
  (not (absentp (getf record :unresolved))))

(defun %inventory-content (record)
  (list (getf record :kind) (getf record :original)
        (getf record :mapping) (getf record :unresolved)))

(defun reconcile-inventory (authoritative captured)
  "Reconcile CAPTURED against AUTHORITATIVE. Equal counts with different ids or
different content fail; only the same records in the same shape reconcile."
  (unless (= (length authoritative) (length captured))
    (return-from reconcile-inventory
      (values nil (format nil "INVENTORY FAIL: count mismatch authoritative=~D captured=~D"
                          (length authoritative) (length captured)))))
  (dolist (record authoritative)
    (let ((other (find (getf record :id) captured
                       :key (lambda (r) (getf r :id)) :test #'string=)))
      (unless other
        (return-from reconcile-inventory
          (values nil (format nil "INVENTORY FAIL: id mismatch, captured lacks ~A"
                              (getf record :id)))))
      (unless (equal (%inventory-content record) (%inventory-content other))
        (return-from reconcile-inventory
          (values nil (format nil "INVENTORY FAIL: content mismatch at ~A"
                              (getf record :id)))))))
  (values t (format nil "INVENTORY OK count=~D" (length authoritative))))

;;; ------------------------------------------------------------------
;;; subscription-is-not-free-reference-cost     SPEC-WORK.md:5770, :4642
;;; ------------------------------------------------------------------

(defun cost-breakdown (&key measured-cash marginal-cash reference-tokens
                            subscription-p (priced-p t))
  "Measured provider cash, estimated marginal cash and virtual reference token
cost as three separately labelled values. A subscription changes the cash
label; it does not make reference token cost zero (SPEC-WORK.md:4642)."
  (declare (ignore subscription-p))
  (list :measured-cash (if priced-p (if measured-cash measured-cash +absent+) +absent+)
        :marginal-cash (if marginal-cash marginal-cash +absent+)
        :reference-tokens (if reference-tokens reference-tokens +absent+)))

(defun subscription-covers-cash-p (breakdown)
  "True when the cash and the reference token cost are kept as separate labels,
so a covered cash charge is never read as a zero reference cost."
  (and (not (absentp (getf breakdown :measured-cash)))
       (not (absentp (getf breakdown :reference-tokens)))
       (not (eql (getf breakdown :measured-cash)
                 (getf breakdown :reference-tokens)))))
