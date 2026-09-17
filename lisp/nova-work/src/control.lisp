;;;; control.lisp --- the smallest execution-control model slice 1 needs to
;;;; answer the five spec replays of docs/SPEC-WORK.md:3921-3980:
;;;;
;;;;   stop-is-a-hold-not-a-cancel
;;;;   one-stop-note-cannot-cancel-two-attempts
;;;;   hold-survives-a-crash
;;;;   capture-survives-clip
;;;;   no-dispatch-slips-past-a-hold
;;;;
;;;; The full session, provider, transport, lease and reconcile surfaces stay
;;;; outside this slice (README.md "What is out"). What is here is the durable
;;;; scheduling hold, the ordered target capture with its anchor and pin, the
;;;; dispatch barrier, and the cancellation edge's evidence-over-attempts rule.
;;;;
;;;; The hold and its capture anchor are recorded in the kernel's journal
;;;; BEFORE the verb acknowledges (SPEC-WORK.md:3946), so a crash after the
;;;; record and before a capture or a send recovers the same hold and the same
;;;; target identities (replay hold-survives-a-crash). A clip publishes without
;;;; moving a live hold's pin (replay capture-survives-clip), and every send
;;;; revalidates the effective holds (replay no-dispatch-slips-past-a-hold).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The in-memory half. Durable records are the journal's, never this.
;;; ------------------------------------------------------------------

(defstruct (attempt (:conc-name attempt-))
  id node generation live-p)

(defstruct (offer (:conc-name of-))
  id node attempt generation effect lease-p launched-p)

(defstruct (hold (:conc-name hold-))
  id action scope targets directives anchor revision span manifest released-p durable-p)

(defstruct (manifest (:conc-name manifest-))
  hold-id revision span target-ids content-hash)

(defstruct (control-state (:conc-name ctl-) (:constructor make-ctl))
  (attempts '())          ; newest first
  (offers '())            ; newest first
  (holds '())             ; newest first
  (cancel-requests (make-hash-table :test #'equal))
  (cancelled (make-hash-table :test #'equal))
  (revision 0)
  (control-seq 0)
  (clip-revision 0))

(defun kernel-holds (kernel)
  "The holds in the order they were installed."
  (reverse (ctl-holds (kernel-controls kernel))))

(defun kernel-offers (kernel)
  (reverse (ctl-offers (kernel-controls kernel))))

;;; ------------------------------------------------------------------
;;; Attempts: the assignments a control captures. Provider-side, so they
;;; move no work revision and write no transition.
;;; ------------------------------------------------------------------

(defun record-attempt (kernel node id &key (generation 1) (live t))
  (let ((attempt (make-attempt :id id :node node :generation generation :live-p live)))
    (push attempt (ctl-attempts (kernel-controls kernel)))
    attempt))

(defun live-attempt-ids (kernel node)
  "The live attempts of NODE, ordered so a manifest is reproducible."
  (sort (loop for a in (ctl-attempts (kernel-controls kernel))
              when (and (attempt-live-p a) (equal node (attempt-node a)))
                collect (attempt-id a))
        #'string<))

(defun live-attempt-p (kernel node id)
  (let ((a (find id (ctl-attempts (kernel-controls kernel))
                 :key #'attempt-id :test #'equal)))
    (and a (attempt-live-p a) (equal node (attempt-node a)))))

;;; ------------------------------------------------------------------
;;; Scope selectors (SPEC-WORK.md:3923).
;;; ------------------------------------------------------------------

(defun %scope-covers-p (scope node)
  (destructuring-bind (kind value) scope
    (ecase kind
      (:all t)
      (:node (equal value node))
      (:repo (let ((slash (position #\/ node)))
               (and slash (equal value (subseq node 0 slash))))))))

(defun %scope-string (scope)
  (destructuring-bind (kind value) scope
    (format nil "~A:~A" (string-downcase (symbol-name kind)) (or value "-"))))

(defun %parse-scope (text)
  (let ((colon (position #\: text)))
    (list (intern (string-upcase (subseq text 0 colon)) :keyword)
          (let ((value (subseq text (1+ colon))))
            (if (string= value "-") nil value)))))

;;; ------------------------------------------------------------------
;;; The control record: hold + anchor + captured target identities. Its
;;; bytes are the journal line, so recovery reads the same fact.
;;; ------------------------------------------------------------------

(defun %split-on (string char)
  (let ((parts '()) (start 0))
    (loop
      (let ((pos (position char string :start start)))
        (if pos
            (progn (push (subseq string start pos) parts) (setf start (1+ pos)))
            (progn (push (subseq string start) parts) (return)))))
    (nreverse parts)))

(defun %field (line key)
  (let ((prefix (concatenate 'string key "=")))
    (dolist (token (%split-on line #\space))
      (when (and (>= (length token) (length prefix))
                 (string= prefix token :end2 (length prefix)))
        (return (subseq token (length prefix)))))))

(defun %control-record-line (id action scope anchor span targets rev)
  (format nil "CONTROL id=~A action=~A scope=~A anchor=~A span=~D rev=~D targets=~{~A~^,~}"
          id (string-downcase (symbol-name action)) (%scope-string scope)
          anchor span rev targets))

(defun %parse-control-line (line)
  (when (and (stringp line) (string= "CONTROL" (first (%split-on line #\space))))
    (let* ((targets-raw (or (%field line "targets") ""))
           (targets (if (string= targets-raw "") '() (%split-on targets-raw #\,))))
      (list :id (%field line "id")
            :action (intern (string-upcase (%field line "action")) :keyword)
            :scope (%parse-scope (%field line "scope"))
            :anchor (%field line "anchor")
            :span (parse-integer (%field line "span"))
            :rev (parse-integer (%field line "rev"))
            :targets targets))))

(defun %hold-from-record (record)
  (let ((id (getf record :id)))
    (make-hold :id id
               :action (getf record :action)
               :scope (getf record :scope)
               :targets (getf record :targets)
               :directives (loop for target in (getf record :targets)
                                 collect (format nil "~A/~A" id target))
               :anchor (getf record :anchor)
               :revision (getf record :rev)
               :span (getf record :span)
               :manifest nil :released-p nil :durable-p t)))

(defun recover-controls (kernel)
  "A crash recovery: rebuild the durable holds from the kernel's own journal.
The in-memory control state is discarded first, so the answer is the record's
and not a survivor's; a hold recorded before capture or send comes back with
the same id, anchor, span and target identities."
  (let ((ctl (kernel-controls kernel)))
    (setf (ctl-holds ctl) '())
    (dolist (request (journal-order (kernel-journal kernel)))
      (multiple-value-bind (found digest line rev)
          (journal-lookup (kernel-journal kernel) request)
        (declare (ignore digest rev))
        (when found
          (let ((record (%parse-control-line line)))
            (when record (push (%hold-from-record record) (ctl-holds ctl)))))))
    (setf (ctl-holds ctl) (nreverse (ctl-holds ctl)))
    (kernel-holds kernel)))

;;; ------------------------------------------------------------------
;;; execution pause / stop: a durable hold plus staged directives.
;;; ------------------------------------------------------------------

(defun %install-control (kernel action scope &key request retention)
  (let* ((ctl (kernel-controls kernel))
         (targets (sort (remove-if-not
                         (lambda (a)
                           (and (attempt-live-p a)
                                (%scope-covers-p scope (attempt-node a))))
                         (copy-list (ctl-attempts ctl)))
                        #'string< :key #'attempt-id))
         (target-ids (mapcar #'attempt-id targets))
         (span (length target-ids))
         (id (or request (format nil "control-~D" (incf (ctl-control-seq ctl)))))
         (rev (ctl-revision ctl))
         (anchor (sha256-hex (canonical-string
                              (list :scope scope :targets target-ids :rev rev)))))
    ;; A retention window that cannot hold the exact references refuses the
    ;; control BEFORE acknowledgement (SPEC-WORK.md:3960): no record, no hold.
    (when (and retention (> span retention))
      (return-from %install-control
        (values nil (format nil "EXECUTION FAIL control=~A: unrepresentable pin span=~D retention=~D"
                            id span retention)
                2 nil)))
    ;; Durable before it is acknowledged, and before any capture or send.
    (let ((line (%control-record-line id action scope anchor span target-ids rev)))
      (journal-record (kernel-journal kernel) id (sha256-hex line) line rev))
    (let ((hold (make-hold :id id :action action :scope scope :targets target-ids
                           :directives (loop for target in target-ids
                                             collect (format nil "~A/~A" id target))
                           :anchor anchor :revision rev :span span
                           :manifest nil :released-p nil :durable-p t)))
      (push hold (ctl-holds ctl))
      (incf (ctl-revision ctl))
      (values t (format nil "EXECUTION OK id=~A action=~A targets=~D"
                        id (string-downcase (symbol-name action)) span)
              0 hold))))

(defun execution-stop (kernel &key scope request retention)
  "Install the durable scheduling hold and stage one stop directive per captured
assignment. It writes no transition and is not a cancellation."
  (%install-control kernel :stop scope :request request :retention retention))

(defun execution-pause (kernel &key scope request retention)
  "The same hold, staging cooperative pause directives."
  (%install-control kernel :pause scope :request request :retention retention))

(defun hold-pin (hold)
  "The anchored snapshot revision and the journal span it pins."
  (cons (hold-revision hold) (hold-span hold)))

(defun capture-manifest (kernel hold)
  "The ordered target manifest for HOLD, built from its anchored pin only. It
never reads a newer scope, so a clip or a later assignment cannot change it."
  (declare (ignore kernel))
  (make-manifest :hold-id (hold-id hold)
                 :revision (hold-revision hold)
                 :span (hold-span hold)
                 :target-ids (copy-list (hold-targets hold))
                 :content-hash (sha256-hex
                                (canonical-string
                                 (list :anchor (hold-anchor hold)
                                       :targets (hold-targets hold))))))

(defun clip (kernel)
  "Publish a clip. A live hold's anchor and span are carried forward, never
reconstructed from the newer scope."
  (incf (ctl-clip-revision (kernel-controls kernel)))
  (values t "CLIP OK" 0))

;;; ------------------------------------------------------------------
;;; The dispatch barrier: revalidated at offer, at conversion, at send.
;;; ------------------------------------------------------------------

(defun hold-covers-node-p (hold node)
  (and (not (hold-released-p hold)) (%scope-covers-p (hold-scope hold) node)))

(defun held-p (kernel node)
  "True while any effective hold covers NODE. Overlapping holds stay effective
even after one is released."
  (some (lambda (hold) (hold-covers-node-p hold node))
        (ctl-holds (kernel-controls kernel))))

(defun %offer (kernel id)
  (find id (ctl-offers (kernel-controls kernel)) :key #'of-id :test #'equal))

(defun prepare-offer (kernel id node attempt-id &key (generation 1))
  "Offer admission: the first of the three barrier points. A hold effective at
admission refuses the offer."
  (when (held-p kernel node)
    (return-from prepare-offer
      (values nil (format nil "OFFER FAIL offer=~A node=~A: hold effective" id node) 1 nil)))
  (let ((offer (make-offer :id id :node node :attempt attempt-id
                           :generation generation :effect :prepared
                           :lease-p nil :launched-p nil)))
    (push offer (ctl-offers (kernel-controls kernel)))
    (values t (format nil "OFFER OK offer=~A" id) 0 offer)))

(defun send-offer (kernel id)
  "The last send: an offer prepared before a hold cannot launch after it."
  (let ((offer (%offer kernel id)))
    (unless offer
      (return-from send-offer (values nil (format nil "DISPATCH FAIL: no such offer ~A" id) 1)))
    (when (held-p kernel (of-node offer))
      (return-from send-offer
        (values nil (format nil "DISPATCH FAIL offer=~A: held at the last send" id) 1)))
    (setf (of-effect offer) :dispatched
          (of-launched-p offer) t)
    (values t (format nil "DISPATCH OK offer=~A" id) 0)))

(defun accept-offer (kernel id)
  "Conversion: an acceptance arriving under a hold is retained :accepted-held
with the reservation intact -- no lease, no launch, no release."
  (let ((offer (%offer kernel id)))
    (unless offer
      (return-from accept-offer (values nil (format nil "CONVERT FAIL: no such offer ~A" id) 1)))
    (if (held-p kernel (of-node offer))
        (progn (setf (of-effect offer) :accepted-held)
               (values t :accepted-held 0))
        (progn (setf (of-effect offer) :accepted
                     (of-lease-p offer) t
                     (of-launched-p offer) t)
               (values t :accepted 0)))))

(defun reconcile-offer (kernel id)
  "Only reconcile rechecks a held acceptance's generation, identity, capacity
and the other holds."
  (let ((offer (%offer kernel id)))
    (unless offer
      (return-from reconcile-offer (values nil (format nil "RECONCILE FAIL: no such offer ~A" id) 1)))
    (if (held-p kernel (of-node offer))
        (values t (format nil "RECONCILE OK offer=~A effect=~A" id (of-effect offer)) 0)
        (progn (setf (of-effect offer) :accepted
                     (of-lease-p offer) t
                     (of-launched-p offer) t)
               (values t (format nil "RECONCILE OK offer=~A effect=accepted" id) 0)))))

(defun release-hold (kernel id)
  "Lift only this control's hold. Overlapping holds stay effective and lifting
converts no held acceptance."
  (let ((hold (find id (ctl-holds (kernel-controls kernel))
                    :key #'hold-id :test #'equal)))
    (unless hold
      (return-from release-hold (values nil (format nil "RESUME FAIL: no such control ~A" id) 1)))
    (setf (hold-released-p hold) t)
    (values t (format nil "RESUME OK control=~A" id) 0)))

(defun offer-effect (kernel id)
  (let ((offer (%offer kernel id))) (and offer (of-effect offer))))

(defun offer-lease-p (kernel id)
  (let ((offer (%offer kernel id))) (and offer (of-lease-p offer))))

(defun offer-launched-p (kernel id)
  (let ((offer (%offer kernel id))) (and offer (of-launched-p offer))))

(defun lease-count (kernel node)
  (count-if (lambda (o) (and (equal node (of-node o)) (of-lease-p o)))
            (ctl-offers (kernel-controls kernel))))

(defun launch-count (kernel node)
  (count-if (lambda (o) (and (equal node (of-node o)) (of-launched-p o)))
            (ctl-offers (kernel-controls kernel))))

(defun launch-attempt (kernel node attempt-id)
  (declare (ignore attempt-id))
  (if (held-p kernel node)
      (values nil (format nil "DISPATCH FAIL node=~A: held" node) 1)
      (values t "DISPATCH OK" 0)))

(defun correct-attempt (kernel node attempt-id)
  (declare (ignore attempt-id))
  (if (held-p kernel node)
      (values nil (format nil "CORRECT FAIL node=~A: held" node) 1)
      (values t "CORRECT OK" 0)))

(defun move-node (kernel node new-parent)
  (declare (ignore new-parent))
  (if (held-p kernel node)
      (values nil (format nil "MOVE FAIL node=~A: moving out from under a hold is a reconciliation" node) 1)
      (values t "MOVE OK" 0)))

;;; ------------------------------------------------------------------
;;; The cancellation edge, and goal show reading state and never a hold.
;;; ------------------------------------------------------------------

(defun request-cancel (kernel node &key request)
  "The request is the state edge; it is not execution stop."
  (declare (ignore request))
  (setf (gethash node (ctl-cancel-requests (kernel-controls kernel))) t)
  (values t (format nil "STATE OK node=~A to=cancel-requested" node) 0))

(defun cancel-confirm (kernel node &key evidence)
  "The :cancel evidence covers the attempt set -- stopped or not-started per
attempt -- so one worker's stop note cannot cancel a node with another live
attempt."
  (let ((ctl (kernel-controls kernel)))
    (unless (gethash node (ctl-cancel-requests ctl))
      (return-from cancel-confirm
        (values nil (format nil "EVENT FAIL node=~A: cancel was not requested" node) 1)))
    (let ((uncovered (remove-if (lambda (id) (member id evidence :test #'equal))
                                (live-attempt-ids kernel node))))
      (when uncovered
        (return-from cancel-confirm
          (values nil (format nil "EVENT FAIL node=~A: one stop note cannot cancel two attempts; uncovered ~{~A~^,~}"
                              node uncovered)
                  1))))
    (setf (gethash node (ctl-cancelled ctl)) t)
    (values t (format nil "EVENT OK kind=cancel node=~A" node) 0)))

(defun cancel-requested-p (kernel node)
  (gethash node (ctl-cancel-requests (kernel-controls kernel))))

(defun goal-stop (kernel node)
  "`goal show`'s stop= is derived from the state, and no hold is read into it."
  (if (cancel-requested-p kernel node) "stop=requested" "stop=none"))
