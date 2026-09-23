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
  ;; The CONFIG/ACTIVE registries are NOT kernel slots: they live on the state
  ;; (src/state.lisp) so a `replay-journal` reconstruction of them is visible
  ;; through the same readers after a restart (nova-tools#1695), and the
  ;; kernel reads them through KERNEL-FLEET, KERNEL-ROUTES and
  ;; KERNEL-ALLOCATIONS below.
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
  controls
  ;; The operator-configured verifiers, one per recipient identity. CONFIG the
  ;; session holds beside the fleet and the routes; a receipt is admitted only
  ;; after one of them vouches (SPEC-WORK.md:3859, src/receipt-admission.lisp).
  verifiers
  ;; The counter that names a CONFIG/ACTIVE request the caller did not name:
  ;; request ids are the caller's (SPEC-WORK.md:315), and an unnamed one is
  ;; still unique per run so two of them never collide as a reuse.
  (config-seq 0))

(defun kernel-fleet (kernel)
  "The fleet section of CONFIG. It lives on the kernel's state, so the half a
`replay-journal` reconstruction rebuilt is the half this reads
(nova-tools#1695)."
  (wstate-fleet (kernel-state kernel)))

(defun kernel-routes (kernel)
  "The model routes of CONFIG, held beside the fleet (SPEC-WORK.md:2289)."
  (wstate-routes (kernel-state kernel)))

(defun kernel-allocations (kernel)
  "The fleet's ACTIVE half: the one allocator per physical machine and the
allocations and observations it holds (SPEC-WORK.md:3592-3731)."
  (wstate-allocations (kernel-state kernel)))

(defvar *before-apply-hook* nil
  "A test seam. When bound, it is called with the envelope after the journal has
recorded it and before any of it is applied, so a stop can be injected exactly
at the ordering boundary SPEC-WORK.md:307 names.")

(defstruct (kernel-command (:conc-name cmd-))
  "One mutation enqueued for the kernel's single command thread. RESULTS carries
the answer back to the caller; BEFORE-APPLY-HOOK is the captured dynamic value
of *BEFORE-APPLY-HOOK* at submit time, since special bindings are thread-local."
  request before-apply-hook results error done-p lock cvar)

(defun make-kernel (&key state journal rev-base (friends '()))
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
    ;; The CONFIG/ACTIVE registries come with the state (src/state.lisp); all
    ;; make-kernel adds is the team's own `friends` list, the one CONFIG the
    ;; session is opened with (SPEC-WORK.md:3459).
    (setf (fleet-friends (wstate-fleet state)) (copy-list friends))
    (let ((k (%make-kernel :state state
                           :journal (or journal (make-ordering-journal))
                           :next-rev (or rev-base (1+ (state-revision state)))
                           :controls (make-ctl)
                           ;; The operator-configured verifiers a receipt needs
                           ;; (SPEC-WORK.md:3859); see receipt-admission.lisp.
                           :verifiers (make-verifier-registry)
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
    (:event-cancel :verb :node :by :reason :request :stamp :clock :generation-owner)
    ;; `dep --add`/`dep --remove` (SPEC-WORK.md:987, :2362): one `:structure`
    ;; event whose `:add` or `:remove` names the edge (nova-tools#1673, #785).
    (:dep :verb :node :by :add :remove :reason
          :request :stamp :clock :generation-owner)
    ;; The two receipt verbs (SPEC-WORK.md:2295-2296). :staged carries the
    ;; immutable stage the readers produced outside the mutation loop; :lease-by
    ;; and :lease-default are the CLI's --by and --default, renamed here because
    ;; :by is this kernel's author field on every request.
    (:acknowledge :verb :node :by :offer :attempt :generation :reply :stage
                  :provenance :provenance-sha256 :staged :expect
                  :staged-payload :payload-sha256 :lease-by :lease-default
                  :observed-model :bench :execution :reason
                  :request :stamp :clock :generation-owner)
    (:decline :verb :node :by :offer :attempt :generation :reply :provenance
              :provenance-sha256 :staged :expect :reason
              :request :stamp :clock :generation-owner)))

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

(defun %validate (state verb event)
  "Answer NIL when the candidate is admissible, or (values rule reason)."
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
       ;; SPEC-WORK.md:1885,2110 -- an unmet dependency gate blocks the
       ;; dependent: a node with a need that is not terminal accepted cannot be
       ;; taken into doing, and the refusal names the blocking node.
       (let ((blocker (%dependency-blocker state id)))
         (when blocker
           (return-from %validate
             (values 10 (format nil "~A needs ~A, which is not settled" id blocker))))))
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
    (:event-cancel (return-from %submit (%submit-terminal kernel request :event-cancel :cancelled)))
    ;; `dep` is a structure verb and a WRITER, not an admission verb
    ;; (SPEC-WORK.md:2362): one journaled `:structure` event edits one `:deps`
    ;; reference edge. See dep-verb.lisp.
    (:dep (return-from %submit (%dep-submit kernel request))))
  (let ((verb (getf request :verb)))
    ;; THE SIX CONFIG/ACTIVE VERBS (nova-tools#1695): the one verb that
    ;; configures the fleet (SPEC-WORK.md:3541), the one verb that configures
    ;; the model routes (SPEC-WORK.md:2289), and the fleet's ACTIVE half,
    ;; `take`, `heartbeat`, `release` and `probe` (SPEC-WORK.md:3592-3731).
    ;; They share `submit`'s answer shape, they are CONFIG and ACTIVE and
    ;; never work -- no root, counter, roadmap or required set moves -- but
    ;; they are MUTATIONS of the one writer all the same, and they go through
    ;; the journal's dedup, acceptance and record like every other: on dev
    ;; they returned here before all three, so nothing they wrote survived a
    ;; restart and a retry was answered by a mutable precondition (`connect
    ;; held by m1`) instead of the recorded disposition SPEC-WORK.md:315
    ;; promises. See %submit-config.
    (when (member verb '(:machine :route :take :heartbeat :release :probe))
      (return-from %submit (%submit-config kernel request)))
    ;; The savepoint capture (SPEC-WORK.md:7161): a READ on the command thread.
    ;; It writes no event, appends nothing and moves no revision; it exists so
    ;; the image and the cut are taken at one revision under the single writer.
    (when (eq verb :savepoint-capture)
      (return-from %submit (savepoint-capture-submit kernel request)))
    ;; The two receipt verbs (SPEC-WORK.md:3857-3868). Their readers already ran
    ;; outside this loop and handed in an immutable stage; the one writer
    ;; revalidates it and admits one envelope. See receipt-admission.lisp.
    (when (member verb '(:acknowledge :decline))
      (return-from %submit (receipt-submit kernel request)))
    ;; `take` and `release` on a NODE: the lease, journaled on the single
    ;; writer (nova-tools #1612 lane, src/take-verb.lisp). These are the work
    ;; tree's own verbs and are not the fleet's `:take`/`:release` above, which
    ;; are over an allocator; the verb keywords are kept apart for that reason.
    (when (eq verb :take-node)
      (return-from %submit (%take-node-submit kernel request)))
    (when (eq verb :release-node)
      (return-from %submit (%release-node-submit kernel request)))
    ;; THE NEW VERBS of draft 26 (SPEC-WORK.md:1014-1058) are mutations and
    ;; belong on this one door like every other. `submit-new-verb` used to be a
    ;; SECOND door that read the state, built a candidate and installed it with
    ;; `(setf (kernel-state kernel) ...)` all on the CALLER's thread, beside a
    ;; writer doing the same -- the read-modify-write rule 6 forbids in as many
    ;; words (SPEC-WORK.md:2603-2616). The body is unchanged; it runs here now.
    ;;
    ;; `:undo` AND `:redo` ARE NOT ROUTED, and this is named rather than
    ;; guessed: the keyword collides. `:undo` on THIS door is the undo machinery
    ;; (`%submit-undo`, fields `:of`, `:reason`); `:undo` on the new-verb door is
    ;; draft 26's CONFIG event (kind `:undo`, fields `:request-of`, `:reason`).
    ;; Two different verbs under one keyword, told apart only by which door the
    ;; caller knocked on. Routing them here makes the undo machinery answer a
    ;; draft-26 request -- measured: `MUTATION FAIL node=-: unknown field:
    ;; request-of`. Which one keeps the keyword is a spec question, so the two
    ;; keep the old direct path and the question is filed.
    (when (and (new-verb-p verb) (not (member verb '(:undo :redo))))
      (return-from %submit (%submit-new-verb-guarded kernel request)))
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
      (multiple-value-bind (rule reason) (%validate (kernel-state kernel) verb requester)
        (when rule
          (return-from %submit
            (values nil (format nil "~A FAIL node=~A: rule ~D: ~A"
                                word (work-event-node requester) rule reason)
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

;;; ------------------------------------------------------------------
;;; The six CONFIG/ACTIVE verbs on the journal (nova-tools#1695)
;;; ------------------------------------------------------------------
;;;
;;; `machine`, `route`, `take`, `heartbeat`, `release` and `probe` used to
;;; answer from six branches that returned before the dedup lookup, before
;;; `journal-accept` and before `journal-record`, so nothing they mutated
;;; reached the journal: nothing survived a restart, and a retry of the same
;;; request id was answered by a mutable precondition -- `connect held by m1`
;;; -- instead of the recorded disposition SPEC-WORK.md:315 promises. They run
;;; through the same three doors as every other mutation now. The dedup is
;;; asked FIRST, on the stable payload the event carries, before any mutable
;;; precondition can be reached; acceptance is asked before the verb runs so a
;;; journal that cannot record refuses before anything mutates; the record is
;;; written once the verb answered OK, before the OK line leaves the writer.
;;; The verbs' own bodies (src/fleet.lisp, src/routes.lisp) validate whole
;;; before their first write, so a refusal mutates nothing and records
;;; nothing. Their events are of kinds of their own field lists
;;; (src/state.lisp), and the replay half that rebuilds the CONFIG and
;;; ACTIVE halves from them lives with `apply-event` there.

(defun %config-verb-word (verb)
  "The leading word each of the six verbs prints, used by the journal-level
refusals the verbs share with the work path's shape."
  (ecase verb
    (:machine "MACHINE")
    (:route "ROUTE")
    ((:take :heartbeat :release) "ALLOC")
    (:probe "PROBE")))

(defun %config-verb-kind (verb)
  "The event kind each of the six verbs writes. :machine and :route are
CONFIG members; :allocation and :probe are the ACTIVE half's records and
observations."
  (ecase verb
    (:machine :machine)
    (:route :route)
    ((:take :heartbeat :release) :allocation)
    (:probe :probe)))

(defun %config-request-field (request field)
  "The request's value for one event field. A boolean given as T is written
:TRUE, the restricted-data boolean's own spelling (src/value.lisp), because T
is not a value the journal's printer can write; every other value passes as
given, and a field the caller did not give is `(:absent)`."
  (let ((value (getf request field +absent+)))
    (if (eq value t) :true value)))

(defun %config-event (kernel request verb)
  "One event of the verb's own kind, built from the request BEFORE anything is
asked of the journal or mutated, so the two-part retry of SPEC-WORK.md:315
reads a stable payload and not a mutable precondition. Its :rev is the state's
current revision, so neither the live path nor a replay moves the work
revision a CONFIG or ACTIVE event was never allowed to move. The request id is
the caller's; an unnamed one is numbered per run so two of them never collide
as a reuse."
  (make-work-event
   :kind (%config-verb-kind verb)
   :node +absent+
   :by (or (getf request :by) "rowan")
   :fields (let ((fields (loop for field in (kind-fields (%config-verb-kind verb))
                               append (list field
                                            (%config-request-field request field)))))
             ;; `take`, `heartbeat` and `release` are three verbs of the one
             ;; :allocation kind: the change each performed is the verb itself,
             ;; which no request carries as a field, so the event says it.
             (when (member verb '(:take :heartbeat :release))
               (setf (getf fields :change) verb))
             fields)
   :stamp (getf request :stamp)
   :clock (getf request :clock)
   :request (or (getf request :request)
                (format nil "~A-~D" (%config-verb-word verb)
                        (incf (kernel-config-seq kernel))))
   :generation-owner (getf request :generation-owner)
   :rev (state-revision (kernel-state kernel))
   :session-written-p nil))

(defun %submit-config (kernel request)
  "One of the six CONFIG/ACTIVE verbs through the journal's dedup, acceptance
and record (nova-tools#1695). Answers the verb's own values on every path, so
the verb's contract with its callers is unchanged."
  (let* ((verb (getf request :verb))
         (word (%config-verb-word verb))
         (event (%config-event kernel request verb))
         (rid (work-event-request event))
         (digest (payload-digest (list event)))
         (journal (kernel-journal kernel)))
    ;; The two-part dedup test (SPEC-WORK.md:315), asked of the journal before
    ;; the verb runs, so no mutable precondition can answer a retry.
    (multiple-value-bind (found recorded-digest recorded-line)
        (journal-lookup journal rid)
      (cond
        ((eq found :unavailable)
         (return-from %submit-config
           (values nil (format nil "~A FAIL request=~A page=~A: dedup unavailable"
                               word rid recorded-digest)
                   1 nil)))
        (found
         (if (string= digest recorded-digest)
             (return-from %submit-config
               (values t recorded-line 0
                       (list :request rid :digest digest :events '() :replayed t)))
             (return-from %submit-config
               (values nil (format nil "~A FAIL request=~A: reused with a different payload"
                                   word rid)
                       1 nil))))))
    ;; Acceptance before anything mutates: a journal that cannot record this
    ;; envelope refuses here, and the verb is never reached.
    (multiple-value-bind (accepted refusal)
        (journal-accept journal
                         (list :request rid :digest digest :events (list event)))
      (unless accepted
        (return-from %submit-config
          (values nil (format nil "~A FAIL request=~A: journal refused acceptance: ~A"
                              word rid refusal)
                  1 nil))))
    ;; The verb runs on a STAGED copy of the three CONFIG/ACTIVE registries
    ;; and the staged state is installed only once the record is durable
    ;; (SPEC-WORK.md:307, the record-then-apply order the work path keeps
    ;; above). The OK line the journal records is the verb's own answer, so
    ;; the record cannot precede the verb; staging keeps the live state
    ;; untouched until it has. A record that fails -- an append or a sync
    ;; error -- or a verb that refuses or signals unwinds to the live state
    ;; exactly as it was: no CONFIG member or ACTIVE allocation is ever live
    ;; without its journaled event (#2880 HOLD).
    (let ((live (kernel-state kernel))
          (committed nil))
      (unwind-protect
           (progn
             (setf (kernel-state kernel) (%stage-config-state live))
             (multiple-value-bind (ok line code verb-event)
                 (ecase verb
                   (:machine (machine-submit kernel request))
                   (:route (route-submit kernel request))
                   (:take (fleet-take-submit kernel request))
                   (:heartbeat (fleet-heartbeat-submit kernel request))
                   (:release (fleet-release-submit kernel request))
                   (:probe (fleet-probe-submit kernel request)))
               (cond
                 (ok
                  ;; Durable first; only then does the staged state become
                  ;; the live one and the OK line leave the writer.
                  (journal-record journal rid digest line (work-event-rev event))
                  (setf committed t)
                  (values t line 0 verb-event))
                 (t
                  ;; Refused: the staged copy is dropped, the acceptance the
                  ;; journal holds is dropped rather than recorded, and the
                  ;; journal stays as it was.
                  (when (typep journal 'file-journal)
                    (setf (journal-pending-envelope journal) nil))
                  (values nil line code nil)))))
        (unless committed
          (setf (kernel-state kernel) live))))))

(defun %stage-copy (object seen)
  "A deep copy of OBJECT for staging a CONFIG/ACTIVE verb: conses, hash tables
and structure instances are copied, and everything else -- strings, numbers,
symbols -- is shared, since no verb mutates one in place. SEEN maps each
already-copied hash table and structure to its copy, so an object reached by
two paths (an allocation record named from two places) stays one object in
the copy."
  (typecase object
    (cons
     (cons (%stage-copy (car object) seen)
           (%stage-copy (cdr object) seen)))
    (hash-table
     (or (gethash object seen)
         (let ((copy (make-hash-table :test (hash-table-test object)
                                      :size (max 1 (hash-table-count object)))))
           (setf (gethash object seen) copy)
           (maphash (lambda (key value)
                      (setf (gethash key copy) (%stage-copy value seen)))
                    object)
           copy)))
    (structure-object
     (or (gethash object seen)
         (let ((copy (copy-structure object)))
           (setf (gethash object seen) copy)
           (dolist (slot (sb-mop:class-slots (class-of object)))
             (let ((name (sb-mop:slot-definition-name slot)))
               (setf (slot-value copy name)
                     (%stage-copy (slot-value object name) seen))))
           copy)))
    (t object)))

(defun %stage-config-state (state)
  "A candidate state for one CONFIG/ACTIVE verb (#2880 HOLD): STATE's own
work tree, carried as it is (no such verb touches it), beside deep copies of
the fleet, the routes and the allocations, which are all the six verbs write.
The verb mutates the copies; %submit-config installs the candidate only after
the verb's record is durable, and otherwise keeps STATE, untouched."
  (let ((staged (copy-wstate state))
        (seen (make-hash-table :test #'eq)))
    (setf (wstate-fleet staged) (%stage-copy (wstate-fleet state) seen)
          (wstate-routes staged) (%stage-copy (wstate-routes state) seen)
          (wstate-allocations staged) (%stage-copy (wstate-allocations state) seen))
    staged))

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
           (unit "leaves"))
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
                 (&key name pointer digest policy-version evidence expiry owner
                       model harness work-type)))
  name       ; display name, selected at start and never swapped mid-session
  pointer    ; a file path in the repository, never inline
  digest     ; the SHA-256 of the prompt content the path resolved to
  policy-version
  evidence   ; a dated measurement, or :UNKNOWN where none has been taken
  expiry     ; the date after which the profile is stale
  owner
  model      ; the manager model identity (e.g. "sonnet", "opus", "sol")
  harness    ; the harness identity (e.g. "claude-cli", "codex-cli")
  work-type) ; the work-type label (e.g. "task", "review")

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

;;; ------------------------------------------------------------------
;;; prompt-profile unique-triple invariant and versioned edit grammar
;;; (SPEC-WORK.md:3349-3376)
;;; ------------------------------------------------------------------

(defstruct (profile-registry
             (:constructor make-profile-registry (&key (profiles nil) (journal nil))))
  "A registry of prompt profiles keyed by name. No two profiles hold one
model, harness and work-type triple (SPEC-WORK.md:3350-3353). The journal
records every edit as a versioned event (SPEC-WORK.md:3372-3376)."
  profiles
  journal)

(defstruct (profile-edit-record
             (:constructor make-profile-edit-record
                 (&key profile-name version fields by stamp)))
  "One versioned record of a profile edit. The edit grammar is one new versioned
record with :by, naming the profile and stating the fields it changes, never
an in-place rewrite (SPEC-WORK.md:3372-3376)."
  profile-name
  version
  fields
  by
  stamp)

(defun profile-triple (profile)
  "The (model, harness, work-type) identity triple of a profile."
  (list (prompt-profile-model profile)
        (prompt-profile-harness profile)
        (prompt-profile-work-type profile)))

(defun profile-registry-find-triple (registry triple)
  "Find a profile in REGISTRY whose triple matches TRIPLE, or NIL."
  (find-if (lambda (p) (equal (profile-triple p) triple))
           (profile-registry-profiles registry)))

(defun profile-registry-find (registry name)
  "Find a profile in REGISTRY by NAME, or NIL."
  (find name (profile-registry-profiles registry)
        :key #'prompt-profile-name :test #'string=))

(defun register-profile (registry profile &key (by "coordinator") (stamp ""))
  "Register PROFILE in REGISTRY. Refuses if another profile already holds the
same (model, harness, work-type) triple (SPEC-WORK.md:3350-3353)."
  (let* ((triple (profile-triple profile))
         (existing (profile-registry-find-triple registry triple)))
    (if existing
        (values nil
                (format nil "PROFILE FAIL name=~A: triple ~S already held by ~A"
                        (prompt-profile-name profile) triple
                        (prompt-profile-name existing)))
        (progn
          (push profile (profile-registry-profiles registry))
          (values t
                  (format nil "PROFILE OK name=~A triple=~S by=~A"
                          (prompt-profile-name profile) triple by))))))

(defun profile-edit (registry profile-name &key pointer digest policy-version
                      evidence expiry owner by stamp)
  "Edit a profile in REGISTRY by creating one new versioned record with :by,
stating the fields it changes. Never an in-place rewrite (SPEC-WORK.md:3372-3376)."
  (let* ((profile (profile-registry-find registry profile-name)))
    (unless profile
      (return-from profile-edit
        (values nil
                (format nil "PROFILE EDIT FAIL name=~A: no such profile"
                        profile-name)
                nil)))
    (let* ((prior-edits (count-if (lambda (r)
                                    (string= (profile-edit-record-profile-name r)
                                             profile-name))
                                  (profile-registry-journal registry)))
           (version (1+ prior-edits))
           (changed-fields '()))
      (when pointer
        (push :pointer changed-fields) (push pointer changed-fields))
      (when digest
        (push :digest changed-fields) (push digest changed-fields))
      (when policy-version
        (push :policy-version changed-fields) (push policy-version changed-fields))
      (when evidence
        (push :evidence changed-fields) (push evidence changed-fields))
      (when expiry
        (push :expiry changed-fields) (push expiry changed-fields))
      (when owner
        (push :owner changed-fields) (push owner changed-fields))
      (when (null changed-fields)
        (return-from profile-edit
          (values nil
                  (format nil "PROFILE EDIT FAIL name=~A: no fields to change"
                          profile-name)
                  nil)))
      (let ((updated (make-prompt-profile
                      :name (prompt-profile-name profile)
                      :pointer (or pointer (prompt-profile-pointer profile))
                      :digest (or digest (prompt-profile-digest profile))
                      :policy-version (or policy-version
                                          (prompt-profile-policy-version profile))
                      :evidence (or evidence (prompt-profile-evidence profile))
                      :expiry (or expiry (prompt-profile-expiry profile))
                      :owner (or owner (prompt-profile-owner profile))
                      :model (prompt-profile-model profile)
                      :harness (prompt-profile-harness profile)
                      :work-type (prompt-profile-work-type profile))))
        (setf (profile-registry-profiles registry)
              (cons updated (remove-if (lambda (p)
                                         (string= (prompt-profile-name p)
                                                  profile-name))
                                       (profile-registry-profiles registry))))
        (let ((record (make-profile-edit-record
                       :profile-name profile-name
                       :version version
                       :fields changed-fields
                       :by (or by "coordinator")
                       :stamp (or stamp ""))))
          (push record (profile-registry-journal registry))
          (values t record updated))))))

(defun profile-edit-journal (registry &key profile-name)
  "Return the edit journal, optionally filtered by PROFILE-NAME."
  (if profile-name
      (remove-if-not (lambda (r)
                       (string= (profile-edit-record-profile-name r)
                                profile-name))
                     (profile-registry-journal registry))
      (profile-registry-journal registry)))

