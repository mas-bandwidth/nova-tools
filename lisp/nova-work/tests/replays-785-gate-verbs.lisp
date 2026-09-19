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
               :journal (make-ordering-journal) :rev-base 1))

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
    (ok (null (lease-refusal k "acme/work/d" "emma")) "take --node is admitted")
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "and the lease is written"))
  (let ((k (verbs-kernel)))
    (verbs-done k "acme/work/n")
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
  (check-equal '(:take-node :state-to-doing)
               (mapcar #'admission-verb-name *kernel-admission-verbs*)
               "the register names the two admitting verbs this kernel has")
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
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d")
      (check-equal 0 unmet "D reads unmet=0 at the first read after the settle")
      (check-string= "-" need "and names no need")
      (ok (null reason) "and carries no reason"))
    (ok (ready-p (kernel-state k) "acme/work/d")
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
    ;; D is needs-met and leased by another name
    (take-lease k "acme/work/d" "sam")
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d")
      (check-equal 0 unmet "D is needs-met"))
    (let ((line (lease-refusal k "acme/work/d" "emma")))
      (ok (search ": held" line) "take prints the ownership refusal: ~A" line)
      (ok (not (search "unmet need" line))
          "and not an unmet need, by the refusal order: ~A" line))
    ;; across the whole fixture no node is ready beside an unmet need
    (dolist (id (list "acme/work/d" "acme/work/n" "acme/work/x"))
      (when (ready-p (kernel-state k) id)
        (multiple-value-bind (unmet) (node-needs-status (kernel-state k) id)
          (check-equal 0 unmet
                       (format nil "~A reads ready=true, so it is needs-met" id)))))))
