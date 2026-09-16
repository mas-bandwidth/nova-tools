;;;; replays-packets.lisp --- the eight packet replays, red first.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md it comes from (4489-4496) and
;;;; asserts the Required outcome of the acceptance table's "Required replays".

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; decision-packet-per-item-revision      SPEC-WORK.md:4489
;;; ------------------------------------------------------------------

(deftest "decision-packet-per-item-revision" "docs/SPEC-WORK.md:4489"
    "one-packet-per-item-revision;supersede-keeps-open-findings;amend-not-duplicate;empty-pulse-noop"
  (let ((store (make-packet-store)))
    ;; machinery builds one packet per item and revision: two equal records of
    ;; the same item and revision are one packet, not two.
    (record-packet store (make-decision-packet :item "acme/work/f1/t1" :revision 3
                                      :delta '((:state :doing :done))
                                      :findings '((:id "f1" :disposition :open))))
    (record-packet store (make-decision-packet :item "acme/work/f1/t1" :revision 3
                                      :delta '((:state :doing :done))
                                      :findings '((:id "f1" :disposition :open))))
    (check-equal 1 (packet-count store) "one packet per item and revision")
    ;; a newer revision supersedes it, keeping its open findings.
    (supersede-packet store (make-decision-packet :item "acme/work/f1/t1" :revision 4
                                         :delta '((:note "amended"))
                                         :findings '((:id "f2" :disposition :open))))
    (let* ((packets (find-packets store "acme/work/f1/t1"))
           (p (car packets))
           (fids (mapcar (lambda (f) (getf f :id)) (decision-packet-findings p))))
      (check-equal 1 (length packets) "a newer revision supersedes, not accumulates")
      (ok (member "f1" fids :test #'string=)
          "the newer revision keeps the superseded packet's open finding f1")
      (ok (member "f2" fids :test #'string=)
          "the newer revision still lists its own open finding f2"))
    ;; while the reader is busy the packet is amended, not duplicated.
    (let* ((p (car (find-packets store "acme/work/f1/t1")))
           (before (packet-count store)))
      (amend-packet store p :delta '((:note "busy-amend")))
      (check-equal before (packet-count store)
                   "amending while busy does not duplicate the packet")
      (ok (member '(:note "busy-amend") (decision-packet-delta p) :test #'equal)
          "the amendment landed on the one existing packet"))
    ;; an empty pulse wakes no model and re-executes nothing.
    (multiple-value-bind (status wake reexec) (pulse store '())
      (check-equal :no-op status "an empty pulse is no-op")
      (check-equal 0 wake "an empty pulse wakes no model")
      (check-equal 0 reexec "an empty pulse re-executes nothing"))))

;;; ------------------------------------------------------------------
;;; packet-is-smallest-sufficient           SPEC-WORK.md:4490
;;; ------------------------------------------------------------------

(deftest "packet-is-smallest-sufficient" "docs/SPEC-WORK.md:4490"
    "delta+rules+findings+behaviour+links;whole-diff-only-when-never-read"
  ;; a reader who has read the entry before carries the delta, not the whole.
  (let* ((reader (make-reader :id "reader-a" :recorded-head "abc123"))
         (p (build-packet reader
                          :item "acme/work/f1/t1" :revision 4
                          :delta '((:state :doing :done))
                          :rules '((:rule 5))
                          :findings '((:id "f1" :disposition :open))
                          :behaviour '(:kind :transition :evidence ("ev-1"))
                          :source-links '("https://example.com/sources/4"))))
    (ok (not (decision-packet-whole-p p)) "a returning reader gets a delta, not the whole diff")
    (check-equal '((:state :doing :done)) (decision-packet-delta p)
                 "the delta since this reader's recorded head")
    (check-equal '((:rule 5)) (decision-packet-rules p) "the rules it touches")
    (ok (eql :open (getf (car (decision-packet-findings p)) :disposition))
        "the open finding keeps its disposition")
    (check-equal '("ev-1") (getf (decision-packet-behaviour p) :evidence)
                 "the new behaviour carries evidence pointers")
    (ok (plusp (length (decision-packet-source-links p))) "links to the full sources are carried"))
  ;; a reader who never read the entry gets the whole diff.
  (let* ((reader (make-reader :id "reader-b" :recorded-head nil))
         (p (build-packet reader
                          :item "acme/work/f1/t1" :revision 4
                          :delta '((:state :unknown :done))
                          :rules '((:rule 5))
                          :findings '()
                          :behaviour '(:kind :transition :evidence ("ev-1"))
                          :source-links '("https://example.com/sources/4"))))
    (ok (decision-packet-whole-p p) "a reader who never read the entry gets the whole diff")))

;;; ------------------------------------------------------------------
;;; no-receipt-of-receipt                   SPEC-WORK.md:4491
;;; ------------------------------------------------------------------

(deftest "no-receipt-of-receipt" "docs/SPEC-WORK.md:4491"
    "one-structured-result;(reader,sha)->gate;independent-not-rerouted;receipt-of-receipt-duplicate"
  (let* ((home (make-verdict-home))
         (result (make-worker-result :reader "reader-a" :sha "sha1"
                                     :gate '(:base "b0" :head "h1" :integration "i")
                                     :outcome :ok)))
    ;; a worker returns one structured result.
    (check-equal "reader-a" (worker-result-reader result) "result names its reader")
    (check-equal "sha1" (worker-result-sha result) "result names its sha")
    (check-equal :ok (worker-result-outcome result) "result names its outcome")
    ;; a verdict is keyed (reader, sha) and a gate (base, head, integration)
    ;; in one durable home.
    (multiple-value-bind (ok-p via)
        (book-verdict home (make-verdict :reader "reader-a" :sha "sha1"
                                         :base "b0" :head "h1" :integration "i")
                      :via :coordinator)
      (ok ok-p "the first verdict is booked")
      (check-equal :coordinator via "a coordinator verdict is booked as such"))
    (let ((v (verdict-lookup home "reader-a" "sha1")))
      (ok v "a verdict keyed (reader, sha) is found in the one durable home")
      (check-equal "b0" (verdict-base v) "gate base")
      (check-equal "h1" (verdict-head v) "gate head")
      (check-equal "i" (verdict-integration v) "gate integration"))
    ;; an independent review is not re-routed through the coordinator.
    (multiple-value-bind (ok-p via)
        (book-verdict home (make-verdict :reader "reader-b" :sha "sha2"
                                         :base "b0" :head "h2" :integration "i")
                      :via :independent)
      (ok ok-p "the independent verdict is booked")
      (check-equal :independent via
                   "an independent review is booked as independent, not re-routed"))
    ;; a receipt of a receipt is refused as a duplicate.
    (multiple-value-bind (ok-p via)
        (book-verdict home (make-verdict :reader "reader-a" :sha "sha1"
                                         :base "b0" :head "h1" :integration "i")
                      :via :coordinator)
      (ok (not ok-p) "a receipt of a receipt is refused")
      (check-equal :duplicate via "the second receipt is refused as a duplicate"))))

;;; ------------------------------------------------------------------
;;; no-survives-the-hop                     SPEC-WORK.md:4492
;;; ------------------------------------------------------------------

(deftest "no-survives-the-hop" "docs/SPEC-WORK.md:4492"
    "named-refusal-with-reason-and-revision;never-silence-or-success"
  (dolist (case '((:decline . "reader declined")
                  (:refused-offer . "offer refused")
                  (:excluded-route . "route excluded")
                  (:asleep . "recipient asleep")
                  (:tripped . "node tripped")
                  (:effort-limit . "effort limit reached")))
    (let ((refusal (hop (car case) (cdr case) 7)))
      (ok (refusal-p refusal) "a refusal, never silence, survives the hop")
      (check-equal (car case) (refusal-kind refusal) "the named refusal kind")
      (check-equal (cdr case) (refusal-reason refusal) "the named refusal reason")
      (check-equal 7 (refusal-revision refusal) "the refusal's revision")))
  (ok (refusal-p (hop :decline "declined" 1)) "a decline arrives as a refusal, never silence")
  (ok (not (eq :success (refusal-kind (hop :asleep "zzz" 2))))
      "an asleep recipient is a refusal, never a success"))

;;; ------------------------------------------------------------------
;;; envelope-up-is-a-copy                   SPEC-WORK.md:4493
;;; ------------------------------------------------------------------

(deftest "envelope-up-is-a-copy" "docs/SPEC-WORK.md:4493"
    "child-fields-byte-copied;learning-in-own-words;parent-opens-dropped-evidence"
  (let* ((child (make-envelope :child-id "child-1"
                               :verdict '(:ok)
                               :result-pointer "ptr-1"
                               :evidence-events '("ev-1" "ev-2")
                               :usage-pointer "usage-1"
                               :head "deadbeef"
                               :learning "the child's distilled learning, in its own words"))
         (up (send-up child)))
    (check-equal "child-1" (envelope-child-id up) "child id byte-copied")
    (check-equal '(:ok) (envelope-verdict up) "verdict byte-copied")
    (check-equal "ptr-1" (envelope-result-pointer up) "result pointer byte-copied")
    (check-equal '("ev-1" "ev-2") (envelope-evidence-events up) "evidence events byte-copied")
    (check-equal "usage-1" (envelope-usage-pointer up) "usage pointer byte-copied")
    (check-equal "deadbeef" (envelope-head up) "exact head byte-copied")
    (check-equal "the child's distilled learning, in its own words"
                 (envelope-learning up)
                 "the child's distilled learning in its own words")
    (check-equal '("ev-1" "ev-2") (open-child-evidence up)
                 "the parent opens the child's evidence and finds what the summary dropped")))

;;; ------------------------------------------------------------------
;;; finality-rises-with-tier                SPEC-WORK.md:4494
;;; ------------------------------------------------------------------

(deftest "finality-rises-with-tier" "docs/SPEC-WORK.md:4494"
    "child-done-is-claim;parent-verifies-own-criteria;worker-success-never-moves"
  (let ((c (make-claim :node "acme/work/f1/t1" :outcome :done :by "child" :evidence '("ev-1"))))
    (ok (claim-p c) "a child's done is a claim, not a verified fact")
    (ok (verify-claim c '(:require-evidence t))
        "a claim with evidence verifies against the parent's own criteria")
    (ok (not (verify-claim (make-claim :node "acme/work/f1/t1" :outcome :done :by "child" :evidence '())
                     '(:require-evidence t)))
        "a claim with no evidence fails the parent's criteria and does not move"))
  (ok (not (tier-move :tier :worker :success-only t :seat :parent))
      "a worker's bare success claim never moves a node on a tier below the seat")
  (ok (tier-move :tier :parent :success-only nil :seat :parent)
      "the seat itself moves on its own verified decision"))

;;; ------------------------------------------------------------------
;;; escalation-is-a-packet                  SPEC-WORK.md:4495
;;; ------------------------------------------------------------------

(deftest "escalation-is-a-packet" "docs/SPEC-WORK.md:4495"
    "undecidable-rises-with-reason+revision;stale-prints-age+reread-reassign-nothing;effort-carries-coordinator-reason"
  (dolist (case '((:hold "blocked on external")
                  (:question "what does the spec mean here")
                  (:exception "unexpected crash")))
    (let ((e (escalate (car case) (cadr case) 9)))
      (ok (escalation-p e) "an undecidable rises as a packet")
      (check-equal (car case) (escalation-kind e) "the escalation kind")
      (check-equal (cadr case) (escalation-reason e) "the escalation reason")
      (check-equal 9 (escalation-revision e) "the escalation revision")))
  ;; the stale pass prints escalated-age= and reread= as information, and
  ;; reassigns nothing.
  (multiple-value-bind (line assigned)
      (stale-pass-line :age 12 :reread 3 :escalation (escalate :question "q" 1))
    (ok (search "escalated-age=12" line) "the stale pass prints escalated-age=")
    (ok (search "reread=3" line) "the stale pass prints reread=")
    (ok (not assigned) "the stale pass reassigns nothing"))
  ;; an :effort widening carries the coordinator's recorded reason.
  (let ((e (escalate :effort "widened: repo is large" 5
                     :coordinator-reason "coordinator widened the effort")))
    (check-equal "coordinator widened the effort" (escalation-coordinator-reason e)
                 "an :effort widening carries the coordinator's recorded reason")))

;;; ------------------------------------------------------------------
;;; partial-child-never-closes-parent       SPEC-WORK.md:4496
;;; ------------------------------------------------------------------

(deftest "partial-child-never-closes-parent" "docs/SPEC-WORK.md:4496"
    "outstanding=<n>;issue-open;count-and-mapping-survive-refusal"
  (let ((parent (make-parent :id "acme/work"
                             :children '("t1" "t2")
                             :issue-mapping '(("t1" . "issues/11") ("t2" . "issues/21")))))
    (observe-child parent "t1" :done)
    (observe-child parent "t2" (make-refusal :kind :decline :reason "declined" :revision 4))
    (ok (not (parent-closed-p parent)) "one done and one refused leaves the parent open")
    (check-equal "outstanding=1" (outstanding-line parent) "the parent reports outstanding=<n>")
    (ok (issue-open-p parent "issues/21") "the mapped external issue for the refused child stays open")
    (ok (not (issue-open-p parent "issues/11"))
        "the mapped external issue for the done child is closed")
    (check-equal 1 (parent-outstanding parent) "the parent's outstanding count survives the refusal")
    (check-equal '(("t1" . "issues/11") ("t2" . "issues/21"))
                 (parent-issue-mapping parent)
                 "the parent's issue mapping survives the refusal unchanged")))
