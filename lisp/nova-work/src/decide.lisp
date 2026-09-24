;;;; decide.lisp --- slice 2 of SPEC-DECIDE: the kernel's `decide` protocol.
;;;;
;;;; docs/SPEC-DECIDE.md:177-187, "The Lisp kernel --- one protocol, one
;;;; in-process fake". The kernel asks through ONE `decide` protocol function,
;;;; with ONE in-process fake and no network call anywhere. A decision advises;
;;;; the machinery decides (rule 5, rule 6):
;;;;
;;;;   * BELOW the floor the kernel keeps its own order, its own abstain or its
;;;;     own tie-break; the provider's answer is recorded as a suggestion and
;;;;     never becomes the decisive answer.
;;;;   * EVERY call is journaled as an event of kind :decide carrying the
;;;;     question's SHA-256; a replay reads that event back by the question hash
;;;;     and reproduces the answer WITHOUT consulting the provider.
;;;;   * Nothing here writes the kernel's authority: no ready order, lease,
;;;;     hold, ownership, count or revision moves (rule 6/rule 7). `decide`
;;;;     touches the journal and nothing else.
;;;;
;;;; The provider seam is `decide-consult`. There is exactly one implementation
;;;; in this slice -- the in-process `fake-decider`, which derives an answer
;;;; from the bounded question alone (rule 9: never the network, never a key on
;;;; disk). No HTTP client, URL or key name appears in this file.

(in-package #:nova-work)

(defstruct (decision
            (:constructor make-decision
                (&key question-hash question-kind answer proposed confidence
                      floor suggestion-p source evidence)))
  question-hash  ; the question's SHA-256, the journal key a replay uses
  question-kind  ; :choice, :score or :noul
  answer         ; the DECISIVE answer: the provider's above the floor, else the kernel's
  proposed       ; what the provider proposed, suggestion or not
  confidence     ; the provider's number in milli-units (0..1000), or NIL
  floor          ; the floor in milli-units (0..1000)
  suggestion-p   ; T when the provider's answer is only a suggestion (rule 5)
  source         ; :provider for a fresh call, :journal for a replay, :kernel below the floor
  evidence)      ; the evidence pointer of rule 10

;;; ------------------------------------------------------------------
;;; The bounded question and its hash.
;;; ------------------------------------------------------------------

(defparameter *decide-kinds* '(:choice :score :noul)
  "The three typed-question shapes of SPEC-DECIDE rule 1.")

(defun normalize-question (question)
  "The bounded, restricted spelling of QUESTION the hash is taken over. Only
the named state and the questions are consulted (rule 1); an unknown key is
dropped by construction because only these keys are read."
  (let ((kind (getf question :kind)))
    (unless (member kind *decide-kinds*)
      (error 'unsupported-input
             :what (format nil "decide: ~A is not a question kind" kind)))
    (list :kind kind
          :question (or (getf question :question) "")
          :options (or (getf question :options) '())
          :legend (or (getf question :legend) '())
          :statement (or (getf question :statement) "")
          :subject (or (getf question :subject) +absent+)
          :evidence (or (getf question :evidence) +absent+))))

(defun question-hash (question)
  "The question's SHA-256, lowercase hex, over its canonical spelling. It is
the journal key a replay looks the answer up by (SPEC-DECIDE.md:177-187)."
  (sha256-hex (canonical-string (normalize-question question))))

(defun decision-request (question-hash)
  "The journal request id one decision on QUESTION-HASH is recorded under."
  (concatenate 'string "decide-" question-hash))

;;; ------------------------------------------------------------------
;;; The durable decision event.
;;; ------------------------------------------------------------------

(defun %milli (value)
  "A 0-1 confidence or floor as the restricted integer spelling (0..1000).
Returns NIL for a non-number, so a missing provider confidence is not zero."
  (and (numberp value) (round (* value 1000))))

(defun decision-event (question answer proposed confidence floor suggestion-p)
  "The one event kind this protocol journals. It carries the question hash,
the decisive answer, the provider's proposed answer and confidence, the floor
and whether the answer is only a suggestion."
  (make-work-event
   :kind :decide
   :node (getf question :subject +absent+)
   :by "kernel"
   :fields (list :question-hash (question-hash question)
                 :question-kind (getf question :kind)
                 :answer answer
                 :proposed (or proposed +absent+)
                 :confidence (or confidence +absent+)
                 :floor (or floor +absent+)
                 :suggestion (if suggestion-p 1 0)
                 :evidence (getf question :evidence +absent+))
   :stamp "2026-09-17T00:00:00Z"
   :clock :tool
   :request (decision-request (question-hash question))
   :generation-owner +absent+
   :rev 0
   :session-written-p nil))

(defun event->decision (event &key (source :journal))
  "Rebuild the decision a recorded :decide event carries."
  (let ((f (work-event-fields event)))
    (make-decision
     :question-hash (getf f :question-hash)
     :question-kind (getf f :question-kind)
     :answer (getf f :answer)
     :proposed (getf f :proposed)
     :confidence (getf f :confidence)
     :floor (getf f :floor)
     :suggestion-p (eql 1 (getf f :suggestion))
     :evidence (getf f :evidence)
     :source source)))

(defun journal-decision (journal event)
  "Append EVENT to JOURNAL as a decision record. The journal is written and
nothing else: the kernel's state, order, leases, holds and ownership are not
touched (rule 6)."
  (let* ((request (work-event-request event))
         (digest (payload-digest (list event)))
         (line (canonical-string (event-record-form event)))
         (envelope (list :request request :digest digest :events (list event))))
    (multiple-value-bind (accepted refusal) (journal-accept journal envelope)
      (unless accepted
        (error 'unsupported-input
               :what (format nil "decide: journal refused acceptance: ~A" refusal))))
    (journal-record journal request digest line (work-event-rev event))))

(defun journaled-decision-event (journal question)
  "The recorded :decide event for QUESTION, or NIL when none is journaled. A
page the journal cannot read answers :unavailable rather than a fresh call."
  (multiple-value-bind (found digest line)
      (journal-lookup journal (decision-request (question-hash question)))
    (declare (ignore digest))
    (cond ((eq found :unavailable)
           (error 'unsupported-input
                  :what "decide: journal page unavailable; refusing to buy a fresh answer"))
          (found (record-form->event (read-restricted line)))
          (t nil))))

(defun recorded-decision (journal question)
  "The journaled decision for QUESTION, or NIL. This is the replay path: it
reads the event's question hash back and never consults a provider."
  (let ((event (journaled-decision-event journal question)))
    (and event (event->decision event :source :journal))))

;;; ------------------------------------------------------------------
;;; The provider protocol and its one in-process fake.
;;; ------------------------------------------------------------------

(defclass fake-decider ()
  ((answer :initarg :answer :initform nil :accessor fake-decider-answer)
   (confidence :initarg :confidence :initform 0.9 :accessor fake-decider-confidence)
   (calls :initform 0 :accessor fake-decider-calls))
  (:documentation "The one in-process provider of this slice. It answers from
the bounded question alone and dials nothing (rule 9)."))

(defun make-fake-decider (&key answer (confidence 0.9))
  "A fake that answers ANSWER at CONFIDENCE, counting every consultation so a
test can prove a replay never makes a fresh call."
  (make-instance 'fake-decider :answer answer :confidence confidence))

(defgeneric decide-consult (provider question)
  (:documentation "Ask PROVIDER one bounded QUESTION. Answers (values answer
confidence). This is the ONLY provider seam; this slice has one implementation
and it is in-process (rule 9)."))

(defmethod decide-consult ((provider fake-decider) question)
  (incf (fake-decider-calls provider))
  (let ((answer (or (fake-decider-answer provider)
                    (ecase (getf question :kind)
                      (:choice (first (getf question :options)))
                      (:score 0)
                      (:noul 0)))))
    (values answer (fake-decider-confidence provider))))

(defun kernel-default-answer (question)
  "The kernel's own order, abstain or tie-break, used BELOW the floor. It is a
pure function of the question and consults no provider."
  (ecase (getf question :kind)
    (:choice :abstain)
    (:score 0)
    (:noul 0)))

;;; ------------------------------------------------------------------
;;; The one protocol function.
;;; ------------------------------------------------------------------

(defgeneric decide (kernel question floor &key provider fallback)
  (:documentation "Ask one bounded QUESTION with a FLOOR and journal the answer.

Above the floor the provider's answer is decisive; below it the kernel keeps
its own fallback and the answer is journaled as a suggestion (rule 5). Every
call is journaled as a :decide event carrying the question hash; a replay with
no provider reads the journal and reproduces the answer without a fresh call.
This writes the journal only -- the kernel's decisive order, leases, holds and
ownership are untouched (rule 6/rule 7)."))

(defmethod decide ((kernel kernel) question floor &key provider fallback)
  (let ((journal (kernel-journal kernel)))
    (or (recorded-decision journal question)
        (multiple-value-bind (proposed confidence)
            (if provider (decide-consult provider question) (values nil nil))
          (let* ((milli-floor (%milli floor))
                 (milli-confidence (%milli confidence))
                 (above-floor-p (and milli-confidence milli-floor
                                     (>= milli-confidence milli-floor)))
                 (answer (if above-floor-p proposed (or fallback
                                                        (kernel-default-answer question))))
                 (suggestion-p (not above-floor-p))
                 (event (decision-event question answer proposed milli-confidence
                                        milli-floor suggestion-p)))
            (journal-decision journal event)
            (event->decision event :source (if provider :provider :kernel)))))))

;;; ------------------------------------------------------------------
;;; Decision packets (SPEC-WORK.md:4546-4556, replay row 4648).
;;; ------------------------------------------------------------------
;;;
;;; What comes back up to the coordinator is a decision packet, built by
;;; machinery, ONE per item and revision. Machinery observes and books; a
;;; model wakes only when there is something to decide:
;;;
;;;   * the first observation of an (item, revision) builds its packet and
;;;     wakes the reader once;
;;;   * a further observation of the same (item, revision) AMENDS that packet
;;;     (new facts appended, findings merged by id) and never builds a second
;;;     one; while the reader is busy the amendment wakes nothing;
;;;   * a newer revision supersedes the packet: a new packet is built for the
;;;     new revision carrying every finding still :open, and the old packet
;;;     names its successor;
;;;   * an observation with no facts and no findings -- an empty pulse -- builds
;;;     nothing, amends nothing and wakes no model;
;;;   * an observation of an older revision than the item's current packet is
;;;     stale and is booked nowhere.

(defstruct (decision-packet
            (:constructor %make-decision-packet
                (&key item revision facts findings amendments superseded-by)))
  item           ; the item the packet decides
  revision       ; the exact revision it was built for
  facts          ; the new behaviour and evidence pointers, oldest first
  findings       ; alist (finding-id . disposition); :open is unresolved
  amendments     ; how many observations amended it after it was built
  superseded-by) ; the revision whose packet superseded it, or NIL

(defstruct (packet-book (:constructor make-packet-book ()))
  (current (make-hash-table :test 'equal)) ; item -> its current packet
  (built '())                              ; every packet ever built, newest first
  (wakes 0))                               ; model wakes this book has caused

(defun %revision< (a b)
  "Revisions are integers or strings; strings compare lexically."
  (if (and (integerp a) (integerp b)) (< a b) (string< (princ-to-string a)
                                                       (princ-to-string b))))

(defun %merge-findings (old new)
  "OLD findings with NEW merged by id: a new disposition for a known id
replaces the old one, an unknown id is appended. Order is first-seen."
  (let ((merged (copy-alist old)))
    (dolist (finding new merged)
      (let ((cell (assoc (car finding) merged :test #'equal)))
        (if cell
            (setf (cdr cell) (cdr finding))
            (setf merged (append merged (list (cons (car finding) (cdr finding))))))))))

(defun open-findings (packet)
  "The findings of PACKET whose disposition is still :open."
  (remove-if-not (lambda (finding) (eq (cdr finding) :open))
                 (decision-packet-findings packet)))

(defun %build-packet (book item revision facts findings)
  (let ((packet (%make-decision-packet :item item :revision revision
                                       :facts (copy-list facts)
                                       :findings (%merge-findings '() findings)
                                       :amendments 0)))
    (setf (gethash item (packet-book-current book)) packet)
    (push packet (packet-book-built book))
    packet))

(defun book-observation (book observation &key reader-busy-p)
  "Book one OBSERVATION, a plist (:item :revision :facts :findings), into
BOOK. Answers (values packet action) where action is :built, :amended,
:superseded, :empty or :stale. Only :built and :superseded wake the reader,
and only when it is not busy; an amendment or an empty pulse wakes nothing."
  (let* ((item (getf observation :item))
         (revision (getf observation :revision))
         (facts (getf observation :facts))
         (findings (getf observation :findings))
         (current (gethash item (packet-book-current book))))
    (flet ((wake () (unless reader-busy-p (incf (packet-book-wakes book)))))
      (cond
        ((and (null facts) (null findings))
         (values current :empty))
        ((null current)
         (wake)
         (values (%build-packet book item revision facts findings) :built))
        ((equal revision (decision-packet-revision current))
         (setf (decision-packet-facts current)
               (append (decision-packet-facts current) facts)
               (decision-packet-findings current)
               (%merge-findings (decision-packet-findings current) findings))
         (incf (decision-packet-amendments current))
         (values current :amended))
        ((%revision< revision (decision-packet-revision current))
         (values current :stale))
        (t
         (let ((next (%build-packet book item revision facts
                                    (%merge-findings (open-findings current)
                                                     findings))))
           (setf (decision-packet-superseded-by current) revision)
           (wake)
           (values next :superseded)))))))

(defun packets-for (book item &optional (revision nil revision-p))
  "Every packet BOOK ever built for ITEM (at REVISION when given), oldest first."
  (reverse (remove-if-not (lambda (p)
                            (and (equal item (decision-packet-item p))
                                 (or (not revision-p)
                                     (equal revision (decision-packet-revision p)))))
                          (packet-book-built book))))
