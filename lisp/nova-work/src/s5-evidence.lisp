;;;; s5-evidence.lisp --- evidence, attempts, generation and a done that costs.
;;;;
;;;; S5, implemented in its own files and wired into the command thread by a
;;;; later card. Everything here is a pure function over records; nothing touches
;;;; the kernel, the session or the write path (src/kernel.lisp, src/session.lisp
;;;; and src/state.lisp are unchanged).
;;;;
;;;; The shapes follow docs/SPEC-WORK.md:
;;;;   :evidence      (:pointer (:criterion) (:against) (:generation) (:attempt))  -- SPEC-WORK.md:996
;;;;   :attempt       (:model :bench :started :ended :result :usage :generation)   -- SPEC-WORK.md:999
;;;;   :correct       bumps the task's :generation                                  -- SPEC-WORK.md:1001
;;;;   :review-attest (:criterion :result :against :generation :by)                 -- SPEC-WORK.md:1003
;;;;
;;;; A `:to :done` must name evidence events whose criteria cover the task's
;;;; `:acceptance`, all written at the task's current generation (rule 5,
;;;; SPEC-WORK.md:4897); a required task with no acceptance can never be done
;;;; (rule 15, SPEC-WORK.md:4938).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Acceptance criteria and the evidence/attempt/attest records.
;;; ------------------------------------------------------------------

(defstruct (acceptance-criterion
            (:conc-name criterion-)
            (:constructor make-criterion (id kind subject predicate)))
  id kind subject predicate)

(defstruct (evidence-event
            (:conc-name evidence-)
            (:constructor make-evidence (pointer criterion against generation
                                         &optional (attempt +absent+))))
  pointer criterion against generation attempt)

(defstruct (attempt-event
            (:conc-name s5-attempt-)
            (:constructor make-s5-attempt (model bench started ended result usage generation)))
  model bench started ended result usage generation)

(defstruct (attest-event
            (:conc-name attest-)
            (:constructor make-attest (criterion result against generation by)))
  criterion result against generation by)

(defstruct (rule-finding
            (:conc-name finding-)
            (:constructor make-s5-finding (rule reason)))
  rule reason)

;;; ------------------------------------------------------------------
;;; The five fields an evidence event is read by, everywhere the snapshot and
;;; the closed index reproduce it (SPEC-WORK.md:548-551, replay
;;; `settle-keeps-id-and-evidence`).
;;; ------------------------------------------------------------------

(defun evidence-five-fields (ev)
  (list :pointer (evidence-pointer ev)
        :criterion (evidence-criterion ev)
        :against (evidence-against ev)
        :generation (evidence-generation ev)
        :attempt (evidence-attempt ev)))

;;; ------------------------------------------------------------------
;;; Pointers: six schemes, one of which qualifies nothing.
;;; ------------------------------------------------------------------

(defun s5-pointer-scheme (pointer)
  "The leading scheme of a pointer, or NIL. commit:, pr:, run:, file:, test:
  are the five that name a fact; note: qualifies nothing (SPEC-WORK.md:1239-1242)."
  (and (stringp pointer)
       (let ((colon (position #\: pointer)))
         (and colon
              (cdr (assoc (subseq pointer 0 colon)
                          '(("commit" . :commit) ("pr" . :pr) ("run" . :run)
                            ("file" . :file) ("test" . :test) ("note" . :note))
                          :test #'string=))))))

(defun note-pointer-p (pointer)
  "note:<scheme>:<id> is the one spell that names no forge fact and qualifies
  nothing, so rule 5 refuses it as completion evidence (SPEC-WORK.md:1268-1270)."
  (and (stringp pointer)
       (let ((colon (position #\: pointer)))
         (and colon (string= "note" (subseq pointer 0 colon))))))

;;; ------------------------------------------------------------------
;;; Generation.
;;; ------------------------------------------------------------------

(defun correct-generation (generation)
  "`correct` bumps the task's generation, voiding every earlier evidence
  qualification (SPEC-WORK.md:1001, 5653982211)."
  (1+ generation))

(defun older-generation-evidence-p (evidence-generation node-generation)
  "An evidence event written at an older generation cannot close the task
  (SPEC-WORK.md:1229-1232, and Stella's finding 2 at 5654659093)."
  (< evidence-generation node-generation))

;;; ------------------------------------------------------------------
;;; Coverage: an evidence event qualifies a criterion when its :criterion names
;;; that criterion's id. Kind, subject and generation are validated separately,
;;; so an honest resolver's fact is never a different criterion's proof
;;; (SPEC-WORK.md:937-943).
;;; ------------------------------------------------------------------

(defun evidence-names-criterion-p (ev criterion)
  (and (string= (evidence-criterion ev) (criterion-id criterion)))) 

(defun criteria-cover-p (cited-evidence acceptance)
  "Every acceptance entry is named by some cited evidence event."
  (every (lambda (c)
           (some (lambda (ev) (evidence-names-criterion-p ev c))
                 cited-evidence))
         acceptance))

;;; ------------------------------------------------------------------
;;; Problem resolver for rule 2: unavailable is not green and not dangling.
;;; ------------------------------------------------------------------

(defun resolve-reference (resolver id branch stamp)
  "Answer :unavailable when the resolver could not read the page holding ID in
  BRANCH, :dangling when it read and found nothing, :found otherwise. A reading
  that reports neither silently would be claiming the reference green with its
  history missing (SPEC-WORK.md:4872-4877)."
  (let ((answer (funcall resolver id branch stamp)))
    (cond ((eq answer :unavailable) :unavailable)
          ((null answer) :dangling)
          (t :found))))

;;; ------------------------------------------------------------------
;;; Rule 5: done without evidence.
;;; ------------------------------------------------------------------

(defun done-finding (cited-evidence acceptance node-generation)
  "NIL when the :to :done is admissible, else a finding naming rule 5. A done
  must name evidence: naming none, naming a note: pointer, naming criteria that
  do not cover the acceptance, or naming evidence of an older generation, is
  each a rule 5 refusal (SPEC-WORK.md:4897-4899)."
  (cond
    ((or (null cited-evidence) (every #'absentp cited-evidence))
     (make-s5-finding 5 "done without evidence"))
    ((some #'note-pointer-p (mapcar #'evidence-pointer cited-evidence))
     (make-s5-finding 5 "a note: pointer is not completion evidence"))
    ((some (lambda (ev) (older-generation-evidence-p (evidence-generation ev) node-generation))
           cited-evidence)
     (make-s5-finding 5 "evidence of an older generation"))
    ((not (criteria-cover-p cited-evidence acceptance))
     (make-s5-finding 5 "evidence does not cover the node's acceptance"))
    (t nil)))

;;; ------------------------------------------------------------------
;;; Rule 15: a required task with no acceptance can never be done.
;;; ------------------------------------------------------------------

(defun rule-15-finding (required-p acceptance)
  (when (and required-p (null acceptance))
    (make-s5-finding 15 "a required task with no acceptance entry can never be done")))

;;; ------------------------------------------------------------------
;;; accept --add on a node derived :done (SPEC-WORK.md:2359-2363).
;;; ------------------------------------------------------------------

(defun accept-add-finding (node-derived-done-p acceptance added-criterion existing-evidence)
  "An accept --add on a node standing :done is refused by rule 5 when the added
  criterion is not already covered, because the standing :to :done would no
  longer cover the node's acceptance. The way through is `correct`, then accept
  --add, then fresh evidence."
  (when node-derived-done-p
    (unless (some (lambda (ev) (evidence-names-criterion-p ev added-criterion))
                  existing-evidence)
      (make-s5-finding 5 "accept --add would leave the standing :to :done uncovered"))))

;;; ------------------------------------------------------------------
;;; A settle keeps the task's identity and its evidence (SPEC-WORK.md:5378).
;;; ------------------------------------------------------------------

(defun settle-to-done (node)
  "Move NODE (a plist carrying :id :children :acceptance :evidence :generation
  :state) to :done, preserving every field but :state and the branch."
  (loop for (k v) on node by #'cddr
        unless (member k '(:state :branch))
          append (list k v)
        into kept
        finally (return (list* :state :done :branch :closed kept))))

;;; ------------------------------------------------------------------
;;; Counting: done, done-unverified and unknown are kept apart (SPEC-WORK.md:2045).
;;; ------------------------------------------------------------------

(defun count-dispositions (nodes)
  "Each node is a plist (:disposition <kw> :verified <T/NIL>). A recorded done
  standing on unverified or stale evidence counts unknown, never done, and is
  reported as its own done-unverified count beside done."
  (let ((done 0) (done-unverified 0) (unknown 0))
    (dolist (n nodes)
      (case (getf n :disposition)
        (:done (if (getf n :verified)
                   (incf done)
                   (progn (incf done-unverified) (incf unknown))))
        (:unknown (incf unknown))))
    (list :done done :done-unverified done-unverified :unknown unknown)))
