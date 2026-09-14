;;;; kernel.lisp --- the C/O transition kernel.
;;;;
;;;; Two transitions of docs/SPEC-WORK.md are supported here and no others:
;;;;
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
  state journal next-rev)

(defun make-kernel (&key state journal (rev-base 1))
  (%make-kernel :state (or state (make-seed-state '()))
                :journal (or journal (make-ordering-journal))
                :next-rev rev-base))

;;; The transition table, for the supported subset only (SPEC-WORK.md:994-1005).

(defparameter *done-edges* '(:doing :review)
  "from :doing to ... :done; from :review to ... :done. No other state reaches
:done, so a :todo item closing is rule 10 and not a silent success.")

(defun %word (verb)
  (ecase verb
    (:state-to-done "STATE")
    (:event-reopen "EVENT")))

(defun %require (request key)
  (let ((value (getf request key)))
    (when (or (null value) (and (stringp value) (string= value "")))
      (error 'unsupported-input
             :what (format nil "unsupported: ~A refusing to guess"
                           (string-downcase (symbol-name key)))))
    value))

(defun %requester-event (kernel request verb)
  (let ((node (%require request :node))
        (by (%require request :by))
        (rid (%require request :request))
        (stamp (%require request :stamp))
        (clock (%require request :clock))
        (owner (%require request :generation-owner)))
    (make-work-event
     :kind (ecase verb (:state-to-done :transition) (:event-reopen :reopen))
     :node node :by by
     :fields (ecase verb
               (:state-to-done
                (list :to :done
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
    (unless (eq :task (wnode-type node))
      (error 'unsupported-input
             :what (format nil "unsupported: ~A is a ~A; a container settles with its members and that cascade is not in slice 1"
                           id (string-downcase (symbol-name (wnode-type node))))))
    (ecase verb
      (:state-to-done
       (unless (eq :o (wnode-branch node))
         (return-from %validate (values 10 (format nil "~A is in C" id))))
       (unless (member (wnode-state node) *done-edges*)
         (return-from %validate
           (values 10 (format nil "no edge from ~A to done"
                              (string-downcase (symbol-name (wnode-state node)))))))
       (let ((evidence (getf (work-event-fields event) :evidence)))
         (when (or (absentp evidence) (null evidence))
           (return-from %validate (values 5 "done without evidence")))))
      (:event-reopen
       (unless (eq :c (wnode-branch node))
         (return-from %validate
           (values 10 (format nil "~A is in O and never left it" id))))
       (unless (eq :done (wnode-state node))
         (return-from %validate
           (values 10 (format nil "no edge from ~A to todo"
                              (string-downcase (symbol-name (wnode-state node)))))))))
    nil))

(defun %session-event (kernel verb requester)
  "The session's own half of the envelope: a :settle beside a close, a :revive
beside a reopen. Outside the payload digest (SPEC-WORK.md:894)."
  (make-work-event
   :kind (ecase verb (:state-to-done :settle) (:event-reopen :revive))
   :node (work-event-node requester)
   :by (work-event-by requester)
   :fields (ecase verb
             (:state-to-done (list :disposition :done
                                   :reason (getf (work-event-fields requester) :reason +absent+)
                                   :already-closed '()))
             (:event-reopen (list :reason (getf (work-event-fields requester) :reason +absent+))))
   :stamp (work-event-stamp requester)
   :clock (work-event-clock requester)
   :request (work-event-request requester)
   :generation-owner (work-event-generation-owner requester)
   :rev (1+ (work-event-rev requester))
   :session-written-p t))

(defun %ok-line (word requester state)
  (format nil "~A OK id=~A request=~A node=~A rev=~D pushed=-"
          word (event-id requester) (work-event-request requester)
          (work-event-node requester) (wstate-revision state)))

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
    (unless (member verb '(:state-to-done :event-reopen))
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
      (let* ((session (%session-event kernel verb requester))
             (envelope (list :request rid :digest digest
                             :events (list requester session)
                             :settle session)))
        ;; Acceptance is asked before anything is applied.
        (multiple-value-bind (accepted refusal) (journal-accept (kernel-journal kernel) envelope)
          (unless accepted
            (return-from %submit
              (values nil (format nil "~A FAIL request=~A: journal refused acceptance: ~A"
                                  word rid refusal)
                      1 nil))))
        ;; All-or-none: the candidate is built whole, then installed.
        (let ((candidate (apply-envelope (kernel-state kernel) envelope)))
          (setf (kernel-state kernel) candidate)
          (setf (kernel-next-rev kernel) (+ 2 (work-event-rev requester)))
          (let ((line (%ok-line word requester candidate)))
            (journal-record (kernel-journal kernel) rid digest line
                            (wstate-revision candidate))
            (values t line 0 envelope)))))))

;;; The counters, read.

(defun ask-size (kernel)
  "`query --ask size` (SPEC-WORK.md:1575). It reads the counter and triggers no
rollup, no scan, no parse and no replay: nothing below touches a node."
  (let* ((state (kernel-state kernel))
         (open (wstate-root-open state))
         (scope (wstate-revision state))
         (unit "items"))
    (values open unit scope
            (format nil "QUERY OK ask=size scope=~D unit=~A open=~D parses=0 replays=0"
                    scope unit open))))

(defun open-issue-count (kernel)
  "The open linked-issue counter. Separate, and never labelled |O|."
  (wstate-issue-open (kernel-state kernel)))

(defun open-leaf-count (kernel)
  "The open leaf-task counter. Separate, and never labelled |O|."
  (wstate-leaf-open (kernel-state kernel)))
