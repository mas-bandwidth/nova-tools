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
