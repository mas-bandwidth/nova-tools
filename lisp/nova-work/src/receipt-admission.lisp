;;;; receipt-admission.lisp --- the verifier result a receipt needs, admitted by
;;;; the one writer (docs/SPEC-WORK.md:3857-3868, the grammar at :2295-2296 and
;;;; the output lines at :6018-6019).
;;;;
;;;; "A receipt is a verified observation and never a request's word"
;;;; (SPEC-WORK.md:3857). The coordinator admits `acknowledge` and `decline`
;;;; only after an **operator-configured verifier** has returned the configured
;;;; recipient identity, a stable receipt id and the digest of the received
;;;; bytes, and the three fields it derives -- `:sender`, `:receipt-digest`,
;;;; `:effect` -- are the session's own half of the envelope, refused when a
;;;; plain request carries them (SPEC-WORK.md:3859-3862).
;;;;
;;;; What is real here, and where it differs from `src/verifier.lisp`'s pure
;;;; model:
;;;;
;;;;   * the verifier is CONFIG the session holds, not a struct a replay hands
;;;;     in: `configure-verifier` registers it on the kernel beside the fleet
;;;;     and the model routes;
;;;;   * the provenance body is read from a real file named by the
;;;;     `--provenance <pointer>` the request carries, and the operator's
;;;;     verifier is run as a real process, both OUTSIDE the mutation loop
;;;;     (`stage-receipt`, SPEC-WORK.md:3863-3865);
;;;;   * the admission runs on the kernel's one command thread, is accepted and
;;;;     durably recorded by the journal before anything is installed, and
;;;;     writes one replayable `:receipt` event plus the session's own
;;;;     `:receipt-seal`, so the ledger is a projection over WSTATE's own
;;;;     history and survives close, reopen and replay.
;;;;
;;;; THE ONE WIRE THIS SLICE FIXES. The specification names what the verifier
;;;; must return and not how it says it, so this file fixes the smallest wire
;;;; that carries it: the provenance bytes go to the configured command's
;;;; stdin, and its first line of stdout is three whitespace-separated tokens,
;;;; `<recipient> <receipt-id> <digest>`. A non-zero exit, an unparsable line, a
;;;; recipient other than the configured one, or a digest that is not the digest
;;;; of the bytes staged is a verifier outage: `provenance unverified`, canonical
;;;; state unchanged, nothing written (SPEC-WORK.md:3868).
;;;;
;;;; NOT HERE. The lease, the reservation, the capacity and W are the assignment
;;;; book's (src/assignment.lisp); an admitted receipt writes none of them, so
;;;; `lease=`, `reserved=` and `committed=` stand at `-` and 0 on the OK line
;;;; until that half is wired. As in `src/kernel.lisp`, the line carries no
;;;; trailing `emitted=`: that is the CLI's count of what it printed, and there
;;;; is no CLI in this slice.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The operator-configured verifier, as CONFIG the session holds
;;; ------------------------------------------------------------------

(defstruct (verifier-registry (:constructor make-verifier-registry ()))
  "The operator's configured verifiers, one per recipient identity. It is
CONFIG beside the fleet and the model routes: no work, no child of O, and no
count, roadmap or required set moves when one is written."
  (by-recipient (make-hash-table :test #'equal))
  (order '()))

(defun verifier-registry-member (registry recipient)
  (and recipient (gethash recipient (verifier-registry-by-recipient registry))))

(defun verifier-registry-count (registry)
  (hash-table-count (verifier-registry-by-recipient registry)))

(defun parse-verifier-line (text)
  "The verifier's answer: the first line of its stdout, three whitespace-
separated tokens `<recipient> <receipt-id> <digest>`. Anything else is no
answer at all, and no answer is a verifier outage (SPEC-WORK.md:3868)."
  (when (stringp text)
    (let* ((end (or (position #\Newline text) (length text)))
           (line (string-trim '(#\Space #\Tab #\Return) (subseq text 0 end)))
           (tokens '())
           (start 0))
      (loop
        (let ((space (position-if (lambda (c) (member c '(#\Space #\Tab)))
                                  line :start start)))
          (when (> (or space (length line)) start)
            (push (subseq line start (or space (length line))) tokens))
          (unless space (return))
          (setf start (1+ space))))
      (let ((tokens (nreverse tokens)))
        (when (= 3 (length tokens))
          (list :recipient (first tokens)
                :receipt-id (second tokens)
                :digest (third tokens)))))))

(defun run-verifier-command (command bytes)
  "Run the operator's configured verifier COMMAND as a real process with BYTES
on its stdin, and read its answer. It runs outside the mutation loop and never
on the command thread; a non-zero exit is no answer (SPEC-WORK.md:3863-3865)."
  #+sbcl
  (let* ((out (make-string-output-stream))
         (process (sb-ext:run-program "/bin/sh" (list "-c" command)
                                      :input (make-string-input-stream bytes)
                                      :output out
                                      :error nil
                                      :search nil
                                      :wait t)))
    (when (and process (eql 0 (sb-ext:process-exit-code process)))
      (parse-verifier-line (get-output-stream-string out))))
  #-sbcl
  (error 'unsupported-input
         :what "no process runner outside SBCL; the engine's platform is pinned"))

(defun configure-verifier (kernel &key recipient command function)
  "Register the operator's verifier for RECIPIENT. COMMAND is the command
string the operator configured, run as a process over the provenance bytes;
FUNCTION is the in-process seam the same wire is tested through. Answers the
`receipt-verifier' the stage will run (SPEC-WORK.md:3859)."
  (unless (and (stringp recipient) (plusp (length recipient)))
    (error 'unsupported-input :what "configure-verifier: malformed recipient"))
  (unless (or command function)
    (error 'unsupported-input
           :what "configure-verifier: no command and no configured reader"))
  (let* ((registry (kernel-verifiers kernel))
         (verifier (make-receipt-verifier
                    :recipient recipient
                    :verify (or function
                                (lambda (bytes) (run-verifier-command command bytes))))))
    (unless (gethash recipient (verifier-registry-by-recipient registry))
      (push recipient (verifier-registry-order registry)))
    (setf (gethash recipient (verifier-registry-by-recipient registry)) verifier)
    verifier))

(defun kernel-verifier (kernel recipient)
  "The operator-configured verifier for RECIPIENT, or NIL. NIL is not a
permissive default: a receipt with no configured verifier is unverified."
  (verifier-registry-member (kernel-verifiers kernel) recipient))

;;; ------------------------------------------------------------------
;;; The stage: the readers run outside the mutation loop
;;; (SPEC-WORK.md:3863-3865)
;;; ------------------------------------------------------------------

(defun provenance-pointer-path (pointer)
  "The path a provenance pointer names. `file:<path>` is the one scheme this
slice resolves; the pointer is a content identity and never a bus body."
  (when (and (stringp pointer) (> (length pointer) 5)
             (string= "file:" (subseq pointer 0 5)))
    (subseq pointer 5)))

(defun read-provenance-bytes (pointer)
  "Read the provenance body POINTER names, as text, from the real file. It runs
outside the mutation loop and signals when the pointer names nothing."
  (let ((path (provenance-pointer-path pointer)))
    (unless path
      (error 'unsupported-input
             :what (format nil "provenance pointer ~A names no file: body"
                           (or pointer "-"))))
    (with-open-file (in path :direction :input :element-type 'character
                             :external-format :utf-8 :if-does-not-exist nil)
      (unless in
        (error 'unsupported-input
               :what (format nil "provenance body ~A is unreadable" pointer)))
      (let ((text (make-string (file-length in))))
        (subseq text 0 (read-sequence text in))))))

(defun stage-receipt (kernel &key provenance recipient)
  "Stage one receipt's inputs OUTSIDE the mutation loop: read the provenance
body the pointer names and run the operator-configured verifier over it, then
freeze the immutable stage at the revision it was taken at. A verifier outage
is an invalid stage that admits nothing and writes nothing
(SPEC-WORK.md:3863-3868)."
  (let ((rev (state-revision (kernel-state kernel))))
    (handler-case
        (let ((bytes (read-provenance-bytes provenance)))
          (stage-provenance bytes (kernel-verifier kernel recipient) :rev rev))
      (error ()
        (make-staged-input :bytes nil :result nil :valid-p nil :rev rev
                           :reason "provenance unverified")))))

;;; ------------------------------------------------------------------
;;; The two events one admission writes
;;; ------------------------------------------------------------------

(defparameter *receipt-session-fields* '(:sender :receipt-digest :effect)
  "The three fields the session derives from the verifier's result. They are
the session's own half of the envelope and are refused when a plain request
carries them (SPEC-WORK.md:3860-3862).")

(defparameter *receipt-request-fields*
  '(:change :offer :attempt :reply :stage :provenance :provenance-sha256 :reason)
  "The request's own half, in the order the `:receipt` kind writes it.")

(defun receipt-word (verb)
  (ecase verb (:acknowledge "ACKNOWLEDGE") (:decline "DECLINE")))

(defun %receipt-fail (verb request reason &optional (code 1))
  (values nil
          (format nil "~A FAIL node=~A offer=~A reply=~A: ~A"
                  (receipt-word verb)
                  (or (getf request :node) "-")
                  (or (getf request :offer) "-")
                  (or (getf request :reply) "-")
                  reason)
          code nil))

(defun receipt-effect (verb stage)
  (ecase verb
    (:acknowledge (ecase stage (:received :received) (:accepted :accepted)))
    (:decline :declined)))

(defun %receipt-event (kernel verb request)
  "The requester's half: one `:receipt` event carrying only what the request
may carry. `:node` is the node the offer pins; the receipt moves no node, no
count, no roadmap and no required set."
  (make-work-event
   :kind :receipt
   :node +absent+
   :by (%require request :by)
   :fields (list :change (if (eq verb :decline) :decline (getf request :stage))
                 :offer (getf request :offer +absent+)
                 :attempt (getf request :attempt +absent+)
                 :reply (getf request :reply +absent+)
                 :stage (getf request :stage +absent+)
                 :provenance (getf request :provenance +absent+)
                 :provenance-sha256 (getf request :provenance-sha256 +absent+)
                 :reason (getf request :reason +absent+))
   :stamp (%require request :stamp)
   :clock (%require request :clock)
   :request (%require request :request)
   :generation-owner (%require request :generation-owner)
   :rev (kernel-next-rev kernel)
   :session-written-p nil))

(defun %receipt-seal-event (requester result effect)
  "The session's own half: the three fields derived from the verifier's result,
never from the request. It is session-written and outside the payload digest
(SPEC-WORK.md:3860-3862, :894)."
  (make-work-event
   :kind :receipt-seal
   :node (work-event-node requester)
   :by (work-event-by requester)
   :fields (list :offer (getf (work-event-fields requester) :offer +absent+)
                 :reply (getf (work-event-fields requester) :reply +absent+)
                 :sender (getf result :recipient)
                 :receipt-digest (getf result :digest)
                 :effect effect)
   :stamp (work-event-stamp requester)
   :clock (work-event-clock requester)
   :request (work-event-request requester)
   :generation-owner (work-event-generation-owner requester)
   :rev (1+ (work-event-rev requester))
   :session-written-p t))

(defun %receipt-ok-line (verb request requester seal effect)
  (let ((stage (getf request :stage)))
    (if (eq verb :decline)
        (format nil "DECLINE OK id=~A request=~A node=~A offer=~A attempt=~A effect=~A released=0 rev=~D pushed=-"
                (event-id requester) (work-event-request requester)
                (or (getf request :node) "-") (or (getf request :offer) "-")
                (or (getf request :attempt) "-")
                (string-downcase (symbol-name effect))
                (work-event-rev seal))
        (format nil "ACKNOWLEDGE OK id=~A request=~A node=~A offer=~A attempt=~A stage=~A effect=~A lease=- reserved=0 committed=0 rev=~D pushed=-"
                (event-id requester) (work-event-request requester)
                (or (getf request :node) "-") (or (getf request :offer) "-")
                (or (getf request :attempt) "-")
                (string-downcase (symbol-name stage))
                (string-downcase (symbol-name effect))
                (work-event-rev seal)))))

;;; ------------------------------------------------------------------
;;; The one writer
;;; ------------------------------------------------------------------

(defun receipt-submit (kernel request)
  "Admit one `acknowledge` or `decline` envelope. It runs on the kernel's one
command thread: the readers already ran outside it and handed in an immutable
stage, and this revalidates that stage before anything is written. A refusal
writes nothing at all."
  (handler-case (%receipt-submit kernel request)
    (unsupported-input (c)
      (%receipt-fail (getf request :verb) request (unsupported-input-what c) 2))))

(defun %receipt-shape-check (verb request)
  "The request's shape, before any mutable precondition is read."
  ;; The session's own half is never the request's word (SPEC-WORK.md:3860). It
  ;; is asked before the key check, because a request carrying :sender names a
  ;; real field in the wrong place and must not read as an unknown one.
  (dolist (field *receipt-session-fields*)
    (when (getf request field)
      (error 'unsupported-input
             :what (format nil "~A is the session's own half and is never a request's word"
                           (string-downcase (symbol-name field))))))
  (%check-request-keys verb request)
  (when (eq verb :acknowledge)
    (let ((stage (getf request :stage)))
      (unless (member stage '(:received :accepted))
        (error 'unsupported-input
               :what (format nil "--stage is one of received or accepted, not ~A"
                             (if stage (string-downcase (princ-to-string stage)) "-"))))
      ;; SPEC-WORK.md:2295 -- `--stage accepted`: --by and --default, required;
      ;; `--stage received`: both exit 2.
      (ecase stage
        (:accepted
         (unless (and (getf request :lease-by) (getf request :lease-default))
           (error 'unsupported-input
                  :what "--stage accepted requires --by and --default")))
        (:received
         (when (or (getf request :lease-by) (getf request :lease-default))
           (error 'unsupported-input
                  :what "--stage received refuses --by and --default"))))))
  (when (and (eq verb :decline) (getf request :stage))
    (error 'unsupported-input :what "decline carries no --stage"))
  (dolist (key '(:offer :reply :provenance :provenance-sha256))
    (let ((value (getf request key)))
      (unless (and (stringp value) (plusp (length value)))
        (error 'unsupported-input
               :what (format nil "malformed ~A" (string-downcase (symbol-name key))))))))

(defun %receipt-tuple-mismatch (kernel request)
  "The offer's immutable tuple, revalidated against the offer the kernel itself
pinned (src/control.lisp `prepare-offer`). Answers NIL when it still holds, or
the field that moved (SPEC-WORK.md:3864-3865). An offer the kernel never pinned
is a mismatch, never a permissive default."
  (let* ((id (getf request :offer))
         (offer (and id (%offer kernel id))))
    (cond
      ((null offer) (format nil "no such offer ~A" (or id "-")))
      ((and (getf request :node)
            (not (equal (getf request :node) (of-node offer))))
       (format nil "node ~A is not offer ~A's ~A"
               (getf request :node) id (of-node offer)))
      ((and (getf request :attempt)
            (not (equal (getf request :attempt) (of-attempt offer))))
       (format nil "attempt ~A is not offer ~A's ~A"
               (getf request :attempt) id (of-attempt offer)))
      ((and (getf request :generation)
            (not (eql (getf request :generation) (of-generation offer))))
       (format nil "generation ~A is not offer ~A's ~A"
               (getf request :generation) id (of-generation offer)))
      (t nil))))

(defun stage-payload-file (pointer)
  "Stage the offered payload outside the mutation loop: read the real bytes the
pointer names and freeze their digest, which the writer matches against
--payload-sha256 before admitting one envelope (SPEC-WORK.md:3863-3866)."
  (handler-case (stage-payload (read-provenance-bytes pointer))
    (error ()
      (make-staged-input :bytes nil :result nil :valid-p nil :rev 0
                         :reason "invalid stage"))))

(defun %receipt-submit (kernel request)
  (let* ((verb (getf request :verb))
         (word (receipt-word verb)))
    (%receipt-shape-check verb request)
    (let* ((requester (%receipt-event kernel verb request))
           (rid (work-event-request requester))
           (digest (payload-digest (list requester))))
      ;; The stable payload and the two-part dedup test come BEFORE any mutable
      ;; precondition is read (SPEC-WORK.md:315).
      (multiple-value-bind (found recorded-digest recorded-line)
          (journal-lookup (kernel-journal kernel) rid)
        (cond
          ((eq found :unavailable)
           (return-from %receipt-submit
             (values nil (format nil "~A FAIL request=~A page=~A: dedup unavailable"
                                 word rid recorded-digest)
                     1 nil)))
          (found
           (if (string= digest recorded-digest)
               (return-from %receipt-submit
                 (values t recorded-line 0
                         (list :request rid :digest digest :events '() :replayed t)))
               (return-from %receipt-submit
                 (values nil (format nil "~A FAIL request=~A: reused with a different payload"
                                     word rid)
                         1 nil))))))
      ;; Now the stage, revalidated by the writer. The readers ran outside this
      ;; loop; nothing here fetches, reads a file or starts a process.
      (let ((staged (getf request :staged)))
        (unless (staged-input-p staged)
          (return-from %receipt-submit
            (%receipt-fail verb request "provenance unverified" 2)))
        (unless (staged-input-valid-p staged)
          (return-from %receipt-submit
            (%receipt-fail verb request
                           (or (staged-input-reason staged) "provenance unverified")
                           2)))
        ;; The verifier vouched for the bytes it read; the request's own
        ;; --provenance-sha256 must name those same bytes.
        (unless (equal (staged-input-digest staged) (getf request :provenance-sha256))
          (return-from %receipt-submit
            (%receipt-fail verb request "invalid stage" 2)))
        ;; A STALE STAGE. The readers ran outside the loop and froze the
        ;; revision they ran at; a stage the writer has already moved past
        ;; admits nothing (SPEC-WORK.md:3866-3867).
        (let ((current (state-revision (kernel-state kernel))))
          (unless (eql (staged-input-rev staged) current)
            (return-from %receipt-submit
              (values nil
                      (format nil "~A FAIL node=~A offer=~A reply=~A staged=~A current=~D: invalid stage"
                              word (or (getf request :node) "-")
                              (or (getf request :offer) "-")
                              (or (getf request :reply) "-")
                              (staged-input-rev staged) current)
                      2 nil))))
        ;; --expect, then the offer's immutable tuple, then the offered
        ;; payload's own stage: the writer revalidates each before it admits one
        ;; envelope (SPEC-WORK.md:3864-3866).
        (let ((expect (getf request :expect))
              (current (state-revision (kernel-state kernel))))
          (when (and expect (not (eql expect current)))
            (return-from %receipt-submit
              (values nil
                      (format nil "~A FAIL node=~A offer=~A reply=~A expect=~D current=~D: stale"
                              word (or (getf request :node) "-")
                              (or (getf request :offer) "-")
                              (or (getf request :reply) "-")
                              expect current)
                      1 nil))))
        (let ((mismatch (%receipt-tuple-mismatch kernel request)))
          (when mismatch
            (return-from %receipt-submit
              (%receipt-fail verb request
                             (format nil "tuple mismatch: ~A" mismatch) 1))))
        (let ((payload (getf request :staged-payload)))
          (when (or payload (getf request :payload-sha256))
            (unless (and (staged-input-p payload)
                         (staged-input-valid-p payload)
                         (equal (staged-input-digest payload)
                                (getf request :payload-sha256)))
              (return-from %receipt-submit
                (%receipt-fail verb request "invalid stage" 2)))))
        ;; An acceptance stands on a verified delivery, never on its own word
        ;; (SPEC-WORK.md:3853-3855, the reason at :6019).
        (when (and (eq verb :acknowledge) (eq (getf request :stage) :accepted))
          (unless (find-if (lambda (row)
                             (and (equal (getf row :offer) (getf request :offer))
                                  (eq (getf row :effect) :received)))
                           (admitted-receipts (kernel-state kernel)))
            (return-from %receipt-submit
              (%receipt-fail verb request "no received receipt" 1))))
        ;; One reply id names one set of received bytes. A second admission of
        ;; the same reply over other bytes is refused by name and writes
        ;; nothing (SPEC-WORK.md:6019).
        (let ((prior (admitted-receipt (kernel-state kernel) (getf request :reply))))
          (when (and prior (not (equal (getf prior :receipt-digest)
                                       (staged-input-digest staged))))
            (return-from %receipt-submit
              (%receipt-fail verb request
                             (format nil "conflicting bytes for receipt ~A"
                                     (getf request :reply))
                             1))))
        (let* ((result (staged-input-result staged))
               (effect (receipt-effect verb (getf request :stage)))
               (seal (%receipt-seal-event requester result effect))
               (envelope (list :request rid :digest digest
                               :events (list requester seal)
                               :seal seal)))
          (multiple-value-bind (accepted refusal)
              (journal-accept (kernel-journal kernel) envelope)
            (unless accepted
              (return-from %receipt-submit
                (values nil (format nil "~A FAIL request=~A: journal refused acceptance: ~A"
                                    word rid refusal)
                        1 nil))))
          ;; One final receipt, durably recorded before any of it is installed
          ;; (SPEC-WORK.md:307).
          (let ((line (%receipt-ok-line verb request requester seal effect)))
            (journal-record (kernel-journal kernel) rid digest line
                            (work-event-rev seal))
            (when *before-apply-hook* (funcall *before-apply-hook* envelope))
            (let ((candidate (apply-envelope (kernel-state kernel) envelope)))
              (setf (kernel-state kernel) candidate)
              (setf (kernel-next-rev kernel) (1+ (work-event-rev seal)))
              (values t line 0 envelope))))))))

;;; ------------------------------------------------------------------
;;; The ledger: a projection over WSTATE's own history
;;; ------------------------------------------------------------------

(defun admitted-receipts (state)
  "Every receipt this state's history admits, newest first. It is read back out
of the journalled envelopes themselves, so a session that is closed, reopened
and replayed reads the same ledger (SPEC-WORK.md:3857-3862)."
  (let ((rows '()))
    (dolist (record (wstate-history state))
      (let ((requester nil) (seal nil))
        (dolist (form (getf record :events))
          (case (getf form :kind)
            (:receipt (setf requester form))
            (:receipt-seal (setf seal form))))
        (when (and requester seal)
          (push (list :request (getf record :request)
                      :offer (getf requester :offer)
                      :attempt (getf requester :attempt)
                      :reply (getf requester :reply)
                      :stage (getf requester :stage)
                      :change (getf requester :change)
                      :provenance (getf requester :provenance)
                      :sender (getf seal :sender)
                      :receipt-digest (getf seal :receipt-digest)
                      :effect (getf seal :effect)
                      :rev (getf seal :rev))
                rows))))
    (nreverse rows)))

(defun admitted-receipt (state reply)
  "The receipt recorded under the reply id REPLY, or NIL."
  (find reply (admitted-receipts state) :key (lambda (r) (getf r :reply)) :test #'equal))
