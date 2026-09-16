;;;; kernel.lisp --- the C/O transition kernel.
;;;;
;;;; Three transition requests of docs/SPEC-WORK.md are supported here:
;;;;
;;;;   state --to doing       one nonterminal :transition (SPEC-WORK.md:994-1005)
;;;;   state --to done        a :transition event and, in the same envelope, the
;;;;                          session's own :settle (SPEC-WORK.md:1202-1209)
;;;;   event --kind reopen    a :reopen scope event and, in the same envelope,
;;;;                          the session's own :revive (SPEC-WORK.md:1210-1214)
;;;;
;;;; Every other verb, kind, state and edge refuses. There is no session here,
;;;; no CLI, no socket, no provider, no import, no W, no savepoint, no batch, no
;;;; clip, no undo and no retention window; see README.md.
;;;;
;;;; The OK line follows the mutation line of SPEC-WORK.md:2884 —
;;;; `<MUTATION> OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|->`
;;;; without the grammar's trailing `emitted=<bytes>`, which is the CLI's count
;;;; of what it printed and there is no CLI in this slice. It is the record the
;;;; dedup answer replays, not a printed line.

(in-package #:nova-work)

(defstruct (kernel (:constructor %make-kernel))
  state journal next-rev
  ;; The single-writer kernel (SPEC-WORK.md SPEC-AHEAD #500): one command thread
  ;; owns O and C and applies mutations in order; readers never touch the thread.
  queue q-lock q-cvar thread closed-p)

(defvar *before-apply-hook* nil
  "A test seam. When bound, it is called with the envelope after the journal has
recorded it and before any of it is applied, so a stop can be injected exactly
at the ordering boundary SPEC-WORK.md:307 names.")

(defstruct (kernel-command (:conc-name cmd-))
  "One mutation enqueued for the kernel's single command thread. REPLY carries
the results back to the caller; BEFORE-APPLY-HOOK is the captured dynamic value
of *BEFORE-APPLY-HOOK* at submit time, since special bindings are thread-local."
  request before-apply-hook results error done-p lock cvar)

(defun make-kernel (&key state journal rev-base)
  "REV-BASE defaults to one past the state's own revision, so a kernel opened
over a reconstructed state issues no id the history already holds
(SPEC-WORK.md:1216-1218 keys a closed row <event-rev>:<id>; :1578 allows
startup and recovery to reconstruct the counters). An explicit REV-BASE at or
below the state's revision is refused rather than silently reissued."
  (let ((state (or state (make-seed-state '()))))
    (when (and rev-base (<= rev-base (state-revision state)))
      (error 'unsupported-input
             :what (format nil "rev-base ~D is at or below the state's own revision ~D"
                           rev-base (state-revision state))))
    (let ((k (%make-kernel :state state
                           :journal (or journal (make-ordering-journal))
                           :next-rev (or rev-base (1+ (state-revision state)))
                           :queue '()
                           :q-lock (sb-thread:make-mutex)
                           :q-cvar (sb-thread:make-waitqueue)
                           :thread nil
                           :closed-p nil)))
      (setf (kernel-thread k)
            (sb-thread:make-thread (lambda () (command-loop k))
                                   :name "nova-work-kernel"))
      k)))

(defun command-loop (kernel)
  "The single command thread. It drains the mailbox, one command at a time, and
applies each against O and C in the order it arrived. Readers never run here."
  (unwind-protect
       (loop
         (let ((cmd nil))
           (sb-thread:with-mutex ((kernel-q-lock kernel))
             (loop while (and (null (kernel-queue kernel))
                              (not (kernel-closed-p kernel)))
                   do (sb-thread:condition-wait (kernel-q-cvar kernel)
                                                (kernel-q-lock kernel)))
             (when (and (kernel-closed-p kernel) (null (kernel-queue kernel)))
               (return))
             (setf cmd (pop (kernel-queue kernel))))
           (run-command kernel cmd)))
    ;; On unwind (shutdown), close so no further command is admitted.
    (sb-thread:with-mutex ((kernel-q-lock kernel))
      (setf (kernel-closed-p kernel) t)
      (sb-thread:condition-broadcast (kernel-q-cvar kernel)))))

(defun run-command (kernel cmd)
  "Run one command on the command thread, then reply to the waiter."
  (let ((*before-apply-hook* (cmd-before-apply-hook cmd)))
    (handler-case
        (multiple-value-bind (okp line code env) (%dispatch kernel (cmd-request cmd))
          (finish-command cmd (list okp line code env) nil))
      (error (c)
        (finish-command cmd nil c)))))

(defun finish-command (cmd results error)
  (sb-thread:with-mutex ((cmd-lock cmd))
    (setf (cmd-results cmd) results
          (cmd-error cmd) error
          (cmd-done-p cmd) t)
    (sb-thread:condition-notify (cmd-cvar cmd))))

;;; What a request may carry, per verb. SPEC-WORK.md:3227
;;; `every-field-has-an-owning-verb` wants every field mapped to its owning
;;; mutation "with no generic set-field escape hatch and no parallel alias", so
;;; an unrecognised key refuses rather than being dropped into a digest that
;;; cannot tell it was there.

(defparameter *request-keys*
  '((:state-to-done :verb :node :by :reason :evidence
     :request :stamp :clock :generation-owner)
    (:state-to-doing :verb :node :by :reason :evidence
     :request :stamp :clock :generation-owner)
    (:event-reopen :verb :node :by :reason
     :request :stamp :clock :generation-owner)
    (:take :verb :node :by :deadline :default :id :lease-id
     :request :stamp :clock :generation-owner)
    (:heartbeat :verb :node :by :evidence
     :request :stamp :clock :generation-owner)
    (:release :verb :node :by :handed :reason
     :request :stamp :clock :generation-owner)))

(defparameter *kind-owned-fields*
  '(:to :blocked-by :evidence :disposition :already-closed :deadline :default :handed)
  "Fields that belong to some event kind of SPEC-WORK.md:823-892. One of these
on a verb that does not own it is refused as forbidden rather than as unknown,
because it names a real field in the wrong place -- a caller-given :blocked-by
on a :to :done is the case, and it must never be quietly overwritten with
(:absent).")

(defun %check-request-keys (verb request)
  (let ((allowed (rest (assoc verb *request-keys*))))
    (loop for key in request by #'cddr
          do (unless (member key allowed)
                (error 'unsupported-input
                       :what (format nil "~A: ~A"
                                     (if (member key *kind-owned-fields*)
                                         "the transition forbids field"
                                         "unknown field")
                                     (string-downcase (symbol-name key))))))))

;;; The transition table, for the supported subset only (SPEC-WORK.md:994-1005).

(defparameter *done-edges* '(:doing :review)
  "from :doing to ... :done; from :review to ... :done. No other state reaches
:done, so a :todo item closing is rule 10 and not a silent success.")

(defparameter *doing-edges* '(:todo :blocked :review :cancel-requested :unknown)
  "The reviewed incoming edges to :doing. Unknown additionally requires a
reason or evidence; done/deferred leave only via reopen, never state.")

(defun %word (verb)
  (ecase verb
    ((:state-to-done :state-to-doing) "STATE")
    (:event-reopen "EVENT")
    (:take "LEASE")
    (:heartbeat "HEARTBEAT")
    (:release "RELEASE")))

(defun %require (request key)
  (let ((value (getf request key)))
    (when (or (null value) (and (stringp value) (string= value "")))
      (error 'unsupported-input
             :what (format nil "unsupported: ~A refusing to guess"
                           (string-downcase (symbol-name key)))))
    value))

(defun %requester-event (kernel request verb)
  (%check-request-keys verb request)
  (let ((node (%require request :node))
        (by (%require request :by))
        (rid (%require request :request))
        (stamp (%require request :stamp))
        (clock (%require request :clock))
        (owner (%require request :generation-owner)))
    (make-work-event
     :kind (ecase verb
             ((:state-to-done :state-to-doing) :transition)
             (:event-reopen :reopen)
             (:take :lease)
             (:heartbeat :heartbeat)
             (:release :release))
     :node node :by by
     :fields (ecase verb
               ((:state-to-done :state-to-doing)
                (list :to (if (eq verb :state-to-done) :done :doing)
                      :reason (getf request :reason +absent+)
                      :blocked-by +absent+
                      :evidence (getf request :evidence +absent+)))
               (:event-reopen
                (list :reason (getf request :reason +absent+)))
               (:take
                (let ((lid (or (getf request :id)
                               (getf request :lease-id)
                               (format nil "l-~A" (subseq (sha256-hex (format nil "~A-~A" rid stamp)) 0 12)))))
                  (list :id lid
                        :deadline (%require request :deadline)
                        :default (getf request :default :release))))
               (:heartbeat
                (let* ((n (%node-quiet (kernel-state kernel) node))
                       (lid (and n (wnode-lease-id n))))
                  (list :lease (or lid +absent+)
                        :evidence (%require request :evidence))))
               (:release
                (let* ((n (%node-quiet (kernel-state kernel) node))
                       (lid (and n (wnode-lease-id n))))
                  (list :lease (or lid +absent+)
                        :handed (getf request :handed +absent+)
                        :reason (getf request :reason +absent+)))))
     :stamp stamp :clock clock :request rid :generation-owner owner
     :rev (kernel-next-rev kernel)
     :session-written-p nil)))

(defun %validate (state verb event)
  "Answer NIL when the candidate is admissible, or (values rule reason)."
  (let* ((id (work-event-node event))
         (node (%node-quiet state id)))
    (unless node
      (return-from %validate (values 2 (format nil "no such node ~A" id))))
    (unless (or (member verb '(:take :heartbeat :release))
                (member (wnode-type node) '(:task :bug)))
      (error 'unsupported-input
             :what (format nil "unsupported: ~A is a ~A; slice 1 containers change branch with their members, not through direct task-state requests"
                           id (string-downcase (symbol-name (wnode-type node))))))
    (ecase verb
      (:take
       (unless (eq :o (wnode-branch node))
         (return-from %validate (values 18 (format nil "~A is in C" id))))
       (let ((deadline (getf (work-event-fields event) :deadline)))
         (when (or (null deadline) (absentp deadline))
           (return-from %validate (values 10 "take requires deadline"))))
       (when (wnode-holder node)
         (let ((cur-dl (wnode-deadline node))
               (cur-holder (wnode-holder node))
               (stamp (work-event-stamp event)))
           (when (or (null cur-dl) (not (string< cur-dl stamp)))
             (return-from %validate
               (values :held (format nil "node=~A holder=~A deadline=~A: held"
                                     id cur-holder cur-dl)))))))
      (:heartbeat
       (unless (eq :o (wnode-branch node))
         (return-from %validate (values 18 (format nil "~A is in C" id))))
       (unless (wnode-holder node)
         (return-from %validate (values :not-held (format nil "node=~A: not held" id))))
       (unless (string= (work-event-by event) (wnode-holder node))
         (return-from %validate
           (values :not-holder (format nil "node=~A holder=~A: not held by ~A"
                                       id (wnode-holder node) (work-event-by event))))))
      (:release
       (unless (wnode-holder node)
         (return-from %validate (values :not-held (format nil "node=~A: not held" id))))
       (unless (string= (work-event-by event) (wnode-holder node))
         (return-from %validate
           (values :not-holder (format nil "node=~A holder=~A deadline=~A: held"
                                       id (wnode-holder node) (wnode-deadline node))))))
      (:state-to-doing
       (unless (and (eq :o (wnode-branch node))
                    (member (wnode-state node) *doing-edges*))
         (return-from %validate
           (values 10 (format nil "no edge from ~A to doing"
                              (string-downcase (symbol-name (wnode-state node)))))))
       (let ((reason (getf (work-event-fields event) :reason))
             (evidence (getf (work-event-fields event) :evidence)))
         (unless (or (absentp reason) (stringp reason))
           (return-from %validate (values 10 "reason is not a string")))
         ;; As in this kernel's done boundary, these are evidence ID shapes,
         ;; not resolution against an evidence store that does not exist yet.
         (unless (or (absentp evidence)
                     (and (listp evidence)
                          (every (lambda (id) (and (stringp id) (plusp (length id))))
                                 evidence)))
           (return-from %validate (values 10 "evidence is not a list of non-empty event ids")))
         (when (and (eq :unknown (wnode-state node))
                    (not (and (stringp reason) (plusp (length reason))))
                    (or (absentp evidence) (null evidence)))
           (return-from %validate (values 10 "unknown to doing requires evidence or a reason")))))
      (:state-to-done
       (unless (eq :o (wnode-branch node))
         (return-from %validate (values 10 (format nil "~A is in C" id))))
       (unless (member (wnode-state node) *done-edges*)
         (return-from %validate
           (values 10 (format nil "no edge from ~A to done"
                              (string-downcase (symbol-name (wnode-state node)))))))
       (let ((evidence (getf (work-event-fields event) :evidence)))
         (when (or (absentp evidence) (null evidence))
           (return-from %validate (values 5 "done without evidence")))
         (unless (listp evidence)
           (return-from %validate (values 5 "evidence is not a list of event ids")))
         (dolist (entry evidence)
           (unless (and (stringp entry) (plusp (length entry)))
             (return-from %validate
               (values 5 "an evidence entry that is not an event id")))
           ;; SPEC-WORK.md:1052 -- a note: pointer is "never as evidence for
           ;; :done"; :2659 puts it in rule 5. This is the one clause of rule 5
           ;; that is checkable without the :evidence event kind, which is out
           ;; of this slice; see README.md for the rest of the clause.
           (when (and (>= (length entry) 5) (string= "note:" (subseq entry 0 5)))
             (return-from %validate
               (values 5 (format nil "~A is a note: pointer and is not completion evidence"
                                 entry)))))))
      (:event-reopen
       (unless (eq :c (wnode-branch node))
         (return-from %validate
           (values 10 (format nil "~A is in O and never left it" id))))
       (unless (eq :done (wnode-state node))
         (return-from %validate
           (values 10 (format nil "no edge from ~A to todo"
                              (string-downcase (symbol-name (wnode-state node)))))))))
    nil))

(defun %derived-branch-event (verb requester node reason rev)
  "One session-written branch event. Ancestor events inherit the requester's
author and provenance; their reason names the direct member that moved them."
  (make-work-event
   :kind (ecase verb (:state-to-done :settle) (:event-reopen :revive))
   :node node
   :by (work-event-by requester)
   :fields (ecase verb
             (:state-to-done (list :disposition :done
                                   :reason reason
                                   :already-closed '()))
             (:event-reopen (list :reason reason)))
   :stamp (work-event-stamp requester)
   :clock (work-event-clock requester)
   :request (work-event-request requester)
   :generation-owner (work-event-generation-owner requester)
   :rev rev
   :session-written-p t))

(defun %session-event (kernel verb requester)
  "The session's own half of the envelope: a :settle beside a close, a :revive
beside a reopen. Outside the payload digest (SPEC-WORK.md:894)."
  (declare (ignore kernel))
  (%derived-branch-event
   verb requester (work-event-node requester)
   (getf (work-event-fields requester) :reason +absent+)
   (1+ (work-event-rev requester))))

(defun %static-container-p (node)
  "The container kinds whose static containment sets this slice represents."
  (member (wnode-type node) '(:work-set :feature)))

(defun %cascade-events (state verb requester session)
  "Build and validate the branch cascade on a private candidate. Each decision
reads a direct required-member counter, then walks at most one ancestor edge."
  (let ((candidate (copy-state state))
        (events (list requester))
        (cascade '())
        (child-id (work-event-node requester))
        (next-rev (1+ (work-event-rev requester))))
    (apply-event candidate requester)
    ;; SPEC-WORK.md:1674-1680 settle-releases-the-lease:
    ;; If the settled item holds an active lease, the settle envelope carries
    ;; a :release for that live lease, written by the settling author.
    (when (and (eq verb :state-to-done)
               (wnode-holder (%node-quiet state child-id)))
      (let ((rel-event (make-work-event
                        :kind :release
                        :node child-id
                        :by (work-event-by requester)
                        :fields (list :lease (or (wnode-lease-id (%node-quiet state child-id)) +absent+)
                                      :handed +absent+
                                      :reason (format nil "settle: ended holder ~A"
                                                      (wnode-holder (%node-quiet state child-id))))
                        :stamp (work-event-stamp requester)
                        :clock (work-event-clock requester)
                        :request (work-event-request requester)
                        :generation-owner (work-event-generation-owner requester)
                        :rev next-rev
                        :session-written-p t)))
        (push rel-event events)
        (apply-event candidate rel-event)
        (incf next-rev)))
    (setf (work-event-rev session) next-rev)
    (push session events)
    (apply-event candidate session)
    (incf next-rev)
    (setf events (nreverse events))
    (loop
      (let* ((child (%node-quiet candidate child-id))
             (parent-id (and child (wnode-parent child)))
             (parent (and parent-id (%node-quiet candidate parent-id))))
        (unless (and parent (%static-container-p parent)) (return))
        (unless (ecase verb
                  (:state-to-done
                   (and (eq :o (wnode-branch parent))
                        (plusp (wnode-required-count parent))
                        (zerop (wnode-required-open parent))))
                  (:event-reopen
                   (and (eq :c (wnode-branch parent))
                        (plusp (wnode-required-open parent)))))
          (return))
        (let ((event (%derived-branch-event verb requester parent-id child-id next-rev)))
          (push event cascade)
          (apply-event candidate event)
          (incf next-rev)
          (setf child-id parent-id))))
    (nconc events (nreverse cascade))))

(defun %ok-line (word requester last-event &key candidate)
  "Derived from the envelope's own events, so the line exists before anything is
applied and the journal can be appended first."
  (case (work-event-kind requester)
    (:lease
     (let* ((fields (work-event-fields requester))
            (holder (work-event-by requester))
            (deadline (getf fields :deadline))
            (def-act (getf fields :default :release))
            (live (if candidate (state-working-count candidate) 1)))
       (format nil "~A OK id=~A request=~A node=~A holder=~A deadline=~A default=~A live=~D rev=~D pushed=-"
               word (event-id requester) (work-event-request requester)
               (work-event-node requester) holder deadline
               (if (and (listp def-act) (eq (first def-act) :escalate))
                   (format nil "escalate:~A" (second def-act))
                   (string-downcase (symbol-name def-act)))
               live (work-event-rev last-event))))
    (:heartbeat
     (format nil "HEARTBEAT OK id=~A request=~A node=~A rev=~D pushed=-"
             (event-id requester) (work-event-request requester)
             (work-event-node requester) (work-event-rev last-event)))
    (:release
     (format nil "RELEASE OK id=~A request=~A node=~A rev=~D pushed=-"
             (event-id requester) (work-event-request requester)
             (work-event-node requester) (work-event-rev last-event)))
    (t
     (format nil "~A OK id=~A request=~A node=~A rev=~D pushed=-"
             word (event-id requester) (work-event-request requester)
             (work-event-node requester) (work-event-rev last-event)))))

(defun %dispatch (kernel request)
  "Run one mutation request synchronously on the caller's thread and answer
(values OK-P LINE EXIT-CODE ENVELOPE). This is the per-command body of the
single command thread; it is never an independent mutation path."
  (handler-case (%submit kernel request)
    (unsupported-input (c)
      (values nil (format nil "~A FAIL node=~A: ~A"
                          (handler-case (%word (getf request :verb))
                            (error () "MUTATION"))
                          (or (getf request :node) "-")
                          (unsupported-input-what c))
              2 nil))))

(defun submit (kernel request)
  "Answer (values OK-P LINE EXIT-CODE ENVELOPE). The request is enqueued onto the
kernel's one command thread and the caller waits for its result; readers of O and
C never touch that thread."
  (let ((cmd (make-kernel-command
              :request request
              :before-apply-hook *before-apply-hook*
              :lock (sb-thread:make-mutex)
              :cvar (sb-thread:make-waitqueue))))
    (sb-thread:with-mutex ((kernel-q-lock kernel))
      (when (kernel-closed-p kernel)
        (error 'unsupported-input :what "the kernel's command thread is closed"))
      (setf (kernel-queue kernel) (nconc (kernel-queue kernel) (list cmd)))
      (sb-thread:condition-notify (kernel-q-cvar kernel)))
    (sb-thread:with-mutex ((cmd-lock cmd))
      (loop until (cmd-done-p cmd)
            do (sb-thread:condition-wait (cmd-cvar cmd) (cmd-lock cmd))))
    (if (cmd-error cmd)
        (error (cmd-error cmd))
        (values-list (cmd-results cmd)))))

(defun %submit (kernel request)
  (let ((verb (getf request :verb)))
    (unless (member verb '(:state-to-done :state-to-doing :event-reopen :take :heartbeat :release))
      (error 'unsupported-input
             :what (format nil "unsupported: verb ~A is not in slice 1"
                           (if verb (string-downcase (princ-to-string verb)) "-"))))
    (let* ((word (%word verb))
           (requester (%requester-event kernel request verb))
           (rid (work-event-request requester))
           (digest (payload-digest (list requester))))
      ;; The two-part dedup test (SPEC-WORK.md:315), asked of the journal. The
      ;; kernel keeps no resident map of request ids.
      (multiple-value-bind (found recorded-digest recorded-line)
          (journal-lookup (kernel-journal kernel) rid)
        (cond
          ((eq found :unavailable)
           (return-from %submit
             (values nil (format nil "~A FAIL request=~A page=~A: dedup unavailable"
                                 word rid recorded-digest)
                     1 nil)))
          (found
           (if (string= digest recorded-digest)
               (return-from %submit
                 (values t recorded-line 0
                         (list :request rid :digest digest :events '() :replayed t)))
               (return-from %submit
                 (values nil (format nil "~A FAIL request=~A: reused with a different payload"
                                     word rid)
                         1 nil))))))
      ;; Validate against the current state as it would be with the event applied.
      (multiple-value-bind (rule reason) (%validate (kernel-state kernel) verb requester)
        (when rule
          (return-from %submit
            (values nil
                    (cond
                      ((member rule '(:held :not-held :not-holder))
                       (format nil "~A FAIL ~A"
                               (if (eq verb :release) "LEASE" word)
                               reason))
                      (t
                       (format nil "~A FAIL node=~A: rule ~D: ~A"
                               word (work-event-node requester) rule reason)))
                    1 nil))))
      (let* ((session (unless (member verb '(:state-to-doing :take :heartbeat :release))
                        (%session-event kernel verb requester)))
             ;; Doing and lease events stay inside O: one event, no branch change or cascade.
             (events (if session
                         (%cascade-events (kernel-state kernel) verb requester session)
                         (list requester)))
             (last-event (car (last events)))
             (envelope (list :request rid :digest digest
                             :events events
                             :settle session)))
        ;; Acceptance is asked before anything is applied.
        (multiple-value-bind (accepted refusal) (journal-accept (kernel-journal kernel) envelope)
          (unless accepted
            (return-from %submit
              (values nil (format nil "~A FAIL request=~A: journal refused acceptance: ~A"
                                  word rid refusal)
                      1 nil))))
        ;; SPEC-WORK.md:307 -- the session "appends it with its request id to the
        ;; local recovery journal, acknowledges only after the journal is
        ;; durable, THEN applies it". So: record, then apply. A stop between the
        ;; two leaves the record written and nothing applied, which is the order
        ;; the two-part retry of :315 rests on; the reverse order would let a
        ;; stop apply an envelope the journal never heard of.
        (let* ((candidate (apply-envelope (kernel-state kernel) envelope))
               (line (%ok-line word requester last-event :candidate candidate)))
          (journal-record (kernel-journal kernel) rid digest line
                          (work-event-rev last-event))
          (when *before-apply-hook* (funcall *before-apply-hook* envelope))
          ;; All-or-none: the candidate is built whole, then installed.
          (setf (kernel-state kernel) candidate)
          (setf (kernel-next-rev kernel) (1+ (work-event-rev last-event)))
          (values t line 0 envelope))))))

;;; The counters, read.

(defun ask-size (kernel)
  "`query --ask size` (SPEC-WORK.md:1575). It reads the counter and triggers no
rollup, no scan, no parse and no replay: nothing below touches a node."
  (let ((parses-before *parses*)
        (replays-before *replays*))
    (let* ((state (kernel-state kernel))
           (open (wstate-root-open state))
           (scope (wstate-revision state))
           (unit "items"))
      (values open unit scope
              ;; The two counts are what THIS ask measured, not a literal.
              (format nil "QUERY OK ask=size scope=~D unit=~A open=~D parses=~D replays=~D"
                      scope unit open
                      (- *parses* parses-before)
                      (- *replays* replays-before))))))

(defun open-issue-count (kernel)
  "The open linked-issue counter. Separate, and never labelled |O|."
  (wstate-issue-open (kernel-state kernel)))

(defun open-leaf-count (kernel)
  "The open leaf-task counter. Separate, and never labelled |O|."
  (wstate-leaf-open (kernel-state kernel)))
