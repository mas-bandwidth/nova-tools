;;;; replays-applicable-delegation.lisp --- the pure part of the coordinator's
;;;; notes, the read-before-the-route `applicable`, the six delegation gates and
;;;; the machinery receipt bound to an exact head (docs/SPEC-WORK.md:4140-4470).
;;;;
;;;; Nothing here starts a session, a dispatch or a child: the functions are the
;;;; pure planning and booking layer the seven `applicable-*` / `delegation-*` /
;;;; `receipt-*` acceptance replays call. The live-session and CLI wiring that a
;;;; later slice owns (see the "owed:" lines in RESULT.md) is not in this slice.

(in-package #:nova-work)


;;; ------------------------------------------------------------------
;;; The coordinator's notes (SPEC-WORK.md:4184)
;;; ------------------------------------------------------------------

(defun note-field (v)
  "An absent note field is the restricted-data spelling (:absent)."
  (if (null v) +absent+ v))

(defun note-id (scope author date source kind text constraint uncertain)
  "Identity is the content: `note:` and the lowercase SHA-256 of the canonical
serialization of the eight fields, absent ones (:absent), in field order
(SPEC-WORK.md:4212)."
  (format nil "note:~A"
          (sha256-hex (canonical-string
                       (list :scope scope :author author :date date :source source
                             :kind kind :text text
                             :constraint (note-field constraint)
                             :uncertain (note-field uncertain))))))

(defun make-note (&key scope author date source kind text constraint uncertain)
  "One note record: every field written, an absent one (:absent)."
  (let ((constraint (note-field constraint))
        (uncertain (note-field uncertain)))
    (list :id (note-id scope author date source kind text constraint uncertain)
          :scope scope :author author :date date :source source :kind kind
          :text text :constraint constraint :uncertain uncertain)))

(defun note-scope-covers-p (note as groups)
  "True when the note's scope covers the reader: its own :coordinator, every
group it belongs to, or the shared :node (SPEC-WORK.md:4334)."
  (let ((scope (getf note :scope)))
    (ecase (first scope)
      (:coordinator (equal (second scope) as))
      (:group (member (second scope) groups :test #'equal))
      (:node t))))


;;; ------------------------------------------------------------------
;;; Constraint evaluation (SPEC-WORK.md:4301)
;;; ------------------------------------------------------------------

(defun candidate-role (candidate)
  (let ((p (position #\/ candidate)))
    (if p (subseq candidate 0 p) candidate)))

(defun candidate-model (candidate)
  (let ((p (position #\/ candidate)))
    (if p (subseq candidate (1+ p)) "")))

(defun constraint-p (form)
  (and (consp form) (eq (first form) :constraint)))

(defun constraint-axis (constraint key)
  (let ((form (assoc key (rest constraint))))
    (and form (rest form))))

(defun constraint-deny (constraint) (constraint-axis constraint :deny))
(defun constraint-prefer (constraint) (constraint-axis constraint :prefer))
(defun constraint-reason (constraint)
  (let ((form (assoc :reason (rest constraint))))
    (and form (second form))))

(defun candidate-axis-value (candidate task-class axis)
  (ecase axis
    (:role (candidate-role candidate))
    (:model (candidate-model candidate))
    (:task-class task-class)))

(defun deny-matches-p (deny candidate task-class)
  "A :deny excludes only when the candidate matches every named axis
(SPEC-WORK.md:4311)."
  (every (lambda (clause)
           (member (candidate-axis-value candidate task-class (first clause))
                   (rest clause) :test #'equal))
         deny))

(defun constraint-excludes-p (constraint candidate task-class)
  (let ((deny (constraint-deny constraint)))
    (and deny (deny-matches-p deny candidate task-class))))


;;; ------------------------------------------------------------------
;;; applicable: the read before the route (SPEC-WORK.md:4329)
;;; ------------------------------------------------------------------

(defstruct (applicable-answer
             (:constructor make-applicable-answer (&key rev from verdicts notes fail)))
  rev from verdicts notes fail)

(defun verdict-eligible-p (v) (eq v :eligible))
(defun verdict-unknown-p (v) (eq v :unknown))
(defun verdict-planning-p (v) (eq v :planning))
(defun verdict-excluded-p (v) (and (consp v) (eq (first v) :excluded)))
(defun excluded-note-ids (v) (rest v))

(defun model-registered-p (model models)
  "MODELS is the model catalog; the symbol T means \"every model registered\",
so no model is unknown when the registry is not consulted."
  (if (eq models t) t (member model models :test #'equal)))

(defun applicable-load-error (source notes-index snapshot-bound-ok)
  "The reason the complete set cannot be loaded, or NIL when it can
(SPEC-WORK.md:4346)."
  (cond
    ((not notes-index) "notes index unloadable")
    ((eq source :none) "no live session and no snapshot")
    ((and (eq source :snapshot) (not snapshot-bound-ok)) "snapshot past a bound")
    (t nil)))

(defun candidate-verdict (candidate task-class notes models)
  "Unknown is not eligible, then a :deny excludes; otherwise eligible."
  (unless (model-registered-p (candidate-model candidate) models)
    (return-from candidate-verdict :unknown))
  (let ((excluded '()))
    (dolist (note notes)
      (let ((constraint (getf note :constraint)))
        (when (and (constraint-p constraint)
                   (constraint-excludes-p constraint candidate task-class))
          (push (getf note :id) excluded))))
    (if excluded (cons :excluded (nreverse excluded)) :eligible)))

(defun snapshot-downgrade (verdict)
  "A snapshot answer is read-only planning: an eligible row downgrades to
:planning (SPEC-WORK.md:4351)."
  (if (verdict-eligible-p verdict) :planning verdict))

(defun applicable (as task-class candidates notes
                   &key (rev 0) (source :live) (notes-index t) (snapshot-bound-ok t)
                        (models t) (groups nil))
  "Evaluate every active note whose scope covers AS against every candidate,
header first, and answer one verdict per candidate. A load failure answers NOTES
FAIL with no verdict; a snapshot answer admits no route."
  (let ((fail (applicable-load-error source notes-index snapshot-bound-ok)))
    (when fail
      (return-from applicable
        (make-applicable-answer :rev rev :from source :verdicts '() :notes '() :fail fail))))
  (let ((app-notes (remove-if-not (lambda (n) (note-scope-covers-p n as groups)) notes))
        (verdicts '()))
    (dolist (candidate candidates)
      (let ((v (candidate-verdict candidate task-class app-notes models)))
        (when (eq source :snapshot) (setf v (snapshot-downgrade v)))
        (push (cons candidate v) verdicts)))
    (make-applicable-answer :rev rev :from source
                            :verdicts (nreverse verdicts) :notes app-notes :fail nil)))


;;; ------------------------------------------------------------------
;;; Route selection and the card builder (SPEC-WORK.md:4331)
;;; ------------------------------------------------------------------

(defun price-route (candidate price-table)
  (cdr (assoc (candidate-model candidate) price-table :test #'equal)))

(defun route-selection (as task-class candidates notes price-table
                        &key (rev 0) (source :live) (notes-index t) (snapshot-bound-ok t)
                             (models t) (groups nil))
  "Call applicable first, then price only the routes that came back eligible."
  (let* ((answer (applicable as task-class candidates notes
                             :rev rev :source source :notes-index notes-index
                             :snapshot-bound-ok snapshot-bound-ok
                             :models models :groups groups))
         (priced '()))
    (unless (applicable-answer-fail answer)
      (dolist (candidate candidates)
        (let ((v (cdr (assoc candidate (applicable-answer-verdicts answer) :test #'equal))))
          (when (verdict-eligible-p v)
            (push (cons candidate (price-route candidate price-table)) priced)))))
    (values answer (nreverse priced))))

(defun build-card (candidate verdict)
  "A card builder handed an excluded or unknown route refuses, naming the note id
or the reason (SPEC-WORK.md:4363)."
  (cond
    ((verdict-eligible-p verdict) (values (list :card candidate) nil))
    ((verdict-unknown-p verdict)
     (values nil "unknown route: model not registered"))
    ((verdict-excluded-p verdict)
     (values nil (format nil "excluded route: note ~A" (excluded-note-ids verdict))))
    (t (values nil (format nil "not eligible: ~A is planning only" candidate)))))


;;; ------------------------------------------------------------------
;;; The six gates of a delegation (SPEC-WORK.md:4378)
;;; ------------------------------------------------------------------

(defparameter *delegation-packet-fields*
  '(:objective :source-revision :criteria :scope :result-contract :checkpoint :effort)
  "Gate 2: the seven fields a bounded packet must carry (rule 1).")

(defun present-p (v)
  (and v (not (and (stringp v) (string= "" v)))))

(defun gate-2-refusal (packet)
  (dolist (field *delegation-packet-fields*)
    (unless (present-p (getf packet field))
      (return (format nil "gate 2: packet lacks ~A"
                      (string-downcase (symbol-name field)))))))

(defun gate-3-refusal (dispatch-cost spent-today ceiling)
  (when (and dispatch-cost ceiling (> (+ dispatch-cost spent-today) ceiling))
    (format nil "gate 3: dispatch would cross daily spend ceiling ~D" ceiling)))

(defun delegation-admission (packet &key (spent-today 0) ceiling dispatch-cost)
  "Admission gates 2 and 3; each refusal is by named gate and reason at exit 2."
  (let ((refusal (gate-2-refusal packet)))
    (when refusal
      (return-from delegation-admission
        (values nil (format nil "DELEGATION FAIL: ~A" refusal) 2))))
  (let ((refusal (gate-3-refusal dispatch-cost spent-today ceiling)))
    (when refusal
      (return-from delegation-admission
        (values nil (format nil "DELEGATION FAIL: ~A" refusal) 2))))
  (values t "DELEGATION OK" 0))

(defun admission-expect (answer)
  "The revision the admission write carries as --expect (SPEC-WORK.md:4356)."
  (applicable-answer-rev answer))

(defun admission (&key expect current-rev)
  "The admission write; a stop or deny written between the check and the write
refuses it stale."
  (if (= expect current-rev)
      (values t "ADMIT OK" 0)
      (values nil (format nil "ADMIT FAIL: stale (expect rev ~D, now ~D)" expect current-rev) 2)))


;;; ------------------------------------------------------------------
;;; Execution and result gates (SPEC-WORK.md:4390)
;;; ------------------------------------------------------------------

(defun gate-4-refusal (recipient-state)
  (when (member recipient-state '(:asleep :unknown))
    (format nil "gate 4: recipient reads ~A"
            (string-downcase (symbol-name recipient-state)))))

(defun offer-to (recipient-state)
  "Gate 4: a live recipient is required; an offer to an asleep or unknown friend
is refused."
  (let ((refusal (gate-4-refusal recipient-state)))
    (if refusal
        (values nil (format nil "OFFER FAIL: ~A" refusal) 2)
        (values t "OFFER OK" 0))))

(defun non-terminating-p (outcome)
  "A timeout is not proof of termination (rule 3)."
  (not (eq outcome :terminated)))

(defun uncertain-outcome-p (outcome)
  (eq outcome :unknown))

(defun retry-permitted-p (outcome)
  "No second attempt is ever started silently after uncertainty about the first
(rule 3)."
  (not (uncertain-outcome-p outcome)))

(defstruct (execution-record
             (:constructor make-execution-record (&key requested-limit expiry stop)))
  requested-limit expiry stop)


;;; ------------------------------------------------------------------
;;; Machinery receipts at an exact head (SPEC-WORK.md:4395)
;;; ------------------------------------------------------------------

(defstruct (machinery-receipt
             (:constructor make-machinery-receipt (&key head result)))
  head result)

(defun book-receipt (head result)
  "A child's result is booked as a machinery receipt at the exact head it ran
against, by no model call (rule 4)."
  (make-machinery-receipt :head head :result result))

(defun review-binds-p (verdict head)
  "A review verdict binds to --head <sha>."
  (equal (getf verdict :head) head))

(defun review-reusable-p (verdict head &key (rebase-checked-p t) (content-unchanged-p t))
  "A verdict binds to one head and is reused only while that head, its reviewed
content and its dependencies are unchanged; never across a changed head or an
unchecked rebase (rule 5)."
  (and (review-binds-p verdict head) rebase-checked-p content-unchanged-p))

;;; ------------------------------------------------------------------
;;; CONFIG roles (SPEC-WORK.md:5214)
;;;
;;; A role is a CONFIG record. It is read from CONFIG and never inferred from
;;; the underlying model: no function lets a model capability confer a role. A
;;; reserved role is spent only on the work class it was reserved for; routine
;;; work is refused by name, and a model capability never cancels or raises an
;;; agreed limit.
;;; ------------------------------------------------------------------

(defstruct (role-record
             (:constructor make-role-record
                 (&key id scope source reserved-for essential-security-only-p
                       different-perspective-p participation-p limit)))
  id scope source reserved-for essential-security-only-p
  different-perspective-p participation-p limit)

(defstruct (role-config (:constructor make-role-config (&key (roles '()))))
  roles)

(defun role-from-config (config role-id)
  "Read ROLE-ID from CONFIG, or NIL. The model is never consulted."
  (find role-id (role-config-roles config)
        :key #'role-record-id :test #'equal))

(defun role-inferred-from-model-p (role-id)
  "A model capability never confers a role, so no role is ever inferred."
  (declare (ignore role-id))
  nil)

(defun agreed-limit-for (config role-id model-capability)
  "The limit CONFIG agrees for ROLE-ID. A model capability never cancels or
raises it, so it does not enter the answer."
  (declare (ignore model-capability))
  (let ((record (role-from-config config role-id)))
    (and record (role-record-limit record))))

(defun spend-role (config role-id work-class)
  "Spend ROLE-ID on WORK-CLASS. A role CONFIG does not hold is refused; an
unreserved role is spent freely; a reserved role is spent only on the class it
was reserved for. Returns (VALUES T NIL) or (VALUES NIL REASON)."
  (let ((record (role-from-config config role-id)))
    (cond
      ((null record)
       (values nil (format nil "no role ~(~A~) in CONFIG" role-id)))
      ((null (role-record-reserved-for record))
       (values t nil))
      ((eq work-class (role-record-reserved-for record))
       (values t nil))
      (t
       (values nil (format nil "role ~(~A~) is reserved for ~(~A~), not ~(~A~)"
                           role-id (role-record-reserved-for record) work-class))))))
