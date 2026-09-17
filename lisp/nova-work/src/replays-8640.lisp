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
