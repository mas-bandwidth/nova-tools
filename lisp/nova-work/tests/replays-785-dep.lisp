;;;; replays-785-dep.lisp --- `dep --add` and `dep --remove` (nova-tools #785).
;;;;
;;;; Rule 6 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:4993-5031, with its replay at :6972 and its line at
;;;; :6031:
;;;;
;;;;   DEP OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|->
;;;;   change=<add|remove> need=<id> met=<true|false> unmet=<n>
;;;;   needs-broken=<true|false> changed=<n> emitted=<bytes>
;;;;
;;;; `met=` is the named need's; `unmet=` and `needs-broken=` are the node's,
;;;; read AFTER the change.

(in-package #:nova-work/tests)

(defparameter *dep-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/m" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/t" :type :task     :parent "acme/work" :state :todo)
    ;; a second repository work set of the same O
    (:id "other/work"   :type :work-set :parent nil          :state :unknown)
    (:id "other/work/x" :type :task     :parent "other/work" :state :doing))
  "D needs the open N. M and T are unattached; other/work/x is a need under
another repository work set of the same O.")

(defun dep-kernel (&optional (seed *dep-seed*))
  (make-kernel :state (make-seed-state seed)
               :journal (make-ordering-journal) :rev-base 1))

(defun dep-add (k node need &key (as "rowan") (reason "the new order")
                                 (request (format nil "dep-add-~A-~A" node need)))
  (dep-edit k :node node :change :add :need need :as as :reason reason
              :request request :stamp "2026-09-16T14:00:00Z"))

(defun dep-remove (k node need &key (as "rowan") (reason "it does not need it")
                                    (request (format nil "dep-rm-~A-~A" node need)))
  (dep-edit k :node node :change :remove :need need :as as :reason reason
              :request request :stamp "2026-09-16T15:00:00Z"))

;;; ------------------------------------------------------------------
;;; an-edge-added-under-work-flags-and-every-way-past-a-need-is-recorded
;;;                                              SPEC-WORK.md:4993, :6972
;;; ------------------------------------------------------------------

(deftest "an-edge-added-under-work-flags-and-every-way-past-a-need-is-recorded"
    "docs/SPEC-WORK.md:4993"
    "expected=the-edge-is-true-whether-or-not-it-is-convenient-and-there-is-no-force"
  ;; `dep --add M` of an open M onto a leased D: written, flagged, stops nothing
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (submit k (list :verb :state-to-done
                                                    :node "acme/work/n" :by "rowan"
                                                    :reason "merged" :evidence '("ev-1")
                                                    :request "done-n"
                                                    :stamp "2026-09-16T12:00:00Z"
                                                    :clock :tool
                                                    :generation-owner "gen-1"))
      (ok okp "N settles first so D can be leased: ~A" line))
    (take-lease k "acme/work/d" "emma")
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/m")
      (ok okp "the edge is admitted under work: ~A" line)
      (check-equal 0 code "at exit 0")
      (ok (search "change=add" line) "change=add: ~A" line)
      (ok (search "need=acme/work/m" line) "need= names it: ~A" line)
      (ok (search "met=false" line) "met= is the need's own: ~A" line)
      (ok (search "unmet=1" line) "unmet= is the node's, after the change: ~A" line)
      (ok (search "needs-broken=true" line) "and an engaged node is flagged: ~A" line))
    ;; it stops nothing
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "the lease stands")
    (check-equal :todo (node-state (kernel-state k) "acme/work/d")
                 "and nothing is reopened or moved")
    (check-equal '("acme/work/n" "acme/work/m")
                 (node-deps (kernel-state k) "acme/work/d")
                 "the edge is on the node, in the order it was added"))
  ;; onto an unengaged :todo node it prints needs-broken=false
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (dep-add k "acme/work/t" "acme/work/m")
      (ok okp "admitted: ~A" line)
      (ok (search "needs-broken=false" line)
          "an unengaged todo node is simply not needs-met: ~A" line)))
  ;; remove, admit, add back: three events with their authors, the lease
  ;; standing, and needs-broken=true from the third
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (dep-remove k "acme/work/d" "acme/work/n")
      (ok okp "the override is a recorded edit of the edge: ~A" line)
      (ok (search "change=remove" line) "change=remove: ~A" line)
      (ok (search "unmet=0" line) "and it takes effect at once: ~A" line))
    (ok (null (handler-case (progn (take-lease k "acme/work/d" "emma") nil)
                (unsupported-input (c) (unsupported-input-what c))))
        "the node is admitted past the need it no longer has")
    (multiple-value-bind (okp line) (dep-add k "acme/work/d" "acme/work/n")
      (ok okp "and the edge goes back: ~A" line)
      (ok (search "needs-broken=true" line)
          "from the third event the node reads needs-broken=true: ~A" line))
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "the lease stands through all three")
    ;; Three recorded acts: the remove, the take, the add back. Two are `dep`
    ;; `:structure` events and the third is the lease -- "Remove, admit, add
    ;; back is therefore not silent: it is three events with their authors"
    ;; (SPEC-WORK.md:5023).
    (check-equal 2 (count-if (lambda (e) (eq :dep (getf e :verb)))
                             (node-structure-log (kernel-state k) "acme/work/d"))
                 "the two edge edits are on the record")
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "and the take between them is the third act")
    (dolist (e (node-structure-log (kernel-state k) "acme/work/d"))
      (check-string= "rowan" (getf e :by) "each names its author")
      (ok (stringp (getf e :reason)) "and each carries its required --reason")))
  ;; the three refusals: each exit 1, nothing written
  (let* ((k (dep-kernel))
         (before (node-deps (kernel-state k) "acme/work/d")))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/d")
      (ok (not okp) "a node that needs itself is a cycle of length one")
      (check-equal 1 code "exit 1")
      (check-string= "DEP FAIL node=acme/work/d: rule 3: acme/work/d needs itself" line
                     "refused rule 3"))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/n" "acme/work/d")
      (ok (not okp) "an edge closing a two-node cycle is refused")
      (check-equal 1 code "exit 1")
      (ok (search "rule 3" line) "refused rule 3: ~A" line))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/nowhere")
      (ok (not okp) "an id naming nothing is refused")
      (check-equal 1 code "exit 1")
      (check-string= "DEP FAIL node=acme/work/d: rule 2: dangling" line
                     "refused rule 2: dangling"))
    (check-equal before (node-deps (kernel-state k) "acme/work/d")
                 "and nothing was written by any of the three"))
  ;; a need under another repository work set of the same O is admitted
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (dep-add k "acme/work/d" "other/work/x")
      (ok okp "rule 1 asks nothing about where a need lives: ~A" line)
      (ok (search "need=other/work/x" line) "and the edge names it: ~A" line))))

;;; ------------------------------------------------------------------
;;; what there is not: an unrecorded road or a flag
;;;                                              SPEC-WORK.md:5021
;;; ------------------------------------------------------------------

(deftest "a-dep-edit-refuses-to-be-unrecorded" "docs/SPEC-WORK.md:5021"
    "expected=a-required-reason-an-author-and-no-force"
  (let ((k (dep-kernel)))
    ;; --reason is required: exit 2, nothing written
    (multiple-value-bind (okp line code)
        (dep-edit k :node "acme/work/d" :change :add :need "acme/work/m"
                    :as "rowan" :reason nil :request "r1"
                    :stamp "2026-09-16T14:00:00Z")
      (ok (not okp) "a dep edit with no reason is refused")
      (check-equal 2 code "at exit 2, an invocation that cannot be read")
      (ok (search "--reason" line) "and the line names the field: ~A" line))
    ;; so is one with no author
    (multiple-value-bind (okp line code)
        (dep-edit k :node "acme/work/d" :change :add :need "acme/work/m"
                    :as nil :reason "because" :request "r2"
                    :stamp "2026-09-16T14:00:00Z")
      (ok (not okp) "a dep edit with no author is refused")
      (check-equal 2 code "at exit 2")
      (ok (search "--as" line) "and the line names the field: ~A" line))
    (check-equal '("acme/work/n") (node-deps (kernel-state k) "acme/work/d")
                 "neither refusal wrote anything")
    ;; removing an edge that is not there is refused rather than silently ok
    (multiple-value-bind (okp line code) (dep-remove k "acme/work/d" "acme/work/m")
      (ok (not okp) "removing an edge the node does not hold is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "no such edge" line) "naming what is missing: ~A" line))))
