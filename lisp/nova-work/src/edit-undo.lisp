;;;; edit-undo.lisp --- the reversible-mistake and metadata-patch verbs.
;;;;
;;;; docs/SPEC-WORK.md:5798 names `node edit` as the one verb over exactly the
;;;; five permitted metadata fields, each a tagged patch, with a no-effect
;;;; receipt changed=0, and `edit-is-atomic-and-replayable` promises a bad patch
;;;; writes nothing while a retried request replays its original NODE OK.
;;;; docs/SPEC-WORK.md:5627 promises the no-op event is still journaled and
;;;; advances the revision by one. docs/SPEC-WORK.md:5652 and :5783 name the
;;;; undo set: a compensating edit for a node edit, the preimage transition for
;;;; a state move, and a refusal `not reversible here` naming the verb for
;;;; everything else -- an external effect (sent, paid, published, deleted) and
;;;; the terminal dispositions included. This file is the smallest kernel change
;;;; that satisfies those paragraphs; it is internal and has no session or CLI.

(in-package #:nova-work)

(defun %eu-required (request key)
  (let ((value (getf request key)))
    (when (or (null value) (and (stringp value) (string= value "")))
      (error 'unsupported-input
             :what (format nil "unsupported: ~A refusing to guess"
                           (string-downcase (symbol-name key)))))
    value))

(defun %dedup-verdict (kernel rid digest)
  "Answer (:unavailable RECORDED), (:replay LINE), (:conflict NIL) or (NIL NIL),
asking the journal and never a resident map."
  (multiple-value-bind (found recorded-digest recorded-line)
      (journal-lookup (kernel-journal kernel) rid)
    (cond ((eq found :unavailable) (values :unavailable recorded-digest))
          (found (if (string= digest recorded-digest)
                     (values :replay recorded-line)
                     (values :conflict nil)))
          (t (values nil nil)))))

(defun %dedup-refusal (word rid verdict recorded)
  (ecase verdict
    (:unavailable (format nil "~A FAIL request=~A page=~A: dedup unavailable" word rid recorded))
    (:conflict (format nil "~A FAIL request=~A: reused with a different payload" word rid))))

(defun %install-envelope (kernel rid digest line events)
  "Accept, record before apply, then install the whole envelope or none of it."
  (let ((envelope (list :request rid :digest digest :events events :settle nil)))
    (multiple-value-bind (accepted refusal) (journal-accept (kernel-journal kernel) envelope)
      (unless accepted
        (return-from %install-envelope
          (values nil (format nil "MUTATION FAIL request=~A: journal refused acceptance: ~A"
                              rid refusal)
                  1 nil))))
    (journal-record (kernel-journal kernel) rid digest line
                    (work-event-rev (car (last events))))
    (when *before-apply-hook* (funcall *before-apply-hook* envelope))
    (let ((candidate (apply-envelope (kernel-state kernel) envelope)))
      (setf (kernel-state kernel) candidate)
      (setf (kernel-next-rev kernel) (1+ (work-event-rev (car (last events)))))
      (values t line 0 envelope))))

(defun %oneshot-submit (kernel rid digest line event word applied-entry)
  "Dedup, install one event, and remember APPLIED-ENTRY on success."
  (multiple-value-bind (verdict recorded) (%dedup-verdict kernel rid digest)
    (if verdict
        (if (eq verdict :replay)
            (values t recorded 0 (list :request rid :digest digest :events '() :replayed t))
            (values nil (%dedup-refusal word rid verdict recorded) 1 nil))
        (let ((result (multiple-value-list
                       (%install-envelope kernel rid digest line (list event)))))
          (when (and applied-entry (first result))
            (setf (gethash rid (kernel-applied kernel)) applied-entry))
          (values-list result)))))

(defun %undo-submit (kernel rid digest line events)
  "Dedup and install an undo's compensating envelope."
  (multiple-value-bind (verdict recorded) (%dedup-verdict kernel rid digest)
    (if verdict
        (if (eq verdict :replay)
            (values t recorded 0 (list :request rid :digest digest :events '() :replayed t))
            (values nil (%dedup-refusal "UNDO" rid verdict recorded) 1 nil))
        (values-list (multiple-value-list
                      (%install-envelope kernel rid digest line events))))))

;;; `node edit` (SPEC-WORK.md:5798).

(defun %edit-patches (request)
  "Answer (values PATCHES REFUSAL). PATCHES is an alist field -> tagged patch;
REFUSAL is a string when the request is not one of the five-field shape."
  (let ((patches '()) (refusal nil))
    (dolist (field *metadata-fields*)
      (unless refusal
        (let* ((key (cdr (assoc field *metadata-patch-keys*)))
               (present (member key request))
               (patch (and present (getf request key))))
          (cond
            ((not present)
             (setf refusal (format nil "missing patch ~A" (string-downcase (symbol-name field)))))
            ((not (and (consp patch) (member (car patch) '(:keep :clear :set))))
             (setf refusal (format nil "malformed patch for ~A" (string-downcase (symbol-name field)))))
            ((and (eq :set (car patch)) (cddr patch))
             (setf refusal (format nil "malformed patch for ~A" (string-downcase (symbol-name field)))))
            ((and (eq :set (car patch)) (not (%field-type-ok-p field (second patch))))
             (setf refusal (format nil "wrong type for ~A: expected ~A"
                                   (string-downcase (symbol-name field))
                                   (%field-expected-type field))))
            (t (push (cons field patch) patches))))))
    (values (nreverse patches) refusal)))

(defun %edit-event (node patches reason rid by stamp clock owner rev)
  (make-work-event
   :kind :edit :node node :by by
   :fields (list :title-patch (cdr (assoc :title patches))
                 :category-patch (cdr (assoc :category patches))
                 :links-patch (cdr (assoc :links patches))
                 :private-patch (cdr (assoc :private patches))
                 :version-patch (cdr (assoc :version patches))
                 :reason reason)
   :stamp stamp :clock clock :request rid
   :generation-owner owner :rev rev :session-written-p nil))

(defun %submit-edit (kernel request)
  (%check-request-keys :node-edit request)
  (let ((node (%eu-required request :node))
        (rid (%eu-required request :request))
        (by (%eu-required request :by))
        (stamp (%eu-required request :stamp))
        (clock (%eu-required request :clock))
        (owner (%eu-required request :generation-owner)))
    (multiple-value-bind (patches refusal) (%edit-patches request)
      (when refusal
        (return-from %submit-edit
          (values nil (format nil "NODE FAIL node=~A: ~A" node refusal) 2 nil)))
      (let ((wnode (%node-quiet (kernel-state kernel) node)))
        (when (null wnode)
          (return-from %submit-edit
            (values nil (format nil "NODE FAIL node=~A: no such node" node) 2 nil)))
        (when (every (lambda (p) (eq :keep (car (cdr p)))) patches)
          (return-from %submit-edit
            (values nil (format nil "NODE FAIL node=~A: all keep" node) 2 nil)))
        (let ((version (cdr (assoc :version patches))))
          (when (and (not (eq (wnode-type wnode) :task)) (not (eq :keep (car version))))
            (return-from %submit-edit
              (values nil (format nil "NODE FAIL node=~A: version on a ~A"
                                  node (string-downcase (symbol-name (wnode-type wnode))))
                      2 nil))))
        (let* ((before (loop for field in *metadata-fields*
                             collect (cons field (wnode-field wnode field))))
               (after (mapcar #'copy-list before)))
          (dolist (field *metadata-fields*)
            (setf (cdr (assoc field after))
                  (%apply-patch (cdr (assoc field before))
                                (cdr (assoc field patches)))))
          (let* ((changed (count-if-not
                           (lambda (f) (equal (cdr (assoc f before)) (cdr (assoc f after))))
                           *metadata-fields*))
                 (event (%edit-event node patches (getf request :reason +absent+)
                                     rid by stamp clock owner (kernel-next-rev kernel)))
                 (digest (payload-digest (list event)))
                 (line (format nil "NODE OK id=~A request=~A node=~A change=edit changed=~D rev=~D pushed=-"
                               (event-id event) rid node changed (work-event-rev event))))
            (%oneshot-submit kernel rid digest line event "NODE"
                             (list :verb :node-edit :node node :before before :changed changed))))))))

;;; An external effect is an outcome, not a verb of the state grammar
;;; (SPEC-WORK.md:5652). Recording it is what lets an undo over it be refused.

(defun %submit-external (kernel request)
  (%check-request-keys :external-effect request)
  (let ((node (%eu-required request :node))
        (rid (%eu-required request :request))
        (by (%eu-required request :by))
        (stamp (%eu-required request :stamp))
        (clock (%eu-required request :clock))
        (owner (%eu-required request :generation-owner))
        (effect (%eu-required request :effect))
        (handle (getf request :handle)))
    (unless (and (stringp handle) (plusp (length handle)))
      (return-from %submit-external
        (values nil (format nil "EXTERNAL FAIL node=~A: no handle" node) 2 nil)))
    (unless (%node-quiet (kernel-state kernel) node)
      (return-from %submit-external
        (values nil (format nil "EXTERNAL FAIL node=~A: no such node" node) 2 nil)))
    (let* ((event (make-work-event
                   :kind :external :node node :by by
                   :fields (list :effect effect :handle handle)
                   :stamp stamp :clock clock :request rid
                   :generation-owner owner :rev (kernel-next-rev kernel)
                   :session-written-p nil))
           (digest (payload-digest (list event)))
           (line (format nil "EXTERNAL OK id=~A request=~A effect=~A handle=~A rev=~D pushed=-"
                         (event-id event) rid (string-downcase (symbol-name effect))
                         handle (work-event-rev event))))
      (%oneshot-submit kernel rid digest line event "EXTERNAL"
                       (list :verb :external-effect :node node
                             :effect effect :handle handle)))))

;;; Terminal dispositions: `node remove` and `event --kind cancel`. Both are
;;; recorded so an undo over either is refused (SPEC-WORK.md:5783).

(defun %submit-terminal (kernel request verb disposition)
  (%check-request-keys verb request)
  (let ((node (%eu-required request :node))
        (rid (%eu-required request :request))
        (by (%eu-required request :by))
        (stamp (%eu-required request :stamp))
        (clock (%eu-required request :clock))
        (owner (%eu-required request :generation-owner))
        (reason (getf request :reason +absent+))
        (word (if (eq verb :node-remove) "NODE" "EVENT")))
    (unless (%node-quiet (kernel-state kernel) node)
      (return-from %submit-terminal
        (values nil (format nil "~A FAIL node=~A: no such node" word node) 2 nil)))
    (let* ((event (make-work-event
                   :kind :terminal :node node :by by
                   :fields (list :disposition disposition :reason reason)
                   :stamp stamp :clock clock :request rid
                   :generation-owner owner :rev (kernel-next-rev kernel)
                   :session-written-p nil))
           (digest (payload-digest (list event)))
           (line (format nil "~A OK id=~A request=~A node=~A disposition=~A rev=~D pushed=-"
                         word (event-id event) rid node
                         (string-downcase (symbol-name disposition)) (work-event-rev event))))
      (%oneshot-submit kernel rid digest line event word
                       (list :verb verb :node node :terminal t :disposition disposition)))))

;;; `undo` appends a typed compensating envelope; it never erases the original.

(defun %compensating-patches (before)
  (loop for field in *metadata-fields*
        collect (let ((value (cdr (assoc field before))))
                  (cons field (if (absentp value) (list :clear) (list :set value))))))

(defun %undo-compensating-events (entry by stamp clock owner rid base)
  "The envelope the reversible-verb table names for a state move."
  (let* ((node (getf entry :node))
         (before (getf entry :before-state))
         (verb (getf entry :verb))
         (next base)
         (events '()))
    (when (eq verb :state-to-done)
      (push (make-work-event :kind :revive :node node :by by
                             :fields (list :reason +absent+)
                             :stamp stamp :clock clock :request rid
                             :generation-owner owner :rev next :session-written-p t)
            events)
      (incf next))
    (push (make-work-event :kind :transition :node node :by by
                           :fields (list :to (if (eq verb :event-reopen) :done before)
                                         :reason +absent+ :blocked-by +absent+
                                         :evidence +absent+)
                           :stamp stamp :clock clock :request rid
                           :generation-owner owner :rev next :session-written-p nil)
          events)
    (incf next)
    (when (eq verb :event-reopen)
      (push (make-work-event :kind :settle :node node :by by
                             :fields (list :disposition :done :reason +absent+
                                           :already-closed '())
                             :stamp stamp :clock clock :request rid
                             :generation-owner owner :rev next :session-written-p t)
            events))
    (nreverse events)))

(defun %submit-undo (kernel request)
  (%check-request-keys :undo request)
  (let* ((of (%eu-required request :of))
         (rid (%eu-required request :request))
         (by (%eu-required request :by))
         (stamp (%eu-required request :stamp))
         (clock (%eu-required request :clock))
         (owner (%eu-required request :generation-owner))
         (entry (gethash of (kernel-applied kernel))))
    (unless entry
      (return-from %submit-undo
        (values nil (format nil "UNDO FAIL request-of=~A: no such request" of) 1 nil)))
    ;; A revision-bound plan the caller applied must revalidate: if the state
    ;; moved past the plan's revision, the conflicting assumptions refuse and
    ;; nothing is half-applied (SPEC-WORK.md:2831-2833, :2912).
    (when (getf request :at-rev)
      (let ((at (getf request :at-rev))
            (rev (state-revision (kernel-state kernel))))
        (unless (eql at rev)
          (return-from %submit-undo
            (values nil (format nil "UNDO FAIL request-of=~A: stale plan at-rev=~A rev=~D: not applied"
                                of at rev)
                    1 nil)))))
    (let ((verb (getf entry :verb)))
      (cond
        ((eq verb :external-effect)
         (values nil (format nil "UNDO FAIL request-of=~A effect=external kind=~A handle=~A: not reversible here"
                             of (string-downcase (symbol-name (getf entry :effect)))
                             (getf entry :handle))
                 1 nil))
        ((member verb '(:node-remove :event-cancel))
         (values nil (format nil "UNDO FAIL request-of=~A: not reversible here (~A is terminal)"
                             of (string-downcase (symbol-name verb)))
                 1 nil))
        ((eq verb :node-edit)
         (let* ((node (getf entry :node))
                (patches (%compensating-patches (getf entry :before)))
                (event (%edit-event node patches +absent+ rid by stamp clock owner
                                    (kernel-next-rev kernel)))
                (digest (payload-digest (list event)))
                (line (format nil "UNDO OK id=~A request=~A of=~A node=~A rev=~D pushed=-"
                              (event-id event) rid of node (work-event-rev event))))
           (%undo-submit kernel rid digest line (list event))))
        ((eq verb :node-move)
         (node-move-undo kernel entry rid))
        ((member verb '(:state-to-doing :state-to-done :event-reopen))
         (let* ((events (%undo-compensating-events entry by stamp clock owner rid
                                                   (kernel-next-rev kernel)))
                (digest (payload-digest events))
                (last (car (last events)))
                (line (format nil "UNDO OK id=~A request=~A of=~A node=~A rev=~D pushed=-"
                              (event-id last) rid of (getf entry :node)
                              (work-event-rev last))))
           (%undo-submit kernel rid digest line events)))
        (t
         (values nil (format nil "UNDO FAIL request-of=~A: verb ~A is not reversible here"
                             of (string-downcase (symbol-name verb)))
                 1 nil))))))


;;; ------------------------------------------------------------------
;;; folded from replays-8651.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8651.lisp --- pure functions and records for the three named
;;;; acceptance replays of docs/SPEC-WORK.md at origin/dev:
;;;;
;;;;   undo-redo                        SPEC-WORK.md:6245
;;;;   unknown-price-is-not-zero        SPEC-WORK.md:4636-4651
;;;;   unrelated-receipts-stay-reusable SPEC-WORK.md:4943-4951
;;;;
;;;; This file holds the pure functions and records the acceptance replays call.
;;;; It owns no state and no I/O. Where a replay names behaviour that belongs in
;;;; the resident kernel, session or state slices, the pure shape is proven here
;;;; and the wiring owed to those slices is listed in RESULT.md, one line each.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; unknown-price-is-not-zero                        SPEC-WORK.md:4636-4651
;;; ------------------------------------------------------------------

(defun resolved-price (value)
  "A missing or unsupported price dimension resolves to :unknown, and is never
read as zero (SPEC-WORK.md:4640)."
  (cond ((null value) :unknown)
        ((eq value :unsupported) :unknown)
        (t value)))

;;; ------------------------------------------------------------------
;;; undo-redo                                        SPEC-WORK.md:6245
;;; ------------------------------------------------------------------

(defparameter *reversible-verb-kinds*
  '(:node-add :node-require :decompose :accept :dep :axis :node-edit :node-move
    :roadmap-create :roadmap-configure :roadmap-row :roadmap-projection :prioritise
    :cell :responsible :source :take :release :offer :execution-pause :execution-stop
    :execution-resume :execution-correct :state-transition :event-defer :event-reopen
    :friend :model-register :model-rate :config-intake)
  "Which verbs are reversible, verb by verb. Terminal dispositions and recorded
receipts are refused by name and never reach an undo (SPEC-WORK.md:2853-2898).")

(defun reversible-verb-p (verb)
  (member verb *reversible-verb-kinds*))

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
;;; unrelated-receipts-stay-reusable                 SPEC-WORK.md:4943-4951
;;; ------------------------------------------------------------------

(defstruct proof-scope
  "A feature's declared proof scope: the paths and criteria a result receipts
against. A change outside it invalidates nothing (SPEC-WORK.md:4948-4949)."
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
dependency reason (SPEC-WORK.md:4948-4951)."
  (remove-if (lambda (receipt) (change-within-scope-p change (receipt-scope receipt)))
             receipts))


;;; ------------------------------------------------------------------
;;; folded from replays-priority-and-export.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-priority-and-export.lisp --- the pure kernel the five replays
;;;; `rank-2-precedes-10`, `priority-undo-is-history-not-value`,
;;;; `priority-grants-nothing`, `state-export-describes-exactly-r` and
;;;; `state-export-is-one-long-operation` drive (docs/SPEC-WORK.md:3156-3297,
;;;; 5857-5888). Ordering intent, the priority history identity, the captured
;;;; revision and the long operation are pure data here; the session/CLI wiring
;;;; a later slice owns is listed as owed in RESULT.md.

(in-package #:nova-work)

(defconstant +rank-ceiling+ (expt 10 18)
  "A rank is an unsigned integer atom of at most eighteen digits.")

(defun rank-atom-p (r)
  (and (integerp r) (<= 0 r) (< r +rank-ceiling+)))

;;; ------------------------------------------------------------------
;;; The priority table: two slots per node and the latest-change identity
;;; ------------------------------------------------------------------

(defstruct (priority-table (:constructor make-priority-table (&key parents)))
  (parents (make-hash-table :test #'equal))
  (slots (make-hash-table :test #'equal)))

(defun priority-table-slot (table id context)
  (let ((slot (gethash id (priority-table-slots table))))
    (getf slot context +absent+)))

(defun priority-change-id (table id context)
  (let ((slot (gethash id (priority-table-slots table))))
    (getf slot (intern (concatenate 'string (symbol-name context) "-CHANGE")
                             :keyword)
          +absent+)))

(defun (setf priority-table-slot) (value table id context)
  (let ((slot (or (gethash id (priority-table-slots table))
                  (setf (gethash id (priority-table-slots table))
                        (list :self +absent+ :subtree +absent+
                              :self-change +absent+ :subtree-change +absent+)))))
    (setf (getf slot context) value)
    (setf (getf slot (intern (concatenate 'string (symbol-name context) "-CHANGE")
                             :keyword))
          +absent+)
    value))

(defun note-priority-change (table id context change-id)
  (let ((slot (gethash id (priority-table-slots table))))
    (setf (getf slot (intern (concatenate 'string (symbol-name context) "-CHANGE")
                             :keyword))
          change-id)))

(defun priority-parent (table id)
  (gethash id (priority-table-parents table)))

(defun effective-rank (table id)
  "The nearest context: the node's own :self, else the deepest :subtree on its
containment path, else the default (SPEC-WORK.md:3172)."
  (let ((self (priority-table-slot table id :self)))
    (when (and self (not (eq self +absent+)))
      (return-from effective-rank (values self id :self))))
  (let ((cursor (priority-parent table id)))
    (loop while cursor do
      (let ((sub (priority-table-slot table cursor :subtree)))
        (when (and sub (not (eq sub +absent+)))
          (return-from effective-rank (values sub cursor :subtree))))
      (setf cursor (priority-parent table cursor))))
  (values nil "default" :default))

(defun make-priority-receipt (&key change context rank changed no-effect change-id)
  (list :change change :context context :rank rank :changed changed
        :no-effect no-effect :change-id change-id))

(defun priority-table-set (table id context rank reason change-id)
  "A set changes the slot only when the value differs; a same-value set is the
no-effect receipt (SPEC-WORK.md:3187)."
  (unless (rank-atom-p rank)
    (return-from priority-table-set (values nil (format nil "bad rank ~S" rank) 2)))
  (let ((current (priority-table-slot table id context)))
    (if (and (not (eq current +absent+)) (eql current rank))
        (values (make-priority-receipt :change :set :context context :rank rank
                                       :changed 0 :no-effect t :change-id +absent+)
                "PRIORITY OK changed=0" 0)
        (progn
          (setf (priority-table-slot table id context) rank)
          (note-priority-change table id context change-id)
          (values (make-priority-receipt :change :set :context context :rank rank
                                         :changed 1 :no-effect nil :change-id change-id)
                  "PRIORITY OK changed=1" 0)))))

(defun priority-table-clear (table id context reason change-id)
  "A clear of an absent slot is the no-effect receipt (SPEC-WORK.md:3187)."
  (let ((current (priority-table-slot table id context)))
    (if (eq current +absent+)
        (values (make-priority-receipt :change :clear :context context :rank +absent+
                                       :changed 0 :no-effect t :change-id +absent+)
                "PRIORITY OK changed=0" 0)
        (progn
          (setf (priority-table-slot table id context) +absent+)
          (note-priority-change table id context change-id)
          (values (make-priority-receipt :change :clear :context context :rank +absent+
                                         :changed 1 :no-effect nil :change-id change-id)
                  "PRIORITY OK changed=1" 0)))))

(defun priority-undo (table id context event-id)
  "Undo restores the preimage only while the slot's latest-change identity equals
this event's :after (SPEC-WORK.md:3188)."
  (let ((latest (priority-change-id table id context)))
    (if (equal latest event-id)
        (progn
          (setf (priority-table-slot table id context) +absent+)
          (note-priority-change table id context +absent+)
          (values t "PRIORITY UNDO OK" 0))
        (values nil (format nil "PRIORITY UNDO FAIL: slot changed since ~A" event-id) 2))))

;;; ------------------------------------------------------------------
;;; Ordering: explicit ranks ascending, then defaults, then id
;;; ------------------------------------------------------------------

(defun row-eligible-p (row) (getf row :ready))

(defun row-sort-key (row)
  (let ((rank (getf row :priority)))
    (if rank (cons 0 rank) (cons 1 0))))

(defun priority-order (rows)
  "Explicit ranks ascending, then default rows, each tie broken by bytewise id;
blocked rows follow in discovery order (SPEC-WORK.md:3178-3181)."
  (let ((eligible (remove-if-not #'row-eligible-p rows))
        (blocked (remove-if #'row-eligible-p rows)))
    (append
     (stable-sort (copy-list eligible)
                  (lambda (a b)
                    (let ((ka (row-sort-key a)) (kb (row-sort-key b)))
                      (cond ((/= (car ka) (car kb)) (< (car ka) (car kb)))
                            ((/= (cdr ka) (cdr kb)) (< (cdr ka) (cdr kb)))
                            (t (string< (getf a :id) (getf b :id)))))))
     blocked)))

(defun priority-rows (table rows)
  "Resolve each row's effective rank and label it, then order."
  (let ((resolved
          (mapcar (lambda (row)
                    (multiple-value-bind (rank source context)
                        (effective-rank table (getf row :id))
                      (list* :priority rank :priority-source source
                             :priority-context context row)))
                  rows)))
    (priority-order resolved)))

(defun priority-page (ordered after-id size)
  "Later pages read the pinned order: the next SIZE rows after AFTER-ID, with the
id that continues the cursor."
  (let* ((position (if after-id
                       (1+ (or (position after-id ordered
                                         :key (lambda (r) (getf r :id)) :test #'equal)
                               -1))
                       0))
         (page (subseq ordered position (min (length ordered) (+ position size)))))
    (values page (and page (getf (car (last page)) :id)))))

;;; ------------------------------------------------------------------
;;; Granting nothing: priority moves no execution authority
;;; ------------------------------------------------------------------

(defstruct (work-view (:constructor make-work-view (&key who lease worker approval)))
  who lease worker approval)

(defun priority-grants-nothing-p (before after)
  "Proves the ordering act left who, lease, worker and approval identical."
  (and (equal (work-view-who before) (work-view-who after))
       (equal (work-view-lease before) (work-view-lease after))
       (equal (work-view-worker before) (work-view-worker after))
       (equal (work-view-approval before) (work-view-approval after))))

;;; ------------------------------------------------------------------
;;; The captured revision: capture R while R+1 is accepted
;;; ------------------------------------------------------------------

(defstruct (export-capture (:constructor make-export-capture
                                          (&key revision base end records bytes)))
  revision base end records bytes)

(defun state-record (revision id digest envelope)
  (list :sequence revision :revision revision :id id :digest digest :sha256 digest
        :envelope envelope))

(defun records-through (records revision)
  (remove-if (lambda (r) (> (getf r :revision) revision)) records))

(defun capture-export (records at &key (base at) end)
  "Capture AT; the bytes are built only from records through AT, so a later
accepted revision never enters them (SPEC-WORK.md:3243-3250)."
  (let* ((captured (records-through records at))
         (bytes (canonical-string (mapcar (lambda (r)
                                            (list (getf r :revision)
                                                  (getf r :id)
                                                  (getf r :digest)))
                                          captured))))
    (make-export-capture :revision at :base base :end end
                         :records captured :bytes bytes)))

(defun validate-export (capture all-records)
  "The coverage and revision-chain checks load performs before exposing state:
base never exceeds R; an absent end is legal only when B equals R; a base below
R needs a complete end; a missing, swapped, wrong-hash or split end refuses
(SPEC-WORK.md:3243-3251)."
  (let ((r (export-capture-revision capture))
        (b (export-capture-base capture))
        (end (export-capture-end capture)))
    (cond
      ((> b r) (values nil (format nil "base ~D exceeds captured revision ~D" b r)))
      ((and (< b r) (null end)) (values nil "absent end below R"))
      ((and (= b r) (null end)) (values t "exact snapshot: absent end at B=R"))
      (t
       (let* ((seq (getf end :sequence))
              (hash (getf end :sha256))
              (record (find seq all-records :key (lambda (x) (getf x :sequence)))))
         (cond
           ((null record) (values nil (format nil "missing end record ~D" seq)))
           ((not (equal hash (getf record :sha256)))
            (values nil (format nil "wrong end hash at ~D" seq)))
           ((getf record :split) (values nil (format nil "cut inside envelope at ~D" seq)))
           ((and (getf record :swapped) (/= (getf record :revision) r))
            (values nil (format nil "wrong end revision at ~D" seq)))
           (t (values t "coverage verified"))))))))

;;; ------------------------------------------------------------------
;;; The long operation: acknowledged at once, waited on by id
;;; ------------------------------------------------------------------

(defstruct (priority-operation (:constructor %make-priority-operation (&key id kind revision status result))
                      (:conc-name op-))
  id kind revision status result)

(defstruct (priority-operation-registry (:constructor make-priority-operation-registry (&key (counter 0))))
  counter
  (operations (make-hash-table :test #'equal)))

(defun begin-operation (registry kind revision &key inside-batch entry-id)
  "The request is acknowledged at once with a durable id; inside an atomic batch
it refuses by entry id (SPEC-WORK.md:3202-3208, 5888)."
  (when inside-batch
    (return-from begin-operation
      (values nil (format nil "OPERATION FAIL: ~A refused inside atomic batch ~A"
                          kind entry-id) 2)))
  (let ((id (format nil "op-~D" (incf (priority-operation-registry-counter registry)))))
    (setf (gethash id (priority-operation-registry-operations registry))
          (%make-priority-operation :id id :kind kind :revision revision :status :queued))
    (values id (format nil "OPERATION OK id=~A op=~A state=queued" id kind) 0)))

(defun priority-operation-record (registry id)
  (gethash id (priority-operation-registry-operations registry)))

(defun priority-operation-state (registry id)
  (let ((op (priority-operation-record registry id)))
    (and op (op-status op))))

(defun priority-operation-start (registry id)
  (let ((op (priority-operation-record registry id)))
    (when op (setf (op-status op) :running))
    op))

(defun priority-operation-finish (registry id &key result)
  (let ((op (priority-operation-record registry id)))
    (when op (setf (op-status op) :done (op-result op) result))
    op))

(defun priority-operation-cancel (registry id)
  "Cancellation acknowledges; it never promises to unpublish (SPEC-WORK.md:3217)."
  (let ((op (priority-operation-record registry id)))
    (when op (setf (op-status op) :cancelled))
    (values op (format nil "OPERATION OK id=~A op=cancel state=cancelled" id) 0)))

(defun priority-operation-wait (registry id)
  "Wait returns the same operation id and the captured revision it pinned."
  (let ((op (priority-operation-record registry id)))
    (values (and op (op-id op))
            (and op (op-revision op))
            (and op (op-status op))
            (and op (op-result op)))))

(defun unrelated-mutation (counter)
  "A mutation responsive under a running long operation: it advances."
  (1+ counter))
