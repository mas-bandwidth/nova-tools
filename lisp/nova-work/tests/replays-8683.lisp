;;;; replays-8683.lisp --- the coordination-tree edge of nova-tools#321
;;;; ("As above, so below": a recursive tree of collaborating nodes).
;;;;
;;;; The issue's *Three distinct structures* keeps the coordination tree apart
;;;; from the work containment tree: every node but the root has one direct
;;;; coordinating parent (`:coordinator`), independent of its containment
;;;; `:parent`, and a reference or sibling collaboration (`:links`) creates no
;;;; second coordinating parent. This file exercises the smallest coherent slice
;;;; of that: the `:coordinator` edge, its query, and its referential integrity
;;;; (a named coordinating parent must exist and the edges must be a forest).
;;;;
;;;; It names the Delegation section of docs/SPEC-WORK.md (the v2 recursive-node
;;;; boundary of #321) and the `coordination-tree-*` replays that section names.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; coordination-tree-is-one-edge              SPEC-WORK.md:4256
;;; ------------------------------------------------------------------

(deftest "coordination-tree-is-one-edge" "docs/SPEC-WORK.md:4256"
    "expected=one-coordinating-parent-per-node;links-create-no-parent;coordination-apart-from-containment;survives-reconstruction"
  (let* ((seed '((:id "root"  :type :work-set :parent nil)
                 (:id "left"  :type :work-set :parent "root" :coordinator "root")
                 (:id "right" :type :work-set :parent "root" :coordinator "root")
                 (:id "leaf"  :type :task :parent "left" :coordinator "root"
                  :links ("right" "https://example.invalid/issues/1"))))
         (state (make-seed-state seed)))
    ;; every node has exactly one coordinating parent, read as its own edge.
    (check-equal "root" (node-coordinator state "left")
                 "left's coordinating parent")
    (check-equal "root" (node-coordinator state "right")
                 "right's coordinating parent")
    ;; the root has none.
    (check-equal nil (node-coordinator state "root")
                 "the root has no coordinating parent")
    ;; a node's coordinator is the edge it names, not its containment parent:
    ;; leaf is contained by left but coordinated by root.
    (check-equal "root" (node-coordinator state "leaf")
                 "leaf's coordinating parent is not its containment parent")
    ;; a sibling reference (:links to right) created no second coordinating parent.
    (check-equal "root" (node-coordinator state "leaf")
                 "a sibling reference created no second coordinating parent")
    ;; the containment tree still holds leaf under left: a container is in its own count.
    (check-equal 2 (node-open-count state "left")
                 "the coordination edge did not move leaf out of its containment")
    ;; the coordination edge survives a reconstruction from the canonical bytes.
    (let* ((text (canonical-string (state-canonical-form state)))
           (rebuilt (reconstruct-state text)))
      (check-equal "root" (node-coordinator rebuilt "leaf")
                   "the coordinating parent survives reconstruction")
      (check-equal nil (node-coordinator rebuilt "root")
                   "the root still has no coordinating parent after reconstruction"))))

;;; ------------------------------------------------------------------
;;; coordination-tree-integrity-refuses         SPEC-WORK.md:4256
;;; ------------------------------------------------------------------

(deftest "coordination-tree-integrity-refuses" "docs/SPEC-WORK.md:4256"
    "expected=dangling-coordinator-refused;cycle-refused;self-refused"
  ;; a :coordinator naming a node that does not exist is refused before publication.
  (handler-case
      (progn (make-seed-state '((:id "a" :type :task :coordinator "nope")))
             (fail "a dangling :coordinator was published"))
    (unsupported-input (c)
      (ok (search "rule 2" (princ-to-string c))
          "a dangling coordinating parent did not name rule 2: ~A" (princ-to-string c))))
  ;; two nodes that coordinate each other are a cycle, and the check terminates.
  (handler-case
      (sb-ext:with-timeout 10
        (make-seed-state '((:id "a" :type :task :coordinator "b")
                           (:id "b" :type :task :coordinator "a")))
        (fail "a cyclic :coordinator chain was published"))
    (sb-ext:timeout ()
      (fail "a cyclic :coordinator chain neither refused nor terminated"))
    (unsupported-input (c)
      (ok (search ":coordinator" (princ-to-string c))
          "the cycle refusal did not name :coordinator: ~A" (princ-to-string c))))
  ;; a node that coordinates itself is the same finding.
  (handler-case
      (sb-ext:with-timeout 10
        (make-seed-state '((:id "a" :type :task :coordinator "a")))
        (fail "a self-coordinating node was published"))
    (sb-ext:timeout ()
      (fail "a self-coordinating node neither refused nor terminated"))
    (unsupported-input () t)))

;;; ------------------------------------------------------------------
;;; E10-F05-01 -- Compare update-and-render tokens/wall time with
;;; manual editing                         docs/SPEC-WORK.md:7887
;;; ------------------------------------------------------------------
;;; specs:
;;;   docs/SPEC-WORK.md:7887 "tokens and wall time to update one fact in O (an
;;;   `evidence` event and a `state --to done`) and regenerate the table, against
;;;   editing the table by hand"; and SPEC-WORK.md:7894 "the honest outcomes are
;;;   a verified saving, inconclusive evidence or a failed hypothesis, each
;;;   reportable and none of them a percentage invented to fill the line."
;;;
;;; The Fixed Tables go/no-go gate (roadmap E10-F05-01) turns on that
;;; comparison. This replay drives the two halves the comparison needs -- the
;;; measured update-and-render, and the measured manual edit -- then asserts the
;;; gate's one requirement the kernel still cannot meet: reporting which of the
;;; three honest verdicts the comparison produced.

(deftest "TestE10F05CompareUpdateAndRenderTokens" "docs/SPEC-WORK.md:7887"
    "expected=verified-saving|inconclusive|failed-hypothesis"
  (let* ((rm (make-roadmap
              :id "rm-1" :axes '(:feature)
              :rows (list (make-roadmap-row :id "f-1" :axis :feature
                                            :kind :required :state :open
                                            :evidence :none :status :current))))
         (k (make-kernel
             :state (make-seed-state
                     '((:id "root" :type :work-set :parent nil :state :unknown)
                       (:id "root/t" :type :task :parent "root" :state :doing)))
             :journal (make-ordering-journal) :rev-base 1)))
    ;; the update half: one fact in O (an evidence event and a state --to done)
    ;; and a regeneration of the table, measured in token cost and wall time.
    (let ((update-tokens 0) (update-wall 0) (manual-wall 0))
      (with-instrumentation
        (let ((t0 (get-internal-real-time)))
          (multiple-value-bind (okp line)
              (submit k (close-request :node "root/t" :request "upd-1"))
            (unless okp
              (fail "updating one fact in O was refused; got ~A" line)))
          (roadmap-render rm :chat)
          (setf update-tokens (+ *parses* *replays*)
                update-wall (- (get-internal-real-time) t0))))
      ;; the manual half: the same generated bytes, edited by hand.
      (let ((t0 (get-internal-real-time)))
        (roadmap-edit-marker (roadmap-render rm :chat) "f-1" "f-1")
        (setf manual-wall (- (get-internal-real-time) t0)))
      ;; the gate. The kernel can measure both halves but exposes no verdict
      ;; that compares them and reports one of the three honest outcomes; that
      ;; is the unbuilt part of E10-F05-01.
      (fail "E10-F05-01: expected the update-and-render cost (tokens=~D wall=~D) compared ~
against the manual edit (wall=~D) to be reported as one of ~
verified-saving | inconclusive | failed-hypothesis; the kernel measured both halves ~
but offers no such comparison verdict" update-tokens update-wall manual-wall))))
