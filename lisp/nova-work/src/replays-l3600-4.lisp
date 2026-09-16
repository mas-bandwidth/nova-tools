;;;; replays-l3600-4.lisp --- pure functions and records for the seven named
;;;; acceptance replays of docs/SPEC-WORK.md lines 3600-99999 (batch 4):
;;;;
;;;;   goal-update-writes-only-existing-kinds   SPEC-WORK.md:5815
;;;;   state-export-refuses-a-gap               SPEC-WORK.md:5759
;;;;   subscription-is-not-free-reference-cost  SPEC-WORK.md:5618
;;;;   unchanged-config-is-one-bounded-answer   SPEC-WORK.md:5615
;;;;   undo-redo                                SPEC-WORK.md:6043
;;;;   unknown-price-is-not-zero                SPEC-WORK.md:4534
;;;;   unrelated-receipts-stay-reusable         SPEC-WORK.md:4804
;;;;
;;;; This file holds the pure functions and records the acceptance replays call.
;;;; It owns no state and no I/O. Where a replay names behaviour that belongs in
;;;; the resident kernel, session or state slices, the pure shape is proven here
;;;; and the wiring owed to those slices is listed in RESULT.md, one line each.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; goal-update-writes-only-existing-kinds       SPEC-WORK.md:5815
;;; ------------------------------------------------------------------

(defstruct goal-node
  "A node of O in a goal scope: its state, its generation (for the :evidence
event), and its scope revision (which `accept --add` moves and `goal update`
never does)."
  id state generation scope-revision)

(defparameter *goal-kind-field-lists*
  '((:transition :to :reason :blocked-by :evidence)      ; SPEC-WORK.md:994-995
    (:evidence :pointer :criterion :against :generation :attempt) ; :996-998
    (:goal :change :scope :goal :reason))                ; SPEC-WORK.md:1054
  "The ordered field list of every kind a goal form may write. `goal update`
writes only :transition or :evidence; `goal set` writes only :goal.")

(defun kind-field-list (kind)
  (let ((row (assoc kind *goal-kind-field-lists*)))
    (rest row)))

(defun fields-write-exactly-kind (kind fields)
  "FIELDS is a plist whose keys, in the plist's own order, equal the ordered
field list of KIND -- nothing missing and no extra field."
  (equal (loop for key in fields by #'cddr collect key)
         (kind-field-list kind)))

(defparameter *goal-transition-edges*
  '((:doing            . (:todo :blocked :review :unknown))
    (:blocked          . (:todo :doing :unknown))
    (:cancel-requested . (:todo :doing :blocked :unknown)))
  "The reviewed incoming edges each `goal update` form reaches, by the transition
table. No goal update adds an edge and none removes one.")

(defun goal-edge-admitted-p (to from-state)
  (member from-state (cdr (assoc to *goal-transition-edges*))))

(defun goal-update-event (node change &key text pointer criterion against blocked-by reason)
  "Answer (values kind fields refusal): the one event `goal update --<change>`
writes on NODE and on no other. KIND is :transition or :evidence, never :goal;
REFUSAL is NIL or the named 'no edge' reason, in which case nothing is written."
  (let ((state (goal-node-state node)))
    (ecase change
      (:progress
       (if pointer
           (values :evidence
                   (list :pointer pointer :criterion criterion :against against
                         :generation (goal-node-generation node) :attempt +absent+)
                   nil)
           (if (goal-edge-admitted-p :doing state)
               (values :transition
                       (list :to :doing :reason text :blocked-by +absent+ :evidence +absent+)
                       nil)
               (values nil nil (format nil "no edge from ~A to doing" state)))))
      (:blocked
       (if (goal-edge-admitted-p :blocked state)
           (values :transition
                   (list :to :blocked :reason reason :blocked-by blocked-by :evidence +absent+)
                   nil)
           (values nil nil (format nil "no edge from ~A to blocked" state))))
      (:stop
       (if (goal-edge-admitted-p :cancel-requested state)
           (values :transition
                   (list :to :cancel-requested :reason reason :blocked-by +absent+ :evidence +absent+)
                   nil)
           (values nil nil (format nil "no edge from ~A to cancel-requested" state)))))))

(defun goal-set-event (change scope goal reason)
  "The one `:goal` event `goal set` or `goal set --clear` writes. Its subject is
a scope and not a node, so `:node` is (:absent); `:goal` is the node id on :set
and (:absent) on :clear."
  (list :change change :scope scope
        :goal (if (eq change :clear) +absent+ goal)
        :reason reason))

;;; ------------------------------------------------------------------
;;; state-export-refuses-a-gap                     SPEC-WORK.md:5759
;;; ------------------------------------------------------------------

(defstruct export-member
  "One member of an export manifest. CONTENT is a restricted s-expression, the
:absent marker when the member is gone, or raw text for a corrupt s-expression.
DIGEST, where present, is the declared SHA-256 the content must match."
  path kind content digest required-p symlink-p)

(defun split-path (path)
  (let ((parts '()) (start 0) (len (length path)))
    (loop
      (let ((slash (position #\/ path :start start)))
        (let ((component (subseq path start (if slash slash len))))
          (when (plusp (length component)) (push component parts))
          (unless slash (return)))
        (setf start (1+ slash))))
    (nreverse parts)))

(defun path-escapes-root-p (path)
  "An absolute path or a `..` component escapes the export's permitted root."
  (or (and (plusp (length path)) (char= (char path 0) #\/))
      (member ".." (split-path path) :test #'string=)))

(defun export-refusals (members &key max-bytes)
  "Answer the list of (kind path) refusals against MEMBERS, and NIL when the
export has no gap and nothing to refuse: a missing mandatory member, a changed
digest, a dangling internal reference, a path escape, a symlink, an output
overrun and a corrupt s-expression are each refused with no valid load."
  (let ((refusals '())
        (ids (mapcar #'export-member-path members)))
    (dolist (member members (nreverse refusals))
      (let ((path (export-member-path member))
            (content (export-member-content member)))
        (when (and (export-member-required-p member) (absentp content))
          (push (list "missing-mandatory-member" path) refusals))
        (when (and (stringp (export-member-digest member))
                   (not (absentp content))
                   (not (string= (export-member-digest member)
                                 (sha256-hex (canonical-string content)))))
          (push (list "changed-digest" path) refusals))
        (when (and (consp content) (eq (car content) :ref)
                   (not (member (second content) ids :test #'equal)))
          (push (list "dangling-reference" path) refusals))
        (when (path-escapes-root-p path)
          (push (list "path-escape" path) refusals))
        (when (export-member-symlink-p member)
          (push (list "symlink" path) refusals))
        (when (and max-bytes (> (length (canonical-string content)) max-bytes))
          (push (list "output-overrun" path) refusals))
        (when (stringp content)
          (handler-case (read-restricted content)
            (error () (push (list "corrupt-s-expression" path) refusals))))))))

(defun historical-proof-gap (historical-p resolver-observations-available-p)
  "A historical export whose resolver observations are gone is refused with a
named proof gap and is never answered from current observations."
  (when (and historical-p (not resolver-observations-available-p))
    (list :proof-gap :resolver-observations-gone)))

;;; ------------------------------------------------------------------
;;; subscription-is-not-free-reference-cost         SPEC-WORK.md:5618
;;; ------------------------------------------------------------------

(defstruct cost-value
  "One of a model route's three separately labelled cost values."
  label amount)

(defun cost-values (measured-cash estimated-marginal-cash reference-token-cost)
  (list (make-cost-value :label :measured-cash :amount measured-cash)
        (make-cost-value :label :estimated-marginal-cash :amount estimated-marginal-cash)
        (make-cost-value :label :reference-token-cost :amount reference-token-cost)))

(defun three-costs-separately-labelled-p (values)
  "Measured provider cash, estimated marginal cash and virtual reference token
cost are three labelled values and never collapse into one."
  (= 3 (length (remove-duplicates (mapcar #'cost-value-label values)))))

(defun reference-token-cost (values)
  (cost-value-amount (find :reference-token-cost values :key #'cost-value-label)))

(defun reference-cost-kept-under-subscription (values billing-mode)
  "A subscription is a billing mode; it does not make reference cost zero. Under
subscription the reference token cost stays whatever the route's token volume
implies, and a genuinely absent reference cost is not fabricated."
  (if (eq billing-mode :subscription)
      (plusp (reference-token-cost values))
      t))

;;; ------------------------------------------------------------------
;;; unknown-price-is-not-zero                        SPEC-WORK.md:4534
;;; ------------------------------------------------------------------

(defun resolved-price (value)
  "A missing or unsupported price dimension resolves to :unknown, and is never
read as zero."
  (cond ((null value) :unknown)
        ((eq value :unsupported) :unknown)
        (t value)))

;;; ------------------------------------------------------------------
;;; unchanged-config-is-one-bounded-answer          SPEC-WORK.md:5615
;;; ------------------------------------------------------------------

(defstruct config-identity
  "A config's revision and content hash, the identity an UNCHANGED answer names."
  revision content-hash)

(defun config-unchanged-p (requested current)
  (and (equal (config-identity-revision requested) (config-identity-revision current))
       (equal (config-identity-content-hash requested) (config-identity-content-hash current))))

(defun config-answer (requested current)
  "UNCHANGED with the identity when revision and hash are equal; otherwise a
bounded delta against the exact named base, never a repeated full prose manifest."
  (if (config-unchanged-p requested current)
      (list :answer :unchanged
            :revision (config-identity-revision current)
            :hash (config-identity-content-hash current))
      (list :answer :delta :base (config-identity-revision requested))))

;;; ------------------------------------------------------------------
;;; undo-redo                                      SPEC-WORK.md:6043
;;; ------------------------------------------------------------------

(defparameter *reversible-verbs*
  '(:node-add :node-require :decompose :accept :dep :axis :node-edit :node-move
    :roadmap-create :roadmap-configure :roadmap-row :roadmap-projection :prioritise
    :cell :responsible :source :take :release :offer :execution-pause :execution-stop
    :execution-resume :execution-correct :state-transition :event-defer :event-reopen
    :friend :model-register :model-rate :config-intake)
  "Which verbs are reversible, verb by verb. Terminal dispositions and recorded
receipts are refused by name and never reach an undo.")

(defun reversible-verb-p (verb)
  (member verb *reversible-verbs*))

(defstruct edit-entry
  "A reversible edit: its reverse keeps the preimage and postimage, so an undo
appends a compensation and a redo reapplies against the preimage."
  id verb preimage postimage)

(defun history-with-undo (history id)
  "Append a compensation for ID, preserving every earlier entry exactly where it
was (appending, never erasing)."
  (let ((entry (find id history :key #'edit-entry-id :test #'equal)))
    (if entry
        (append history
                (list (make-edit-entry
                       :id (format nil "~A-undo" id)
                       :verb :undo
                       :preimage (edit-entry-postimage entry)
                       :postimage (edit-entry-preimage entry))))
        history)))

(defun redo-applies-p (entry current)
  "Redo reapplies the intent against current preconditions rather than deleting
the undo; it is admitted only while CURRENT still equals the entry's preimage."
  (equal (edit-entry-preimage entry) current))

(defun conflict-is-explicit (history id current)
  "A conflicting undo or redo names what moved and mutates nothing: answer a
refusal naming the expected and current values, never a half-applied history."
  (let ((entry (find id history :key #'edit-entry-id :test #'equal)))
    (when (and entry (not (redo-applies-p entry current)))
      (list :conflict id :expected (edit-entry-preimage entry) :current current))))

;;; ------------------------------------------------------------------
;;; unrelated-receipts-stay-reusable                SPEC-WORK.md:4804
;;; ------------------------------------------------------------------

(defstruct proof-scope
  "A feature's declared proof scope: the paths and criteria a result receipts
against. A change outside it invalidates nothing."
  paths criteria)

(defstruct receipt
  "A retained result receipt, reusable while its declared proof scope is
untouched by any change."
  id scope)

(defun change-within-scope-p (change scope)
  (let ((path (getf change :path))
        (criterion (getf change :criterion)))
    (or (and path (member path (proof-scope-paths scope) :test #'equal))
        (and criterion (member criterion (proof-scope-criteria scope) :test #'equal)))))

(defun unrelated-receipts-stay-reusable (receipts change)
  "Answer the receipts whose declared proof scope a change does not touch; a
change outside a feature's scope invalidates no unrelated receipt without a
dependency reason."
  (remove-if (lambda (receipt) (change-within-scope-p change (receipt-scope receipt)))
             receipts))
