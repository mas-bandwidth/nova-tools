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
  state journal next-rev fleet routes allocations
  ;; SPEC-WORK.md:4780 -- the read-time needs view the candidate gate consults:
  ;; the evidence records a node's standing done names, the verification
  ;; session whose cache holds the raw facts, and each node's generation. It is
  ;; the session's, never payload, and no event carries it.
  needs-view
  ;; The single-writer kernel (SPEC-WORK.md:2603-2616): one command thread owns O
  ;; and C and applies mutations in order; readers never touch it. The queue
  ;; holds accepted commands, Q-LOCK/Q-CVAR guard the mailbox, THREAD is the
  ;; command thread, and CLOSED-P refuses further enqueues on shutdown.
  queue q-lock q-cvar thread closed-p
  ;; The applied-request index the undo path reads for an original request's
  ;; preimage and its reversibility. It is not the dedup index, which stays the
  ;; journal's (SPEC-WORK.md:2117 forbids an unbounded request-id map).
  (applied (make-hash-table :test #'equal))
  ;; The execution-control state: attempts, offers, durable holds and their
  ;; captures. It moves no work revision and writes no transition; see
  ;; control.lisp and SPEC-WORK.md:3921-3980.
  controls)

(defvar *before-apply-hook* nil
  "A test seam. When bound, it is called with the envelope after the journal has
recorded it and before any of it is applied, so a stop can be injected exactly
at the ordering boundary SPEC-WORK.md:307 names.")

(defstruct (kernel-command (:conc-name cmd-))
  "One mutation enqueued for the kernel's single command thread. RESULTS carries
the answer back to the caller; BEFORE-APPLY-HOOK is the captured dynamic value
of *BEFORE-APPLY-HOOK* at submit time, since special bindings are thread-local."
  request before-apply-hook results error done-p lock cvar)

(defun make-kernel (&key state journal rev-base (friends (quote ())) needs-view)
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
                           ;; The fleet is CONFIG supplied to the session, never a
                           ;; constant in the tool; see src/fleet.lisp.
                           :fleet (make-fleet :friends friends)
                           ;; The model route registry is CONFIG supplied/held by
                           ;; the session beside the fleet; see src/routes.lisp.
                           :routes (make-route-registry)
                           ;; The fleet's ACTIVE half: one authoritative allocator
                           ;; per physical machine and the allocations it holds,
                           ;; never CONFIG; see src/fleet.lisp.
                           :allocations (make-fleet-registry)
                           :controls (make-ctl)
                           :needs-view needs-view
                           :queue '()
                           :q-lock (sb-thread:make-mutex)
                           :q-cvar (sb-thread:make-waitqueue)
                           :thread nil
                           :closed-p nil)))
      (setf (kernel-thread k) (start-command-thread k))
      k)))

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
    (:node-edit :verb :node :by :reason :title-patch :category-patch :links-patch
                :private-patch :version-patch :request :stamp :clock :generation-owner)
    (:undo :verb :of :at-rev :by :request :stamp :clock :generation-owner)
    (:undo-plan :verb :of :by :request :stamp :clock :generation-owner)
    (:redo :verb :of :at-rev :by :request :stamp :clock :generation-owner)
    (:redo-plan :verb :of :by :request :stamp :clock :generation-owner)
    (:external-effect :verb :node :by :effect :handle :owner :state
                      :request :stamp :clock :generation-owner)
    (:node-remove :verb :node :by :reason :request :stamp :clock :generation-owner)
    (:event-cancel :verb :node :by :reason :request :stamp :clock :generation-owner)))

(defparameter *kind-owned-fields* '(:to :blocked-by :evidence :disposition :already-closed)
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
    (:event-reopen "EVENT")))

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
     :kind (ecase verb ((:state-to-done :state-to-doing) :transition) (:event-reopen :reopen))
     :node node :by by
     :fields (ecase verb
               ((:state-to-done :state-to-doing)
                (list :to (if (eq verb :state-to-done) :done :doing)
                      :reason (getf request :reason +absent+)
                      :blocked-by +absent+
                      :evidence (getf request :evidence +absent+)))
               (:event-reopen
                (list :reason (getf request :reason +absent+))))
     :stamp stamp :clock clock :request rid :generation-owner owner
     :rev (kernel-next-rev kernel)
     :session-written-p nil)))

(defun %validate (state verb event &optional view)
  "Answer NIL when the candidate is admissible, or (values RULE REASON).

RULE is a validator rule number, or the keyword :NEEDS for rule 3's dependency
refusal, which carries no rule number on its line (SPEC-WORK.md:4890)."
  (let* ((id (work-event-node event))
         (node (%node-quiet state id)))
    (unless node
      (return-from %validate (values 2 (format nil "no such node ~A" id))))
    (unless (member (wnode-type node) '(:task :bug))
      (error 'unsupported-input
             :what (format nil "unsupported: ~A is a ~A; slice 1 containers change branch with their members, not through direct task-state requests"
                           id (string-downcase (symbol-name (wnode-type node))))))
    (ecase verb
      (:state-to-doing
       ;; SPEC-WORK.md:4869 rule 3 -- needs-met is a precondition of the
       ;; candidate gate and is no validator rule, and it is read BEFORE the
       ;; verb's other preconditions, the transition table's own edge among
       ;; them (:4894). The refusal carries NO rule number (:4890): a rule
       ;; number on the line is what would send the whole walk looking, and the
       ;; whole walk reads nothing of `:deps` but existence and cycle.
       (multiple-value-bind (tail) (needs-gate-refusal state id :view view)
         (when tail (return-from %validate (values :needs tail))))
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
           (return-from %validate (values 10 "unknown to doing requires evidence or a reason"))))
)
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
  "The container kinds whose static containment sets this slice represents. A
roadmap settles with its members like any other container (SPEC-WORK.md:1648)."
  (member (wnode-type node) '(:work-set :feature :roadmap)))

(defun %cascade-events (state verb requester session)
  "Build and validate the branch cascade on a private candidate. Each decision
reads a direct required-member counter, then walks at most one ancestor edge."
  (let ((candidate (copy-state state))
        (events (list requester session))
        (cascade '())
        (child-id (work-event-node requester))
        (next-rev (1+ (work-event-rev session))))
    (apply-event candidate requester)
    (apply-event candidate session)
    (loop
      (let* ((child (%node-quiet candidate child-id))
             (parent-id (and child (wnode-parent child)))
             (parent (and parent-id (%node-quiet candidate parent-id))))
        (unless (and parent (%static-container-p parent)) (return))
        (unless (ecase verb
                  (:state-to-done
                   (and (eq :o (wnode-branch parent))
                        (plusp (wnode-required-count parent))
                        (zerop (wnode-required-open parent))
                        ;; SPEC-WORK.md:1951 -- a parent is green only when
                        ;; every required child and every dependency gate is
                        ;; satisfied. An unmet need holds the container open.
                        (%needs-settled-p candidate parent-id)))
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

(defun %ok-line (word requester session)
  "Derived from the envelope's own events, so the line exists before anything is
applied and the journal can be appended first."
  (format nil "~A OK id=~A request=~A node=~A rev=~D pushed=-"
          word (event-id requester) (work-event-request requester)
          (work-event-node requester) (work-event-rev session)))

(defun %dispatch (kernel request)
  "Run one mutation request synchronously and answer (values OK-P LINE EXIT-CODE
ENVELOPE). This is the per-command body of the single command thread; it is
never an independent mutation path (SPEC-WORK.md rule 6: a mutation outside the
command loop is a defect)."
  (handler-case (%submit kernel request)
    (unsupported-input (c)
      (values nil (format nil "~A FAIL node=~A: ~A"
                          (handler-case (%word (getf request :verb))
                            (error () "MUTATION"))
                          (or (getf request :node) "-")
                          (unsupported-input-what c))
              2 nil))))

(defun %submit (kernel request)
  (case (getf request :verb)
    (:node-edit (return-from %submit (%submit-edit kernel request)))
    (:undo (return-from %submit (%submit-undo kernel request)))
    (:undo-plan (return-from %submit (%submit-undo-plan kernel request)))
    (:redo (return-from %submit (%submit-redo kernel request)))
    (:redo-plan (return-from %submit (%submit-redo-plan kernel request)))
    (:external-effect (return-from %submit (%submit-external kernel request)))
    (:node-remove (return-from %submit (%submit-terminal kernel request :node-remove :removed)))
    (:event-cancel (return-from %submit (%submit-terminal kernel request :event-cancel :cancelled))))
  (let ((verb (getf request :verb)))
    ;; The one verb that configures the fleet (SPEC-WORK.md:3541) is CONFIG,
    ;; not a work-tree transition: it shares `submit`'s answer shape but never
    ;; touches the root, its counters or its history.
    (when (eq verb :machine)
      (return-from %submit (machine-submit kernel request)))
    ;; The one verb that configures the model routes (SPEC-WORK.md:2289, *Model
    ;; routes*) is CONFIG too: it writes a `:kind :route` member beside the
    ;; fleet and never touches the root, its counters or its history.
    (when (eq verb :route)
      (return-from %submit (route-submit kernel request)))
    ;; The fleet's ACTIVE half (SPEC-WORK.md:3592-3731): `take`, `heartbeat`,
    ;; `release` and `probe` are verbs over the one allocator per machine. They
    ;; write ACTIVE allocation records and observations, never CONFIG members
    ;; and never the work tree.
    (when (eq verb :take)
      (return-from %submit (fleet-take-submit kernel request)))
    (when (eq verb :heartbeat)
      (return-from %submit (fleet-heartbeat-submit kernel request)))
    (when (eq verb :release)
      (return-from %submit (fleet-release-submit kernel request)))
    (when (eq verb :probe)
      (return-from %submit (fleet-probe-submit kernel request)))
    (unless (member verb '(:state-to-done :state-to-doing :event-reopen))
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
      (multiple-value-bind (rule reason)
          (%validate (kernel-state kernel) verb requester (kernel-needs-view kernel))
        (when rule
          (return-from %submit
            (values nil (if (integerp rule)
                            (format nil "~A FAIL node=~A: rule ~D: ~A"
                                    word (work-event-node requester) rule reason)
                            ;; rule 3's dependency refusal: the verb's own FAIL
                            ;; line with one tail and no rule number (:4901).
                            (format nil "~A FAIL node=~A: ~A"
                                    word (work-event-node requester) reason))
                    1 nil))))
      (let* ((before-state (node-state (kernel-state kernel) (work-event-node requester)))
             (session (unless (eq verb :state-to-doing)
                        (%session-event kernel verb requester)))
             ;; Doing stays inside O: one event, no branch change or cascade.
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
        (let ((line (%ok-line word requester last-event)))
          (journal-record (kernel-journal kernel) rid digest line
                          (work-event-rev last-event))
          (when *before-apply-hook* (funcall *before-apply-hook* envelope))
          ;; All-or-none: the candidate is built whole, then installed.
          (let ((candidate (apply-envelope (kernel-state kernel) envelope)))
            (setf (kernel-state kernel) candidate)
            (setf (kernel-next-rev kernel) (1+ (work-event-rev last-event)))
            (setf (gethash rid (kernel-applied kernel))
                  (list :verb verb :node (work-event-node requester)
                        :before-state before-state :request request))
            (values t line 0 envelope)))))))

;;; The counters, read.

;;; `node remove` (SPEC-WORK.md:2372-2396)

(defun %open-subtree (state id)
  "The subtree of ID split into its still-open ids and its already-closed ids,
parents before children."
  (let ((open '()) (closed '()))
    (labels ((walk (x)
               (if (eq :o (wnode-branch (%node-quiet state x)))
                   (push x open)
                   (push x closed))
               (dolist (c (wnode-children (%node-quiet state x)))
                 (walk c))))
      (walk id))
    (values (nreverse open) (nreverse closed))))

(defun node-remove (kernel id &key (by "rowan") (reason "removed")
                              (request (format nil "remove-~A" id))
                              (stamp "2026-09-14T12:00:00Z")
                              (generation-owner "gen-4"))
  "A node and the still-open items of its subtree settle into C with disposition
removed, one :settle each in one envelope; the node's own settle names the
already-closed ids beneath it; a finished item's disposition, evidence and
settle stamp are untouched. A node already in C is a no-effect NODE NOTE
(SPEC-WORK.md:2372-2396, replay remove-settles-only-open-items)."
  (let* ((state (kernel-state kernel))
         (node (%node-quiet state id)))
    (unless node
      (error 'unsupported-input :what (format nil "no such node ~A" id)))
    (if (eq :c (wnode-branch node))
        (let ((row (find id (wstate-rows state) :key (lambda (r) (getf r :node))
                         :test #'equal)))
          (values t (format nil "NODE NOTE already-closed node=~A disposition=~A settled=~A"
                            id (or (getf row :disposition) :done) (or (getf row :stamp) "-"))
                  0))
        (multiple-value-bind (open-ids closed-ids) (%open-subtree state id)
          (let* ((rev (kernel-next-rev kernel))
                 (last-rev rev)
                 (events '()))
            (dolist (item (reverse open-ids))
              (push (make-work-event
                     :kind :settle :node item :by by
                     :fields (list :disposition :removed :reason reason
                                   :already-closed (if (equal item id) closed-ids '()))
                     :stamp stamp :clock :tool :request request
                     :generation-owner generation-owner :rev rev
                     :session-written-p t)
                    events)
              (setf last-rev rev)
              (incf rev))
            (setf events (nreverse events))
            (let ((envelope (list :request request
                                  :digest (payload-digest events)
                                  :events events)))
              (setf (kernel-state kernel) (apply-envelope state envelope))
              (setf (kernel-next-rev kernel) (1+ last-rev))
              (values t
                      (format nil "NODE OK id=~A request=~A node=~A rev=~D pushed=- changed=~D"
                              (event-id (car (last events))) request id last-rev
                              (length events))
                      0)))))))

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


;;; ------------------------------------------------------------------
;;; folded from replays-8661.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8661.lisp --- the pure part of the five acceptance replays of
;;;; docs/SPEC-WORK.md named on card 8661: the coordination measure
;;;; `cost-per-accepted-decision` (:6054, :6080), the single-writer kernel
;;;; `single-writer-kernel-total-order` (:2621, :6083, :6480), the grammar
;;;; `every-field-has-an-owning-verb` (:2934, :2960, :5686), the manager
;;;; profile status line `prompt-profile-expired-shows-on-the-status-line`
;;;; (:3366, :6197) and the fleet member
;;;; `machine-is-config-and-never-a-work-tree-node` (:3620, :6092).
;;;;
;;;; Nothing here starts a session, a client, a thread or a profile: these are
;;;; the pure functions the five `replays-8661` acceptance cases drive. The
;;;; live session, transport and CLI wiring those paragraphs sit on is out of
;;;; this slice and is named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; cost-per-accepted-decision (SPEC-WORK.md:6054, :6080, :6477)
;;; ------------------------------------------------------------------

(defun coordination-measure (decisions &key (latency-bound 60))
  "The coordination measure is cost per accepted decision across tiers. A
decision is accepted only when it is neither wrong nor missed and its recovery
latency is observed at or under LATENCY-BOUND; a cheap decision that failed a
gate is not accepted and its cost is not counted. Answers the accepted cost, the
accepted count, the per-decision ratio (or :UNKNOWN when none is accepted) and
each gate's tally (SPEC-WORK.md:6054)."
  (let ((accepted 0) (cost 0) (wrong 0) (missed 0) (slow 0))
    (dolist (d decisions)
      (let ((lat (getf d :recovery-latency)))
        (cond
          ((getf d :wrong) (incf wrong))
          ((getf d :missed) (incf missed))
          ((or (not (numberp lat)) (> lat latency-bound)) (incf slow))
          (t (incf accepted)
             (incf cost (if (numberp (getf d :cost)) (getf d :cost) 0))))))
    (list :measure "cost-per-accepted-decision"
          :total (length decisions)
          :accepted accepted
          :accepted-cost cost
          :cost-per-accepted (if (plusp accepted) (/ cost accepted) :unknown)
          :wrong wrong :missed missed :slow slow)))

;;; ------------------------------------------------------------------
;;; single-writer-kernel-total-order (SPEC-WORK.md:2621, :6083, :6480)
;;; ------------------------------------------------------------------

(defun kernel-command-loop (submissions)
  "Apply every submission in one total order, numbering it with the sequence
number the journal records. A submission not made through the loop is not
applied: it is a defect by the validator rule (SPEC-WORK.md:2621). Answers the
journal, in order, and the defects."
  (let ((seq 0) (journal '()) (defects '()))
    (dolist (s submissions)
      (if (getf s :outside-loop)
          (push s defects)
          (progn
            (incf seq)
            (push (list :seq seq
                        :request (getf s :request)
                        :client (getf s :client)
                        :line (format nil "~A OK request=~A seq=~D"
                                      (getf s :verb "STATE")
                                      (getf s :request) seq))
                  journal))))
    (list :journal (nreverse journal) :defects (nreverse defects))))

(defun kernel-defect-line (submission)
  "The validator's refusal of a mutation outside the command loop."
  (values nil
          (format nil "KERNEL FAIL request=~A: mutation outside the command loop"
                  (getf submission :request))
          2))

(defun journal-seq-numbers (journal)
  (mapcar (lambda (e) (getf e :seq)) journal))

;;; ------------------------------------------------------------------
;;; every-field-has-an-owning-verb (SPEC-WORK.md:2934, :2960, :5686)
;;; ------------------------------------------------------------------

(defparameter *mutation-grammar*
  '((:state-to-done :kind :transition
     :fields (:to :reason :blocked-by :evidence) :subject :node)
    (:state-to-doing :kind :transition
     :fields (:to :reason :blocked-by :evidence) :subject :node)
    (:event-reopen :kind :reopen :fields (:reason) :subject :node))
  "Each mutation verb, its event kind, its ordered field list and its subject
(SPEC-WORK.md:2960).")

(defparameter *field-owning-verbs*
  '((:to :state-to-done :state-to-doing)
    (:reason :state-to-done :state-to-doing :event-reopen)
    (:blocked-by :state-to-done :state-to-doing)
    (:evidence :state-to-done :state-to-doing)
    (:disposition :state-to-done)
    (:already-closed :state-to-done))
  "Every canonical field and the mutation verb(s) that own it. A field no verb
owns is unreachable by any recorded act (SPEC-WORK.md:2934).")

(defun mutation-verbs ()
  (mapcar #'car *mutation-grammar*))

(defun verb-event-kind (verb)
  (getf (cdr (assoc verb *mutation-grammar*)) :kind))

(defun verb-ordered-fields (verb)
  (getf (cdr (assoc verb *mutation-grammar*)) :fields))

(defun verb-subject (verb)
  (getf (cdr (assoc verb *mutation-grammar*)) :subject))

(defun owning-verbs (field)
  (rest (assoc field *field-owning-verbs*)))

(defun every-field-has-an-owning-verb-p ()
  "Reads both ways: every field to its verb, and every mutation verb to its
event kind, its ordered field list and its subject (SPEC-WORK.md:2960)."
  (and
   ;; every field has at least one owning verb, and every owning verb is real
   (every (lambda (row)
            (and (rest row)
                 (every (lambda (v) (member v (mutation-verbs))) (rest row))))
          *field-owning-verbs*)
   ;; every mutation verb has a kind, an ordered field list and a subject
   (every (lambda (v)
            (and (verb-event-kind v) (verb-ordered-fields v) (verb-subject v)))
          (mutation-verbs))
   ;; the reverse: every field a verb lists resolves back to that verb
   (every (lambda (v)
            (every (lambda (f) (member v (owning-verbs f)))
                   (verb-ordered-fields v)))
          (mutation-verbs))))

;;; ------------------------------------------------------------------
;;; prompt-profile-expired-shows-on-the-status-line (SPEC-WORK.md:3366)
;;; ------------------------------------------------------------------

(defstruct (prompt-profile
             (:constructor make-prompt-profile
                 (&key name pointer digest policy-version evidence expiry owner)))
  name       ; display name, selected at start and never swapped mid-session
  pointer    ; a file path in the repository, never inline
  digest     ; the SHA-256 of the prompt content the path resolved to
  policy-version
  evidence   ; a dated measurement, or :UNKNOWN where none has been taken
  expiry     ; the date after which the profile is stale
  owner)

(defun prompt-profile-state (profile &key content-digest today)
  "The status line's `profile-state=`: absent when no profile is named, mismatch
when the content at the pointer does not hash to the pinned digest, stale when
the expiry has passed, unknown when no evidence measurement was ever taken, and
current otherwise (SPEC-WORK.md:3366)."
  (cond
    ((null profile) :absent)
    ((and content-digest (prompt-profile-digest profile)
          (not (string= content-digest (prompt-profile-digest profile)))) :mismatch)
    ((and (prompt-profile-expiry profile) today
          (string< (prompt-profile-expiry profile) today)) :stale)
    ((or (null (prompt-profile-evidence profile))
         (eq :unknown (prompt-profile-evidence profile))) :unknown)
    (t :current)))

(defun prompt-profile-status-line (profile &key content-digest today)
  "The status line names the profile and its state; a stale profile carries the
expired date, never a silently swapped prompt (SPEC-WORK.md:3366)."
  (let ((state (prompt-profile-state profile
                                     :content-digest content-digest :today today)))
    (format nil "SESSION OK profile=~A profile-state=~A~@[ expired=~A~]"
            (if profile (prompt-profile-name profile) "-")
            (string-downcase (symbol-name state))
            (and (eq state :stale) (prompt-profile-expiry profile)))))

(defun prompt-profile-invocation (profile content-digest &key session)
  "Before a manager session is invoked the content at the pointer is hashed and
compared with the pinned digest: a mismatch refuses at exit 1 and never runs on
unknown bytes (SPEC-WORK.md:3366)."
  (if (and profile (prompt-profile-digest profile)
           (not (string= content-digest (prompt-profile-digest profile))))
      (values nil
              (format nil "SESSION FAIL session=~A profile=~A: digest mismatch"
                      (or session "-") (prompt-profile-name profile))
              1)
      (values t "SESSION OK" 0)))

