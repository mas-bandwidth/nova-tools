;;;; slice-11-dependencies.lisp --- the dependency replays of nova-tools #785.
;;;;
;;;; docs/SPEC-WORK.md:850-886 -- `:deps` is a reference edge: needed, not
;;;; owned, not counted, validated for existence and for dependency state. It
;;;; is part of the durable seed and the reverse edge is rebuilt by rule 2 on
;;;; replay. docs/SPEC-WORK.md:1885 -- an unmet dependency gate blocks a
;;;; parent's done, exactly as an open bug does. docs/SPEC-WORK.md:1951 -- "A
;;;; parent is green only when every required child and every dependency gate
;;;; is satisfied". docs/SPEC-WORK.md:2110-2114 -- `query ready` excludes a
;;;; dependent whose need is open and admits it once the need is terminal
;;;; accepted. docs/SPEC-WORK.md:709 -- the reverse-dependency index answers
;;;; `what does this block` in one bounded lookup, never a walk of O.
;;;;
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

;;; One dependent leaf a whose one need b is open. The seed is local so the
;;; slice carries its own fixture.
(defparameter *dependency-seed*
  '((:id "root"   :type :work-set :parent nil    :state :unknown)
    (:id "root/a" :type :task     :parent "root" :state :todo :deps ("root/b"))
    (:id "root/b" :type :task     :parent "root" :state :doing))
  "a needs b; b is open, so a is not ready and cannot be taken into doing.")

(defparameter *dependency-two-seed*
  '((:id "root"    :type :work-set :parent nil    :state :unknown)
    (:id "root/a"  :type :task     :parent "root" :state :todo
     :deps ("root/n1" "root/n2"))
    (:id "root/n1" :type :task     :parent "root" :state :doing)
    (:id "root/n2" :type :task     :parent "root" :state :doing))
  "One dependent whose readiness needs both needs settled.")

(defparameter *dependency-parent-seed*
  '((:id "root"      :type :work-set :parent nil    :state :unknown)
    (:id "root/f"    :type :feature  :parent "root" :state :unknown
     :deps ("root/need"))
    (:id "root/f/t"  :type :task     :parent "root/f" :state :doing)
    (:id "root/need" :type :task     :parent "root" :state :doing))
  "A feature f whose required leaf is done but which needs root/need settled.")

(defun dependency-kernel (&optional (seed *dependency-seed*))
  (make-kernel :state (make-seed-state seed) :journal (make-ordering-journal)
               :rev-base 1))

;;; ------------------------------------------------------------------
;;; dependency-edge-is-journaled                    SPEC-WORK.md:850-886
;;; ------------------------------------------------------------------

(deftest "dependency-edge-is-journaled" "docs/SPEC-WORK.md:850-886,2114"
    "expected=forward-edge-and-reverse-index-in-the-durable-bytes-and-replayed"
  (let ((state (make-seed-state *dependency-seed*)))
    (check-equal '("root/b") (node-deps state "root/a")
                 "the forward need edge is recorded")
    (check-equal '("root/a") (node-dependents state "root/b")
                 "the reverse blocks edge is recorded")
    (let ((bytes (canonical-string (state-canonical-form state))))
      (let ((replayed (reconstruct-state bytes)))
        (check-equal (node-deps state "root/a") (node-deps replayed "root/a")
                     "the need edge survives the durable bytes and replay")
        (check-equal (node-dependents state "root/b")
                     (node-dependents replayed "root/b")
                     "the reverse index survives the durable bytes and replay")
        (check-equal (root-digest state) (root-digest replayed)
                     "the reconstruction is equal")))))

;;; ------------------------------------------------------------------
;;; ready-needs-every-dependency-settled            SPEC-WORK.md:2110-2114
;;; ------------------------------------------------------------------

(deftest "ready-needs-every-dependency-settled" "docs/SPEC-WORK.md:2110-2114"
    "expected=not-ready-until-every-need-is-terminal-accepted"
  (let ((k (dependency-kernel *dependency-two-seed*)))
    (ok (not (member "root/a" (ready-nodes (kernel-state k) :view (session-needs-view k)) :test #'string=))
        "a with two open needs is not ready")
    (multiple-value-bind (okp line) (submit k (close-request :node "root/n1" :request "dep-n1"))
      (ok okp "the first need closes: ~A" line))
    (ok (not (member "root/a" (ready-nodes (kernel-state k) :view (session-needs-view k)) :test #'string=))
        "one settled need of two does not make a ready")
    (multiple-value-bind (okp line) (submit k (close-request :node "root/n2" :request "dep-n2"))
      (ok okp "the second need closes: ~A" line))
    (ok (member "root/a" (ready-nodes (kernel-state k) :view (session-needs-view k)) :test #'string=)
        "a is ready once every need is terminal accepted")))

;;; ------------------------------------------------------------------
;;; doing-refused-by-unmet-dependency-names-it      SPEC-WORK.md:1885,2110
;;; ------------------------------------------------------------------

(deftest "doing-refused-by-unmet-dependency-names-it" "docs/SPEC-WORK.md:1885,2110"
    "expected=doing-refused-naming-the-blocker;accepted-once-the-need-is-settled"
  (let ((k (dependency-kernel)))
    (multiple-value-bind (okp line code)
        (submit k (doing-request :node "root/a" :request "dep-start-a"))
      (ok (null okp) "a with an open need cannot be taken into doing")
      (check-equal 1 code "the refusal is a refusal")
      (ok (search "root/b" line) "the refusal names the blocking node: ~A" line))
    (check-equal :todo (node-state (kernel-state k) "root/a")
                 "a did not move and O is unchanged")
    (multiple-value-bind (okp line) (submit k (close-request :node "root/b" :request "dep-b"))
      (ok okp "the need settles: ~A" line))
    ;; Rule 1 asks for the need's own evidence to be VERIFIED, not merely
    ;; recorded, so the session installs the view it built from the settle
    ;; (nova-tools #785; Stella's HOLD on df714493).
    (refresh-needs-view k)
    (multiple-value-bind (okp line)
        (submit k (doing-request :node "root/a" :request "dep-start-a"))
      (ok okp "a starts once its need is settled: ~A" line)
      (check-equal :doing (node-state (kernel-state k) "root/a") "a is doing"))))

;;; ------------------------------------------------------------------
;;; reverse-dependency-index-is-bounded                     SPEC-WORK.md:709
;;; ------------------------------------------------------------------

(deftest "reverse-dependency-index-is-bounded" "docs/SPEC-WORK.md:709"
    "expected=one-bounded-lookup-answers-what-this-blocks"
  (let ((state (make-seed-state *dependency-seed*)))
    (multiple-value-bind (dependents visits)
        (with-instrumentation
          (values (node-dependents state "root/b") *visits*))
      (check-equal '("root/a") dependents "the reverse index answers the dependents")
      (ok (<= visits 1)
          "the answer is one bounded lookup, never a walk of O (visits=~D)"
          visits))
    (check-equal '() (node-dependents state "root/a")
                 "a node nothing needs blocks nothing")))

;;; ------------------------------------------------------------------
;;; parent-green-needs-dependencies                         SPEC-WORK.md:1951
;;; ------------------------------------------------------------------

(deftest "parent-green-needs-dependencies" "docs/SPEC-WORK.md:1951"
    "expected=parent-not-green-while-a-dependency-gate-is-unsatisfied"
  ;; The need is open: the last required child closing does not green f.
  (let ((k (dependency-kernel *dependency-parent-seed*)))
    (multiple-value-bind (okp line)
        (submit k (close-request :node "root/f/t" :request "dep-close-t"))
      (ok okp "the required leaf closes: ~A" line))
    (check-equal :o (node-branch (kernel-state k) "root/f")
                 "f is not settled while its need is open")
    (check-equal :unknown (node-state (kernel-state k) "root/f")
                 "f did not green"))
  ;; The need settled first: the same close greens the parent.
  (let ((k (dependency-kernel *dependency-parent-seed*)))
    (multiple-value-bind (okp line)
        (submit k (close-request :node "root/need" :request "dep-close-need"))
      (ok okp "the need closes: ~A" line))
    (multiple-value-bind (okp line)
        (submit k (close-request :node "root/f/t" :request "dep-close-t"))
      (ok okp "the required leaf closes: ~A" line))
    (check-equal :c (node-branch (kernel-state k) "root/f")
                 "f settles once its need and every required child are settled")))
