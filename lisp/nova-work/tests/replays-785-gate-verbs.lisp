;;;; replays-785-gate-verbs.lisp --- the candidate gate (nova-tools #785).
;;;;
;;;; The replays of rules 3 and 4 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:4869-4962, cut from the replay text at
;;;; docs/SPEC-WORK.md:6923-6970. Slice 1 built the predicate; this file asserts
;;;; it as a precondition of the candidate gate at every admission verb this
;;;; kernel has, in the fixed refusal order, at exit 1, with nothing written.
;;;;
;;;; Rule 3's list of admission verbs is written for the whole grammar. This
;;;; kernel has two of them -- `take --node` and `state --to doing` -- and
;;;; `*kernel-admission-verbs*` is the register that says so. The last case
;;;; below asserts that register against the kernel's own verb table, so a third
;;;; admitting verb cannot arrive ungated without failing a test: a later verb
;;;; is caught by an instrument and not by a promise (:4878).

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; fixtures
;;; ------------------------------------------------------------------

(defparameter *verbs-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/x" :type :task     :parent "acme/work" :state :todo))
  "D depends on the open N. X is unrelated, and no rule here touches it.")

(defun verbs-kernel (&optional (seed *verbs-seed*))
  (make-kernel :state (make-seed-state seed)
               :journal (make-ordering-journal) :rev-base 1
               :friends (list "glenn" "rowan" "emma" "sam")))

(defun verbs-doing (k node &key (request (format nil "doing-~A" node))
                                (stamp "2026-09-16T11:00:00Z"))
  (submit k (list :verb :state-to-doing :node node :by "rowan" :reason "started"
                  :request request :stamp stamp :clock :tool
                  :generation-owner "gen-1")))

(defun verbs-done (k node &key (request (format nil "done-~A" node))
                               (stamp "2026-09-16T12:00:00Z"))
  (unless (member (node-state (kernel-state k) node) (list :doing :review))
    (verbs-doing k node :request (format nil "pre-~A" request)))
  (submit k (list :verb :state-to-done :node node :by "rowan" :reason "merged"
                  :evidence (list "ev-1") :request request :stamp stamp
                  :clock :tool :generation-owner "gen-1")))

(defun verbs-reopen (k node &key (request (format nil "reopen-~A" node))
                                 (stamp "2026-09-16T13:00:00Z"))
  (submit k (list :verb :event-reopen :node node :by "rowan" :reason "reverted"
                  :request request :stamp stamp :clock :tool
                  :generation-owner "gen-1")))

(defun lease-refusal (k id by &key view dry-run)
  "The `LEASE FAIL` line `take` refuses with, or NIL when it is admitted."
  (handler-case (progn (take-lease k id by :view view :dry-run dry-run) nil)
    (unsupported-input (c) (unsupported-input-what c))))

;;; ------------------------------------------------------------------
;;; every-admission-verb-refuses-an-unmet-need   SPEC-WORK.md:4869, :6923
;;; ------------------------------------------------------------------

(deftest "every-admission-verb-refuses-an-unmet-need" "docs/SPEC-WORK.md:4869"
    "expected=exit-1-one-FAIL-line-ending-unmet-need-and-nothing-written"
  ;; `take --node` over D with N open and no lease on D
  (let* ((k (verbs-kernel))
         (before (length (state-lease-log (kernel-state k))))
         (line (lease-refusal k "acme/work/d" "emma")))
    (check-string= "LEASE FAIL node=acme/work/d unmet=1: unmet need acme/work/n need-open"
                   line "take --node names the need and its one reason")
    (ok (null (node-holder (kernel-state k) "acme/work/d"))
        "no lease is written")
    (check-equal before (length (state-lease-log (kernel-state k)))
                 "the lease log is unchanged"))
  ;; the same call under --dry-run prints the same refusal and writes nothing
  (let* ((k (verbs-kernel))
         (line (lease-refusal k "acme/work/d" "emma" :dry-run t)))
    (check-string= "LEASE FAIL node=acme/work/d unmet=1: unmet need acme/work/n need-open"
                   line "--dry-run prints the same refusal")
    (ok (null (node-holder (kernel-state k) "acme/work/d")) "and writes nothing"))
  ;; `state --to doing` over D: exit 1, and STATE FAIL carries NO rule number
  (let* ((k (verbs-kernel))
         (rev (state-revision (kernel-state k))))
    (multiple-value-bind (okp line code) (verbs-doing k "acme/work/d")
      (ok (not okp) "state --to doing is refused")
      (check-equal 1 code "at exit 1")
      (check-string= "STATE FAIL node=acme/work/d: unmet need acme/work/n need-open"
                     line "the refusal names the need and carries no rule number")
      (ok (not (search "rule " line)) "no rule number on the line: ~A" line))
    (check-equal :todo (node-state (kernel-state k) "acme/work/d")
                 "no transition is written")
    (check-equal rev (state-revision (kernel-state k)) "the revision does not move"))
  ;; with N met, each prints its OK
  (let ((k (verbs-kernel)))
    (multiple-value-bind (okp line) (verbs-done k "acme/work/n")
      (ok okp "the need settles: ~A" line))
    (refresh-needs-view k)
    (ok (null (lease-refusal k "acme/work/d" "emma")) "take --node is admitted")
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "and the lease is written"))
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (multiple-value-bind (okp line) (verbs-doing k "acme/work/d")
      (ok okp "state --to doing is admitted: ~A" line)))
  ;; a `take --node` of an id the session does not hold prints that verb's
  ;; no-such-node refusal and no `unmet need` (SPEC-WORK.md:6936)
  (let* ((k (verbs-kernel))
         (line (lease-refusal k "acme/work/nowhere" "emma")))
    (ok (search "no such node" line) "the no-such-node refusal stands: ~A" line)
    (ok (not (search "unmet need" line)) "and names no unmet need: ~A" line)))

;;; ------------------------------------------------------------------
;;; a-verb-that-can-admit-declares-its-needs-gate (the kernel's half)
;;;                                              SPEC-WORK.md:4878, :6942
;;; ------------------------------------------------------------------

(deftest "a-kernel-verb-that-can-admit-declares-its-needs-gate"
    "docs/SPEC-WORK.md:4878"
    "expected=the-register-holds-every-built-admitting-verb-and-no-other"
  ;; The generated schema file's coverage test is the Go half and is its own
  ;; slice. This is the kernel's half: every verb of `*kernel-admission-verbs*`
  ;; is gated, and the register names every verb this kernel has that can write
  ;; a lease or a `:to :doing` transition.
  ;; Stella's [P1b]: the register is DERIVED from *KERNEL-DISPATCH*, so it
  ;; cannot drift from what `%submit` actually routes. Asserted as a
  ;; derivation, not as a literal.
  (check-equal (append (loop for entry in *kernel-dispatch*
                             when (member (kernel-dispatch-effect entry)
                                          *kernel-admitting-effects*)
                               collect (first entry))
                       (list :state-to-doing))
               (mapcar (function admission-verb-name) *kernel-admission-verbs*)
               "the register is derived from the dispatch table")
  (ok (member :take (mapcar (function admission-verb-name) *kernel-admission-verbs*))
      "the machine allocation path is in the register: rule 3 names it (:4871)")
  (ok (member :take-node (mapcar (function admission-verb-name) *kernel-admission-verbs*))
      "and so is the node lease")
  ;; every verb `%submit` dispatches with an admitting effect carries a mark
  (dolist (entry *kernel-dispatch*)
    (when (member (kernel-dispatch-effect entry) *kernel-admitting-effects*)
      (ok (find (first entry) *kernel-admission-verbs*
                :key (function admission-verb-name))
          "~A is dispatched with an admitting effect and must be registered"
          (first entry))))
  (dolist (entry *kernel-admission-verbs*)
    (check-equal :refuses (admission-verb-gate entry)
                 (format nil "~A is marked needs-gate: refuses"
                         (admission-verb-name entry)))
    (ok (plusp (length (admission-verb-form entry)))
        "~A names the form the gate is per" (admission-verb-name entry))))

;;; ------------------------------------------------------------------
;;; a-reopened-need-under-a-doing-dependent-leaves-the-set-green
;;;                                              SPEC-WORK.md:4885, :6938
;;; ------------------------------------------------------------------

(deftest "a-reopened-need-under-a-doing-dependent-leaves-the-set-green"
    "docs/SPEC-WORK.md:4885"
    "expected=the-whole-walk-reads-nothing-of-deps-but-existence-and-cycle"
  (let ((k (verbs-kernel)))
    ;; D goes :doing while N is met
    (multiple-value-bind (okp line) (verbs-done k "acme/work/n")
      (ok okp "N settles: ~A" line))
    (refresh-needs-view k)
    (multiple-value-bind (okp line) (verbs-doing k "acme/work/d")
      (ok okp "D goes doing under a met need: ~A" line))
    ;; the need is reopened under it
    (multiple-value-bind (okp line) (verbs-reopen k "acme/work/n")
      (ok okp "N reopens: ~A" line))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d")
      (check-equal 1 unmet "D now has one unmet need")
      (check-string= "acme/work/n" need "and names it")
      (check-equal :need-reverted reason "with the reverted token"))
    ;; the whole walk stays green: it reads nothing of :deps
    (multiple-value-bind (okp line code) (cow-candidate-gate (kernel-state k))
      (ok okp "the load gate is green over a reopened need: ~A" line)
      (check-equal 0 code "at exit 0")
      (ok (not (search "acme/work/d" line)) "no finding names D: ~A" line)
      (ok (not (search "rule 10" line)) "and none names rule 10: ~A" line))
    (check-equal '() (cow-load-findings (kernel-state k)) "findings=0")
    (ok (cow-partition-holds-p (kernel-state k)) "the partition still holds")
    ;; and it survives a reconstruction, as a clip and a fresh session do
    (let ((fresh (reconstruct-state (canonical-string (state-canonical-form (kernel-state k))))))
      (check-equal '() (cow-load-findings fresh) "findings=0 after a reconstruction")
      (multiple-value-bind (unmet) (node-needs-status fresh "acme/work/d")
        (check-equal 1 unmet "and the reading is rebuilt from the events")))
    ;; an unrelated mutation on another node is admitted after each
    (multiple-value-bind (okp line) (verbs-doing k "acme/work/x")
      (ok okp "an unrelated node still moves: ~A" line))
    ;; D's own lease and state stand: the tree kills nothing (:4977)
    (check-equal :doing (node-state (kernel-state k) "acme/work/d")
                 "D is still :doing")
    ;; but the next admission verb on D is refused by rule 3
    (ok (search "unmet need" (or (lease-refusal k "acme/work/d" "emma") ""))
        "the next take on D is refused")))

;;; ------------------------------------------------------------------
;;; needs-met-is-read-not-released               SPEC-WORK.md:4952, :6961
;;; ------------------------------------------------------------------

(deftest "needs-met-is-read-not-released" "docs/SPEC-WORK.md:4952"
    "expected=no-release-event-no-stored-waiting-list-nothing-to-sweep"
  (let* ((k (verbs-kernel))
         (events-on-d
           (lambda ()
             (count "acme/work/d"
                    (loop for record in (state-history (kernel-state k))
                          append (mapcar (lambda (e) (getf e :node))
                                         (getf record :events)))
                    :test #'equal))))
    (check-equal 0 (funcall events-on-d) "no event names D to begin with")
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d")
      (check-equal 1 unmet "D is not needs-met"))
    ;; `state --to done` on N, and no further command
    (multiple-value-bind (okp line) (verbs-done k "acme/work/n")
      (ok okp "N settles: ~A" line))
    (refresh-needs-view k)
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d"
                           :view (kernel-needs-view k))
      (check-equal 0 unmet "D reads unmet=0 at the first read after the settle")
      (check-string= "-" need "and names no need")
      (ok (null reason) "and carries no reason"))
    (ok (ready-p (kernel-state k) "acme/work/d" :view (session-needs-view k))
        "and ready=true, with nothing else in the way")
    (check-equal 0 (funcall events-on-d)
                 "and the journal holds no event whose :node is D")))

;;; ------------------------------------------------------------------
;;; ready-true-implies-needs-met-and-not-the-reverse
;;;                                              SPEC-WORK.md:4930, :6957
;;; ------------------------------------------------------------------

(deftest "ready-true-implies-needs-met-and-not-the-reverse"
    "docs/SPEC-WORK.md:4930"
    "expected=the-gate-is-the-dependency-predicate-and-not-the-whole-of-ready"
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    ;; D is needs-met and leased by another name
    (take-lease k "acme/work/d" "sam")
    (multiple-value-bind (unmet)
        (node-needs-status (kernel-state k) "acme/work/d" :view (kernel-needs-view k))
      (check-equal 0 unmet "D is needs-met"))
    (let ((line (lease-refusal k "acme/work/d" "emma")))
      (ok (search ": held" line) "take prints the ownership refusal: ~A" line)
      (ok (not (search "unmet need" line))
          "and not an unmet need, by the refusal order: ~A" line))
    ;; Across the whole fixture no node is ready beside an unmet need. `ready-p`
    ;; reads the RECORDED half here -- it grows its `:view` in the next slice,
    ;; with the rest of rule 5's reading -- so the count it is checked against
    ;; is read the same way.
    (dolist (id (list "acme/work/d" "acme/work/n" "acme/work/x"))
      (when (ready-p (kernel-state k) id)
        (multiple-value-bind (unmet)
            (node-needs-status (kernel-state k) id)
          (check-equal 0 unmet
                       (format nil "~A reads ready=true, so it is needs-met" id)))))))

;;; ------------------------------------------------------------------
;;; the gate and the write are one act inside the single writer
;;;    Stella's [P1a] on 7333349f          SPEC-WORK.md:4892
;;; ------------------------------------------------------------------

(deftest "a-take-and-its-gate-are-one-act-in-the-single-writer"
    "docs/SPEC-WORK.md:4892"
    "expected=no-window-between-the-check-and-the-write"
  ;; "Each verb evaluates needs-met for the node it names inside the single
  ;; writer, at the revision the request is applied at, so there is no window
  ;; between the check and the write."
  ;;
  ;; The first cut read the gate on the CALLER's thread and then wrote the lease
  ;; directly. Stella paused it between the two with a semaphore, reopened the
  ;; need through a normal `submit`, and the paused take still granted the lease.
  ;;
  ;; Three assertions, none of them a sleep.
  ;;
  ;; 1. STRUCTURAL: the take is a request with an id the journal records before
  ;;    the apply. A mutation on the caller's thread has no journal record at
  ;;    all, so this could not have held before.
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (take-lease k "acme/work/d" "emma" :request "writer-take")
    (multiple-value-bind (found digest line) (journal-lookup (kernel-journal k) "writer-take")
      (declare (ignore digest))
      (ok found "the take is in the journal, recorded before it was applied")
      (ok (search "LEASE OK" line) "with its own OK line: ~A" line))
    (let ((lease (find :lease (state-lease-log (kernel-state k))
                       :key (lambda (e) (getf e :kind)))))
      (ok lease "and it wrote a :lease entry")
      (check-string= "emma" (getf lease :holder) "naming the holder")
      (ok (integerp (getf lease :rev)) "at the revision it was applied at"))
    ;; a retry of the same request id answers the original line and writes once
    (let ((before (length (state-lease-log (kernel-state k)))))
      (take-lease k "acme/work/d" "emma" :request "writer-take")
      (check-equal before (length (state-lease-log (kernel-state k)))
                   "a retry of the request id writes no second lease")))
  ;; 2. TOTAL ORDER: a take and a reopen raced from two threads are two commands
  ;;    in one order, and the lease -- when it is granted at all -- is always
  ;;    applied at a revision BELOW the revive that unmet the need. Under the
  ;;    old code the lease was written after the revive and carried no revision
  ;;    to check it by.
  (dotimes (round 12)
    (let* ((k (verbs-kernel))
           (taken nil))
      (verbs-done k "acme/work/n")
      (refresh-needs-view k)
      (let ((racers
              (list (sb-thread:make-thread
                     (lambda ()
                       (handler-case
                           (progn (take-lease k "acme/work/d" "emma"
                                              :request (format nil "race-take-~D" round))
                                  (setf taken t))
                         (unsupported-input () nil)))
                     :name "racing-take")
                    (sb-thread:make-thread
                     (lambda ()
                       (verbs-reopen k "acme/work/n"
                                     :request (format nil "race-reopen-~D" round)))
                     :name "racing-reopen"))))
        (dolist (thread racers) (sb-thread:join-thread thread :default nil)))
      (let ((lease (find :lease (state-lease-log (kernel-state k))
                         :key (lambda (e) (getf e :kind))))
            (revive (find-if (lambda (r)
                               (and (eq :revive (getf r :kind))
                                    (equal "acme/work/n" (getf r :node))))
                             (state-closed-rows (kernel-state k)))))
        (ok revive "the reopen was applied in round ~D" round)
        (if taken
            (progn
              (ok lease "a granted take wrote its :lease in round ~D" round)
              (ok (< (getf lease :rev) (getf revive :rev))
                  "the lease was applied BEFORE the revive that unmet the need (round ~D: lease=~D revive=~D)"
                  round (getf lease :rev) (getf revive :rev)))
            (ok (null lease)
                "a refused take wrote no lease in round ~D" round)))))
  ;; 3. --dry-run writes nothing at all and journals nothing
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (let ((before (length (state-lease-log (kernel-state k)))))
      (take-lease k "acme/work/d" "emma" :dry-run t :request "dry-take")
      (ok (null (node-holder (kernel-state k) "acme/work/d"))
          "--dry-run grants no lease")
      (check-equal before (length (state-lease-log (kernel-state k)))
                   "and writes no lease-log entry")
      (ok (not (journal-lookup (kernel-journal k) "dry-take"))
          "and journals nothing"))))
;;; ------------------------------------------------------------------
;;; the machine allocation path is an admission verb too
;;;    Stella's [P1b] on 7333349f          SPEC-WORK.md:4871
;;; ------------------------------------------------------------------

(defun alloc-machine (k &optional (id "m-review"))
  "Register a machine and REFUSE to continue if the registration did not take --
an allocation test whose machine was never registered passes for the wrong
reason, which is how the first cut of this case would have looked green."
  (multiple-value-bind (okp line)
      (submit k (list :verb :machine :change :register :machine id
                  :name "fixture" :owner "glenn" :connect "profile:fixture"
                  :roles (list :build :test) :permits (list "go-test")
                  :excludes '() :limits (list :cores 16 :concurrent 2) :facts nil
                  :declared-by "glenn" :request (format nil "register-~A" id)
                  :stamp "2026-09-19T05:30:00Z" :clock :tool
                  :generation-owner "gen-1"))
    (ok okp "the fixture machine registered: ~A" line))
  k)

(deftest "the-allocation-path-refuses-an-unmet-need" "docs/SPEC-WORK.md:4871"
    "expected=rule-3-names-the-allocation-and-the-register-is-derived-not-written"
  ;; Rule 3's list of admission verbs names `take --machine`, the allocation.
  ;; `%submit` dispatches `:take` to `fleet-take-submit`, which wrote one with
  ;; no needs read: over D with an open need Stella's fixture got
  ;; `ALLOC OK ... node=acme/work/d ... changed=1`, exit 0.
  (let* ((k (alloc-machine (verbs-kernel)))
         (friends '("glenn" "rowan")))
    (declare (ignore friends))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d")
      (check-equal 1 unmet "D has one unmet need")
      (check-string= "acme/work/n" need "named")
      (check-equal :need-open reason "and open"))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :take :machine "m-review" :node "acme/work/d"
                        :allocation-id "review-allocation" :holder "rowan"
                        :request "alloc-1"))
      (ok (not okp) "the allocation is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "ALLOC FAIL" line) "with the ALLOC FAIL line: ~A" line)
      (ok (search "unmet need acme/work/n need-open" line)
          "carrying rule 3's one tail: ~A" line)
      (ok (not (search "ALLOC OK" line)) "and never an OK: ~A" line))
    ;; nothing was written
    (check-equal 0 (length (fleet-live-allocations (kernel-allocations k)
                                                  :machine "m-review"))
                 "no allocation was written")
    ;; with the need met, the allocation is admitted
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (multiple-value-bind (okp line)
        (submit k (list :verb :take :machine "m-review" :node "acme/work/d"
                        :allocation-id "review-allocation" :holder "rowan"
                        :request "alloc-2"))
      (ok okp "once the need is met the allocation is admitted: ~A" line)
      (ok (search "ALLOC OK" line) "with the OK line: ~A" line))))

;;; ------------------------------------------------------------------
;;; an identical replay answers its original receipt, whatever the state
;;;    Stella's [P2] on 1a11652d          SPEC-WORK.md:315
;;; ------------------------------------------------------------------

(deftest "a-replayed-take-answers-its-original-receipt-after-the-state-moved"
    "docs/SPEC-WORK.md:315"
    "expected=the-durable-contract-is-about-the-recorded-payload-and-not-the-state-now"
  ;; Her witness: `same-take` succeeds at revision 3, a normal submit reopens
  ;; its need, and the IDENTICAL request retried comes back `LEASE FAIL ...
  ;; need-reverted` at exit 1 -- although the journal holds the original
  ;; `LEASE OK`. An identical replay grants no new work: the work was granted
  ;; and recorded already.
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (multiple-value-bind (okp original code)
        (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                        :request "same-take" :stamp "2026-09-16T12:00:00Z"
                        :clock :tool))
      (ok okp "the take succeeds: ~A" original)
      (check-equal 0 code "at exit 0")
      ;; the need is reopened by a normal submit, so the node is no longer
      ;; needs-met and a FRESH take would be refused
      (multiple-value-bind (okp line) (verbs-reopen k "acme/work/n")
        (ok okp "the need is reopened: ~A" line))
      (ok (search "need-reverted" (or (lease-refusal k "acme/work/d" "sam") ""))
          "a fresh take is refused now")
      ;; the identical request answers the ORIGINAL receipt
      (let ((leases (length (state-lease-log (kernel-state k))))
            (rev (state-revision (kernel-state k))))
        (multiple-value-bind (okp replayed code)
            (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                            :request "same-take" :stamp "2026-09-16T12:00:00Z"
                            :clock :tool))
          (ok okp "the identical request is admitted as a replay")
          (check-equal 0 code "at exit 0")
          (check-string= original replayed "and answers the original receipt"))
        (check-equal leases (length (state-lease-log (kernel-state k)))
                     "and writes no second lease")
        (check-equal rev (state-revision (kernel-state k))
                     "and takes no second revision"))))
  ;; `--expect` is checked AFTER the replay answer, so a request carrying its
  ;; original expectation does not fail on its own retry
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (let ((expect (state-revision (kernel-state k))))
      (multiple-value-bind (okp original)
          (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                          :request "expect-take" :expect expect
                          :stamp "2026-09-16T12:00:00Z" :clock :tool))
        (ok okp "the take succeeds under its expectation: ~A" original)
        ;; the set moves under it
        (verbs-doing k "acme/work/x")
        (ok (/= expect (state-revision (kernel-state k))) "the revision has moved")
        (multiple-value-bind (okp replayed code)
            (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                            :request "expect-take" :expect expect
                            :stamp "2026-09-16T12:00:00Z" :clock :tool))
          (ok okp "the identical request with its original --expect still replays")
          (check-equal 0 code "at exit 0")
          (check-string= original replayed "to the original receipt")))))
  ;; a node that no longer exists, or is now in C, does not change the answer
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (multiple-value-bind (okp original)
        (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                        :request "settled-take" :stamp "2026-09-16T12:00:00Z"
                        :clock :tool))
      (ok okp "the take succeeds: ~A" original)
      (node-remove k "acme/work/d")
      (check-equal :c (node-branch (kernel-state k) "acme/work/d") "D is now in C")
      (multiple-value-bind (okp replayed)
          (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                          :request "settled-take" :stamp "2026-09-16T12:00:00Z"
                          :clock :tool))
        (ok okp "the identical request still replays over a node now in C")
        (check-string= original replayed "to the original receipt"))))
  ;; and the same id with a DIFFERENT payload is still refused, whatever moved
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
    (refresh-needs-view k)
    (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                    :request "conflict-take" :stamp "2026-09-16T12:00:00Z"
                    :clock :tool))
    (let ((leases (length (state-lease-log (kernel-state k)))))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :take-node :node "acme/work/d" :by "sam"
                          :request "conflict-take" :stamp "2026-09-16T12:00:00Z"
                          :clock :tool))
        (ok (not okp) "a different payload under the same id is refused")
        (check-equal 1 code "at exit 1")
        (ok (search "reused with a different payload" line) "by name: ~A" line))
      (check-equal leases (length (state-lease-log (kernel-state k)))
                   "and writes nothing"))))

;;; ------------------------------------------------------------------
;;; the same, against the REAL file journal
;;;    Stella: every new test of a journaled command runs against the real
;;;    file journal as well as the fake, because the fake is permissive
;;; ------------------------------------------------------------------

(defun verbs-file-kernel (path &key (seed *verbs-seed*))
  "A kernel over a real `file-journal`, not the permissive ordering fake."
  (let ((state (make-seed-state seed)))
    (values (make-kernel :state state
                         :journal (open-file-journal
                                   path :initial-state-hash (root-digest state))
                         :rev-base 1
                         :friends (list "glenn" "rowan" "emma" "sam"))
            state)))

(deftest "a-take-replays-from-the-real-file-journal" "docs/SPEC-WORK.md:315"
    "expected=one-durable-record-per-take-and-an-identical-retry-answers-it"
  (let* ((path (test-journal-path "take-node-replay"))
         (k (verbs-file-kernel path)))
    (unwind-protect
         (progn
           (verbs-done k "acme/work/n")
           (refresh-needs-view k)
           (multiple-value-bind (okp original code)
               (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                               :request "file-take" :stamp "2026-09-16T12:00:00Z"
                               :clock :tool))
             (ok okp "the take succeeds against a real journal: ~A" original)
             (check-equal 0 code "at exit 0")
             ;; the state moves under it, exactly as in the fake case
             (verbs-reopen k "acme/work/n")
             (let ((leases (length (state-lease-log (kernel-state k)))))
               (multiple-value-bind (okp replayed code)
                   (submit k (list :verb :take-node :node "acme/work/d" :by "emma"
                                   :request "file-take" :stamp "2026-09-16T12:00:00Z"
                                   :clock :tool))
                 (ok okp "the identical request replays")
                 (check-equal 0 code "at exit 0")
                 (check-string= original replayed
                                "to the original receipt the FILE journal holds"))
               (check-equal leases (length (state-lease-log (kernel-state k)))
                            "and writes no second lease"))
             ;; and a different payload under the same id is refused by the real
             ;; journal too
             (multiple-value-bind (okp line code)
                 (submit k (list :verb :take-node :node "acme/work/d" :by "sam"
                                 :request "file-take" :stamp "2026-09-16T12:00:00Z"
                                 :clock :tool))
               (ok (not okp) "a different payload under the same id is refused")
               (check-equal 1 code "at exit 1")
               (ok (search "reused with a different payload" line) "by name: ~A" line))))
      (close-file-journal (kernel-journal k)))))
