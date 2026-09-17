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
