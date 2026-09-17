;;;; assignment.lisp --- the assignment and execution-control book: offer,
;;;; acknowledge (received / accepted), decline, the offer deadline and the
;;;; receipt ledger (docs/SPEC-WORK.md:3835-3919).
;;;;
;;;; Slice 1's C/O kernel has the three transition verbs only. This file is the
;;;; pure booking layer the five assignment acceptance replays drive: an
;;;; admitted offer writes :effect :dispatched, a pending-offer entry under the
;;;; node and the friend, and a reservation keyed by (offer, attempt); an
;;;; acceptance after a verified received creates exactly one lease or binds to
;;;; the holder's own; --until leaves an offer overdue and unreconciled; a late
;;;; or duplicate receipt is retained. The live session, verifier, transport and
;;;; CLI wiring a later slice owns stays out; the tests exercise the pure book
;;;; directly, exactly as the applicable/delegation slice does.
;;;;
;;;; The book is a value: every function copies it and returns the new one, so
;;;; "the offer leaves node state, W, the lease index, attempts, evidence and
;;;; completion unchanged" is a comparison and not a convention.

(in-package #:nova-work)

(defstruct (leasebook
            (:constructor make-leasebook
                (&key (nodes '()) (w '()) (leases '()) (holders '()) (pending '())
                      (reservations '()) (evidence '()) (completion '())
                      (receipts '()) (requests '()) (generation '())
                      (observed-models '()) (effects '()) (bindings '())
                      (index-node '()) (index-friend '())))
            (:conc-name leasebook-))
  nodes            ; alist node -> its C/O state, read-only here
  w                ; alist node -> holder; one entry is one W membership
  leases           ; alist node -> (:holder :deadline :default)
  holders          ; alist node -> the current or proposed holder
  pending          ; alist offer -> the pinned offer tuple and its state
  reservations     ; alist (offer . attempt) -> (:reserve :state)
  evidence         ; the evidence index, read-only here
  completion       ; the completion index, read-only here
  receipts         ; alist receipt-id -> (:digest :effect :request)
  requests         ; alist request -> its recorded disposition
  generation       ; alist node -> current generation
  observed-models  ; alist node -> the observed model, or :unknown
  effects          ; alist (offer . attempt) -> newest effect
  bindings         ; alist (offer . attempt) -> the lease it bound to
  index-node       ; alist node -> offers, the pending-offer index under the node
  index-friend)    ; alist friend -> offers, the pending-offer index under the friend

(defun %lget (key alist)
  (cdr (assoc key alist :test #'equal)))

(defun %lput (key value alist)
  (acons key value (remove key alist :key #'car :test #'equal)))

(defun %pending-update (book offer update)
  (let ((nbk (copy-leasebook book))
        (entry (%lget offer (leasebook-pending book))))
    (setf (leasebook-pending nbk) (%lput offer (funcall update entry) (leasebook-pending book)))
    nbk))

(defmacro %refuse (word control &rest args)
  "A refusal writes nothing: the untouched BOOK is returned beside the nil."
  `(values nil (format nil "~A FAIL: ~A" ,word (format nil ,control ,@args)) nil book))

;;; ------------------------------------------------------------------
;;; offer (SPEC-WORK.md:3861-3888)
;;; ------------------------------------------------------------------

(defun leasebook-offer (book &key offer node generation attempt to profile reserve until
                                     free-slots request request-ref payload-sha256
                                     staged-digest predecessor-offer predecessor-attempt
                                     predecessor-node requested-model held profile-ok
                                     current-generation)
  "Admit one offer. An admitted offer writes :effect :dispatched, a pending-offer
entry under the node and the friend, and a reservation keyed by (offer, attempt).
It writes no node state, no W entry, no lease, no observed attempt, no evidence
and no completion. A refusal writes nothing."
  (declare (ignore request-ref requested-model))
  (cond
    ((or (null offer) (and (stringp offer) (string= offer ""))) (%refuse "OFFER" "malformed offer id"))
    ((or (null node) (and (stringp node) (string= node ""))) (%refuse "OFFER" "malformed node"))
    ((or (null to) (and (stringp to) (string= to ""))) (%refuse "OFFER" "malformed recipient"))
    ((or (null attempt) (and (stringp attempt) (string= attempt ""))) (%refuse "OFFER" "malformed attempt"))
    ((or (null profile) (and (stringp profile) (string= profile ""))) (%refuse "OFFER" "malformed profile"))
    ((not (and (integerp reserve) (plusp reserve))) (%refuse "OFFER" "reservation is not positive"))
    ((and free-slots (> reserve free-slots))
     (%refuse "OFFER" "reservation ~D exceeds declared free capacity ~D" reserve free-slots))
    (held (%refuse "OFFER" "effective hold on the scope"))
    ((eq profile-ok nil) (%refuse "OFFER" "profile is not that friend's at that revision"))
    ((and payload-sha256 staged-digest (not (equal payload-sha256 staged-digest)))
     (%refuse "OFFER" "staged payload digest is not --payload-sha256"))
    ((and current-generation (not (equal current-generation generation)))
     (%refuse "OFFER" "stale generation"))
    ((and predecessor-offer predecessor-node (not (equal predecessor-node node)))
     (%refuse "OFFER" "predecessor is not this node's"))
    ((%lget offer (leasebook-pending book)) (%refuse "OFFER" "reused offer id ~A" offer))
    ((find attempt (leasebook-pending book)
           :key (lambda (p) (getf (cdr p) :attempt)))
     (%refuse "OFFER" "reused attempt id ~A" attempt))
    ((find-if (lambda (p) (and (equal (getf (cdr p) :node) node)
                              (eq (getf (cdr p) :state) :overdue)))
              (leasebook-pending book))
     (%refuse "OFFER" "an overdue unreconciled offer admits no new offer"))
    (t
     (let* ((holder (%lget node (leasebook-holders book)))
            (entry (list :offer offer :node node :to to :attempt attempt
                         :generation generation :profile profile :reserve reserve
                         :until until :state :pending :received nil)))
       (when (and holder (not (equal holder to)))
         (return-from leasebook-offer
           (%refuse "OFFER" "cross-holder: ~A holds ~A; an offer cannot create a shadow lease"
                    holder node)))
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-holders nbk)
               (if holder (leasebook-holders book) (%lput node to (leasebook-holders book))))
         (setf (leasebook-pending nbk) (%lput offer entry (leasebook-pending book)))
         (setf (leasebook-reservations nbk)
               (%lput (cons offer attempt) (list :reserve reserve :state :held)
                      (leasebook-reservations book)))
         (setf (leasebook-effects nbk)
               (%lput (cons offer attempt) :dispatched (leasebook-effects book)))
         (setf (leasebook-index-node nbk)
               (%lput node (append (%lget node (leasebook-index-node book)) (list offer))
                      (leasebook-index-node book)))
         (setf (leasebook-index-friend nbk)
               (%lput to (append (%lget to (leasebook-index-friend book)) (list offer))
                      (leasebook-index-friend book)))
         (values t (format nil "OFFER OK offer=~A node=~A" offer node) :dispatched nbk))))))

;;; ------------------------------------------------------------------
;;; acknowledge --stage received (SPEC-WORK.md:3874-3875)
;;; ------------------------------------------------------------------

(defun leasebook-received (book &key offer node generation attempt receipt-digest
                                      receipt-id request verifier profile payload-sha256)
  "Admit delivery only: a verified report that the named recipient received that
exact offer. Consents to nothing: no lease, no W, no conversion, no release."
  (declare (ignore receipt-digest profile payload-sha256))
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "ACKNOWLEDGE" "no such offer ~A" offer))
      ((null verifier) (%refuse "ACKNOWLEDGE" "provenance unverified"))
      ((not (equal (getf entry :node) node)) (%refuse "ACKNOWLEDGE" "offer is for another node"))
      ((not (equal (getf entry :generation) generation))
       (%refuse "ACKNOWLEDGE" "offer is for another generation"))
      ((not (equal (getf entry :attempt) attempt)) (%refuse "ACKNOWLEDGE" "offer is for another attempt"))
      (t
       (let ((nbk (%pending-update book offer
                                   (lambda (e) (list* :received t e)))))
         (setf (leasebook-effects nbk) (%lput (cons offer attempt) :received (leasebook-effects book)))
         (when receipt-id
           (setf (leasebook-receipts nbk)
                 (%lput receipt-id (list :digest receipt-digest :effect :received :request request)
                        (leasebook-receipts book))))
         (values t (format nil "ACKNOWLEDGE OK stage=received offer=~A" offer) :received nbk))))))

;;; ------------------------------------------------------------------
;;; acknowledge --stage accepted (SPEC-WORK.md:3890-3903)
;;; ------------------------------------------------------------------

(defun leasebook-accepted (book &key offer node generation attempt by default deadline
                                        observed-model receipt-digest receipt-id request)
  "Accept ownership. With no live lease the one accepted envelope creates one
canonical :lease and one W entry and converts the reservation to committed; with
the holder's own live lease it binds the assignment and changes neither deadline
nor default. A cross-holder acceptance or a late one creates no lease."
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "ACKNOWLEDGE" "no such offer ~A" offer))
      ((not (getf entry :received)) (%refuse "ACKNOWLEDGE" "no earlier verified :received on the offer"))
      ((not (equal (getf entry :generation) generation))
       (%refuse "ACKNOWLEDGE" "generation changed"))
      ((member (getf entry :state) '(:declined :accepted :overdue :late :replaced))
       (%late-accept book offer attempt receipt-digest receipt-id request))
      (t
       (let* ((lease (%lget node (leasebook-leases book)))
              (holder (and lease (getf lease :holder))))
         (cond
           ((and lease (not (equal holder by)))
            (%late-accept book offer attempt receipt-digest receipt-id request))
           (t
            (let ((nbk (%pending-update book offer
                                        (lambda (e) (list* :state :accepted e)))))
              (setf (leasebook-reservations nbk)
                    (let ((key (cons offer attempt)))
                      (%lput key (list* :state :committed (%lget key (leasebook-reservations book)))
                             (leasebook-reservations book))))
              (if lease
                  ;; Bind to the holder's own lease; deadline and default are not fields of the binding.
                  (setf (leasebook-bindings nbk)
                        (%lput (cons offer attempt) (getf lease :holder) (leasebook-bindings book)))
                  (progn
                    (setf (leasebook-leases nbk)
                          (%lput node (list :holder by :deadline deadline :default default)
                                 (leasebook-leases book)))
                    (setf (leasebook-w nbk) (%lput node by (leasebook-w book)))))
              (setf (leasebook-observed-models nbk)
                    (%lput node (if observed-model observed-model :unknown)
                           (leasebook-observed-models book)))
              (setf (leasebook-effects nbk) (%lput (cons offer attempt) :accepted (leasebook-effects book)))
              (when receipt-id
                (setf (leasebook-receipts nbk)
                      (%lput receipt-id (list :digest receipt-digest :effect :accepted :request request)
                             (leasebook-receipts book))))
              (values t (format nil "ACKNOWLEDGE OK stage=accepted offer=~A lease=~A"
                                offer (if lease holder by))
                      :accepted nbk)))))))))

(defun %late-accept (book offer attempt receipt-digest receipt-id request)
  "A late acceptance is retained :late: it revives no lease, converts nothing,
releases nothing and overwrites no successor."
  (let ((nbk (%pending-update book offer (lambda (e) (list* :state :late e)))))
    (setf (leasebook-effects nbk) (%lput (cons offer attempt) :late (leasebook-effects book)))
    (when receipt-id
      (setf (leasebook-receipts nbk)
            (%lput receipt-id (list :digest receipt-digest :effect :late :request request)
                   (leasebook-receipts book))))
    (values t (format nil "ACKNOWLEDGE OK stage=accepted late offer=~A" offer) :late nbk)))

;;; ------------------------------------------------------------------
;;; decline (SPEC-WORK.md:3910-3912)
;;; ------------------------------------------------------------------

(defun leasebook-decline (book &key offer node generation attempt receipt-id receipt-digest request)
  (declare (ignore node generation))
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "DECLINE" "no such offer ~A" offer))
      ((eq :pending (getf entry :state))
       (let ((nbk (%pending-update book offer (lambda (e) (list* :state :declined e)))))
         (setf (leasebook-reservations nbk)
               (let ((key (cons offer attempt)))
                 (%lput key (list* :state :released (%lget key (leasebook-reservations book)))
                        (leasebook-reservations book))))
         (setf (leasebook-effects nbk) (%lput (cons offer attempt) :declined (leasebook-effects book)))
         (values t (format nil "DECLINE OK offer=~A" offer) :declined nbk)))
      (t
       ;; A decline after acceptance or over an uncertain execution is :late and
       ;; releases neither committed nor uncertain capacity.
       (let ((nbk (%pending-update book offer (lambda (e) (list* :state :late e)))))
         (setf (leasebook-effects nbk) (%lput (cons offer attempt) :late (leasebook-effects book)))
         (values t (format nil "DECLINE OK late offer=~A" offer) :late nbk))))))

;;; ------------------------------------------------------------------
;;; --until and lease expiry (SPEC-WORK.md:3905-3910)
;;; ------------------------------------------------------------------

(defun lease-until (book &key offer now)
  "At --until an unanswered offer is overdue and unreconciled: no new offer and no
automatic launch is admitted for it and the reservation stands."
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "LEASE" "no such offer ~A" offer))
      ((and (getf entry :until) now (string< (getf entry :until) now))
       (let ((nbk (%pending-update book offer (lambda (e) (list* :state :overdue e)))))
         (values t (format nil "LEASE OK offer=~A overdue unreconciled" offer) :overdue nbk)))
      (t (values t (format nil "LEASE OK offer=~A until not reached" offer) :pending book)))))

(defun leasebook-expire (book &key node now)
  "Lease expiry takes the task out of W and leaves the friend's ACTIVE capacity
and any uncertain execution retained; it claims no stop and no completion."
  (declare (ignore now))
  (let ((lease (%lget node (leasebook-leases book))))
    (cond
      ((null lease) (%refuse "LEASE" "no live lease on ~A" node))
      (t
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-w nbk) (remove node (leasebook-w book) :key #'car :test #'equal))
         (setf (leasebook-leases nbk)
               (%lput node (list* :expired t (leasebook-leases book)) (leasebook-leases book)))
         (values t (format nil "LEASE OK node=~A expired" node) :expired nbk))))))

;;; ------------------------------------------------------------------
;;; receipts: late, duplicate, conflicting (SPEC-WORK.md:3912-3919)
;;; ------------------------------------------------------------------

(defun leasebook-receipt (book &key receipt-id receipt-digest request stage
                                     offer node generation attempt by default deadline
                                     observed-model verifier)
  "The receipt ledger. The same request replays its recorded disposition; the
same verified receipt under a fresh request journals one no-effect :duplicate and
consumes no capacity twice; conflicting bytes for one receipt id are refused."
  (let ((seen (%lget receipt-id (leasebook-receipts book))))
    (cond
      ((and seen (not (equal (getf seen :digest) receipt-digest)))
       (%refuse "ACKNOWLEDGE" "conflicting bytes for receipt id ~A" receipt-id))
      ((and seen request (equal request (getf seen :request)))
       (values t (format nil "ACKNOWLEDGE OK replay receipt=~A" receipt-id)
               (getf seen :effect) book))
      (seen
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-requests nbk)
               (%lput request (list :effect :duplicate) (leasebook-requests book)))
         (when (null (%lget request (leasebook-requests book)))
           (setf (leasebook-receipts nbk)
                 (%lput receipt-id (list :digest receipt-digest :effect :duplicate :request request)
                        (leasebook-receipts book))))
         (values t (format nil "ACKNOWLEDGE OK duplicate receipt=~A" receipt-id) :duplicate nbk)))
      (t
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-receipts nbk)
               (%lput receipt-id (list :digest receipt-digest :effect :pending :request request)
                      (leasebook-receipts book)))
         (ecase stage
           (:received
            (multiple-value-bind (okp line effect rbk)
                (leasebook-received nbk :offer offer :node node :generation generation
                                    :attempt attempt :receipt-digest receipt-digest
                                    :receipt-id receipt-id :request request :verifier verifier)
              (values okp line effect rbk)))
           (:accepted
            (multiple-value-bind (okp line effect rbk)
                (leasebook-accepted nbk :offer offer :node node :generation generation
                                    :attempt attempt :by by :default default
                                    :deadline deadline :observed-model observed-model
                                    :receipt-digest receipt-digest :receipt-id receipt-id
                                    :request request)
              (values okp line effect rbk)))))))))


;;; ------------------------------------------------------------------
;;; folded from replays-applicable-delegation.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

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
             (:constructor make-applicable-answer
                 (&key rev from verdicts notes fail shown more)))
  rev from verdicts notes fail shown more)

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

(defun note-constraint-p (note)
  (constraint-p (getf note :constraint)))

(defun cap-notes (notes max)
  "The note rows the `--max` cap presents, and whether any row was cut. A
constraint-bearing note is ordered before a narrative one, so a :deny that
sorts last under --max 1 is still printed (SPEC-WORK.md:5498)."
  (if (null max)
      (values notes nil)
      (let* ((constraints (remove-if-not #'note-constraint-p notes))
             (narrative (remove-if #'note-constraint-p notes))
             (ordered (append constraints narrative)))
        (values (subseq ordered 0 (min max (length ordered)))
                (> (length ordered) max)))))

(defun applicable (as task-class candidates notes
                   &key (rev 0) (source :live) (notes-index t) (snapshot-bound-ok t)
                        (models t) (groups nil) (max nil))
  "Evaluate every active note whose scope covers AS against every candidate,
header first, and answer one verdict per candidate. A load failure answers NOTES
FAIL with no verdict and no row; a snapshot answer admits no route. MAX caps the
printed note rows, ordered so a constraint row is never cut for a narrative one."
  (let ((fail (applicable-load-error source notes-index snapshot-bound-ok)))
    (when fail
      (return-from applicable
        (make-applicable-answer :rev rev :from source :verdicts '() :notes '()
                                :shown '() :more nil :fail fail))))
  (let ((app-notes (remove-if-not (lambda (n) (note-scope-covers-p n as groups)) notes))
        (verdicts '()))
    (dolist (candidate candidates)
      (let ((v (candidate-verdict candidate task-class app-notes models)))
        (when (eq source :snapshot) (setf v (snapshot-downgrade v)))
        (push (cons candidate v) verdicts)))
    (multiple-value-bind (shown more) (cap-notes app-notes max)
      (make-applicable-answer :rev rev :from source
                              :verdicts (nreverse verdicts) :notes app-notes
                              :shown shown :more more :fail nil))))


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


;;; ------------------------------------------------------------------
;;; folded from replays-attempts-capabilities.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-attempts-capabilities.lisp --- the pure part of the five
;;;; execution-attribution replays of docs/SPEC-WORK.md:3385-3429 and the
;;;; Required replays table at :5655-5680.
;;;;
;;;; Each function is the smallest record the named paragraph promises:
;;;;
;;;;   four-capability-groups-and-three-fields  (:3390-3399, :5731)
;;;;   dispatch-ack-and-ownership-are-three     (:3401-3413, :5670)
;;;;   requested-model-is-not-observed-model    (:3407-3409, :5673)
;;;;   a-retry-does-not-overwrite-its-attempt   (:3409-3410, :5673)
;;;;   silence-is-a-ping-not-a-verdict          (:3415-3429, :5676)
;;;;
;;;; Nothing here starts a session, sends a ping or launches a worker: the
;;;; records are the facts the acceptance replays assert, and the *dispatch*,
;;;; *offer* and wake-protocol transports that would carry them live remain
;;;; outside this slice.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Capability groups and the three fields (SPEC-WORK.md:3390-3399)
;;; ------------------------------------------------------------------

(defparameter *capability-group-kinds*
  '(:child-agents :swarms :local-models :one-shots)
  "The four execution capability groups each friend may expose (:3390).")

(defstruct (capability-group
             (:constructor make-capability-group
                 (&key id kind source last-verified availability constraints
                       declared-support runtime-verified free-capacity)))
  "One catalog entry: the stable id, source, last-verified stamp, availability
and budget/permission constraints, plus declared support, successful runtime
verification and current free capacity kept as three separate fields."
  id kind source last-verified availability constraints
  declared-support runtime-verified free-capacity)

(defun capability-declared-support-p (g)
  "CONFIG says the friend may do this. It is not a runtime observation."
  (and (capability-group-declared-support g) t))

(defun capability-runtime-verified-p (g)
  "A successful runtime verification, never inferred from declared support."
  (and (capability-group-runtime-verified g) t))

(defun capability-free-capacity (g)
  "Current free capacity where observed, otherwise :unknown. A catalog entry is
not evidence of free credits (:3393)."
  (let ((c (capability-group-free-capacity g)))
    (if (null c) :unknown c)))

(defun capability-fields-collapse-p (g)
  "True when the three fields have been answered by one reading: support and
runtime agree, and free capacity is a bare boolean rather than a reading."
  (let ((support (capability-declared-support-p g))
        (runtime (capability-runtime-verified-p g))
        (free (capability-free-capacity g)))
    (and (eq support runtime) (member free '(t nil)))))

;;; ------------------------------------------------------------------
;;; Dispatch, delivery, acknowledgement and ownership (SPEC-WORK.md:3401-3413)
;;; ------------------------------------------------------------------

(defstruct (dispatch-fact
             (:constructor %make-dispatch-fact (kind &key request node to state
                                                             reserved declared)))
  "One dispatch fact. KIND keeps the four facts apart; RESERVED is the pending
offer's reservation and DECLARED the capacity the offer named."
  kind request node to state reserved declared)

(defun dispatch-offer (request node to declared-slots)
  "A dispatch records intent and a stable request identity, and its pending
offer reserves only the explicitly declared capacity (:3402)."
  (%make-dispatch-fact :offer :request request :node node :to to
                       :state :dispatched :reserved declared-slots
                       :declared declared-slots))

(defun delivery-receipt (offer)
  "Delivery is its own fact, distinct from the dispatch and the acceptance."
  (%make-dispatch-fact :delivery :request (dispatch-fact-request offer)
                       :node (dispatch-fact-node offer)
                       :to (dispatch-fact-to offer)
                       :state :received :reserved 0 :declared 0))

(defun acknowledgement (offer &key stage)
  "Acknowledgement is its own fact; STAGE is :received or :accepted."
  (check-type stage (member :received :accepted))
  (%make-dispatch-fact :acknowledge :request (dispatch-fact-request offer)
                       :node (dispatch-fact-node offer)
                       :to (dispatch-fact-to offer)
                       :state stage :reserved 0 :declared 0))

(defun accepted-ownership (offer holder)
  "Accepted ownership is its own fact, naming the holder it is bound to."
  (%make-dispatch-fact :ownership :request (dispatch-fact-request offer)
                       :node (dispatch-fact-node offer)
                       :to holder :state :accepted :reserved 0 :declared 0))

(defun facts-collapsed-p (&rest facts)
  "True when any two of the dispatch facts have been read as one."
  (let ((kinds (mapcar #'dispatch-fact-kind facts)))
    (or (some #'null kinds)
        (/= (length kinds) (length (remove-duplicates kinds))))))

(defun pending-offer-p (f)
  "A dispatched offer is pending until it is reconciled."
  (and (eq (dispatch-fact-kind f) :offer)
       (eq (dispatch-fact-state f) :dispatched)))

(defun pending-offer-reserved (f) (dispatch-fact-reserved f))
(defun pending-offer-declared (f) (dispatch-fact-declared f))

(defun offer-after-timeout (offer)
  "A timeout alone never blindly launches a duplicate while the old worker may
still be running (:3403): it marks the offer overdue and launches nothing, so
the reservation stands until reconciliation."
  (let ((o (copy-dispatch-fact offer)))
    (setf (dispatch-fact-state o) :overdue)
    o))

(defun dispatch-launched-p (f)
  "True only once a launched worker is recorded; a timeout records none."
  (eq (dispatch-fact-state f) :launched))

(defun second-offer-admitted-p (pending other)
  "One holder has one lease: while PENDING is live, a second offer to a
different name is refused and creates no shadow lease."
  (not (and (pending-offer-p pending)
            (not (equal (dispatch-fact-to pending) (dispatch-fact-to other))))))

;;; ------------------------------------------------------------------
;;; Requested vs observed model, and the retry (SPEC-WORK.md:3407-3410)
;;; ------------------------------------------------------------------

(defstruct (attempt
             (:constructor make-attempt (&key id node requested-model observed usage)))
  "One attempt: its stable id, the model that was requested and the model that
was observed (NIL when none was), and its own usage reading."
  id node requested-model observed usage)

(defun attempt-observed-model (a)
  "The observed model, or :unknown when no observation was recorded. A friend's
usual model is never proof of the model that executed a delegated task (:3408)."
  (let ((m (attempt-observed a)))
    (if (null m) :unknown m)))

(defun append-attempt (attempts new)
  "A retry appends a second attempt record; it never overwrites the attempt
before it (:3410)."
  (append attempts (list new)))

(defun attempts-collapsed-p (attempts)
  "True when two attempt records share one identity, the collapse a retry must
never cause."
  (/= (length attempts)
      (length (remove-duplicates attempts :key #'attempt-id :test #'equal))))

;;; ------------------------------------------------------------------
;;; Availability, silence and the one bounded ping (SPEC-WORK.md:3415-3429)
;;; ------------------------------------------------------------------

(defun silence-action (elapsed threshold &key resting reserved pinged-p)
  "The action the configured silence threshold asks for: :ping once, then
:already-pinged; :rest and :reserved suppress the ping; :quiet below the
threshold (:3419-3424)."
  (cond
    ((and resting (>= elapsed threshold)) :rest)
    ((and reserved (>= elapsed threshold)) :reserved)
    ((< elapsed threshold) :quiet)
    (pinged-p :already-pinged)
    (t :ping)))

(defun availability-after-window (&key answered-p resting)
  "The availability state after the configured answer window: an answer is
available; an unresolved nonresponse is :unconfirmed; explicit rest keeps its
own reason."
  (cond
    (answered-p (list :available t :reason nil))
    (resting (list :available nil :reason :explicit-rest))
    (t (list :available nil :reason :unconfirmed))))

(defun silence-verdict (availability)
  "The scheduling verdict for an availability reading. An unconfirmed capacity
asserts neither sleep nor exhausted credit without evidence (:3424)."
  (list :state (if (getf availability :available) :available :unavailable)
        :reason (getf availability :reason)
        :sleep nil
        :exhausted nil))

(defun probe-outcome (reply)
  "A failed probe is unresolved delivery, not a failed friend (:3425)."
  (if (eq reply :failed) :unresolved-delivery reply))
