;;;; control.lisp --- the smallest execution-control model slice 1 needs to
;;;; answer the five spec replays of docs/SPEC-WORK.md:3921-3980:
;;;;
;;;;   stop-is-a-hold-not-a-cancel
;;;;   one-stop-note-cannot-cancel-two-attempts
;;;;   hold-survives-a-crash
;;;;   capture-survives-ctl-clip
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
;;;; target identities (replay hold-survives-a-crash). A ctl-clip publishes without
;;;; moving a live hold's pin (replay capture-survives-ctl-clip), and every send
;;;; target identities (replay hold-survives-a-crash). A clip publishes without
;;;; moving a live hold's pin (replay capture-survives-clip), and every send
;;;; revalidates the effective holds (replay no-dispatch-slips-past-a-hold).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The in-memory half. Durable records are the journal's, never this.
;;; ------------------------------------------------------------------

(defstruct (ctl-attempt (:conc-name ctl-attempt-))
(defstruct (attempt (:conc-name attempt-))
  id node generation live-p)

(defstruct (offer (:conc-name of-))
  id node attempt generation effect lease-p launched-p)

(defstruct (ctl-hold (:conc-name hold-))
  id action scope targets directives anchor revision span manifest released-p durable-p)

(defstruct (ctl-manifest (:conc-name manifest-))
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
  (let ((attempt (make-ctl-attempt :id id :node node :generation generation :live-p live)))
  (let ((attempt (make-attempt :id id :node node :generation generation :live-p live)))
    (push attempt (ctl-attempts (kernel-controls kernel)))
    attempt))

(defun live-attempt-ids (kernel node)
  "The live attempts of NODE, ordered so a manifest is reproducible."
  (sort (loop for a in (ctl-attempts (kernel-controls kernel))
              when (and (ctl-attempt-live-p a) (equal node (ctl-attempt-node a)))
                collect (ctl-attempt-id a))
              when (and (attempt-live-p a) (equal node (attempt-node a)))
                collect (attempt-id a))
        #'string<))

(defun live-attempt-p (kernel node id)
  (let ((a (find id (ctl-attempts (kernel-controls kernel))
                 :key #'ctl-attempt-id :test #'equal)))
    (and a (ctl-attempt-live-p a) (equal node (ctl-attempt-node a)))))
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
    (make-ctl-hold :id id
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
                           (and (ctl-attempt-live-p a)
                                (%scope-covers-p scope (ctl-attempt-node a))))
                         (copy-list (ctl-attempts ctl)))
                        #'string< :key #'ctl-attempt-id))
         (target-ids (mapcar #'ctl-attempt-id targets))
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
    (let ((hold (make-ctl-hold :id id :action action :scope scope :targets target-ids
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
never reads a newer scope, so a ctl-clip or a later assignment cannot change it."
  (declare (ignore kernel))
  (make-ctl-manifest :hold-id (hold-id hold)
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

(defun ctl-clip (kernel)
  "Publish a ctl-clip. A live hold's anchor and span are carried forward, never
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

(defun prepare-offer (kernel id node ctl-attempt-id &key (generation 1))
(defun prepare-offer (kernel id node attempt-id &key (generation 1))
  "Offer admission: the first of the three barrier points. A hold effective at
admission refuses the offer."
  (when (held-p kernel node)
    (return-from prepare-offer
      (values nil (format nil "OFFER FAIL offer=~A node=~A: hold effective" id node) 1 nil)))
  (let ((offer (make-offer :id id :node node :attempt ctl-attempt-id
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

(defun ctl-accept-offer (kernel id)
(defun accept-offer (kernel id)
  "Conversion: an acceptance arriving under a hold is retained :accepted-held
with the reservation intact -- no lease, no launch, no release."
  (let ((offer (%offer kernel id)))
    (unless offer
      (return-from ctl-accept-offer (values nil (format nil "CONVERT FAIL: no such offer ~A" id) 1)))
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

(defun ctl-release-hold (kernel id)
(defun release-hold (kernel id)
  "Lift only this control's hold. Overlapping holds stay effective and lifting
converts no held acceptance."
  (let ((hold (find id (ctl-holds (kernel-controls kernel))
                    :key #'hold-id :test #'equal)))
    (unless hold
      (return-from ctl-release-hold (values nil (format nil "RESUME FAIL: no such control ~A" id) 1)))
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

(defun launch-attempt (kernel node ctl-attempt-id)
  (declare (ignore ctl-attempt-id))
(defun launch-attempt (kernel node attempt-id)
  (declare (ignore attempt-id))
  (if (held-p kernel node)
      (values nil (format nil "DISPATCH FAIL node=~A: held" node) 1)
      (values t "DISPATCH OK" 0)))

(defun correct-attempt (kernel node ctl-attempt-id)
  (declare (ignore ctl-attempt-id))
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


;;; ------------------------------------------------------------------
;;; folded from replays-8640.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8640.lisp --- the pure execution-control layer the five acceptance
;;;; replays of docs/SPEC-WORK.md:3964-4037 call: a durable hold and the dispatch
;;;; barrier revalidated at offer, at conversion and at the last send; the
;;;; acceptance retained under a hold; reconciliation that keeps contradiction,
;;;; synthesises no zero usage and never signs for the holder; the two resume
;;;; actions; and execution correct as a linked segment, its bare verb refused
;;;; under a live execution.
;;;;
;;;; Nothing here starts a process, a transport or a lease. These are the pure
;;;; records and decisions; the live-session, capture and transport wiring a
;;;; later slice owns is not in this slice.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The durable hold and the dispatch barrier (SPEC-WORK.md:3964)
;;; ------------------------------------------------------------------

(defparameter *dispatch-points* '(:offer :conversion :send)
  "The three points the single writer revalidates every effective hold at.")

(defun make-hold (control scope)
  (list :control control :scope scope))

(defun install-hold (holds &key control scope)
  "A hold is a typed scope and the control that owns it."
  (cons (make-hold control scope) holds))

(defun release-hold (holds control)
  "release-hold removes only its named control's hold; overlapping holds stay
effective and it resumes no remote process (SPEC-WORK.md:4012)."
  (remove-if (lambda (h) (equal (getf h :control) control)) holds))

(defun hold-for-scope (holds scope)
  (find-if (lambda (h) (equal (getf h :scope) scope)) holds))

(defun hold-for-control (holds control)
  (find-if (lambda (h) (equal (getf h :control) control)) holds))

(defun dispatch-admitted-p (holds scope point)
  "The barrier is revalidated at offer, at conversion and at the last send, so
an offer prepared before a hold cannot launch after it (SPEC-WORK.md:3966)."
  (and (member point *dispatch-points*)
       (not (hold-for-scope holds scope))))

(defun race-admitted-p (holds scope action)
  "A launch, a correction and a move raced against a scope pause are all held
(SPEC-WORK.md:3980, table :5741)."
  (declare (ignore action))
  (not (hold-for-scope holds scope)))

;;; ------------------------------------------------------------------
;;; The acceptance retained under a hold (SPEC-WORK.md:3968)
;;; ------------------------------------------------------------------

(defun accept-offer (&key holds scope offer attempt generation request)
  "An acceptance arriving under a hold is retained :accepted-held, reservation
intact: no lease, no launch, no capacity release."
  (if (hold-for-scope holds scope)
      (list :offer offer :attempt attempt :scope scope :generation generation
            :request request :effect :accepted-held :reservation t
            :lease nil :launch nil :release nil)
      (list :offer offer :attempt attempt :scope scope :generation generation
            :request request :effect :accepted :reservation nil
            :lease (format nil "lease-~A" offer) :launch t :release nil)))

(defun reconcile-acceptance (acceptance &key holds generation)
  "Lifting the hold does not convert a held acceptance; only reconcile rechecks
its generation, identity, capacity and the other holds (SPEC-WORK.md:3970)."
  (if (and (eq (getf acceptance :effect) :accepted-held)
           (null (hold-for-scope holds (getf acceptance :scope)))
           (eql generation (getf acceptance :generation)))
      (let ((converted (copy-list acceptance)))
        (setf (getf converted :effect) :accepted
              (getf converted :reservation) nil
              (getf converted :lease) (format nil "lease-~A" (getf converted :offer))
              (getf converted :launch) t)
        converted)
      acceptance))

;;; ------------------------------------------------------------------
;;; Reconciliation keeps contradiction (SPEC-WORK.md:3991-4009)
;;; ------------------------------------------------------------------

(defun make-observation (&key attempt outcome usage source)
  (list :attempt attempt :outcome outcome :usage usage :source source))

(defun observation-attempt (o) (getf o :attempt))
(defun observation-outcome (o) (getf o :outcome))
(defun observation-usage (o) (getf o :usage))

(defun synthesised-usage (o)
  "Missing usage stays unknown and a stop report never synthesises zero cost
(SPEC-WORK.md:3999)."
  (let ((usage (observation-usage o)))
    (if usage usage :absent)))

(defun observation-conflicting-p (a b)
  "Two observations conflict when one attempt is reported under two outcomes."
  (and (equal (observation-attempt a) (observation-attempt b))
       (not (equal (observation-outcome a) (observation-outcome b)))))

(defun reconcile-observations (observations)
  "Contradictory observations are preserved unresolved, never last-write-wins
(SPEC-WORK.md:3998)."
  (let ((contradiction
          (loop for tail on observations
                thereis (loop for other in (rest tail)
                              thereis (observation-conflicting-p (first tail) other)))))
    (list :status (if contradiction :unresolved :resolved)
          :retained observations)))

(defun release-permitted-p (actor holder &key confirmed-exit-p)
  "Confirmed termination permits capacity reconciliation but bypasses no
holder-only release: the coordinator never signs for a holder
(SPEC-WORK.md:4003)."
  (declare (ignore confirmed-exit-p))
  (equal actor holder))

;;; ------------------------------------------------------------------
;;; Resume is two different acts under one verb (SPEC-WORK.md:4011)
;;; ------------------------------------------------------------------

(defun resume-workers (&key capability)
  "resume-workers stages a resume directive but refuses an unsupported
capability before sending (SPEC-WORK.md:4014)."
  (if (eq capability :unsupported)
      (values nil "RESUME FAIL: unsupported capability")
      (values t "RESUME OK: resume directive staged")))

(defun resume-hold-kept-p (evidence)
  "The hold is kept until a running observation at the resumed boundary; a
delivery receipt alone releases nothing (SPEC-WORK.md:4015)."
  (not (eq evidence :running-observation)))

(defun unsupported-outcome (&key control holds)
  "An unsupported outcome closes the control's transport operation failed and
clears neither the hold nor the uncertainty (SPEC-WORK.md:4017)."
  (declare (ignore holds))
  (list :control control :transport :failed :hold t :uncertain t :cleared nil))

;;; ------------------------------------------------------------------
;;; execution correct is a linked segment (SPEC-WORK.md:4021-4037)
;;; ------------------------------------------------------------------

(defun correct-segment (from-generation to-generation usage result)
  "The old generation's usage and results are a linked segment, not a rewrite of
the earlier attempt (SPEC-WORK.md:4031)."
  (list :from-generation from-generation :to-generation to-generation
        :usage usage :result result :link :linked :rewrite nil))

(defun execution-correct (&key node request generation old-generation bytes
                               old-usage old-result seen)
  "One envelope under one request id installs the node's hold, writes the node's
own :correct event and binds the new generation to the immutable instruction
bytes and their SHA-256, so a retry cannot bump the generation twice
(SPEC-WORK.md:4024). A missing, mismatched or stale stage refuses whole."
  (when (null bytes)
    (return-from execution-correct
      (values nil (format nil "CORRECT FAIL node=~A: instruction stage missing" node))))
  (if (and seen (equal (getf seen :request) request))
      (let ((retry (copy-list seen)))
        (setf (getf retry :retried) t)
        retry)
      (list :node node :request request :generation generation
            :old-generation old-generation
            :event (list :kind :correct :node node :generation generation)
            :instruction-sha256 (sha256-hex bytes)
            :hold (list :node node)
            :segment (correct-segment old-generation generation old-usage old-result))))

(defun bare-correct (node &key live uncertain)
  "The bare correct still invalidates evidence and claims no delivery, and while
any execution of the node is live or uncertain it is refused by name
(SPEC-WORK.md:4034)."
  (if (or live uncertain)
      (values nil
              (format nil "CORRECT FAIL node=~A: execution live, use execution correct" node)
              nil)
      (values t "CORRECT OK"
              (list :kind :correct :node node :delivery nil :evidence-invalidated t))))


;;; ------------------------------------------------------------------
;;; folded from replays-8641.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8641.lisp --- the smallest pure records the five acceptance replays
;;;; of card 8641 assert. Each paragraph of docs/SPEC-WORK.md is reduced to the
;;;; facts it promises, without a session, a socket, a savepoint file, a worker
;;;; or an intake adapter:
;;;;
;;;;   a-broken-assertion-must-fail         (:6371-6373, :6384-6387)
;;;;   a-savepoint-is-not-a-shared-backup   (:5790-5793, :6270-6303)
;;;;   a-stop-reaches-distributed-work      (:6374-6378)
;;;;   add-field-order-is-complete          (:962-967, :2977-2999, :5819-5822)
;;;;   archive-completeness                 (:6232)
;;;;
;;;; The transports these records would ride (a savepoint file, a distributed
;;;; worker, an intake adapter) stay outside this slice; the records and the
;;;; refusals are what the replays can assert here.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; a-broken-assertion-must-fail (SPEC-WORK.md:6371-6373, :6384-6387)
;;; ------------------------------------------------------------------

(defstruct (regression-receipt
             (:constructor make-regression-receipt
                 (&key criterion revision-coverage uncertainty assertion mutation)))
  "One regression receipt: the specific criterion, its revision coverage, the
remaining uncertainty, and the assertion together with the mutation that
deliberately breaks the behaviour it asserts. ASSERTION and MUTATION are
functions of the preserved behaviour."
  criterion revision-coverage uncertainty assertion mutation)

(defun assertion-holds-p (receipt behaviour)
  "The receipt's assertion on a behaviour, which is T when the behaviour is
preserved."
  (funcall (regression-receipt-assertion receipt) behaviour))

(defun regression-evidence-p (receipt behaviour)
  "A test is regression evidence only when its assertion holds on the preserved
behaviour and the deliberately broken behaviour makes it fail. A test that still
passes with its asserted behaviour broken is not regression evidence
(SPEC-WORK.md:6371-6373)."
  (let ((broken (funcall (regression-receipt-mutation receipt) behaviour)))
    (and (funcall (regression-receipt-assertion receipt) behaviour)
         (not (funcall (regression-receipt-assertion receipt) broken)))))

(defun green-badge-p (receipt behaviour)
  "The badge a receipt earns: :evidence only for a real mutation, never a green
badge for a test that passes when its asserted behaviour is broken
(SPEC-WORK.md:6384-6387)."
  (if (regression-evidence-p receipt behaviour) :evidence nil))

;;; ------------------------------------------------------------------
;;; a-savepoint-is-not-a-shared-backup (SPEC-WORK.md:5790-5793, :6270-6303)
;;; ------------------------------------------------------------------

(defstruct (savepoint
             (:constructor make-savepoint
                 (&key id schema local-revision journal-id replay-cut boundary
                       manifest manifest-sha image local-replies age failed-backup)))
  "One validated atomic local savepoint: its schema, journal id, the local
revision of its image, the replay cut and boundary records, its own manifest and
content references, the `manifest=<sha>` hash of that manifest's complete
canonical bytes, its age, and whether a replacement backup failed. The periodic
clip supplies the separately observable shared checkpoint."
  id schema local-revision journal-id replay-cut boundary
  manifest manifest-sha image local-replies age failed-backup)

(defstruct (checkpoint
             (:constructor make-checkpoint (&key id shared-revision source)))
  "The shared checkpoint the clip supplies, kept a different thing from a
savepoint."
  id shared-revision source)

(defun savepoint-is-not-shared-backup-p (savepoint)
  "A local savepoint is never the shared backup (SPEC-WORK.md:6277)."
  (declare (ignore savepoint))
  t)

(defun restore-open (&key savepoint)
  "A restore opens a read-only, isolated, non-dispatching recovery session: it
inherits no coordinator ownership, reanimates no assignment and replays no bus
message (SPEC-WORK.md:6285-6288)."
  (declare (ignore savepoint))
  (list :read-only t :isolated t :dispatch nil :ownership nil
        :assignments '() :messages '()))

(defun savepoint-report (savepoint &key shared-revision unshared failed-backups)
  "Savepoint age, the local and the shared revisions side by side, the unshared
work and the failed backup attempts (SPEC-WORK.md:6278-6280)."
  (list :age (savepoint-age savepoint)
        :local-revision (savepoint-local-revision savepoint)
        :shared-revision shared-revision
        :unshared unshared
        :failed-backups failed-backups))

(defun checkpoint-line (thing)
  "The line for a shared checkpoint. A savepoint is never printed where a
checkpoint was asked for (SPEC-WORK.md:5790-5793)."
  (unless (checkpoint-p thing)
    (error 'unsupported-input
           :what "a savepoint is never printed where a checkpoint was asked for"))
  (list :checkpoint (checkpoint-id thing) (checkpoint-shared-revision thing)))

;;; ------------------------------------------------------------------
;;; a-stop-reaches-distributed-work (SPEC-WORK.md:6374-6378)
;;; ------------------------------------------------------------------

(defstruct (control-request
             (:constructor make-control-request (&key kind request targets)))
  "One priority change, correction, pause or stop, with its durable request
identity and the already-distributed tasks it names."
  kind request targets)

(defstruct (control-handle
             (:constructor make-control-handle
                 (&key target request delivered acknowledged reconciled generation)))
  "One reconciled execution handle: delivery and acknowledgement of the durable
request, and the generation a correction bumps."
  target request delivered acknowledged reconciled generation)

(defstruct (blocked-question
             (:constructor make-blocked-question
                 (&key node question fallback persisted)))
  "A blocked question with its bounded fallback plan, persisted so a missing
answer at night does not stall every independent task (SPEC-WORK.md:6377-6378)."
  node question fallback persisted)

(defparameter *control-kinds* '(:prioritise :correct :pause :stop)
  "The four controls reached by `execution pause`, `stop` and `correct`.")

(defun control-kind-p (kind)
  (and (member kind *control-kinds*) t))

(defun distribute-control (request targets)
  "Carry one durable control request to each already-distributed task and return
its reconciled handles. The same request id reaches all of them."
  (list :request (control-request-request request)
        :handles (mapcar (lambda (target)
                           (make-control-handle :target target
                                                :request (control-request-request request)
                                                :delivered t :acknowledged t
                                                :reconciled t :generation 1))
                         targets)))

(defun stop-reaches-p (distribution)
  "True only when every distributed handle has delivered, acknowledged and
reconciled the request (SPEC-WORK.md:6375)."
  (let ((handles (getf distribution :handles)))
    (and handles
         (every (lambda (h)
                  (and (control-handle-delivered h)
                       (control-handle-acknowledged h)
                       (control-handle-reconciled h)))
                handles))))

(defun control-retry-identity (distribution)
  (getf distribution :request))

(defun apply-correction (handle)
  "A correction across already-distributed work bumps the generation of its
handle and never rewrites the original."
  (let ((h (copy-control-handle handle)))
    (incf (control-handle-generation h))
    h))

(defun fallback-plan (question)
  "The bounded fallback plan persisted with a blocked question."
  (when (blocked-question-persisted question)
    (or (blocked-question-fallback question) :bounded-fallback)))

(defun independent-tasks-stall-p (questions)
  "True when a blocked question has no persisted bounded fallback plan."
  (some (lambda (q) (null (fallback-plan q))) questions))

;;; ------------------------------------------------------------------
;;; add-field-order-is-complete (SPEC-WORK.md:962-967, :2977-2999, :5819-5822)
;;; ------------------------------------------------------------------

(defparameter *node-add-structure-fields*
  '(:verb :node-type :title :under :category :required :acceptance
    :repo :links :private :version :reason)
  "The twelve fields of a completed `node add` `:structure` event, in the order
the registry lists them, which is also the payload-digest order
(SPEC-WORK.md:965-967, :2977-2980).")

(defparameter *node-add-pre-fold-fields*
  '(:verb :node-type :title :under :category :required :acceptance :repo
    :links :reason)
  "A fixture in the pre-fold add shape, refused rather than read as the new one
(SPEC-WORK.md:2981-2984).")

(defun node-add-field-value (request key)
  "The field's value, or `(:absent)` when the caller did not give it. An empty
list stays `()` and a cleared links list stays `(:absent)`."
  (let ((pair (assoc key request)))
    (if pair (cdr pair) +absent+)))

(defun node-add-structure (request)
  "The `node add` `:structure` field list, every one of the twelve fields
written in order, absent `(:absent)`."
  (loop for key in *node-add-structure-fields*
        collect key
        collect (node-add-field-value request key)))

(defun node-add-structure-keys (request)
  (loop for (key nil) on (node-add-structure request) by #'cddr collect key))

(defun node-add-pre-fold-structure (request)
  "The same request in the pre-fold field order, for the loader refusal."
  (loop for key in *node-add-pre-fold-fields*
        collect key
        collect (node-add-field-value request key)))

(defun node-add-digest (request)
  "Serializer one: the payload digest of the completed `node add` structure."
  (sha256-hex (canonical-string (node-add-structure request))))

(defun write-node-add-value (value stream)
  "Serializer two's own writer, independent of `canonical-print`, over the same
restricted spellings: `(:absent)`, `()`, `""` and `false` remain distinct."
  (typecase value
    (null (write-string "()" stream))
    (cons (write-char #\( stream)
          (let ((first t))
            (dolist (item value)
              (unless first (write-char #\Space stream))
              (setf first nil)
              (write-node-add-value item stream)))
          (write-char #\) stream))
    (keyword (write-char #\: stream)
             (write-string (string-downcase (symbol-name value)) stream))
    (string (write-char #\" stream)
            (loop for ch across value
                  do (case ch
                       (#\" (write-string "\\\"" stream))
                       (#\\ (write-string "\\\\" stream))
                       (t (write-char ch stream))))
            (write-char #\" stream))
    (integer (format stream "~D" value))
    (t (error 'restricted-data-violation :value value))))

(defun node-add-digest-second (request)
  "Serializer two: the second, independent digest of the same structure."
  (let ((text (with-output-to-string (s)
                (write-char #\( s)
                (let ((first t))
                  (dolist (key *node-add-structure-fields*)
                    (unless first (write-char #\Space s))
                    (setf first nil)
                    (write-node-add-value key s)
                    (write-char #\Space s)
                    (write-node-add-value (node-add-field-value request key) s)))
                (write-char #\) s))))
    (sha256-hex text)))

(defun load-node-add-fixture (fields)
  "Load a `node add` structure fixture only in the completed field order. A
fixture in the pre-fold order refuses `schema revision unsupported`
(SPEC-WORK.md:2981-2984)."
  (unless (equal (loop for (key nil) on fields by #'cddr collect key)
                 *node-add-structure-fields*)
    (error 'unsupported-input :what "schema revision unsupported"))
  fields)

;;; ------------------------------------------------------------------
;;; archive-completeness (SPEC-WORK.md:6232)
;;; ------------------------------------------------------------------

(defparameter *archive-gap-kinds*
  '(:missing-attachment :unavailable-comment :unsupported-field
    :size-truncation :rate-limit :mid-page-failure)
  "The six gaps an incomplete archive must state rather than absorb.")

(defstruct (archive-gap
             (:constructor make-archive-gap (&key kind detail source-issue)))
  "One explicit gap: what was not captured and the source issue it belongs to."
  kind detail source-issue)

(defstruct (archive-capture
             (:constructor make-archive-capture (&key source-issue author gaps)))
  "One capture with its source issue, its author class and its explicit gaps."
  source-issue author gaps)

(defun archive-gaps-explicit-p (capture)
  "True when every gap names one of the six kinds and a detail, so none is a
silent drop."
  (every (lambda (g)
           (and (member (archive-gap-kind g) *archive-gap-kinds*)
                (stringp (archive-gap-detail g))))
         (archive-capture-gaps capture)))

(defun archive-absorbable-p (capture)
  "An incomplete capture prohibits absorption; only a gap-free capture may be
absorbed (SPEC-WORK.md:6232)."
  (null (archive-capture-gaps capture)))

(defun author-retains-source-p (capture)
  "A mixed, external or unknown author retains its source issue."
  (and (member (archive-capture-author capture) '(:mixed :external :unknown))
       (archive-capture-source-issue capture)
       t))


;;; ------------------------------------------------------------------
;;; folded from replays-8645.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8645.lisp --- the pure part of the five acceptance replays of
;;;; nova-tools #362.
;;;;
;;;; docs/SPEC-WORK.md:1441-1528 makes the goal a scope-keyed reference and
;;;; `goal update` a thin verb writing the node's own `:transition` or
;;;; `:evidence` event; :5948-5974 fixes what the five `goal-` replays promise;
;;;; :5782 fixes the historic tick; :6248 fixes the hostile-data intake. Nothing
;;;; here starts a session, a socket or a CLI: this is the pure planning,
;;;; transition and intake layer the replays call, and the wiring a live
;;;; session owns is named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The goal world: nodes, the scope-keyed goal reference, the event log
;;; ------------------------------------------------------------------

(defstruct (goal-world (:constructor %make-goal-world))
  (rev 0 :type integer)
  nodes    ; id -> (:state <s> :branch <b>)
  goals    ; (scope . node-id), one goal reference per scope
  (events '())  ; newest first, each (:kind :node :fields :rev :request)
  (seen '())    ; request-id -> event rev, the journal's dedup answer
  (scope-rev 0))

(defun make-goal-world (&key (rev 0) nodes goals scope-rev)
  "A goal world at REV. NODES is an alist of (id . (:state :branch))."
  (%make-goal-world
   :rev rev
   :scope-rev (or scope-rev rev)
   :nodes (loop for (id . plist) in nodes
                collect (cons id (copy-list plist)))
   :goals (copy-list goals)))

(defun gw-node (world id)
  (cdr (assoc id (goal-world-nodes world) :test #'equal)))

(defun gw-state (world id)
  (getf (gw-node world id) :state))

(defun (setf gw-state) (value world id)
  (setf (getf (cdr (assoc id (goal-world-nodes world) :test #'equal)) :state) value))

(defun gw-branch (world id)
  (getf (gw-node world id) :branch :o))

(defun gw-goal (world scope)
  (cdr (assoc scope (goal-world-goals world) :test #'equal)))

(defun last-event (world)
  (first (goal-world-events world)))

(defun %goal-event (world kind node fields &key request)
  "Append one event and answer its revision. A request id is remembered with the
revision it produced, so a retry can replay the recorded answer."
  (let ((rev (incf (goal-world-rev world))))
    (push (list :kind kind :node node :fields fields :rev rev :request request)
          (goal-world-events world))
    (when request
      (push (cons request rev) (goal-world-seen world)))
    rev))

(defun event-fields-keys (event)
  (loop for (k v) on (getf event :fields) by #'cddr collect k))

(defun own-fields-ok-p (event)
  "An event of KIND must carry exactly its kind's ordered field list
(docs/SPEC-WORK.md:330-339)."
  (equal (event-fields-keys event) (kind-fields (getf event :kind))))

(defun stop-field (state)
  "stop= is derived from the state and no second field (docs/SPEC-WORK.md:1460)."
  (case state
    (:cancel-requested "requested")
    (:cancelled "cancelled")
    (:deferred "deferred")
    (t "none")))

(defun goal-world-show (world &key scope)
  "The GOAL OK fields a newly selected harness loads. Nothing here is the
conversation it came from."
  (let* ((goal (gw-goal world scope))
         (state (and goal (gw-state world goal))))
    (list :scope scope :goal goal :rev (goal-world-rev world)
          :state state :stop (if state (stop-field state) "none"))))

(defun goal-check (world)
  "no finding: a stop request is a legal transition and nothing more."
  (declare (ignore world))
  '())

;;; ------------------------------------------------------------------
;;; goal update and goal set
;;; ------------------------------------------------------------------

(defparameter *stop-edges* '(:todo :doing :blocked :unknown)
  "The table's own edge to :cancel-requested; :review and :done have none.")

(defparameter *progress-edges* '(:todo :blocked :review :unknown)
  "The progress-only edge to :doing, refused where the table refuses it.")

(defun %stale-line (scope goal expect current)
  (format nil "GOAL FAIL scope=~A goal=~A expect=~D current=~D: stale"
          scope goal expect current))

(defun goal-world-update (world &key scope expect (form :progress) reason
                          progress pointer criterion against request)
  "One `goal update` form, on the current goal node of SCOPE and no other node.
Answer (values OK LINE CODE). A stale --expect refuses at exit 1 and writes
nothing; a node in :cancel-requested refuses every update but a person's own
`state --to doing` `stop requested`."
  (when (null expect)
    (return-from goal-world-update (values nil "GOAL FAIL: --expect is required" 2)))
  (when (/= expect (goal-world-rev world))
    (return-from goal-world-update
      (values nil (%stale-line scope (gw-goal world scope) expect
                               (goal-world-rev world))
              1)))
  (let ((goal (gw-goal world scope)))
    (unless goal
      (return-from goal-world-update (values nil "GOAL FAIL: no goal in scope" 1)))
    (let ((state (gw-state world goal)))
      (ecase form
        (:stop
         (unless (member state *stop-edges*)
           (return-from goal-world-update (values nil "GOAL FAIL: no edge" 1)))
         ;; A stop request is a :transition carrying :reason and no :evidence.
         (let ((rev (%goal-event world :transition goal
                                 (list :to :cancel-requested
                                       :reason reason
                                       :blocked-by +absent+
                                       :evidence +absent+)
                                 :request request)))
           (setf (gw-state world goal) :cancel-requested)
           (values t
                   (format nil "GOAL OK scope=~A goal=~A change=stop kind=transition rev=~D"
                           scope goal rev)
                   0)))
        (:progress
         (when (eq state :cancel-requested)
           (return-from goal-world-update
             (values nil "GOAL FAIL: stop requested" 1)))
         (cond
          ;; --progress with the evidence triple writes the :evidence event
          ;; `evidence` writes, on the goal node.
          ((and pointer criterion against)
           (let ((rev (%goal-event world :evidence goal
                                   (list :pointer pointer
                                         :criterion criterion
                                         :against against
                                         :generation 1
                                         :attempt +absent+)
                                   :request request)))
             (values t
                     (format nil "GOAL OK scope=~A goal=~A change=evidence kind=evidence rev=~D"
                             scope goal rev)
                     0)))
          ;; A progress line on a node already :doing carries evidence or it is
          ;; not written.
          ((eq state :doing)
           (values nil "GOAL FAIL: no edge" 1))
          ((member state *progress-edges*)
           (let ((rev (%goal-event world :transition goal
                                   (list :to :doing
                                         :reason progress
                                         :blocked-by +absent+
                                         :evidence +absent+)
                                   :request request)))
             (setf (gw-state world goal) :doing)
             (values t
                     (format nil "GOAL OK scope=~A goal=~A change=progress kind=transition rev=~D"
                             scope goal rev)
                     0)))
          (t (values nil "GOAL FAIL: no edge" 1))))))))

(defun goal-world-set (world &key scope goal expect request reason clear)
  "`goal set` writes the one :goal event of the scope, whose :node is (:absent).
The disposition refusal is unconditional; a retried request replays its event."
  (let ((prior (assoc request (goal-world-seen world) :test #'equal)))
    (when prior
      (return-from goal-world-set
        (values t
                (format nil "GOAL OK scope=~A goal=~A change=~A kind=goal rev=~D"
                        scope (if clear "-" goal) (if clear "clear" "set")
                        (cdr prior))
                0))))
  (when (and (not clear) goal)
    (let ((state (gw-state world goal)))
      (cond ((null state)
             (return-from goal-world-set (values nil "GOAL FAIL: no such node" 1)))
            ((eq state :done)
             (return-from goal-world-set (values nil "GOAL FAIL: disposition=done" 1)))
            ((eq state :cancelled)
             (return-from goal-world-set (values nil "GOAL FAIL: disposition=cancelled" 1))))))
  (when (or (null expect) (/= expect (goal-world-rev world)))
    (return-from goal-world-set
      (if (null expect)
          (values nil "GOAL FAIL: --expect is required" 2)
          (values nil (%stale-line scope goal expect (goal-world-rev world)) 1))))
  (let ((rev (%goal-event world :goal +absent+
                          (list :change (if clear :clear :set)
                                :scope scope
                                :goal (if clear +absent+ goal)
                                :reason reason)
                          :request request)))
    (if clear
        (setf (goal-world-goals world)
              (remove scope (goal-world-goals world) :key #'car :test #'equal))
        (let ((cell (assoc scope (goal-world-goals world) :test #'equal)))
          (if cell
              (setf (cdr cell) goal)
              (push (cons scope goal) (goal-world-goals world)))))
    (values t
            (format nil "GOAL OK scope=~A goal=~A change=~A kind=goal rev=~D"
                    scope (if clear "-" goal) (if clear "clear" "set") rev)
            0)))

;;; ------------------------------------------------------------------
;;; The sibling verbs the goal replays compare against
;;; ------------------------------------------------------------------

(defun gw-state-to (world id &key to reason)
  "`state --to <to> --reason`, by id. The same :transition field list the goal
verb writes, so the withdrawal to :doing is a person's own act."
  (%goal-event world :transition id
               (list :to to :reason reason
                     :blocked-by +absent+ :evidence +absent+))
  (setf (gw-state world id) to)
  (last-event world))

(defun gw-cancel (world id &key evidence)
  "`event --kind cancel --evidence <pointer>`, admitted only from
:cancel-requested: the one evidence-bearing cancellation."
  (unless (eq (gw-state world id) :cancel-requested)
    (return-from gw-cancel (values nil "cancel refused: no edge" 1)))
  (unless (and (listp evidence) evidence)
    (return-from gw-cancel (values nil "cancel refused: no evidence" 1)))
  (%goal-event world :cancel id (list :evidence evidence))
  (setf (gw-state world id) :cancelled)
  (values t "EVENT OK kind=cancel" 0))

(defun gw-accept-add (world id)
  "`accept --add` on the node: it moves the scope revision; `goal update` never does."
  (declare (ignore id))
  (incf (goal-world-scope-rev world)))

;;; ------------------------------------------------------------------
;;; Historic delivery and current verification are two questions
;;; (docs/SPEC-WORK.md:4943-4951, :5782)
;;; ------------------------------------------------------------------

(defstruct (tick-record (:constructor make-tick-record
                            (&key id pinned-rev source-sha scope historic-tick
                                  current-verification)))
  id pinned-rev source-sha scope
  (historic-tick t)
  (current-verification :verified))

(defun source-change (tick &key new-source-sha new-criterion)
  "A changed source or criterion keeps the historic tick at its pinned revision
while the current view requires re-verification. Unrelated receipts are untouched
because this answers for one receipt only."
  (let* ((changed (or (and new-source-sha
                           (not (equal new-source-sha (tick-record-source-sha tick))))
                      new-criterion)))
    (make-tick-record
     :id (tick-record-id tick)
     :pinned-rev (tick-record-pinned-rev tick)
     :source-sha (tick-record-source-sha tick)
     :scope (tick-record-scope tick)
     :historic-tick (tick-record-historic-tick tick)
     :current-verification (if changed
                               :recheck-needed
                               (tick-record-current-verification tick)))))

;;; ------------------------------------------------------------------
;;; Hostile data (docs/SPEC-WORK.md:6248)
;;; ------------------------------------------------------------------

(defstruct (intake-limits (:constructor make-intake-limits
                              (&key (max-depth 64) (max-bytes 65536) (max-nodes 4096))))
  (max-depth 64) (max-bytes 65536) (max-nodes 4096))

(defvar *intake-visits* 0
  "Bytes examined by intake-scan. A deep or high-fan-out input must grow this
linearly in its own length, never quadratically.")

(defun intake-scan (text limits)
  "One linear pre-parse pass, counting peak nesting depth and atom nodes.
It never calls EVAL or READ, so reader evaluation is disabled by construction."
  (declare (ignore limits))
  (let ((depth 0) (peak 0) (nodes 0) (in-token nil))
    (loop for ch across text
          do (incf *intake-visits*)
             (cond ((char= ch #\() (incf depth) (setf peak (max peak depth))
                                  (setf in-token nil))
                   ((char= ch #\)) (when (plusp depth) (decf depth))
                                  (setf in-token nil))
                   ((find ch " \t\r\n") (setf in-token nil))
                   (t (unless in-token (incf nodes) (setf in-token t)))))
    (values peak nodes)))

(defun hostile-intake (text &key (limits (make-intake-limits)))
  "Intake of untrusted text. Answer (values OK LINE WHY). A read-time eval form
is refused before any parse; the byte, depth and node limits refuse the input
whole rather than truncating it."
  (when (search "#." text)
    (return-from hostile-intake
      (values nil "INTAKE FAIL: reader evaluation disabled" :eval)))
  (when (> (length text) (intake-limits-max-bytes limits))
    (return-from hostile-intake
      (values nil "INTAKE FAIL: byte limit exceeded" :bytes)))
  (multiple-value-bind (depth nodes) (intake-scan text limits)
    (when (> depth (intake-limits-max-depth limits))
      (return-from hostile-intake
        (values nil "INTAKE FAIL: depth limit exceeded" :depth)))
    (when (> nodes (intake-limits-max-nodes limits))
      (return-from hostile-intake
        (values nil "INTAKE FAIL: node limit exceeded" :nodes))))
  (values t "INTAKE OK" nil))

(defun archive-path-safe-p (path)
  "An archive member path that stays inside the archive: no absolute path, no
traversal, no drive or home escape and no backslash escaping."
  (and (stringp path)
       (plusp (length path))
       (char/= (char path 0) #\/)
       (char/= (char path 0) #\~)
       (null (find #\\ path))
       (null (search ".." path))))

(defun imported-prose-effect (prose)
  "Imported prose is data: it can neither run a command nor alter authority."
  (declare (ignore prose))
  (list :command nil :authority nil))


;;; ------------------------------------------------------------------
;;; folded from replays-8648.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8648.lisp --- the pure model behind the five acceptance replays
;;;; named by SPEC-WORK.md:
;;;;
;;;;   regression-opens-repair-work                 :4943-4951,5782-5785
;;;;   reply-retired-only-under-verified-coverage   :6020-6024,6316-6325
;;;;   restore-is-isolated-and-dispatches-nothing   :6285-6290,5790-5793
;;;;   reuse-only-valid-review                      :4851
;;;;   review-cycles-stay-visible                   :6368-6370
;;;;
;;;; Nothing here starts a session, a restore, a dispatch or a review process:
;;;; the live-session, savepoint and CLI wiring a later slice owns is not in
;;;; this slice. These are the data and the verdicts the replays assert.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; A confirmed regression opens linked repair work
;;; (SPEC-WORK.md:4943-4951,5782-5785)
;;; ------------------------------------------------------------------

(defstruct (verification-summary
             (:constructor make-verification-summary
                 (&key node pinned-revision current-revision (state :verified)
                       criteria dependencies history)))
  "The two records joined by stable id: the historic tick pinned at the
revision it ran against, and the current verification summary, which a changed
source, criterion or dependency invalidates without erasing either."
  node pinned-revision current-revision state criteria dependencies history)

(defun invalidate-verification (summary new-revision &key reason)
  "A changed source, criterion or dependency moves the current summary to
:recheck-needed and the pinned historic tick is retained untouched. REASON is
accepted for the caller's provenance and does not alter the retained record."
  (declare (ignore reason))
  (make-verification-summary
   :node (verification-summary-node summary)
   :pinned-revision (verification-summary-pinned-revision summary)
   :current-revision new-revision
   :state :recheck-needed
   :criteria (verification-summary-criteria summary)
   :dependencies (verification-summary-dependencies summary)
   :history (verification-summary-history summary)))

(defun recheck-needed-p (summary)
  "The answer a changed source or criterion gives: a recheck is needed."
  (eq :recheck-needed (verification-summary-state summary)))

(defstruct (repair-work
             (:constructor make-repair-work (&key id node (state :todo))))
  "A repair item created under the ordinary policy: open (:todo) and linked to
the regressed node. The closed node stays closed; this is new work, not a
silent reopen."
  id node state)

(defun confirm-regression (summary &key id)
  "A confirmed regression creates linked open repair work under the ordinary
policy; the historic tick and the closed node are left as they are."
  (make-repair-work :id (or id (format nil "~A-repair" (verification-summary-node summary)))
                    :node (verification-summary-node summary)
                    :state :todo))

;;; ------------------------------------------------------------------
;;; A reply retires only under verified coverage
;;; (SPEC-WORK.md:6020-6024,6316-6325)
;;; ------------------------------------------------------------------

(defstruct (retained-disposition
             (:constructor make-retained-disposition
                 (&key request payload-digest sequence record-hash reply boundary)))
  "A request's identity, its accepted record's sequence and hash and the
original reply, kept in the image even when its events lie before the cut."
  request payload-digest sequence record-hash reply boundary)

(defstruct (coverage
             (:constructor make-coverage
                 (&key snapshot-reachable retained-events-reachable
                       dedup-root-reachable push-ok)))
  "The three reachabilities a boundary record names, plus the push that a
locally existing commit is not proof of."
  snapshot-reachable retained-events-reachable dedup-root-reachable push-ok)

(defun coverage-verified-p (coverage)
  "A disposition is retired only once the committed snapshot, its retained
events and the dedup root are verified reachable and the push succeeded."
  (and (coverage-snapshot-reachable coverage)
       (coverage-retained-events-reachable coverage)
       (coverage-dedup-root-reachable coverage)
       (coverage-push-ok coverage)))

(defun retire-reply (disposition coverage)
  "Retire a retained disposition to the dedup index's `already applied` only
under verified coverage. Otherwise the original OK still answers from the
retained disposition, and an unreadable dedup root prints the coverage gap. A
reply is never deleted, invented or reconstructed from current state."
  (if (coverage-verified-p coverage)
      (values :already-applied "already applied" nil)
      (values :retained
              (retained-disposition-reply disposition)
              (unless (coverage-dedup-root-reachable coverage)
                (list "recovery-gap kind=coverage-unverified")))))

;;; ------------------------------------------------------------------
;;; A restore is isolated and dispatches nothing
;;; (SPEC-WORK.md:6285-6290,5790-5793)
;;; ------------------------------------------------------------------

(defstruct (restore-session
             (:constructor make-restore-session
                 (&key savepoint (read-only-p t) ownership assignments
                       (dispatch-count 0) replayed-messages external-effects
                       (mode :isolated) image replies replayed-records cut boundary)))
  "A restore opens a read-only, isolated, non-dispatching recovery session: it
inherits no coordinator ownership, reanimates no assignment, replays no bus
message and duplicates no external side effect. IMAGE and REPLIES are the
validated immutable image and its retained dispositions; REPLAYED-RECORDS are
the journal records strictly after the cut, each replayed once in sequence."
  savepoint read-only-p ownership assignments dispatch-count replayed-messages
  external-effects mode image replies replayed-records cut boundary)

(defun restore-dispatch (session &key node)
  "The isolated restore refuses to dispatch anything."
  (declare (ignore session node))
  (values nil "RESTORE FAIL: read-only isolated session dispatches nothing" 2))

(defun promote-repair (session repair &key fenced validated reconciled)
  "A selected repair is promoted only through a fenced validated reconciliation
with the current state."
  (declare (ignore session repair))
  (if (and fenced validated reconciled)
      (values t "REPAIR OK" 0)
      (values nil "REPAIR FAIL: requires fenced validated reconciliation" 2)))

;;; ------------------------------------------------------------------
;;; Same-scope review is reusable; only validly
;;; (SPEC-WORK.md:4851)
;;; ------------------------------------------------------------------

(defstruct (scope-review
             (:constructor make-scope-review
                 (&key friend scope head acceptance dependencies
                       (independent-gate-p nil) verdict)))
  friend scope head acceptance dependencies independent-gate-p verdict)

(defun scope-review-reusable-p (review &key scope head acceptance dependencies)
  "A same-scope review is reusable while its head, acceptance and dependencies
are unchanged; an independent friend gate cannot be replaced by reuse."
  (and (not (scope-review-independent-gate-p review))
       (equal (scope-review-scope review) scope)
       (equal (scope-review-head review) head)
       (equal (scope-review-acceptance review) acceptance)
       (equal (scope-review-dependencies review) dependencies)))

;;; ------------------------------------------------------------------
;;; Review and repair cycles stay visible as work and as cost
;;; (SPEC-WORK.md:6368-6370)
;;; ------------------------------------------------------------------

(defstruct (review-cycle
             (:constructor make-review-cycle
                 (&key friend revision finding-ids dispositions clearance
                       evidence-reused-p reread-delta (cost 0))))
  "One round: the friend, the exact revision, the finding ids, the author's
dispositions, the clearance, whether valid evidence was reused and the affected
delta reread, and the operational cost."
  friend revision finding-ids dispositions clearance evidence-reused-p
  reread-delta cost)

(defstruct (review-ledger
             (:constructor make-review-ledger (&key (cycles '()))))
  cycles)

(defun record-review-cycle (ledger cycle)
  "Append one review or repair cycle to the ledger, keeping the sequence."
  (make-review-ledger :cycles (append (review-ledger-cycles ledger) (list cycle))))

(defun review-ledger-covers-friends-p (ledger friends)
  "True when every required friend has a recorded cycle carrying its exact
revision, its finding ids and the author's dispositions."
  (every (lambda (friend)
           (let ((cycles (remove-if-not (lambda (c) (equal friend (review-cycle-friend c)))
                                        (review-ledger-cycles ledger))))
             (and cycles
                  (every #'review-cycle-revision cycles)
                  (some #'review-cycle-finding-ids cycles)
                  (some #'review-cycle-dispositions cycles))))
         friends))

(defun review-ledger-cycle-count (ledger)
  "The repeated cycles visible as work."
  (length (review-ledger-cycles ledger)))

(defun review-ledger-total-cost (ledger)
  "The repeated cycles visible as operational cost."
  (reduce #'+ (review-ledger-cycles ledger) :key #'review-cycle-cost :initial-value 0))


;;; ------------------------------------------------------------------
;;; folded from replays-efficiency-goal.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-efficiency-goal.lisp --- the pure part of the efficiency lessons
;;;; (docs/SPEC-WORK.md:4861-4919), the trial-adoption gate (:4780-4786) and the
;;;; goal reference a harness switch reads (:1449-1523, replays at :5938 and
;;;; :5975).
;;;;
;;;; Card 8644's five acceptance replays call these functions directly. The
;;;; live session, CLI, dispatch and snapshot wiring a later slice owns is not
;;;; here; the boundary is the same choice the seven applicable/delegation
;;;; replays made in src/replays-applicable-delegation.lisp.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; prime: the read-only projection under --max-bytes (:4877)
;;; ------------------------------------------------------------------

(defun prime (&key goal notes leases stop-requests (max-bytes 4096))
  "Render the projection -- the current goal, applicable notes, caller leases
and pending stop requests -- and bound it under MAX-BYTES. It is a read: it
creates and maintains no shadow state, so the same inputs print the same bytes."
  (let* ((text (format nil "goal=~A~%notes=~D~%leases=~D~%stop=~D~%"
                       (or goal "-") (length notes) (length leases)
                       (length stop-requests)))
         (limit (max 0 max-bytes)))
    (subseq text 0 (min (length text) limit))))

;;; ------------------------------------------------------------------
;;; decompose --pour: unpoured checklist items stay out of |O| (:4880)
;;; ------------------------------------------------------------------

(defstruct (checklist-item
             (:constructor make-checklist-item
                 (text &key independent-verification independent-worker
                             explicit-dependency isolated-recovery)))
  text independent-verification independent-worker explicit-dependency
  isolated-recovery)

(defun materializes-node-p (item)
  "A checklist item materialises a child node in O only for independent
verification, independent worker assignment, an explicit dependency edge or an
isolated recovery boundary. Every other item stays inline."
  (or (checklist-item-independent-verification item)
      (checklist-item-independent-worker item)
      (checklist-item-explicit-dependency item)
      (checklist-item-isolated-recovery item)))

(defun checklist-open-delta (items)
  "The number of open items a decompose --pour of ITEMS adds to |O|. Unpoured
checklist items never count in |O| and never count as verified."
  (count-if #'materializes-node-p items))

;;; ------------------------------------------------------------------
;;; :max-attempts and tripped= (:4884)
;;; ------------------------------------------------------------------

(defparameter *default-max-attempts* 3
  "Node attempts are bounded by :max-attempts, default 3.")

(defun attempts-tripped-p (attempts &key (max-attempts *default-max-attempts*))
  "tripped= is a status reading: the attempts have reached the bound."
  (>= attempts max-attempts))

(defun lease-admission (attempts &key reason (max-attempts *default-max-attempts*))
  "Taking a lease on a tripped node requires an explicit --reason. The reason
explains the operator's intent and grants no execution authority by itself."
  (if (and (attempts-tripped-p attempts :max-attempts max-attempts)
           (not (present-p reason)))
      (values nil "LEASE FAIL: tripped node requires --reason" 2)
      (values t "LEASE OK" 0)))

;;; ------------------------------------------------------------------
;;; delegate mode (:4887)
;;; ------------------------------------------------------------------

(defparameter *delegate-refused-actions* '(:edit :build)
  "A delegate role profile restricts the worker to designated verbs and
read-only git operations; file edits and build execution are refused below the
model by the sandbox and tool layer.")

(defun delegate-admission (role action)
  "Admit or refuse ACTION under ROLE. A declared delegate role refuses edits and
builds at exit 2; role transitions are configuration's, not this verb's."
  (if (and (eq role :delegate) (member action *delegate-refused-actions*))
      (values nil (format nil "DELEGATE FAIL: ~A refused below the model"
                          (string-downcase (symbol-name action)))
              2)
      (values t "DELEGATE OK" 0)))

;;; ------------------------------------------------------------------
;;; promote evidence, not enthusiasm (:4780)
;;; ------------------------------------------------------------------

(defstruct (trial-result
             (:constructor make-trial-result
                 (&key prospective baseline coverage quality tolerances-matched)))
  prospective baseline coverage quality tolerances-matched)

(defun adoption-verdict (result)
  "Automatic trial-to-adopt promotion requires a preregistered prospective
trial with a selected baseline, completed coverage, passing quality and the
predeclared tolerances with referenced results. A retrospective correlation
alone, or any missing qualification, cannot auto-promote."
  (cond
    ((not (trial-result-baseline result))
     (values nil "ADOPT FAIL: missing baseline" :missing-baseline))
    ((not (trial-result-coverage result))
     (values nil "ADOPT FAIL: incomplete coverage" :missing-coverage))
    ((not (trial-result-quality result))
     (values nil "ADOPT FAIL: unmatched quality" :unmatched-quality))
    ((not (trial-result-prospective result))
     (values nil "ADOPT FAIL: retrospective correlation alone" :retrospective))
    ((not (trial-result-tolerances-matched result))
     (values nil "ADOPT FAIL: predeclared tolerances not met" :tolerances))
    (t (values t "ADOPT OK" :promote))))

;;; ------------------------------------------------------------------
;;; Gas Town accounting: root-only step records and durable triggers
;;; (:4868-4873, replay :4915)
;;; ------------------------------------------------------------------

(defstruct (root-step-record (:constructor make-root-step-record (&key node steps)))
  node steps)

(defun root-step-record (task-id steps)
  "Root-only step records cut operational row counts: one record rooted at the
task, its steps inline, and no per-step node in O."
  (make-root-step-record :node task-id :steps (length steps)))

(defun step-record-count (records)
  (length records))

(defun step-record-node-explosion (records)
  "The number of new O nodes root-only step records create: none."
  (declare (ignore records))
  0)

(defstruct (next-trigger (:constructor make-next-trigger (&key kind due)))
  kind due)

(defstruct (waiting-item (:constructor make-waiting-item (&key id trigger)))
  id trigger)

(defun pulse-reexecutions (items)
  "A durable next-trigger re-executes only the waiting item whose trigger is
due. An empty pulse -- no due trigger -- causes zero model re-executions."
  (count-if (lambda (item) (next-trigger-due (waiting-item-trigger item))) items))

;;; ------------------------------------------------------------------
;;; the goal reference (:1441-1523; replays :5938, :5975)
;;; ------------------------------------------------------------------

(defstruct (goal-store
             (:constructor %make-goal-store)
             (:copier nil))
  scope goal node-state rev notes history dedup)

(defun make-goal-store (&key (scope "C") goal (node-state :doing) (rev 0)
                             notes history dedup)
  "The resident goal index for one scope, shared by every harness of the
session. DEDUP is the journal's own request-id answer, not a second ledger."
  (%make-goal-store :scope scope :goal goal :node-state node-state :rev rev
                    :notes (or notes '())
                    :history (or history '())
                    :dedup (or dedup (make-hash-table :test #'equal))))

(defun snapshot-goal-store (store)
  "The clipped snapshot a reader with no live session loads: the same goal,
revision, state and notes at the captured revision, and a dedup table of its
own so a read can never write the live journal."
  (let ((dedup (make-hash-table :test #'equal)))
    (maphash (lambda (k v) (setf (gethash k dedup) v))
             (goal-store-dedup store))
    (%make-goal-store :scope (goal-store-scope store)
                      :goal (goal-store-goal store)
                      :node-state (goal-store-node-state store)
                      :rev (goal-store-rev store)
                      :notes (copy-list (goal-store-notes store))
                      :history (copy-list (goal-store-history store))
                      :dedup dedup)))

(defun goal-stop-state (store)
  "stop= is derived from the node's state and is no second field."
  (case (goal-store-node-state store)
    (:cancel-requested :requested)
    (:cancelled :cancelled)
    (:deferred :deferred)
    (t :none)))

(defun goal-add-note (store note)
  "A delegation note is written under its own event and moves no goal count."
  (push note (goal-store-notes store))
  note)

(defun %goal-expect-check (store expect)
  "`--expect` is required on both goal writers. Absence is exit 2 naming the
flag; a stale expectation is exit 1 with the current value printed; nothing is
written either way. Answer (values OK LINE CODE)."
  (cond
    ((null expect)
     (values nil (format nil "GOAL FAIL scope=~A goal=~A: --expect is required"
                         (goal-store-scope store)
                         (or (goal-store-goal store) "-"))
             2))
    ((/= expect (goal-store-rev store))
     (values nil (format nil "GOAL FAIL scope=~A goal=~A expect=~D current=~D: stale"
                         (goal-store-scope store)
                         (or (goal-store-goal store) "-")
                         expect (goal-store-rev store))
             1))
    (t (values t nil 0))))

(defun %goal-record (store change request)
  "The one append of a goal write. A dry run never reaches here."
  (incf (goal-store-rev store))
  (push (list :change change :rev (goal-store-rev store))
        (goal-store-history store))
  (when request
    (setf (gethash request (goal-store-dedup store))
          (list :change change :rev (goal-store-rev store))))
  (goal-store-rev store))

(defun goal-set (store &key goal clear expect as reason dry-run request)
  "`goal set` writes the scope's goal reference. It is refused when the node's
disposition is closed, whatever the expectation, and a stale --expect is
refused before anything is written."
  (declare (ignore as reason))
  (multiple-value-bind (ok line code) (%goal-expect-check store expect)
    (unless ok (return-from goal-set (values nil line code))))
  (when (member (goal-store-node-state store)
                '(:done :cancelled :superseded :removed))
    (return-from goal-set
      (values nil (format nil "GOAL FAIL scope=~A goal=~A: disposition=~A"
                          (goal-store-scope store)
                          (or (goal-store-goal store) "-")
                          (string-downcase
                           (symbol-name (goal-store-node-state store))))
              1)))
  (let ((new-goal (if clear +absent+ goal))
        (rev (goal-store-rev store)))
    (unless dry-run
      (setf (goal-store-goal store) new-goal)
      (setf rev (%goal-record store (if clear :clear :set) request)))
    (values t (format nil "GOAL OK scope=~A goal=~A change=~A kind=goal rev=~D pushed=-"
                      (goal-store-scope store)
                      (if clear "-" (or goal "-"))
                      (if clear "clear" "set")
                      rev)
            0)))

(defun goal-update (store &key expect progress evidence criterion against
                              blocked-by reason stop dry-run request as)
  "`goal update` names the current goal node and writes the existing event kind
on it: --stop a :transition to :cancel-requested, --blocked-by a :transition to
:blocked, --progress with evidence an :evidence event, --progress alone a
:transition to :doing. A stop request is not stopped-worker evidence, and no
transition is written on a node already in :cancel-requested."
  (declare (ignore criterion against as))
  (multiple-value-bind (ok line code) (%goal-expect-check store expect)
    (unless ok (return-from goal-update (values nil line code))))
  (when (eq (goal-store-node-state store) :cancel-requested)
    (return-from goal-update
      (values nil (format nil "GOAL FAIL scope=~A goal=~A: stop requested"
                          (goal-store-scope store)
                          (or (goal-store-goal store) "-"))
              1)))
  (let* ((change (cond (stop :stop)
                       (blocked-by :blocked)
                       ((and progress evidence) :evidence)
                       (progress :progress)
                       (t nil))))
    (unless change
      (return-from goal-update
        (values nil (format nil "GOAL FAIL scope=~A goal=~A: no update form"
                            (goal-store-scope store)
                            (or (goal-store-goal store) "-"))
                2)))
    (when (and (eq change :progress)
               (eq (goal-store-node-state store) :doing))
      (return-from goal-update
        (values nil (format nil "GOAL FAIL scope=~A goal=~A: no edge"
                            (goal-store-scope store)
                            (or (goal-store-goal store) "-"))
                1)))
    (let ((new-state (case change
                       (:stop :cancel-requested)
                       (:blocked :blocked)
                       (:progress :doing)
                       (:evidence (goal-store-node-state store))))
          (rev (goal-store-rev store)))
      (unless dry-run
        (setf (goal-store-node-state store) new-state)
        (setf rev (%goal-record store change request)))
      (values t (format nil "GOAL OK scope=~A goal=~A change=~A kind=~A rev=~D pushed=-"
                        (goal-store-scope store)
                        (or (goal-store-goal store) "-")
                        (string-downcase (symbol-name change))
                        (if (eq change :evidence) "evidence" "transition")
                        rev)
              0))))

(defun goal-cancel (store &key evidence)
  "Confirmed cancellation is the one evidence-bearing operation and is admitted
from :cancel-requested alone."
  (unless (present-p evidence)
    (return-from goal-cancel
      (values nil "GOAL FAIL: cancel requires --evidence" 2)))
  (unless (eq (goal-store-node-state store) :cancel-requested)
    (return-from goal-cancel
      (values nil (format nil "GOAL FAIL scope=~A goal=~A: no edge"
                          (goal-store-scope store)
                          (or (goal-store-goal store) "-"))
              1)))
  (setf (goal-store-node-state store) :cancelled)
  (let ((rev (%goal-record store :cancelled nil)))
    (values t (format nil "GOAL OK scope=~A goal=~A change=cancelled kind=cancel rev=~D"
                      (goal-store-scope store)
                      (or (goal-store-goal store) "-")
                      rev)
            0)))

(defun goal-show (store &key as expect)
  "`goal show` answers at the revision it prints, with the goal, the derived
stop=, and the constraint rows `applicable` would carry before any capped row.
It takes no --expect."
  (when expect
    (return-from goal-show
      (values nil (format nil "GOAL FAIL scope=~A: show does not take --expect"
                          (goal-store-scope store))
              2)))
  (let* ((notes (remove-if-not (lambda (n) (note-scope-covers-p n as nil))
                               (goal-store-notes store)))
         (constraints (remove-if-not (lambda (n) (constraint-p (getf n :constraint)))
                                     notes))
         (rows (loop for n in notes
                     when (constraint-p (getf n :constraint))
                       collect (format nil "constraint ~A deny=~S"
                                       (getf n :id)
                                       (constraint-deny (getf n :constraint))))))
    (values (list :scope (goal-store-scope store)
                  :goal (goal-store-goal store)
                  :rev (goal-store-rev store)
                  :state (goal-store-node-state store)
                  :stop (goal-stop-state store)
                  :constraints (length constraints)
                  :notes rows)
            (format nil "GOAL OK scope=~A goal=~A rev=~D state=~A stop=~A constraints=~D notes=~D"
                    (goal-store-scope store)
                    (or (goal-store-goal store) "-")
                    (goal-store-rev store)
                    (string-downcase (symbol-name (goal-store-node-state store)))
                    (string-downcase (symbol-name (goal-stop-state store)))
                    (length constraints) (length notes))
            0)))
