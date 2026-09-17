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
  state journal next-rev fleet)

(defvar *before-apply-hook* nil
  "A test seam. When bound, it is called with the envelope after the journal has
recorded it and before any of it is applied, so a stop can be injected exactly
at the ordering boundary SPEC-WORK.md:307 names.")

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
    (%make-kernel :state state
                  :journal (or journal (make-ordering-journal))
                  :next-rev (or rev-base (1+ (state-revision state)))
                  ;; The fleet is CONFIG supplied to the session, never a
                  ;; constant in the tool; see src/fleet.lisp.
                  :fleet (make-fleet :friends friends))))

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

(defun %ok-line (word requester session)
  "Derived from the envelope's own events, so the line exists before anything is
applied and the journal can be appended first."
  (format nil "~A OK id=~A request=~A node=~A rev=~D pushed=-"
          word (event-id requester) (work-event-request requester)
          (work-event-node requester) (work-event-rev session)))

(defun submit (kernel request)
  "Answer (values OK-P LINE EXIT-CODE ENVELOPE). Nothing is applied unless the
whole envelope is applied."
  (handler-case (%submit kernel request)
    (unsupported-input (c)
      (values nil (format nil "~A FAIL node=~A: ~A"
                          (handler-case (%word (getf request :verb))
                            (error () "MUTATION"))
                          (or (getf request :node) "-")
                          (unsupported-input-what c))
              2 nil))))

(defun %submit (kernel request)
  (let ((verb (getf request :verb)))
    ;; The one verb that configures the fleet (SPEC-WORK.md:3541) is CONFIG,
    ;; not a work-tree transition: it shares `submit`'s answer shape but never
    ;; touches the root, its counters or its history.
    (when (eq verb :machine)
      (return-from %submit (machine-submit kernel request)))
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
      (let* ((session (unless (eq verb :state-to-doing)
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
            (values t line 0 envelope)))))))

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
