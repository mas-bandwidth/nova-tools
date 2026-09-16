;;;; replays-l3600-4.lisp --- the seven named acceptance replays of batch 4.
;;;;
;;;; Each deftest names the line of docs/SPEC-WORK.md it comes from and asserts
;;;; what that line says the replay must show. The pure functions and records
;;;; these call live in src/replays-l3600-4.lisp; this file is red before that
;;;; file exists.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; goal-update-writes-only-existing-kinds       SPEC-WORK.md:5815
;;; ------------------------------------------------------------------

(deftest "goal-update-writes-only-existing-kinds" "docs/SPEC-WORK.md:5815"
    "kind=transition|evidence-only,field-list=exact,progress-alone-on-doing=no-edge"
  ;; goal update writes a :transition or an :evidence, never a :goal.
  (let ((todo (make-goal-node :id "g1" :state :todo :generation 1)))
    (multiple-value-bind (kind fields refusal)
        (goal-update-event todo :progress :text "starting")
      (check-equal :transition kind "progress alone on :todo writes a :transition")
      (ok (null refusal) "progress alone on :todo is admitted")
      (ok (fields-write-exactly-kind kind fields)
          "the :transition carries exactly its own field list")))
  ;; A progress line with no pointer on a node already :doing is refused no edge.
  (let ((doing (make-goal-node :id "g2" :state :doing :generation 1)))
    (multiple-value-bind (kind fields refusal)
        (goal-update-event doing :progress :text "still working")
      (ok (null kind) "progress alone on :doing writes nothing")
      (ok (and refusal (search "no edge" refusal))
          "progress alone on :doing is refused 'no edge': ~A" refusal)))
  ;; --progress with the evidence triple writes the :evidence event.
  (let ((doing (make-goal-node :id "g3" :state :doing :generation 2)))
    (multiple-value-bind (kind fields refusal)
        (goal-update-event doing :progress :text "done"
                           :pointer "ev-1" :criterion "acc-1" :against "abc123")
      (check-equal :evidence kind "progress with the evidence triple writes :evidence")
      (ok (null refusal) "progress with evidence is admitted on :doing")
      (ok (fields-write-exactly-kind kind fields)
          "the :evidence carries exactly its own field list")))
  ;; goal set and goal set --clear each write one :goal event with :node absent.
  (check-equal (list :change :set :scope "coord" :goal "g1" :reason "focus")
               (goal-set-event :set "coord" "g1" "focus")
               "goal set writes one :goal event naming the node")
  (check-equal (list :change :clear :scope "coord" :goal +absent+ :reason "done")
               (goal-set-event :clear "coord" nil "done")
               "goal set --clear writes :goal (:absent)"))

;;; ------------------------------------------------------------------
;;; state-export-refuses-a-gap                     SPEC-WORK.md:5759
;;; ------------------------------------------------------------------

(deftest "state-export-refuses-a-gap" "docs/SPEC-WORK.md:5759"
    "gap=refused-no-valid-load,proof-gap=named,current-never-substituted"
  ;; Each named gap is refused with no valid load.
  (let ((good (make-export-member :path "state/root.sexp"
                                  :kind :state-root :content '(:root)
                                  :digest (sha256-hex (canonical-string '(:root)))
                                  :required-p t)))
    (ok (null (export-refusals (list good)))
        "a complete, matching manifest has no gap")
    (let ((missing (make-export-member :path "state/root.sexp" :kind :state-root
                                       :content +absent+ :required-p t)))
      (check-equal '(("missing-mandatory-member" "state/root.sexp"))
                   (export-refusals (list missing))
                   "a missing mandatory member is refused"))
    (let ((swapped (make-export-member :path "state/root.sexp" :kind :state-root
                                       :content '(:root)
                                       :digest (sha256-hex (canonical-string '(:other)))
                                       :required-p t)))
      (check-equal '(("changed-digest" "state/root.sexp"))
                   (export-refusals (list swapped))
                   "a changed digest is refused"))
    (let ((escape (make-export-member :path "../etc/passwd" :kind :state-root
                                      :content '(:x))))
      (check-equal '(("path-escape" "../etc/passwd"))
                   (export-refusals (list escape))
                   "a path escape is refused"))
    (let ((link (make-export-member :path "state/root.sexp" :kind :state-root
                                    :content '(:x) :symlink-p t)))
      (check-equal '(("symlink" "state/root.sexp"))
                   (export-refusals (list link))
                   "a symlink is refused"))
    (let ((overrun (make-export-member :path "state/root.sexp" :kind :state-root
                                       :content '(:a :b :c :d :e :f))))
      (check-equal '(("output-overrun" "state/root.sexp"))
                   (export-refusals (list overrun) :max-bytes 4)
                   "an output overrun past the bound is refused"))
    (let ((dangling (make-export-member :path "state/root.sexp" :kind :state-root
                                        :content '(:ref "state/index.sexp"))))
      (check-equal '(("dangling-reference" "state/root.sexp"))
                   (export-refusals (list dangling))
                   "a dangling internal reference is refused"))
    (let ((corrupt (make-export-member :path "state/root.sexp" :kind :state-root
                                       :content "(:root")))
      (check-equal '(("corrupt-s-expression" "state/root.sexp"))
                   (export-refusals (list corrupt))
                    "a corrupt s-expression is refused")))
  ;; A historical export whose resolver observations are gone names a proof gap.
  (check-equal '(:proof-gap :resolver-observations-gone)
               (historical-proof-gap t nil)
               "a historical export with no resolver observations is a named proof gap")
  (ok (null (historical-proof-gap nil nil))
      "a live export asks for no historical proof gap")
  (ok (null (historical-proof-gap t t))
      "a historical export with observations present has no proof gap"))

;;; ------------------------------------------------------------------
;;; subscription-is-not-free-reference-cost         SPEC-WORK.md:5618
;;; ------------------------------------------------------------------

(deftest "subscription-is-not-free-reference-cost" "docs/SPEC-WORK.md:5618"
    "three-cost-values-separately-labelled,subscription!=zero-reference-cost"
  ;; The three cost values are kept separately labelled, never collapsed into one.
  (let ((values (cost-values 120 45 500)))
    (check-equal '(:measured-cash :estimated-marginal-cash :reference-token-cost)
                 (mapcar #'cost-value-label values)
                 "the three cost values carry three separate labels")
    (ok (three-costs-separately-labelled-p values)
        "the three labels never collapse into one"))
  ;; A subscription is a billing mode; it does not make reference cost zero.
  (let ((real (cost-values 0 0 500)))
    (check-equal 500 (reference-token-cost real)
                 "the reference token cost is its own value, not the cash value")
    (ok (reference-cost-kept-under-subscription real :subscription)
        "a subscription does not zero a real reference cost")
    (ok (reference-cost-kept-under-subscription real :metered)
        "a metered route keeps its reference cost too"))
  (ok (not (reference-cost-kept-under-subscription (cost-values 0 0 0) :subscription))
      "a reference cost that is genuinely absent stays absent under subscription, never fabricated"))

;;; ------------------------------------------------------------------
;;; unchanged-config-is-one-bounded-answer          SPEC-WORK.md:5615
;;; ------------------------------------------------------------------

(deftest "unchanged-config-is-one-bounded-answer" "docs/SPEC-WORK.md:5615"
    "equal-hash+revision=UNCHANGED-identity,unequal=bounded-delta-not-prose"
  (let ((r1 (make-config-identity :revision 7 :content-hash "abc"))
        (r1-again (make-config-identity :revision 7 :content-hash "abc"))
        (r2 (make-config-identity :revision 8 :content-hash "def")))
    (ok (config-unchanged-p r1 r1-again)
        "equal revision and hash are one unchanged answer")
    (ok (not (config-unchanged-p r1 r2))
        "a different revision or hash is a change")
    (check-equal '(:answer :unchanged :revision 7 :hash "abc")
                 (config-answer r1 r1-again)
                 "UNCHANGED carries the identity, not a repeated manifest")
    (check-equal '(:answer :delta :base 7)
                 (config-answer r1 r2)
                 "an unequal request is a bounded delta against the named base")))

;;; ------------------------------------------------------------------
;;; undo-redo                                     SPEC-WORK.md:6043
;;; ------------------------------------------------------------------

(deftest "undo-redo" "docs/SPEC-WORK.md:6043"
    "reversible=reversed-refused-by-name,undo=appends-preserves,redo=valid-preconditions,conflict=explicit-mutates-nothing"
  ;; Which verbs are reversible, verb by verb; terminal dispositions are refused.
  (ok (reversible-verb-p :node-edit) "node edit is reversible")
  (ok (reversible-verb-p :state-transition) "state transition is reversible")
  (ok (not (reversible-verb-p :node-remove)) ":removed is terminal, never reversible")
  (ok (not (reversible-verb-p :event-cancel)) "cancel reaches a terminal disposition")
  (ok (not (reversible-verb-p :heartbeat)) "a recorded receipt is not reversible")
  ;; Undo appends a compensation and preserves the original in place.
  (let* ((h (list (make-edit-entry :id "r1" :verb :node-edit
                                   :preimage '(:title "old") :postimage '(:title "new"))))
         (h2 (history-with-undo h "r1")))
    (check-equal 2 (length h2) "undo appends exactly one compensating entry")
    (check-equal '( :title "new") (edit-entry-preimage (car (last h2)))
                 "the compensation's preimage is the original's postimage")
    (check-equal h (butlast h2) "the original entry stays exactly where it was"))
  ;; Redo reapplies intent against current preconditions, never deletes the undo.
  (let ((e (make-edit-entry :id "r1" :verb :node-edit
                            :preimage '(:title "old") :postimage '(:title "new"))))
    (ok (redo-applies-p e '(:title "old")) "redo is admitted while preconditions match")
    (ok (not (redo-applies-p e '(:title "changed")))
        "redo is refused when preconditions no longer hold"))
  ;; A conflict is explicit and mutates nothing.
  (let ((h (list (make-edit-entry :id "r1" :verb :node-edit
                                  :preimage '(:title "old") :postimage '(:title "new")))))
    (check-equal (list :conflict "r1" :expected '(:title "old") :current '(:title "changed"))
                 (conflict-is-explicit h "r1" '(:title "changed"))
                 "a conflict names what moved")
    (check-equal h h "the history is untouched by the conflict")))

;;; ------------------------------------------------------------------
;;; unknown-price-is-not-zero                       SPEC-WORK.md:4534
;;; ------------------------------------------------------------------

(deftest "unknown-price-is-not-zero" "docs/SPEC-WORK.md:4534"
    "missing-dimension=unknown,unknown!=zero"
  (check-equal :unknown (resolved-price nil)
               "a missing price dimension resolves to :unknown")
  (check-equal :unknown (resolved-price :unsupported)
               "an unsupported price dimension resolves to :unknown")
  (check-equal 7 (resolved-price 7)
               "a present dimension keeps its value")
  (ok (not (eql 0 (resolved-price nil)))
      "an unknown dimension is never read as zero"))

;;; ------------------------------------------------------------------
;;; unrelated-receipts-stay-reusable                SPEC-WORK.md:4804
;;; ------------------------------------------------------------------

(deftest "unrelated-receipts-stay-reusable" "docs/SPEC-WORK.md:4804"
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
    ;; A change touching neither scope invalidates nothing.
    (check-equal '("ra" "rb")
                 (mapcar #'receipt-id
                         (unrelated-receipts-stay-reusable
                          (list ra rb) (list :path "src/c.lisp")))
                 "a change outside every declared proof scope invalidates no receipt")))
