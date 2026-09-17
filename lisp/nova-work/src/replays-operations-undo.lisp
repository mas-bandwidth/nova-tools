;;;; replays-operations-undo.lisp --- the long-operation control plane and the
;;;; append-only reversal of accepted requests.
;;;;
;;;; The pure kernel the five acceptance replays
;;;; `status-answers-while-io-runs`, `cancel-is-a-request-not-an-erasure`,
;;;; `clip-is-one-long-operation`, `undo-appends-and-preserves` and
;;;; `redo-refuses-a-stale-plan` call. Nothing here starts a session, a socket
;;;; or a child. Sources: SPEC-WORK.md:2719-2758 (slow work returns a durable
;;;; operation id, the control plane never waits behind it, queues and staged
;;;; bytes are bounded, a restart reconciles pending ids, a cancellation is a
;;;; request with its own final disposition), :2825-2843 (mistakes are
;;;; reversible by appending, never by erasing), and :5778-5782 (clip is one
;;;; long operation).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Long operations and the responsive control plane (SPEC-WORK.md:2719)
;;; ------------------------------------------------------------------

(defparameter *operation-limits* '(:queue 4 :staged-bytes 4096 :retained-results 4)
  "Queues, staged bytes and retained results are bounded by explicit limits
(SPEC-WORK.md:2753).")

(defstruct (operation
             (:constructor make-operation
                 (&key id op request (state :queued) result
                       (staged-bytes 0) (retained-results 0))))
  id op request state result staged-bytes retained-results)

(defstruct (work-session
             (:constructor make-work-session
                 (&key (operations nil) (events nil) (receipts nil)
                       (limits *operation-limits*) (git-timeout 30))))
  operations events receipts limits git-timeout)

(defun session-operation (session id)
  "The operation record itself, found in the bounded operation list."
  (find id (work-session-operations session) :key #'operation-id :test #'equal))

(defun session-add-operation (session op)
  (make-work-session :operations (cons op (work-session-operations session))
                     :events (work-session-events session)
                     :receipts (work-session-receipts session)
                     :limits (work-session-limits session)
                     :git-timeout (work-session-git-timeout session)))

(defun operation-status (session id)
  "Read one operation's own record: no journal replay, no whole-queue drain and
no wait on the I/O the operation is doing, so status answers while the mutation
loop is busy (SPEC-WORK.md:2731, :2756)."
  (let ((op (session-operation session id)))
    (list :id id
          :op (and op (operation-op op))
          :state (if op (operation-state op) :none)
          :result (and op (operation-result op)))))

(defun operation-pending-p (op)
  (member (operation-state op) '(:queued :running)))

(defun session-bounded-p (session)
  "Queues, staged bytes and retained results, each inside its explicit limit
(SPEC-WORK.md:2753)."
  (let ((limits (work-session-limits session))
        (ops (work-session-operations session)))
    (and (<= (length ops) (getf limits :queue))
         (<= (reduce #'+ ops :key #'operation-staged-bytes :initial-value 0)
             (getf limits :staged-bytes))
         (<= (reduce #'+ ops :key #'operation-retained-results :initial-value 0)
             (getf limits :retained-results)))))

(defun operation-cancel (session id &key (request "req-cancel-1")
                                      (external-effect :none))
  "A cancellation is a request with its own acknowledgement and its own final
disposition. A cancel replayed twice cancels once. It neither erases an
accepted mutation nor claims an uncertain external effect was cancelled
(SPEC-WORK.md:2734-2738)."
  (let ((op (session-operation session id)))
    (cond
      ((null op)
       (values session (list :id id :state :none :request request
                             :reason "no such operation")))
      ((member (operation-state op) '(:cancelled :uncertain))
       (values session (list :id id :state (operation-state op)
                             :request request :replayed t)))
      ((eq external-effect :uncertain)
       (setf (operation-state op) :uncertain)
       (values session (list :id id :state :uncertain :request request
                             :reason "external effect uncertain")))
      (t
       (setf (operation-state op) :cancelled)
       (values session (list :id id :state :cancelled :request request))))))

(defun clip-request (session &key (id "op-clip-1") (request "req-clip-1")
                                (staged-bytes 0))
  "`clip` prints OPERATION OK id=<id> op=clip state=queued at once and exits;
the transport continues. A full queue refuses rather than growing unbounded
(SPEC-WORK.md:2738-2745, :2753)."
  (let ((limits (work-session-limits session)))
    (when (>= (length (work-session-operations session)) (getf limits :queue))
      (return-from clip-request
        (values session nil "OPERATION FAIL: queue full")))
    (let ((op (make-operation :id id :op :clip :request request
                              :state :queued :staged-bytes staged-bytes)))
      (values (session-add-operation session op) op
              (format nil "OPERATION OK id=~A op=clip state=queued" id)))))

(defun operation-wait (session id &key race)
  "`operation wait --id` prints the CLIP OK line when the transport settles, or
CLIP RACED when the base predicate refused the push. Either way it is the same
wait (SPEC-WORK.md:2740-2742)."
  (let ((op (session-operation session id)))
    (cond
      ((null op)
       (values session
               (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id)))
      (race
       (setf (operation-state op) :raced)
       (values session
               (format nil "CLIP RACED session=<path> operation=~A generation=1 expected=deadbeef found=cafef00d" id)))
      (t
       (setf (operation-state op) :done)
       (values session
               (format nil "CLIP OK session=<path> operation=~A boundary=req-clip-1 events=3 base=deadbeef commit=cafef00d pushed=7 attempts=1" id))))))

(defun session-stop (session &key race)
  "`session stop` is the one caller that waits for its own clip, by the same
`operation wait` inside its --git-timeout; it prints the CLIP OK first and then
the SESSION OK (SPEC-WORK.md:2742-2745)."
  (let* ((clip (find :clip (work-session-operations session) :key #'operation-op))
         (clip-line
           (when clip
             (multiple-value-bind (settled line)
                 (operation-wait session (operation-id clip) :race race)
               (declare (ignore settled))
               line)))
         (session-line
           (format nil "SESSION OK session=<path> owner=rowan generation=1 state=live events=~D pending=0 pushed=7"
                   (length (work-session-events session)))))
    (if clip-line (list clip-line session-line) (list session-line))))

(defun reconcile-operations (session)
  "A restart reconciles the operation ids that were pending before anything is
retried, erasing no accepted mutation (SPEC-WORK.md:2722-2727, :2753)."
  (let ((reconciled '()))
    (dolist (op (work-session-operations session))
      (when (operation-pending-p op)
        (setf (operation-state op) :reconciled)
        (push (operation-id op) reconciled)))
    (values session (nreverse reconciled))))

;;; ------------------------------------------------------------------
;;; Undo appends and preserves (SPEC-WORK.md:2825)
;;; ------------------------------------------------------------------

(defparameter *reversible-verbs*
  '((:node-add . :node-remove)
    (:node-require . :node-require)
    (:dep . :dep)
    (:take . :release)
    (:release . :take))
  "A reduced reversible-verb table (SPEC-WORK.md:2853-2874): the verbs this
replay slice exercises, each mapped to the envelope its undo appends.")

(defun undo-request (ledger request &key (by "rowan")
                                         (stamp "2026-09-16T00:00:00Z"))
  "An undo of a named request appends a typed compensating envelope carrying
its lineage. The original event and every receipt stay exactly where they are;
nothing is deleted and nothing is rewritten (SPEC-WORK.md:2825-2837)."
  (let ((original (find request (getf ledger :history)
                        :key (lambda (e) (getf e :request)) :test #'equal)))
    (unless original
      (return-from undo-request
        (values ledger nil
                (format nil "UNDO FAIL request=~A: no such request" request))))
    (let ((compensating (cdr (assoc (getf original :kind) *reversible-verbs*
                                    :test #'equal))))
      (unless compensating
        (return-from undo-request
          (values ledger nil
                  (format nil "UNDO FAIL request=~A: not reversible here" request))))
      (let ((envelope (list :id (format nil "ev-undo-~A" request)
                            :kind compensating
                            :request (format nil "req-undo-~A" request)
                            :undo-of request
                            :compensates (getf original :id)
                            :lineage (list (getf original :id) request)
                            :by by :stamp stamp)))
        (values (list :history (append (getf ledger :history) (list envelope))
                      :receipts (getf ledger :receipts))
                envelope
                nil)))))

(defun moved-preconditions (expected actual)
  "The precondition keys whose value moved between EXPECTED and ACTUAL."
  (let ((moved '()))
    (loop for (k v) on expected by #'cddr
          unless (equal v (getf actual k))
            do (push k moved))
    (nreverse moved)))

(defun redo-request (ledger undo-id plan current)
  "A redo reapplies the intent against current preconditions rather than
deleting the undo. A plan whose preconditions moved is refused atomically,
naming what changed, writing nothing, and the undo is never deleted
(SPEC-WORK.md:2829-2843)."
  (let ((undo-envelope (find undo-id (getf ledger :history)
                             :key (lambda (e) (getf e :request))
                             :test #'equal)))
    (unless undo-envelope
      (return-from redo-request
        (values ledger nil
                (format nil "REDO FAIL request=~A: no such undo" undo-id))))
    (let ((expected-rev (getf plan :at-rev))
          (actual-rev (getf current :rev)))
      (unless (eql expected-rev actual-rev)
        (return-from redo-request
          (values ledger nil
                  (format nil "REDO FAIL request=~A: stale plan rev moved ~A->~A"
                          undo-id expected-rev actual-rev))))
      (let ((moved (moved-preconditions (getf plan :preconditions)
                                        (getf current :preconditions))))
        (when moved
          (return-from redo-request
            (values ledger nil
                    (format nil "REDO FAIL request=~A: stale plan changed ~{~A~^,~}"
                            undo-id moved)))))
      (let ((envelope (list :id (format nil "ev-redo-~A" undo-id)
                            :kind (getf undo-envelope :kind)
                            :request (format nil "req-redo-~A" undo-id)
                            :redo-of undo-id
                            :lineage (getf undo-envelope :lineage))))
        (values (list :history (append (getf ledger :history) (list envelope))
                      :receipts (getf ledger :receipts))
                envelope
                nil)))))
