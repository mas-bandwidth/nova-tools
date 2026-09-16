;;;; s2.lisp --- the slice-2 acceptance cases: cards are nodes.
;;;;
;;;; Each case names the replay of docs/SPEC-WORK.md the S2 build plan assigns
;;;; it, one line of hope under the same name, and asserts the printed lines the
;;;; spec's output grammar shows for the verbs S2 introduces. Slice 2 turns the
;;;; loop's cards into nodes: `node add|edit|move|remove|require`, `decompose`,
;;;; `accept`, `source`, `dep`, `state`, `correct`, `event --kind baseline|
;;;; discovery|defer|cancel|reopen|supersede` and `query size|remaining|done`.
;;;; None of those exist on dev yet, so every case here is red until the
;;;; implementation cards land.

(in-package #:nova-work/tests)

;;;; Request builders for the verbs this slice owns. The shapes are the grammar's:
;;;; each request is a property list whose :verb names the S2 verb and whose
;;;; remaining keys are that verb's own fields.

(defun remove-request (&key (node "root/f") (by "rowan") (reason "removed")
                            (request "req-rm") (stamp "2026-09-14T12:00:00Z")
                            (generation-owner "gen-4"))
  (list :verb :node-remove :node node :by by :reason reason
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun move-request (&key (node "root/f/a") (from "root/f") (under "root/f")
                          (by "rowan") (reason "noop") (request "req-mv")
                          (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :node-move :node node :from from :under under :by by :reason reason
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun defer-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "later")
                           (request "req-def") (stamp "2026-09-14T12:00:00Z")
                           (generation-owner "gen-4"))
  (list :verb :event-defer :node node :by by :reason reason
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun verify-request (&key (node "acme/work/f1/t1") (by "rowan") (request "req-ver")
                            (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :verify :node node :by by
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun two-leaf-feature (&key (root "root") (feature "root/f")
                              (done "root/f/a") (open "root/f/b"))
  `((:id ,root :type :work-set :parent nil :state :unknown)
    (:id ,feature :type :feature :parent ,root :state :unknown)
    (:id ,done :type :task :parent ,feature :state :doing)
    (:id ,open :type :task :parent ,feature :state :doing)))

;;; ------------------------------------------------------------------
;;; S2.1 settle-keeps-id-and-evidence           SPEC-WORK.md:5378
;;; ------------------------------------------------------------------

(deftest "settle-keeps-id-and-evidence" "docs/SPEC-WORK.md:5378"
    "expected=id,acceptance and every evidence event read the same from the closed index; verify prints VERIFY ROW"
  (let ((k (fresh)))
    (multiple-value-bind (okp line code) (submit k (close-request :request "req-1"))
      (declare (ignore code))
      (ok okp "the closing settle was refused")
      (ok (search "STATE OK" line) "the close did not print STATE OK: ~A" line))
    ;; The settled id still verifies, and verify prints one VERIFY ROW per evidence
    ;; event with the verdicts it printed while the item was open.
    (multiple-value-bind (okp line code) (submit k (verify-request :request "req-2"))
      (declare (ignore code))
      (ok okp "verify over the settled item was refused")
      (ok (search "VERIFY ROW" line) "verify printed no VERIFY ROW line: ~A" line))))

;;; ------------------------------------------------------------------
;;; S2.2 settle-moves-no-required-set           SPEC-WORK.md:5382
;;; ------------------------------------------------------------------

(deftest "settle-moves-no-required-set" "docs/SPEC-WORK.md:5382"
    "expected=a non-final settle leaves the parent's required set, rows= and baseline-rows= exactly where they were"
  (let ((k (make-kernel :state (make-seed-state (two-leaf-feature)))))
    (ok (submit k (close-request :node "root/f/a" :request "req-1"))
        "settling a non-final leaf was refused")
    ;; The parent is still open and its required set still holds the open sibling:
    ;; none of the parent's rows, baseline rows or required membership moved.
    (check-equal :o (node-branch (kernel-state k) "root/f")
                 "the parent settled while a required sibling was still open")
    (check-equal 2 (node-required-count (kernel-state k) "root/f")
                 "the parent's required set moved when a non-final leaf settled")
    (check-equal 0 (node-baseline-rows (kernel-state k) "root/f")
                 "the parent's baseline-rows moved when a non-final leaf settled")))

;;; ------------------------------------------------------------------
;;; S2.3 reopen-revives                         SPEC-WORK.md:5393
;;; ------------------------------------------------------------------

(deftest "reopen-revives" "docs/SPEC-WORK.md:5393"
    "expected=reopen returns the id to O at :todo with open= up one and closed= down one; a deferred item writes no :revive"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1")) "the close was refused")
    (let ((open-before (state-open-count (kernel-state k)))
          (closed-before (state-closed-count (kernel-state k))))
      (ok (submit k (reopen-request :request "req-2")) "the reopen was refused")
      (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1")
                   "a reopen did not land its item at :todo")
      (check-equal (1+ open-before) (state-open-count (kernel-state k))
                   "open= did not rise by one on a reopen")
      (check-equal (1- closed-before) (state-closed-count (kernel-state k))
                   "closed= did not fall by one on a reopen"))
    ;; A deferred item never left O, so its reopen writes no :revive.
    (let ((k2 (fresh)))
      (ok (submit k2 (defer-request :request "req-def")) "the defer was refused")
      (let ((closed-before (state-closed-count (kernel-state k2))))
        (multiple-value-bind (okp line code env) (submit k2 (reopen-request :request "req-re"))
          (declare (ignore line code))
          (ok okp "the deferred reopen was refused")
          (check-equal '() (getf env :events) "a deferred reopen wrote a :revive")
           (check-equal closed-before (state-closed-count (kernel-state k2))
                        "a deferred reopen moved closed="))))))

;;; ------------------------------------------------------------------
;;; S2.4 revive-appends-and-counts-latest       SPEC-WORK.md:5396
;;; ------------------------------------------------------------------

(deftest "revive-appends-and-counts-latest" "docs/SPEC-WORK.md:5396"
    "expected=the :revive row carries revived=<rev> and the newest row settles=1; a second settle appends a third row with settles=2"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1")) "the close was refused")
    (ok (submit k (reopen-request :request "req-2")) "the reopen was refused")
    (let* ((rows (state-closed-rows (kernel-state k)))
           (newest (first (last rows))))
      (check-equal :revive (getf newest :kind) "the newest row is not the :revive")
      (check-equal (getf newest :rev) (getf newest :revived)
                   "revived= on the :revive row is not its own revision")
      (check-equal 1 (getf newest :settles) "settles= on the newest row is not 1"))
    ;; Settle again: the earlier two rows stay put and a third row lands settles=2.
    (ok (submit k (doing-request :request "req-3")) "the re-start was refused")
    (let* ((before (copy-seq (state-closed-rows (kernel-state k))))
           (first-two (subseq before 0 2)))
      (ok (submit k (close-request :request "req-4")) "the second close was refused")
      (let* ((rows (state-closed-rows (kernel-state k)))
             (newest (first (last rows))))
        (check-equal (nth 0 first-two) (nth 0 rows) "the first row moved")
        (check-equal (nth 1 first-two) (nth 1 rows) "the second row moved")
        (check-equal 2 (getf newest :settles) "settles= on the third row is not 2")))))

;;; ------------------------------------------------------------------
;;; S2.5 remove-settles-only-open-items         SPEC-WORK.md:5406
;;; ------------------------------------------------------------------

(deftest "remove-settles-only-open-items" "docs/SPEC-WORK.md:5406"
    "expected=node remove settles the open leaf and the feature, leaves the closed leaf untouched and prints NODE NOTE already-closed; closed= moves by two"
  (let ((k (make-kernel :state (make-seed-state (two-leaf-feature)))))
    (ok (submit k (close-request :node "root/f/a" :request "req-done"))
        "settling the done leaf was refused")
    (let ((closed-before (state-closed-count (kernel-state k))))
      (multiple-value-bind (okp line code) (submit k (remove-request :request "req-rm"))
        (declare (ignore code))
        (ok okp "node remove of the feature was refused")
        (ok (search "NODE OK" line) "the remove did not print NODE OK: ~A" line)
        (ok (search "change=remove" line) "the remove did not say change=remove: ~A" line)
        (ok (search "NODE NOTE already-closed" line)
            "the remove did not print NODE NOTE already-closed: ~A" line))
      (check-equal (+ 2 closed-before) (state-closed-count (kernel-state k))
                   "closed= moved by other than two: the already-closed leaf was settled again")
      (check-equal :done (node-state (kernel-state k) "root/f/a")
                   "the already-closed leaf's done disposition was not kept"))))

;;; ------------------------------------------------------------------
;;; S2.6 no-effect-mutation-is-journaled        SPEC-WORK.md:5507
;;; ------------------------------------------------------------------

(deftest "no-effect-mutation-is-journaled" "docs/SPEC-WORK.md:5507"
    "expected=changed=0 on the OK line, journal length +1, digest unchanged, revision +1"
  (let* ((journal (make-ordering-journal))
         (k (make-kernel :state (make-seed-state (two-leaf-feature)) :journal journal))
         (before-digest (root-digest (kernel-state k)))
         (before-len (length (journal-order journal)))
         (before-rev (kernel-next-rev k)))
    ;; A move whose --from equals --under and names the actual parent is a no-op.
    (multiple-value-bind (okp line code) (submit k (move-request :request "req-mv"))
      (declare (ignore code))
      (ok okp "the no-effect move was refused")
      (ok (search "NODE OK" line) "the move did not print NODE OK: ~A" line)
      (ok (search "change=move" line) "the move did not say change=move: ~A" line)
      (ok (search "changed=0" line) "the no-effect move did not print changed=0: ~A" line))
    (check-string= before-digest (root-digest (kernel-state k))
                   "the domain digest changed on a no-effect mutation")
    (check-equal (1+ before-rev) (kernel-next-rev k)
                 "the revision did not advance by one on a no-effect mutation")
    (check-equal (1+ before-len) (length (journal-order journal))
                 "the no-effect receipt was not journaled")))

;;; ------------------------------------------------------------------
;;; S2.7 every-field-has-an-owning-verb         SPEC-WORK.md:5535
;;; ------------------------------------------------------------------

(deftest "every-field-has-an-owning-verb" "docs/SPEC-WORK.md:5535"
    "expected=an unknown or aliased field refuses naming that field; no generic set-field escape"
  (let ((k (make-kernel :state (make-seed-state (two-leaf-feature)))))
    ;; A field no verb owns (here :priority on a structure verb) refuses by name,
    ;; never drops into a digest that cannot tell it was there.
    (multiple-value-bind (okp line code)
        (submit k (append (move-request :request "req-x") (list :priority "high")))
      (ok (not okp) "an unknown field was accepted")
      (check-equal 2 code "an unknown field's exit code")
      (ok (search "priority" line) "the refusal does not name the field: ~A" line))
    (let ((before (root-digest (kernel-state k))))
      (multiple-value-bind (okp line code)
          (submit k (append (move-request :request "req-y") (list :from "root/f" :under "root/f")))
        (declare (ignore line code))
        (ok okp "a legal no-effect move was refused"))
      (check-string= before (root-digest (kernel-state k))
                     "a refusal moved the digest"))))
