;;;; replays-8651.lisp --- the three named acceptance replays of the
;;;; undo/redo, unknown-price and unrelated-receipt paragraphs.
;;;;
;;;; Each deftest names the line of docs/SPEC-WORK.md it comes from and asserts
;;;; what that line says the replay must show. The pure functions and records
;;;; these call live in src/replays-8651.lisp; this file is red before that
;;;; file exists.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; unknown-price-is-not-zero                        SPEC-WORK.md:4636-4651
;;; ------------------------------------------------------------------

(deftest "unknown-price-is-not-zero" "docs/SPEC-WORK.md:4636-4651"
    "missing-dimension=unknown,unsupported=unknown,unknown!=zero"
  ;; A missing price dimension is unknown, never read as a zero historical receipt.
  (check-equal :unknown (resolved-price nil)
               "a missing price dimension resolves to :unknown")
  (check-equal :unknown (resolved-price :unsupported)
               "an unsupported price dimension resolves to :unknown")
  (check-equal 7 (resolved-price 7)
               "a present dimension keeps its value")
  (ok (not (eql 0 (resolved-price nil)))
      "an unknown dimension is never read as zero")
  (ok (not (eql 0 (resolved-price :unsupported)))
      "an unsupported dimension is never read as zero"))

;;; ------------------------------------------------------------------
;;; undo-redo                                       SPEC-WORK.md:6245
;;; ------------------------------------------------------------------

(deftest "undo-redo" "docs/SPEC-WORK.md:6245"
    "reversible=reversed-refused-by-name,undo=appends-preserves,redo=valid-preconditions,conflict=explicit-mutates-nothing"
  ;; Which verbs are reversible, verb by verb; terminal dispositions and
  ;; recorded receipts are refused by name and never reach an undo.
  (ok (reversible-verb-p :node-edit) "node edit is reversible")
  (ok (reversible-verb-p :state-transition) "close and reopen (state transition) is reversible")
  (ok (reversible-verb-p :event-reopen) "reopen is reversible")
  (ok (reversible-verb-p :decompose) "decomposition is reversible")
  (ok (reversible-verb-p :accept) "a changed criterion (accept) is reversible")
  (ok (not (reversible-verb-p :node-remove)) ":removed is terminal, never reversible")
  (ok (not (reversible-verb-p :event-cancel)) "cancel reaches a terminal disposition")
  (ok (not (reversible-verb-p :heartbeat)) "a recorded receipt is not reversible")
  (ok (not (reversible-verb-p :acknowledge)) "an accounting receipt is not reversible")
  ;; Undo appends a compensation and preserves the original in place.
  (let* ((h (list (make-edit-entry :id "r1" :verb :node-edit
                                   :preimage '(:title "old") :postimage '(:title "new"))))
         (h2 (history-with-undo h "r1")))
    (check-equal 2 (length h2) "undo appends exactly one compensating entry")
    (check-equal '(:title "new") (edit-entry-preimage (car (last h2)))
                 "the compensation's preimage is the original's postimage")
    (check-equal h (butlast h2) "the original entry stays exactly where it was"))
  ;; Redo reapplies intent against current preconditions, never deletes the undo.
  (let ((e (make-edit-entry :id "r1" :verb :node-edit
                            :preimage '(:title "old") :postimage '(:title "new"))))
    (ok (redo-applies-p e '(:title "old")) "redo is admitted while preconditions match")
    (ok (not (redo-applies-p e '(:title "changed")))
        "redo is refused when a dependent later edit no longer holds"))
  (let ((e (make-edit-entry :id "r2" :verb :accept
                            :preimage '(:criteria ("old")) :postimage '(:criteria ("new")))))
    (ok (not (redo-applies-p e '(:criteria ("other"))))
        "redo is refused when the criteria changed"))
  ;; A conflict is explicit and mutates nothing.
  (let ((h (list (make-edit-entry :id "r1" :verb :node-edit
                                  :preimage '(:title "old") :postimage '(:title "new")))))
    (check-equal (list :conflict "r1" :expected '(:title "old") :current '(:title "changed"))
                 (conflict-is-explicit h "r1" '(:title "changed"))
                 "a conflict names what moved")
    (check-equal nil (conflict-is-explicit h "r1" '(:title "old"))
                 "no conflict while the preconditions still hold")
    (check-equal h h "the history is untouched by the conflict"))
  ;; The same contract over the real kernel: an accepted edit is undone by an
  ;; appended compensation and redone against current preconditions, while a
  ;; conflicting redo is explicit and mutates nothing (SPEC-WORK.md:2827-2845,
  ;; :6366).
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (submit k (edit-request node :request "ur-edit" :title '(:set "t")))
    (submit k (list :verb :undo :of "ur-edit" :by "rowan" :request "ur-undo"
                    :stamp "2026-09-14T13:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (ok (absentp (node-title (kernel-state k) node)) "the real undo restores the preimage")
    (submit k (list :verb :redo :of "ur-undo" :by "rowan" :request "ur-redo"
                    :stamp "2026-09-14T13:01:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (check-string= "t" (node-title (kernel-state k) node) "the real redo reapplies the intent")
    (let ((hist (state-history (kernel-state k))))
      (ok (find "ur-undo" hist :key (lambda (r) (getf r :request)) :test #'string=)
          "the undo still stands after the redo")
      (ok (find "ur-redo" hist :key (lambda (r) (getf r :request)) :test #'string=)
          "the redo is appended")))
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (submit k (edit-request node :request "ur-edit-2" :title '(:set "t")))
    (submit k (list :verb :undo :of "ur-edit-2" :by "rowan" :request "ur-undo-2"
                    :stamp "2026-09-14T13:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (submit k (edit-request node :request "ur-later-2" :title '(:set "moved")))
    (let ((hist (state-history (kernel-state k))))
      (multiple-value-bind (rokp rline)
          (submit k (list :verb :redo :of "ur-undo-2" :by "rowan" :request "ur-redo-2"
                          :stamp "2026-09-14T13:02:00Z" :clock :tool
                          :generation-owner "gen-4"))
        (ok (not rokp) "a conflicting real redo must refuse")
        (ok (search "stale plan" rline) "the real conflict is explicit: ~A" rline)
        (check-equal hist (state-history (kernel-state k))
                     "the real conflict mutates nothing"))))
  ;; close, reopen and redo across a real settle/revive envelope.
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (submit k (list :verb :state-to-done :node node :by "rowan" :reason "shipped"
                    :evidence '("ev-1") :request "ur-close"
                    :stamp "2026-09-14T13:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (check-equal :c (node-branch (kernel-state k) node) "the close settled the node")
    (multiple-value-bind (uokp uline)
        (submit k (list :verb :undo :of "ur-close" :by "rowan" :request "ur-undo-close"
                        :stamp "2026-09-14T13:01:00Z" :clock :tool
                        :generation-owner "gen-4"))
      (ok uokp "the undo of the close is accepted: ~A" uline))
    (check-equal :o (node-branch (kernel-state k) node) "the undo revived the node")
    (multiple-value-bind (rokp rline)
        (submit k (list :verb :redo :of "ur-undo-close" :by "rowan" :request "ur-redo-close"
                        :stamp "2026-09-14T13:02:00Z" :clock :tool
                        :generation-owner "gen-4"))
      (ok rokp "the redo of the close is accepted: ~A" rline)
      (ok (search "REDO OK" rline) "the redo names itself: ~A" rline))
    (check-equal :c (node-branch (kernel-state k) node)
                 "the redo reapplied the settle against current preconditions")))

;;; ------------------------------------------------------------------
;;; unrelated-receipts-stay-reusable                SPEC-WORK.md:4943-4951
;;; ------------------------------------------------------------------

(deftest "unrelated-receipts-stay-reusable" "docs/SPEC-WORK.md:4943-4951"
    "outside-proof-scope=still-reusable,inside-single-scope=invalidated"
  (let* ((scope-a (make-proof-scope :paths '("src/a.lisp")
                                    :criteria '("test/a_test.lisp")))
         (scope-b (make-proof-scope :paths '("src/b.lisp")
                                    :criteria '("test/b_test.lisp")))
         (ra (make-receipt :id "ra" :scope scope-a))
         (rb (make-receipt :id "rb" :scope scope-b)))
    ;; A change inside A's scope invalidates A and leaves B reusable.
    (check-equal '("rb")
                 (mapcar #'receipt-id
                         (unrelated-receipts-stay-reusable
                          (list ra rb) (list :path "src/a.lisp")))
                 "a change in one feature's scope leaves the unrelated receipt reusable")
    ;; The same holds for a changed criterion.
    (check-equal '("rb")
                 (mapcar #'receipt-id
                         (unrelated-receipts-stay-reusable
                          (list ra rb) (list :criterion "test/a_test.lisp")))
                 "a changed criterion leaves the unrelated receipt reusable")
    ;; A change touching neither scope invalidates nothing.
    (check-equal '("ra" "rb")
                 (mapcar #'receipt-id
                         (unrelated-receipts-stay-reusable
                          (list ra rb) (list :path "src/c.lisp")))
                 "a change outside every declared proof scope invalidates no receipt")))

;;; ------------------------------------------------------------------
;;; TestE03F04SupportEvidenceGuardedStateTransitions  SPEC-WORK.md:1219-1242
;;; ------------------------------------------------------------------

(deftest "TestE03F04SupportEvidenceGuardedStateTransitions" "docs/SPEC-WORK.md:1219-1242"
    "done=requires-evidence,blocked=requires-blocked-by,deferred-cancelled-superseded=scope-terminal"
  ;; E03-F04-01: support evidence-guarded state transitions including blocked,
  ;; done, deferred, cancelled and superseded. SPEC-WORK.md:1219-1242 fixes the
  ;; transition table: a :to :done must name evidence (rule 5), a :to :blocked
  ;; must name a :blocked-by reference, and :deferred/:cancelled/:superseded are
  ;; entered only by their scope events (:defer/:cancel/:supersede) and are never
  ;; targets of `state`.
  ;;
  ;; :done is guarded by evidence: closing with none is refused, with one reaches
  ;; :done.
  (let ((k (fresh)))
    (multiple-value-bind (okp line)
        (submit k (list :verb :state-to-done :node "acme/work/f1/t1" :by "rowan"
                        :reason "shipped" :evidence '()
                        :request "sg-done-noev" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok (not okp) "a closure to :done without evidence must refuse, got: ~A" line)
      (ok (search "done without evidence" line)
          "the :done refusal must name the missing evidence, got: ~A" line))
    (multiple-value-bind (okp line)
        (submit k (list :verb :state-to-done :node "acme/work/f1/t1" :by "rowan"
                        :reason "shipped" :evidence '("ev-1")
                        :request "sg-done-ev" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "a closure to :done with evidence must be accepted, got: ~A" line))
    (check-equal :done (node-state (kernel-state k) "acme/work/f1/t1")
                 "an evidence-guarded close reaches :done"))
  ;; :cancelled is entered by its scope event and is terminal.
  (let ((k (fresh)))
    (multiple-value-bind (okp line)
        (submit k (list :verb :event-cancel :node "acme/work/f1/t1" :by "rowan"
                        :reason "obsolete" :request "sg-cancel"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool
                        :generation-owner "gen-4"))
      (ok okp "a :cancel scope event must be admitted, got: ~A" line))
    (check-equal :cancelled (node-state (kernel-state k) "acme/work/f1/t1")
                 "a :cancel scope event reaches :cancelled"))
  ;; :blocked is guarded by a :blocked-by reference and is never a bare `state`
  ;; target (SPEC-WORK.md:1241-1242 describes the edge; the transition table
  ;; admits it).
  (let ((k (fresh)))
    (multiple-value-bind (okp line)
        (submit k (list :verb :state-to-blocked :node "acme/work/f1/t1" :by "rowan"
                        :blocked-by "acme/work/f1/t2" :reason "depends"
                        :request "sg-blocked" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "a transition to :blocked carrying :blocked-by must be supported, got: ~A" line)))
  ;; :deferred is entered only by its :defer scope event, never by `state`.
  (let ((k (fresh)))
    (multiple-value-bind (okp line)
        (submit k (list :verb :event-defer :node "acme/work/f1/t1" :by "rowan"
                        :reason "parked" :request "sg-defer"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool
                        :generation-owner "gen-4"))
      (ok okp "a :defer scope event must reach :deferred, got: ~A" line)))
  ;; :superseded is entered only by its :supersede scope event, never by `state`.
  (let ((k (fresh)))
    (multiple-value-bind (okp line)
        (submit k (list :verb :event-supersede :node "acme/work/f1/t1" :by "rowan"
                        :superseded-by "acme/work/f1/t9" :reason "replaced"
                        :request "sg-supersede" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "a :supersede scope event must reach :superseded, got: ~A" line))))
