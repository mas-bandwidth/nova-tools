;;;; replays-8682.lisp --- the nova-work v2 slice of nova-tools#854, item 1:
;;;; verdicts are state, not events (pit stop 3, #828 D). A read's APPROVE/HOLD
;;;; is a row on the PR node, re-evaluated every tick until the PR merges or
;;;; closes; a head move makes the row stale and a read owed. This retires the
;;;; 51 approvals lost because enqueue was decided once.
;;;;
;;;; The pure data these test is the verdict row and its per-tick state, added
;;;; to src/replays-verdict-state.lisp. The live-session and enqueue wiring a
;;;; later slice owns is kept out of this file.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; verdict-is-state-on-the-node             SPEC-WORK.md:4535
;;; ------------------------------------------------------------------

(deftest "verdict-is-state-on-the-node" "docs/SPEC-WORK.md:4535"
    "expected=row-on-the-node;head-move-stales;terminal-final;approve-reconsidered-each-tick"
  ;; A read's APPROVE is a row on the PR node, not a one-shot event: the row
  ;; names the node, the reader and the head the read was made at.
  (let ((row (make-verdict-row :node "PR-777" :reader "stella"
                               :head "aaa111" :decision :approve)))
    (check-equal "PR-777" (verdict-row-node row)
                 "the verdict row lives on the node")
    (check-equal "stella" (verdict-row-reader row)
                 "the verdict row names its reader")
    (check-equal "aaa111" (verdict-row-head row)
                 "the verdict row records the head it read")
    (check-equal t (verdict-approved-p row) "the row carries APPROVE")
    (check-equal nil (verdict-hold-p row) "an APPROVE is not a HOLD")
    ;; Re-evaluated every tick until the PR merges or closes: at the same head
    ;; the row stays current and owes no read.
    (tick-verdict-row row "aaa111")
    (check-equal :current (verdict-row-state row)
                 "an unmoved head re-reads current")
    (check-equal nil (read-owed-p row) "an unmoved head owes no read")
    ;; A head move makes the row stale and a read owed. The approval is not
    ;; lost because enqueue was decided once; it is reconsidered at the new head.
    (tick-verdict-row row "bbb222")
    (check-equal :stale (verdict-row-state row)
                 "a head move makes the row stale")
    (check-equal t (read-owed-p row) "a stale row owes a read at the new head"
                 ))
  ;; A HOLD is the same row shape and owes the same read after a head move.
  (let ((row (make-verdict-row :node "PR-778" :reader "emma"
                               :head "ccc333" :decision :hold)))
    (check-equal t (verdict-hold-p row) "the row carries HOLD")
    (check-equal nil (verdict-approved-p row) "a HOLD is not an APPROVE")
    (tick-verdict-row row "ddd444")
    (check-equal :stale (verdict-row-state row) "a moved head stales a HOLD too")
    (check-equal t (read-owed-p row) "a stale HOLD owes a read"))
  ;; Terminal: once the PR merges or closes, the row is final and owes no read,
  ;; whatever the head has done since.
  (dolist (terminus '(:merged :closed))
    (let ((row (make-verdict-row :node "PR-779" :reader "stella"
                                 :head "eee555" :decision :hold
                                 :merged (eq terminus :merged)
                                 :closed (eq terminus :closed))))
      (tick-verdict-row row "fff666")
      (check-equal :final (verdict-row-state row)
                   (format nil "a ~A PR's row is final" terminus))
      (check-equal nil (read-owed-p row)
                   (format nil "a ~A PR owes no read" terminus)))))

;;; ------------------------------------------------------------------
;;; E07-F05-01 -- Exercise completed cell, new discovery,
;;; dependency/handoff and changed focus     docs/SPEC-WORK.md:7866
;;; ------------------------------------------------------------------
;;; docs/SPEC-WORK.md:7866 : "Use the method to carry the Fixed Tables work
;;; forward, including at least an inventory correction, a completed cell,
;;; newly discovered work, a dependency or handoff, and a changed focus."
;;; The roadmap criterion E07-F05-01 asks that the kernel can exercise those
;;; four moves in one workflow: settle a cell, discover new work after the
;;; baseline, carry a dependency edge that gates a dependent, and change a
;;; roadmap focus. This replay drives each through the kernel's own verbs and
;;; asserts the behaviour.

(deftest "TestE07F05ExerciseCompletedCellNewDiscovery" "docs/SPEC-WORK.md:7866"
    "expected=completed-cell;new-discovery;dependency-handoff;changed-focus"
  (let* ((seed '((:id "root"      :type :work-set :parent nil      :state :unknown)
                  (:id "root/f0"   :type :feature  :parent "root"   :state :doing)
                  (:id "root/f0/t" :type :task     :parent "root/f0" :state :doing)
                  (:id "root/f1"   :type :feature  :parent "root"   :state :doing)
                  (:id "root/f1/t" :type :task     :parent "root/f1" :state :doing
                   :deps ("root/f0/t"))
                  (:id "root/f2"   :type :feature  :parent "root"   :state :doing)
                  (:id "root/f2/t" :type :task     :parent "root/f2" :state :doing)))
         (k (make-kernel :state (make-seed-state seed)
                         :journal (make-ordering-journal) :rev-base 1)))
    ;; new discovery: a fourth feature surfaces after the baseline and stands
    ;; in O beside the settled rows.
    (multiple-value-bind (okp line code)
        (node-add k :id "root/f3" :type :feature :parent "root")
      (ok okp "the discovered work was refused: ~A" line)
      (check-equal 0 code "node add exit"))
    (check-equal :o (node-branch (kernel-state k) "root/f3")
                 "the discovered work did not enter the state")

    ;; changed focus: a roadmap over the four features, then an out-of-scope
    ;; cell narrows the focus to the applicable rows and an in-scope mark
    ;; restores it. Focus is the query over the same nodes, not a copy.
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "R"
                        :axes '(("lang"    . ("root/f0" "root/f1" "root/f2" "root/f3"))
                                ("platform" . ("linux" "mac")))
                        :reason "new" :request "rm-1")
      (ok okp "the roadmap was not created: ~A" line)
      (check-equal 0 code "roadmap create exit"))
    (check-equal 4 (roadmap-rows-count k "rm")
                 "the roadmap did not carry all four live rows")
    (check-equal 4 (roadmap-applicable-count k "rm" "linux")
                 "the focused member's applicable rows were not all four")
    (multiple-value-bind (okp line code)
        (roadmap-cell k :roadmap "rm" :coord '("root/f2" "linux")
                      :out-of-scope t :reason "out" :request "c1")
      (ok okp "an out-of-scope cell was refused: ~A" line)
      (check-equal 0 code "out-of-scope exit"))
    (ok (roadmap-cell-out-of-scope-p k "rm" '("root/f2" "linux"))
        "the out-of-scope cell was not recorded")
    (check-equal 3 (roadmap-applicable-count k "rm" "linux")
                 "the changed focus did not narrow the applicable rows")
    (check-equal 4 (roadmap-applicable-count k "rm" "mac")
                 "an unrelated member's focus moved")
    (check-equal 4 (roadmap-rows-count k "rm")
                 "a focus change moved the required set")
    (multiple-value-bind (okp line code)
        (roadmap-cell k :roadmap "rm" :coord '("root/f2" "linux")
                      :in-scope t :reason "in" :request "c2")
      (ok okp "an in-scope cell was refused: ~A" line)
      (check-equal 0 code "in-scope exit"))
    (check-equal 4 (roadmap-applicable-count k "rm" "linux")
                 "the restored focus did not bring the row back")

    ;; dependency/handoff: f1's leaf needs f0's leaf, so it is not ready while
    ;; the need is open, and becomes ready once the need settles and is done.
    (ok (not (member "root/f1/t" (ready-nodes (kernel-state k)) :test #'string=))
        "the dependent was ready while its need was open")
    (multiple-value-bind (okp line)
        (submit k (close-request :node "root/f0/t" :request "close-f0t"))
      (ok okp "settling the need was refused: ~A" line))
    (ok (member "root/f1/t" (ready-nodes (kernel-state k)) :test #'string=)
        "the dependent did not become ready once its need settled")

    ;; completed cell: settle f2's leaf, and f2 follows it to done.
    (multiple-value-bind (okp line)
        (submit k (close-request :node "root/f2/t" :request "close-f2t"))
      (ok okp "completing the cell was refused: ~A" line))
    (check-equal :done (node-disposition (kernel-state k) "root/f2")
                 "the completed cell did not read back as done")))

;;; ------------------------------------------------------------------
;;; TestE10F04CollectReleaseLevelResultsFrom     SPEC-WORK.md:7239-7240
;;; ------------------------------------------------------------------
;;; Criterion E10-F04-02 (docs/roadmaps/nova-work.sexp): "Collect
;;; release-level results from the owning fencing, recovery, paging and
;;; as-of features without re-owning their assertions." The contract is
;;; docs/SPEC-WORK.md:7239-7240 -- *before a release each suite's
;;; exact-revision results are attached* -- read with 953-954 and 2083-2090:
;;; a suite (fencing, recovery, paging, as-of) owns its own assertion, and a
;;; release COLLECTS the result by naming the suite's id in its `:deps`, read
;;; back through the reverse-dependency index, so the release never re-owns
;;; (stores a second copy of) the suite's assertion.

(deftest "TestE10F04CollectReleaseLevelResultsFrom" "docs/SPEC-WORK.md:7239-7240"
    "expected=release-collects-the-four-owning-features-by-reference;released-read-through-the-reverse-index;feature-assertion-never-re-owned"
  ;; The four owning features each keep their own assertion (a disposition and
  ;; its evidence); none stores a release version of its own.
  (let ((features (list (make-finding "acme/fencing" :disposition :done
                                      :evidence (list '(:criterion :merged :against "f1f1f1")))
                        (make-finding "acme/recovery" :disposition :done
                                      :evidence (list '(:criterion :merged :against "c0c0c0")))
                        (make-finding "acme/paging" :disposition :done
                                      :evidence (list '(:criterion :merged :against "a9a9a9")))
                        (make-finding "acme/as-of" :disposition :done
                                      :evidence (list '(:criterion :merged :against "05105f"))))))
    ;; The release task collects them by id reference: its :deps name the four
    ;; features and carry none of their assertions.
    (let ((release (make-release-task "acme/release/v0.5.0"
                                      :version "v0.5.0"
                                      :deps '("acme/fencing" "acme/recovery"
                                              "acme/paging" "acme/as-of")
                                      :branch :c
                                      :settle-stamp "2026-09-20T00:00:00Z")))
      (check-equal 4 (length (release-task-deps release))
                   "the release names all four owning features")
      (check-equal '("acme/fencing" "acme/recovery" "acme/paging" "acme/as-of")
                   (release-task-deps release)
                   "the release references the features by id, never by assertion")
      ;; Each feature's release-level result is collected from the release task
      ;; that names it, through the reverse-dependency index.
      (dolist (feature features)
        (check-equal "v0.5.0" (finding-released feature (list release))
                     (format nil "~A collects its release from the referencing task"
                             (finding-id feature)))
        (check-equal "v0.5.0" (getf (disposition-row feature (list release)) :released)
                     (format nil "~A's release-level result is read from the referencing task's version"
                             (finding-id feature)))
        ;; ...and its own assertion is untouched: the disposition row still
        ;; carries :done, the feature's own fact, not a release-owned one.
        (check-equal :done (getf (disposition-row feature (list release)) :disposition)
                     (format nil "~A keeps its own disposition" (finding-id feature))))
      ;; Without re-owning: drop the release reference and every feature's
      ;; release-level result is unresolved, because it was never stored on the
      ;; feature itself -- it was collected, not owned.
      (dolist (feature features)
        (check-equal "-" (finding-released feature '())
                     (format nil "~A owns no release version of its own" (finding-id feature)))))))
