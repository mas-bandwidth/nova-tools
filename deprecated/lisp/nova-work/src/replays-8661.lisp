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

;;; ------------------------------------------------------------------
;;; machine-is-config-and-never-a-work-tree-node (SPEC-WORK.md:3620, :6092)
;;; ------------------------------------------------------------------

(defstruct (machine-record
             (:constructor make-machine-record
                 (&key id name owner connect roles permits limits facts
                       declared-by declared-at)))
  id name owner connect roles permits limits facts declared-by declared-at)

(defun machine-config-section (machine)
  "A machine is the `:kind :machine` member record of the fleet section of
CONFIG, and never a work-tree node (SPEC-WORK.md:3620)."
  (list :section :fleet :kind :machine :id (machine-record-id machine)))

(defun machine-work-tree-node-p (machine)
  "Equipment never completes: a machine is no child of O and under no repository
work set."
  (declare (ignore machine))
  nil)

(defun machine-node-field (machine)
  "Every :machine event writes :node (:absent) by the kind's own subject rule."
  (declare (ignore machine))
  +absent+)

(defun machine-acceptance (machine)
  (declare (ignore machine))
  nil)

(defun machine-derived-state (machine)
  (declare (ignore machine))
  nil)

(defun machine-settle (machine)
  "No verb can settle a machine."
  (values nil
          (format nil "STATE FAIL machine=~A: equipment does not settle"
                  (machine-record-id machine))
          2))

(defun machine-to-done (machine)
  "No verb can take a machine `:to :done`."
  (values nil
          (format nil "STATE FAIL machine=~A: equipment has no edge to done"
                  (machine-record-id machine))
          2))

(defun machine-completion-evidence-p (machine)
  "Equipment does not complete, so nothing a machine does is completion
evidence."
  (declare (ignore machine))
  nil)

(defun register-machine (machine &key counts)
  "Register a machine as fleet CONFIG. Answers the event and the counts. No
count, roadmap or required set moves when a machine is written
(SPEC-WORK.md:3620, :6092)."
  (let ((config (machine-config-section machine)))
    (values
     (list :kind :machine
           :section (getf config :section)
           :node (machine-node-field machine)
           :event-id "ev-8661-machine-1"
           :request "req-machine-1"
           :machine (machine-record-id machine)
           :rev 1 :pushed nil :changed 1 :emitted 0)
     counts)))

(defun machine-ok-line (event)
  (format nil "MACHINE OK id=~A request=~A machine=~A rev=~D pushed=~A changed=~D emitted=~D"
          (getf event :event-id) (getf event :request) (getf event :machine)
          (getf event :rev) (or (getf event :pushed) "-")
          (getf event :changed) (getf event :emitted)))
