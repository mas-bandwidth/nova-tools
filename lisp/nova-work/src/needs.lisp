;;;; needs.lisp --- the needs-met predicate (nova-tools #785).
;;;;
;;;; docs/SPEC-WORK.md:4780-4867, rules 1 and 2 of *The dependency gate and the
;;;; hand report*. One function answers whether a need is met, and one reason
;;;; token says why when it is not. Rule 2's vocabulary is the whole of it:
;;;; NEED-OPEN, NEED-REVERTED, NEED-UNAVAILABLE, NEED-CLOSED-UNACCEPTED,
;;;; NEED-UNVERIFIED. Nothing here is wired to a verb: rule 3's gate at the
;;;; admission verbs is the next slice.
;;;;
;;;; Rule 1: a need is *terminal accepted* when it is in C with disposition
;;;; `done` AND every evidence event its standing `:to :done` names qualifies
;;;; its criterion by a raw fact the session's verification cache already
;;;; holds. Recorded is not verified (:4785). The gate never fetches (:4789).
;;;; Stale does not unmeet a need (:4760): the `:against`-versus-source
;;;; comparison that makes an event stale is the one comparison left out, so a
;;;; `source` bump re-blocks no dependent.
;;;;
;;;; THE READ-TIME SEAM. `wstate` carries no evidence store — a `:to :done`
;;;; holds its evidence as opaque event ids (src/kernel.lisp:217) — and no
;;;; verification cache: that lives in a VERIFICATION-SESSION (src/verifier.lisp)
;;;; the tree does not hold. So rule 1's verified half reaches the gate through
;;;; a NEEDS-VIEW the session hands it: the evidence records a node's standing
;;;; done names, the verification session whose cache already holds the raw
;;;; facts, each node's current generation, and each node's `responsible`.
;;;; The IDS come from the tree and the PROOF comes from the view: rule 1 is a
;;;; correspondence, id by id, between the evidence events the standing `:to
;;;; :done` names and the records the session resolved and verified. A view that
;;;; is missing, empty, partial, or holding a verified record the standing done
;;;; never named proves nothing, and the need reads `need-unverified`.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; the reason vocabulary                        SPEC-WORK.md:4820
;;; ------------------------------------------------------------------

(defparameter *needs-reasons*
  '(:need-open :need-reverted :need-unavailable :need-closed-unaccepted
    :need-unverified)
  "The five tokens of rule 2's table and the whole vocabulary. A row prints one
of these whenever `unmet=` is above zero, and nothing else ever.")

(defun needs-reason-token (reason)
  "The printed spelling of a reason, or `-` where a node is needs-met."
  (if (null reason)
      "-"
      (progn
        (unless (member reason *needs-reasons*)
          (error 'unsupported-input
                 :what (format nil "~S is not one of the five need reasons" reason)))
        (string-downcase (symbol-name reason)))))

;;; ------------------------------------------------------------------
;;; the needs view                               SPEC-WORK.md:4780-4790
;;; ------------------------------------------------------------------

(defstruct (needs-view
            (:constructor make-needs-view
                (&key session evidence generations responsible engaged)))
  "The read-time facts rule 1 needs and the tree does not hold.

SESSION is a VERIFICATION-SESSION: its cache holds the raw resolutions `verify`
established, and its resolver list names the identities a fact may be
attributed to. The gate reads that cache and runs no resolver.

EVIDENCE is ((node-id record ...) ...): the VERIFY-EVIDENCE records the node's
standing `:to :done` names. GENERATIONS is ((node-id . generation) ...): a
`correct` bumps a node's generation, and evidence of an older generation
qualifies nothing (:4835). RESPONSIBLE is ((node-id . name) ...), read for the
resolver column of rule 2's table. ENGAGED is the list of ids a session knows
to be engaged by something the tree does not hold -- a pending or accepted
offer, a live allocation, an `:attempt` of the current generation, a report of
a launch with no stop after it - read by rule 5 beside the lease and the state
the tree does hold (SPEC-WORK.md:4765)."
  session evidence generations responsible engaged)

(defun needs-view-node-evidence (view id)
  "The evidence records VIEW names for ID, or NIL where it names none."
  (and view (cdr (assoc id (needs-view-evidence view) :test #'equal))))

(defun needs-view-node-generation (view id)
  (and view (cdr (assoc id (needs-view-generations view) :test #'equal))))

(defun needs-view-node-responsible (view id)
  (and view (cdr (assoc id (needs-view-responsible view) :test #'equal))))

;;; ------------------------------------------------------------------
;;; rule 1's verified half                       SPEC-WORK.md:4780-4792
;;; ------------------------------------------------------------------

(defparameter *need-default-generation* 1
  "The generation a node is at before any `correct`. A view naming no generation
for a node, and an evidence record carrying none, are both read as this, so a
MISMATCH always refuses rather than a missing field silently passing.")

(defun %need-current-generation (view id)
  (or (needs-view-node-generation view id) *need-default-generation*))

(defun %need-evidence-qualifies-p (view id evidence)
  "Whether one evidence event of ID qualifies its criterion by a raw fact the
session's verification cache already holds. No fetch, and no staleness: the
`:against`-versus-source comparison is the one left out (:4760)."
  (let ((generation (%need-current-generation view id))
        (evidence-generation (or (verify-evidence-generation evidence)
                                 *need-default-generation*))
        (session (and view (needs-view-session view))))
    (cond
      ;; a `correct` bumped the node's generation: evidence of the older one is
      ;; of the older generation and qualifies nothing (:4835).
      ((not (eql generation evidence-generation)) nil)
      ;; no session means no cache, and a fact no cache holds is not verified.
      ((null session) nil)
      (t
       (let* ((pointer (verify-evidence-pointer evidence))
              (subject (verify-evidence-subject evidence))
              (scheme (pointer-scheme pointer))
              (resolver (and scheme
                             (find scheme (verification-session-resolvers session)
                                   :key #'verification-resolver-scheme
                                   :test #'equal)))
              (identity (and resolver (verification-resolver-command resolver)))
              (cache (verification-session-cache session))
              (fact (and cache identity
                         (verification-cache-lookup cache pointer subject identity))))
         (and fact
              (eq :holds (verification-fact-fact fact))
              (verify-qualifies-p evidence)
              t))))))

(defun standing-done-evidence (state id)
  "The evidence event ids the node's STANDING `:to :done` transition names, and
whether one stands at all. Answers (values IDS STANDING-P).

The standing one is the newest `:to :done` that no later `:reopen` has undone,
which is what rule 1 means by \"its standing `:to :done`\" (SPEC-WORK.md:4780).
The journal is read oldest first, so the last word wins."
  (let ((ids (list)) (standing nil))
    (dolist (record (state-history state))
      (dolist (e (getf record :events))
        (when (equal id (getf e :node))
          (case (getf e :kind)
            (:transition
             (when (eq :done (getf e :to))
               (let ((evidence (getf e :evidence)))
                 (setf ids (if (absentp evidence) (list) evidence)
                       standing t))))
            (:reopen (setf ids (list) standing nil))
            (t nil)))))
    (values ids standing)))

(defun %need-evidence-record (view id event-id)
  "The view record for EVENT-ID *bound to node ID*. A record naming another node,
or an id the standing done never named, is no proof of anything here."
  (find-if (lambda (r)
             (and (equal event-id (verify-evidence-event-id r))
                  (equal id (verify-evidence-node r))))
           (needs-view-node-evidence view id)))

(defun %need-evidence-verified-p (state id view)
  "True when EVERY evidence event the node's standing `:to :done` names is bound
to a view record for this node and this generation whose raw fact the cache
already holds (SPEC-WORK.md:4780).

The ids come from the TREE and the proof comes from the view, so a view that is
missing, empty, partial, or holding a verified record the standing done never
named, proves nothing and the need reads `need-unverified`. This is not a
nonempty check: it is a correspondence, id by id.

A node with no standing `:to :done` at all cannot be proved terminal accepted
by this route and is unverified; the container clause answers a container
before this is reached, and rule 2 row 4 answers a node whose disposition is
cancelled, superseded or removed."
  (multiple-value-bind (ids standing) (standing-done-evidence state id)
    (and standing
         (consp ids)
         (every (lambda (event-id)
                  (let ((record (%need-evidence-record view id event-id)))
                    (and record (%need-evidence-qualifies-p view id record))))
                ids))))

;;; ------------------------------------------------------------------
;;; the one predicate                            SPEC-WORK.md:4780-4867
;;; ------------------------------------------------------------------

(defun %need-resolver (state need reason dependent view)
  "The resolver rule 2's table names for one unmet need."
  (let ((n (%node-quiet state need)))
    (case reason
      ((:need-open :need-reverted)
       (or (and n (wnode-holder n))
           (needs-view-node-responsible view need)
           "-"))
      (:need-unavailable "-")
      (:need-closed-unaccepted
       (or (and dependent (needs-view-node-responsible view dependent)) "-"))
      (:need-unverified
       (or (needs-view-node-responsible view need) "-"))
      (t "-"))))

(defun need-met-p (state need &key view dependent)
  "Rule 1's *terminal accepted*, and rule 2's one reason when it is not.

Answers (values MET-P REASON RESOLVER). MET-P is true only for a need that is
in C with disposition `done` and whose standing done's every evidence event is
verified from the cache, or for a container whose every direct required member
is met by this same rule. REASON is NIL when met and one of *NEEDS-REASONS*
otherwise; DEPENDENT is the node that holds the edge, which rule 2's table
names as the resolver of a closed-unaccepted need.

This reads the tree, the closed index and the verification cache and fetches
nothing (:4789). A cancelled, superseded or removed need is never met and never
becomes met (:4841)."
  (let ((n (%node-quiet state need)))
    (labels ((unmet (reason)
               (values nil reason (%need-resolver state need reason dependent view))))
      (cond
        ;; An id naming nothing is validator rule 2's `dangling` at the verb
        ;; that would write the edge; read here it is a page that cannot be
        ;; read, and incomplete is not green (:4839).
        ((null n) (unmet :need-unavailable))
        ;; Row 4 is read before rows 1 and 2, and from the disposition rather
        ;; than the branch. The table says a cancelled, superseded or removed
        ;; need is in C; this kernel's `event --kind cancel` writes a
        ;; `:terminal` event that sets the disposition and leaves the node in O
        ;; (src/edit-undo.lisp:248-253), while `node remove` settles into C with
        ;; disposition `removed` (src/kernel.lisp:480). Reading the disposition
        ;; keeps the substance of :4841 -- never met and never becomes met --
        ;; under both spellings. The branch gap is a finding, not a licence.
        ((%need-closed-unaccepted-p state need) (unmet :need-closed-unaccepted))
        ;; rows 1 and 2: the need is in O.
        ((eq :o (wnode-branch n))
         (unmet (if (%need-revived-p state need) :need-reverted :need-open)))
        (t
         (let ((row (%need-settle-row state need)))
           (cond
             ;; row 3: in C, and the closed-index page that says how cannot be read.
             ((null row) (unmet :need-unavailable))
             (t
              (let ((disposition (getf row :disposition)))
                (cond
                  ((not (eq disposition :done)) (unmet :need-unverified))
                  ;; the container clause: no evidence of its own, met with its
                  ;; direct required members, and an empty required set never
                  ;; settles (:4849).
                  ((%need-container-p n)
                   (let ((members (%need-required-members state n)))
                     (if (null members)
                         (unmet :need-open)
                         (let ((first-unmet
                                 (loop for m in members
                                       for (met reason) = (multiple-value-list
                                                           (need-met-p state m
                                                                       :view view
                                                                       :dependent need))
                                       unless met return reason)))
                           (if first-unmet (unmet first-unmet) (values t nil "-"))))))
                  ;; row 5: done, and short of rule 1.
                  ((not (%need-evidence-verified-p state need view))
                   (unmet :need-unverified))
                  (t (values t nil "-"))))))))))))

(defun node-needs-status (state id &key view)
  "The dependency reading of one node: (values UNMET FIRST-NEED REASON RESOLVER).

Every unmet need is counted and the first in `:deps` order is named, with its
one reason (:4859). A node with no `:deps` is needs-met, so UNMET is 0,
FIRST-NEED is `-` and REASON is NIL. The direct needs only: a need's own needs
were its own gate (:4755). This reads and writes nothing."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (let ((unmet 0) (first-need nil) (first-reason nil) (first-resolver "-"))
      (dolist (need (wnode-deps n))
        (multiple-value-bind (met reason resolver)
            (need-met-p state need :view view :dependent id)
          (unless met
            (incf unmet)
            (unless first-need
              (setf first-need need first-reason reason first-resolver resolver)))))
      (values unmet (or first-need "-") first-reason first-resolver))))

(defun node-needs-met-p (state id &key view)
  "True when every need of ID is met, so a node with no `:deps` is needs-met."
  (zerop (node-needs-status state id :view view)))

;;; ------------------------------------------------------------------
;;; the candidate gate                           SPEC-WORK.md:4869-4962
;;; ------------------------------------------------------------------
;;;
;;; Rule 3: every admission verb refuses a node that is not needs-met, by one
;;; predicate, at exit 1, and no flag buys a way past. The needs check is a
;;; precondition of the candidate gate and is no validator rule (:4885): the
;;; whole walk at load and at every clip reads nothing of `:deps` but rule 2's
;;; existence and rule 3's cycle, so a node that went `:doing` while its need
;;; was met, and whose need is then reopened, leaves every load as green as it
;;; was. The built slice's `rule 10` label on its refusal was a defect against
;;; that sentence, because a rule number on the line is what would send the
;;; whole walk looking; it is gone.
;;;
;;; The order of refusals is fixed (:4894): an invocation that cannot be read,
;;; exit 2; then the fence; then a stale `--expect`; then a node the session
;;; does not hold, by that verb's existing line; then a scheduling hold; then
;;; needs-met; and only then the verb's other preconditions -- a lease already
;;; held, capacity, a missing edge of the transition table.

(defstruct (admission-verb
            (:constructor make-admission-verb (name form gate &key reason)))
  "One entry of the register rule 3's coverage test reads. NAME is the kernel's
verb keyword, FORM the spelling of the gated form, GATE one of :REFUSES,
:WITHHOLDS or :EXEMPT, and REASON is required of :EXEMPT."
  name form gate reason)

(defparameter *kernel-dispatch*
  (list
   ;; verb            handler                        admitting effect  gate      gated forms
   (list :machine     'machine-submit                nil               nil       nil)
   (list :route       'route-submit                  nil               nil       nil)
   (list :take        'fleet-take-submit             :allocation       :refuses  (list "--machine --node"))
   (list :heartbeat   'fleet-heartbeat-submit        nil               nil       nil)
   (list :release     'fleet-release-submit          nil               nil       nil)
   (list :probe       'fleet-probe-submit            nil               nil       nil)
   (list :take-node   '%take-node-submit             :lease            :refuses  nil)
   ;; `dep` is a WRITER verb and NOT an admission verb: rule 6 says scope edits
   ;; are ungated (SPEC-WORK.md:5018). It is in this table because the table is
   ;; what `%submit` routes by, not because it admits anything.
   (list :dep         '%dep-submit                   nil               nil       nil)
   ;; `report` records a hand act and changes no tree state, so it admits
   ;; nothing; it is here because this table is what `%submit` routes by, and
   ;; because the whole mutation must happen inside the writer.
   (list :report      '%report-submit                nil               nil       nil))
  "THE DISPATCH TABLE `%submit` routes by, and the one source of truth for what
each dispatched verb can ADMIT.

Stella's [P1b] on 7333349f: `%submit` dispatched `:take` to `fleet-take-submit`,
which wrote an ACTIVE allocation with no needs read, and the coverage test
compared a hand-written registry against the same literal two-name list, so it
could not see the bypass. An instrument that repeats the promise is a mirror.
The registry below is DERIVED from this table, so a verb cannot be dispatched
with an admitting effect and be missing from the register.

Rule 3 names the allocation explicitly among the admission verbs (:4871), which
is why `:take` carries `:allocation`. The three transition verbs are not here:
they run through `%submit`'s own tail, and `:state-to-doing` is added to the
register beside this table.")

(defun kernel-dispatch-entry (verb) (assoc verb *kernel-dispatch*))
(defun kernel-dispatch-handler (entry) (second entry))
(defun kernel-dispatch-effect (entry) (third entry))
(defun kernel-dispatch-gate (entry) (fourth entry))
(defun kernel-dispatch-forms (entry) (fifth entry))

(defparameter *kernel-admitting-effects* (list :lease :allocation :transition-doing)
  "The admitting effects this kernel can produce. The spec's closed list of
admitting kinds is `:lease`, `:handoff`, `:reassign`, `:offer`, `:acknowledge`,
the allocation, `:packet` and a `:transition` carrying `:to :doing` (:4880); the
three here are the ones this kernel has a path for.")

(defparameter *kernel-admission-verbs*
  (append
   ;; DERIVED from the dispatch table: every dispatched verb with an admitting
   ;; effect, with no hand-written list to drift from it.
   (loop for entry in *kernel-dispatch*
         when (member (third entry) *kernel-admitting-effects*)
           collect (make-admission-verb (first entry)
                                        (string-downcase (symbol-name (first entry)))
                                        (fourth entry)))
   ;; and the one admitting verb `%submit` routes through its own transition
   ;; tail rather than through the table.
   (list (make-admission-verb :state-to-doing "state --to doing" :refuses)))
  "The admission verbs this kernel has, each marked as rule 3 requires, DERIVED
from *KERNEL-DISPATCH*. Rule 3's list also names `release --handed`, `reassign`,
`offer`, `acknowledge --stage accepted`, `goal update --progress`, `task
packet`, `execution reconcile`, `undo` and `redo`, which have no form in this
kernel yet.")

(defun needs-refusal-tail (need reason)
  "The one tail every admission verb's refusal carries (:4901)."
  (format nil "unmet need ~A ~A" need (needs-reason-token reason)))

(defun needs-gate-refusal (state id &key view)
  "The candidate gate's needs precondition. Answers NIL when ID is needs-met,
or (values TAIL UNMET NEED REASON) when it is not. Evaluated for the node the
verb names, inside the single writer, at the revision the request is applied
at, so there is no window between the check and the write (:4892)."
  (multiple-value-bind (unmet need reason) (node-needs-status state id :view view)
    (when (plusp unmet)
      (values (needs-refusal-tail need reason) unmet need reason))))

;;; `take --node` (rule 3's first admitting verb). The lease mechanics are
;;; `%take-lease-unchecked` in src/state.lisp; the gate is here, because it
;;; reads the verification cache and src/verifier.lisp is its neighbour.

(defvar *take-lease-request-counter* 0
  "A monotonic counter so a caller that names no request id still gets a unique
one. A real session draws the id; this keeps the convenience wrapper honest.")

(defun take-lease (kernel id by &key view dry-run request (stamp "2026-09-14T12:00:00Z"))
  "`take --node`: one live lease per node, gated on needs-met.

This is a convenience wrapper and NOT the writer. It submits a `:take-node`
request, so the gate and the lease write happen together on the kernel's one
command thread, at the revision the request is applied at (SPEC-WORK.md:4892).
`%take-node-submit` in src/take-verb.lisp is the verb.

STELLA'S [P1a] ON 7333349f: the first cut read the gate here, on the caller's
thread, and then called `%take-lease-unchecked` directly. Her barrier witness
paused take immediately after the predicate read, reopened the need through a
normal `submit`, and take still granted the lease. There is no window now
because there is no second thread: a reopen is another command in the same total
order.

It answers the holder, as it always did, and signals `unsupported-input` carrying
the verb's own `FAIL` line on a refusal, so every existing caller reads the same."
  (multiple-value-bind (okp line code)
      (submit kernel (list :verb :take-node
                           :node id
                           :by by
                           :request (or request
                                        (format nil "take-~A-~D" id
                                                (incf *take-lease-request-counter*)))
                           :stamp stamp
                           :clock :tool
                           :dry-run dry-run
                           :view view))
    (declare (ignore code))
    (unless okp (error 'unsupported-input :what line))
    by))

;;; ------------------------------------------------------------------
;;; `needs-broken`, the reading                  SPEC-WORK.md:4965-4988
;;; ------------------------------------------------------------------
;;;
;;; Rule 5: a node reads `needs-broken=true` when it has an unmet need AND at
;;; least one of three things is true -- it is engaged, it is in C, or the
;;; reason of one of its unmet needs is `need-reverted` -- and false otherwise.
;;; It is a reading on work that went ahead of its gate, never a finding, and
;;; it stops no worker: what it does is refuse the next admission verb on that
;;; node by rule 3, while `heartbeat`, `attempt` and `evidence` are recorded as
;;; before. It goes one edge and no further: a dependent of a `needs-broken`
;;; node that is itself still settled and met is not flagged.
;;;
;;; It is DERIVED (:4972), so there is no slot: the flag `%recheck-needs-broken`
;;; used to maintain on the write path is gone, with the sentence "written
;;; nowhere as authority" for its reason. It clears by itself at the first read
;;; after the node is needs-met again, which a stored flag cannot promise.

(defun node-engaged-p (state id &key view)
  "Rule 5's *engaged* (SPEC-WORK.md:4765): a node holding a live lease, or whose
state is `:doing` or `:review`.

The spec's full list also names a pending or accepted offer, a live allocation,
an `:attempt` of the node's current generation, and a report of a launch with no
report of a stop after it. None of those lives on the node in this kernel --
offers and attempts are `src/control.lisp`'s, allocations `src/fleet.lisp`'s,
and `:report` is not built at all -- so VIEW carries them when a session has
them: its ENGAGED entry, when present, is consulted beside the two facts the
tree itself holds. A view naming none adds none."
  (let ((n (%node-quiet state id)))
    (and n
         (or (and (wnode-holder n) t)
             (member (wnode-state n) '(:doing :review))
             (and view
                  (member id (needs-view-engaged view) :test #'equal)
                  t))
         t)))

(defun node-needs-broken (state id &key view)
  "Rule 5's reading, derived at read time and stored nowhere."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (multiple-value-bind (unmet need reason) (node-needs-status state id :view view)
      (declare (ignore need))
      (and (plusp unmet)
           (or (node-engaged-p state id :view view)
               (eq :c (wnode-branch n))
               (eq :need-reverted reason)
               ;; the reason a row names is the FIRST unmet need's; rule 5 asks
               ;; whether ANY unmet need reads need-reverted.
               (some (lambda (dep)
                       (multiple-value-bind (met r)
                           (need-met-p state dep :view view :dependent id)
                         (and (not met) (eq :need-reverted r))))
                     (wnode-deps n)))
           t))))

(defun state-needs-broken-count (state &key view)
  "`check`'s `needs-broken=<n>` count (SPEC-WORK.md:5933). A count and never a
`WORK FAIL <id>` line: a red set refuses every mutation, and one reopened need
must not stop a team, so `check` exits 0 over any number of them."
  (count-if (lambda (id) (node-needs-broken state id :view view))
            (wstate-order state)))

;;; ------------------------------------------------------------------
;;; the `ready` row                              SPEC-WORK.md:4859, :5941
;;; ------------------------------------------------------------------

(defstruct (ready-row
             (:constructor make-ready-row (id &key ready reason resolver
                                                (need "-") (unmet 0) needs-broken
                                                kind state holder responsible)))
  "One row of `query --ask ready`. SPEC-WORK.md:5941:

  QUERY ROW <id> kind=<k> state=<s> ready=<true|false> reason=<text|->
  need=<id|-> unmet=<n> needs-broken=<true|false> resolver=<name|->
  ... responsible=<name|-> holder=<name|unowned>

`need=`, `unmet=` and `needs-broken=` are rule 2's and rule 5's additions, and
where `unmet=` is above zero `reason=` is one of the five tokens -- the kernel
slice's built text `blocked by <id>` gives way to the token with `need=` beside
it (SPEC-WORK.md:4859)."
  id ready reason resolver need unmet needs-broken kind state holder responsible)


;;; ------------------------------------------------------------------
;;; `ready` reads the one predicate                SPEC-WORK.md:4930
;;; ------------------------------------------------------------------
;;;
;;; "The gate is the dependency predicate and not the whole of `ready`": every
;;; row `ready` prints `ready=true` is needs-met, and the converse is false,
;;; since `ready` also folds agreed scope, acceptance readiness, ownership,
;;; availability and resource limits.
;;;
;;; It reads `node-needs-status`, the same predicate the candidate gate reads,
;;; and not the recorded half. Before Stella's repair of the evidence check the
;;; two answered the same thing; once recorded stopped meaning verified, a
;;; `ready-p` on the recorded half would have printed `ready=true` over a node
;;; every admission verb refuses.

(defun ready-p (state id &key view)
  "Open leaf work that is needs-met."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (and (eq :o (wnode-branch n))
         (member (wnode-type n) (quote (:task :bug)))
         (zerop (node-needs-status state id :view view))
         t)))

(defun ready-nodes (state &key view)
  "`query ready`: the ready items, in seed order. This is a read -- it visits
nodes and mutates none."
  (loop for id in (wstate-order state)
        when (ready-p state id :view view) collect id))

(defun node-ready-row (state id &key view)
  "One `ready` row for ID, read from the tree. Nothing is written."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (multiple-value-bind (unmet need reason resolver)
        (node-needs-status state id :view view)
      (make-ready-row id
                      :ready (ready-p state id :view view)
                      :reason (needs-reason-token reason)
                      :resolver (if (plusp unmet)
                                    resolver
                                    (or (wnode-holder n)
                                        (needs-view-node-responsible view id)
                                        "-"))
                      :need need
                      :unmet unmet
                      :needs-broken (node-needs-broken state id :view view)
                      :kind (wnode-type n)
                      :state (wnode-state n)
                      :holder (wnode-holder n)
                      :responsible (needs-view-node-responsible view id)))))

(defun state-ready-rows (state &key view)
  "Every open node's `ready` row, in seed order. The one reading: `ready-rows`
in src/node-verbs.lisp is the pure model over hand-built items and answers the
same five tokens, never `blocked by <id>`."
  (loop for id in (wstate-order state)
        for n = (%node-quiet state id)
        when (and n (eq :o (wnode-branch n)))
          collect (node-ready-row state id :view view)))

(defun %row-flag (value) (if value "true" "false"))

(defun ready-row-line (row)
  "The `QUERY ROW` line of SPEC-WORK.md:5941, without the three priority fields,
which are *Priority*'s and are not this slice's."
  (format nil "QUERY ROW ~A kind=~(~A~) state=~(~A~) ready=~A reason=~A need=~A unmet=~D needs-broken=~A resolver=~A responsible=~A holder=~A"
          (ready-row-id row)
          (or (ready-row-kind row) :-)
          (or (ready-row-state row) :-)
          (%row-flag (ready-row-ready row))
          (or (ready-row-reason row) "-")
          (or (ready-row-need row) "-")
          (or (ready-row-unmet row) 0)
          (%row-flag (ready-row-needs-broken row))
          (or (ready-row-resolver row) "-")
          (or (ready-row-responsible row) "-")
          (or (ready-row-holder row) "unowned")))
