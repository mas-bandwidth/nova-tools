;;;; replays-validator-rule-witnesses.lisp --- the refusals nobody asked for.
;;;;
;;;; MEASURED BY MUTATION against dev@11aa07a7, one edit each, 335-case suite,
;;;; a fresh isolated ASDF cache per run. Each of these is one `(return-from
;;;; %validate ...)` neutralised in src/kernel.lisp, and each left the suite
;;;; 335/335 GREEN:
;;;;
;;;;   :191  `state --to doing` with a --reason that is not a string
;;;;   :212  `state --to done` on a node that is already in C
;;;;   :221  `--evidence` that is not a list
;;;;   :236  `event --kind reopen` on a node that never left O
;;;;   :239  `event --kind reopen` on a node whose state is not :done
;;;;   :295  the settle cascade's own (plusp (wnode-required-count parent))
;;;;
;;;; These are REFUSALS, so a test of the happy path can never reach them: only
;;;; a case that asks for the wrong thing and reads the line back can. Each
;;;; assertion below names the rule number and the words the refusal carries,
;;;; because a refusal whose text is not read is a refusal that can quietly
;;;; become a different refusal.
;;;;
;;;; Nothing is fixed here. These are the cases the rules never had.

(in-package #:nova-work/tests)

(defparameter *validator-seed*
  '((:id "v"        :type :work-set :parent nil  :state :unknown)
    (:id "v/t1"     :type :task     :parent "v"  :state :doing)
    (:id "v/t2"     :type :task     :parent "v"  :state :todo)
    ;; A container whose ONE member is optional, so its required-count is zero.
    (:id "v/opt"    :type :feature  :parent "v"  :state :unknown)
    (:id "v/opt/c1" :type :task     :parent "v/opt" :state :doing :required nil))
  "Two leaves under a work-set, and a feature whose only member is optional.")

(defun validator-kernel ()
  (make-kernel :state (make-seed-state *validator-seed*)))

(defun %refusal-of (k request)
  "Submit and demand a refusal, answering its line. A rule that admits what it
is supposed to refuse is the defect; a rule that refuses with the wrong words is
the next one."
  (multiple-value-bind (okp line code) (submit k request)
    (ok (not okp) "the request was ADMITTED: ~A" line)
    (check-equal 1 code "the refusal is not exit 1")
    line))

(deftest "the-transition-validator-refuses-in-its-own-words"
    "docs/SPEC-WORK.md:823,887,2659"
    "expected=doing-reason-must-be-a-string;done-on-a-node-in-C;evidence-must-be-a-list;reopen-only-from-C;reopen-only-from-done"
  ;; 1. `state --to doing --reason` that is not a string. Rule 10.
  (let ((k (validator-kernel)))
    (let ((line (%refusal-of k (list :verb :state-to-doing :node "v/t2" :by "rowan"
                                     :reason 17 :request "vr-1"
                                     :stamp "2026-09-14T12:00:00Z" :clock :tool
                                     :generation-owner "gen-4"))))
      (ok (search "rule 10" line) "the refusal carries no rule number: ~A" line)
      (ok (search "reason is not a string" line)
          "the refusal does not say what is wrong: ~A" line))
    ;; the node did not move
    (check-equal :todo (node-state (kernel-state k) "v/t2") "the refused request moved the node"))
  ;; 2. `state --to done` on a node that is already in C. Rule 10.
  (let ((k (validator-kernel)))
    (ok (submit k (list :verb :state-to-done :node "v/t1" :by "rowan" :reason "shipped"
                        :evidence '("ev-1") :request "vr-2a"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool
                        :generation-owner "gen-4"))
        "the first settle was refused")
    (check-equal :c (node-branch (kernel-state k) "v/t1") "the node did not settle")
    (let ((rev (state-revision (kernel-state k)))
          (line (%refusal-of k (list :verb :state-to-done :node "v/t1" :by "rowan"
                                     :reason "again" :evidence '("ev-2") :request "vr-2b"
                                     :stamp "2026-09-14T12:01:00Z" :clock :tool
                                     :generation-owner "gen-4"))))
      (ok (search "rule 10" line) "the refusal carries no rule number: ~A" line)
      (ok (search "v/t1 is in C" line) "the refusal does not name the branch: ~A" line)
      ;; A second settle must not reach apply-event, where it would raise
      ;; `rule 18: v/t1 is already in C` instead of answering a refusal line.
      (check-equal rev (state-revision (kernel-state k))
                   "the refused second settle advanced the revision")
      (check-equal 1 (length (state-closed-rows (kernel-state k)))
                   "the refused second settle wrote a closed-index row")))
  ;; 3. `--evidence` that is not a list. Rule 5.
  (let ((k (validator-kernel)))
    (let ((line (%refusal-of k (list :verb :state-to-done :node "v/t1" :by "rowan"
                                     :reason "shipped" :evidence "ev-1" :request "vr-3"
                                     :stamp "2026-09-14T12:00:00Z" :clock :tool
                                     :generation-owner "gen-4"))))
      (ok (search "rule 5" line) "the refusal carries no rule number: ~A" line)
      (ok (search "evidence is not a list" line)
          "the refusal does not say what is wrong: ~A" line))
    (check-equal :o (node-branch (kernel-state k) "v/t1") "the refused settle moved the node"))
  ;; 4. `event --kind reopen` on a node that never left O. Rule 10.
  (let ((k (validator-kernel)))
    (let ((line (%refusal-of k (list :verb :event-reopen :node "v/t1" :by "rowan"
                                     :reason "regressed" :request "vr-4"
                                     :stamp "2026-09-14T12:00:00Z" :clock :tool
                                     :generation-owner "gen-4"))))
      (ok (search "rule 10" line) "the refusal carries no rule number: ~A" line)
      (ok (search "never left it" line)
          "the refusal does not say the node never left O: ~A" line))
    (check-equal :o (node-branch (kernel-state k) "v/t1") "the node moved anyway"))
  ;; 5. `event --kind reopen` on a node in C whose state is not :done. The
  ;; transition table has no edge from a cancelled item back to todo.
  ;;
  ;; The fixture is built with `apply-event`, the primitive the live and the
  ;; replay paths share and the one tests/replays-8660.lisp already drives for
  ;; the same reason, because `event --kind cancel` CANNOT build it: it leaves
  ;; the node in O (nova-tools#1594, open), so a reopen after a cancel is
  ;; refused by the branch clause above and never reaches this one. That is a
  ;; defect of `cancel` and is not asserted here either way.
  (let* ((k (validator-kernel))
         (cancelled (make-work-event :kind :terminal :node "v/t1" :by "rowan"
                                     :fields (list :disposition :cancelled
                                                   :reason "not doing it")
                                     :stamp "2026-09-14T12:00:00Z" :clock :tool
                                     :request "vr-5a" :generation-owner "gen-4"
                                     :rev 90 :session-written-p t)))
    (ok (submit k (list :verb :state-to-done :node "v/t1" :by "rowan" :reason "shipped"
                        :evidence (list "ev-1") :request "vr-5s"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool
                        :generation-owner "gen-4"))
        "the settle that puts the node in C was refused")
    (apply-event (kernel-state k) cancelled)
    (check-equal :c (node-branch (kernel-state k) "v/t1") "the fixture node is not in C")
    (check-equal :cancelled (node-state (kernel-state k) "v/t1")
                 "the fixture node is not cancelled")
    (let ((line (%refusal-of k (list :verb :event-reopen :node "v/t1" :by "rowan"
                                     :reason "regressed" :request "vr-5b"
                                     :stamp "2026-09-14T12:01:00Z" :clock :tool
                                     :generation-owner "gen-4"))))
      (ok (search "rule 10" line) "the refusal carries no rule number: ~A" line)
      (ok (search "no edge from" line) "the refusal does not name the missing edge: ~A" line)
      (ok (search "to todo" line) "the refusal does not name the destination: ~A" line))))

(deftest "an-optional-member-settling-does-not-settle-its-container"
    "docs/SPEC-WORK.md:1951"
    "expected=required-count-zero-holds-the-container-open;the-member-still-settles"
  ;; The settle cascade walks one ancestor edge only when the parent has a
  ;; REQUIRED member set that is now empty: `(plusp (wnode-required-count
  ;; parent))` AND `(zerop (wnode-required-open parent))`. With the `plusp`
  ;; deleted, a container whose required-count is ZERO -- every member optional
  ;; -- satisfies `zerop required-open` trivially and settles with the first
  ;; optional member that finishes. Nothing in the suite asked, so nothing
  ;; noticed.
  (let ((k (validator-kernel)))
    (check-equal 0 (node-required-count (kernel-state k) "v/opt")
                 "the fixture's container has a required member after all")
    (ok (submit k (list :verb :state-to-done :node "v/opt/c1" :by "rowan"
                        :reason "shipped" :evidence '("ev-1") :request "vo-1"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool
                        :generation-owner "gen-4"))
        "the optional member did not settle")
    (check-equal :c (node-branch (kernel-state k) "v/opt/c1") "the member is in C")
    (check-equal :o (node-branch (kernel-state k) "v/opt")
                 "a container with NO required members settled with an optional one")
    (check-equal :o (node-branch (kernel-state k) "v")
                 "and the cascade carried on past it to the root")))

;;;; ------------------------------------------------------------------
;;;; E08-F01-04: List next valid actions and missing prerequisites
;;;; without granting authority or executing suggestions.
;;;;
;;;; docs/SPEC-WORK.md:2112 -- `ready --node X`: the work that can actually
;;;; be started under X ... every row that cannot proceed prints its exact
;;;; reason and who can resolve it, because waiting is not execution.
;;;;
;;;; The kernel slice that realises this criterion is `query ready`: it lists
;;;; the ready items (next valid actions), excludes a dependent whose need is
;;;; open (the missing prerequisite), and, through `ready-rows`, names that
;;;; missing prerequisite and its resolver -- all as a pure read that grants no
;;;; authority and executes nothing. The "next valid actions" and "missing
;;;; prerequisites" halves are pinned by replays-8681.lisp and
;;;; slice-05-durable-journal.lisp; what no test yet pins is the other half of
;;;; the sentence: that the listing is read-only.
;;;; ------------------------------------------------------------------

(defparameter *e08f01-seed*
  '((:id "v"         :type :work-set :parent nil  :state :unknown)
    (:id "v/ready"   :type :task     :parent "v"  :state :todo)
    (:id "v/blocked" :type :task     :parent "v"  :state :todo
     :deps ("v/need"))
    (:id "v/need"    :type :task     :parent "v"  :state :doing))
  "A work-set with one ready leaf, one leaf whose unmet need blocks it, and
that open need.")

(deftest "TestE08F01ListNextValidActionsAnd"
    "docs/SPEC-WORK.md:2112"
    "expected=next-valid-actions-and-missing-prerequisites-listed;the-listing-is-read-only"
  (let* ((k (make-kernel :state (make-seed-state *e08f01-seed*)))
         (rev-before (state-revision (kernel-state k)))
         (branches-before (mapcar (lambda (id) (node-branch (kernel-state k) id))
                                  '("v" "v/ready" "v/blocked" "v/need"))))
    ;; 1. It lists next valid actions and skips the dependent whose missing
    ;; prerequisite is still open.
    (let ((ready (ready-nodes (kernel-state k))))
      (ok (member "v/ready" ready :test #'string=)
          "a leaf with no open need is a next valid action and is not listed")
      (ok (not (member "v/blocked" ready :test #'string=))
          "a dependent whose prerequisite is open is listed as a next valid action"))
    ;; 2. The missing prerequisite is named, with its resolver.
    (let ((row (find "v/blocked" (ready-rows
                                  (list (make-ready-item "v/ready" :branch :o)
                                        (make-ready-item "v/blocked" :branch :o
                                                         :deps '("v/need"))
                                        (make-ready-item "v/need" :branch :o
                                                         :holder "freddy")))
                     :key #'ready-row-id :test #'equal)))
      (ok row "the blocked leaf has no ready row")
      (ok (not (ready-row-ready row)) "the blocked leaf reads as ready")
      (check-equal "blocked by v/need" (ready-row-reason row)
                   "the row does not name the missing prerequisite")
      (check-equal "freddy" (ready-row-resolver row)
                   "the row does not name who can resolve the missing prerequisite"))
    ;; 3. The listing granted no authority and executed nothing: the state is
    ;; exactly as it was before the queries.
    (check-equal rev-before (state-revision (kernel-state k))
                 "querying ready moved the revision")
    (check-equal branches-before
                 (mapcar (lambda (id) (node-branch (kernel-state k) id))
                         '("v" "v/ready" "v/blocked" "v/need"))
                 "querying ready changed a node's branch")))

;;; ------------------------------------------------------------------
;;; E11-F06-02: envelope-up-is-a-copy
;;;
;;; docs/SPEC-WORK.md:4652 (prose :4589-4591): "the child's verdict, result
;;; pointer, evidence events, usage pointer and exact head arrive byte-copied
;;; by machinery beside its distilled learning in its own words; the parent
;;; can open the child's evidence from the envelope and find what the summary
;;; dropped."
;;;
;;; The kernel's one child-result-books-upward machine is `book-receipt` /
;;; `machinery-receipt` (assignment.lisp:639-646), which realises the ADJACENT
;;; contract receipt-at-exact-head (SPEC-WORK.md:4395/4646): a HEAD and one
;;; opaque RESULT blob, nothing else. It byte-copies whatever the child hands
;;; it, but it has no first-class verdict, result-pointer, evidence, usage or
;;; distilled-learning slots, and no way for a parent to open the child's
;;; evidence and find what the summary dropped. This test states the full
;;; contract; it is RED until the envelope-up machine exists.
;;; ------------------------------------------------------------------

(deftest "TestE11F06EnvelopeUpIsACopy"
    "docs/SPEC-WORK.md:4652"
    "expected=child-verdict-result-pointer-evidence-usage-and-exact-head-byte-copied-beside-distilled-learning;parent-opens-child-evidence"
  (let* ((head "e11f060204")
         (child-result (list :verdict :done
                             :result-pointer "note:child/ev-1"
                             :evidence (list (list :id "ev-1" :kind :test))
                             :usage (list :input 11 :output 9)))
         (envelope (book-receipt head child-result)))
    ;; The exact head arrives byte-copied: the one field the receipt keeps.
    (check-equal head (machinery-receipt-head envelope)
                 "the envelope did not carry the exact head")
    ;; The verdict, result pointer, evidence events and usage pointer follow
    ;; the child byte-copied -- never retold.
    (check-equal child-result (machinery-receipt-result envelope)
                 "the envelope did not byte-copy the child's verdict, result pointer, evidence and usage")
    ;; Beside the copy, the child's distilled learning must travel in its own
    ;; words, and the parent must be able to open the child's evidence from the
    ;; envelope. The receipt keeps a head and an opaque result blob and nothing
    ;; else: no learning slot and no evidence-opening.
    (ok nil "envelope-up-is-a-copy not met: the parent received a receipt with only :head and a flat :result (the receipt-at-exact-head contract); the child's distilled learning in its own words is not carried beside them and the parent cannot open the child's evidence from the envelope")))
