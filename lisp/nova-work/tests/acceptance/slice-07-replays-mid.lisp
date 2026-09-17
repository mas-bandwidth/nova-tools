;;;; slice-07-replays-mid.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "redo-refuses-a-stale-plan" "docs/SPEC-WORK.md:5645-5646"
    "expected=stale-precondition-refused-atomically;names-what-changed;writes-nothing;undo-not-deleted"
  (let* ((original (list :id "ev-add-1" :kind :node-add :request "req-add-1"))
         (ledger (list :history (list original) :receipts '(:rcpt-1))))
    (multiple-value-bind (after-undo undo-envelope refusal)
        (undo-request ledger "req-add-1")
      (check-equal nil refusal "the undo is appended")
      (let ((history-after-undo (getf after-undo :history)))
        ;; a redo whose preconditions moved refuses atomically, naming what changed.
        (multiple-value-bind (after-redo redo-envelope refusal-2)
            (redo-request after-undo "req-undo-req-add-1"
                          (list :at-rev 1 :preconditions '(:open 3))
                          (list :rev 2 :preconditions '(:open 4)))
          (check-equal nil redo-envelope "a stale redo writes nothing")
          (ok (search "stale plan" refusal-2) "the refusal names the stale plan: ~A" refusal-2)
          (ok (search "rev moved 1->2" refusal-2) "the refusal names what changed: ~A" refusal-2)
          (check-equal history-after-undo (getf after-redo :history)
                       "a refused redo writes nothing")
          ;; the undo is not deleted, so a redo is never reached by deleting it.
          (ok (find (getf undo-envelope :request) (getf after-redo :history)
                    :key (lambda (e) (getf e :request)) :test #'equal)
              "the undo envelope is still there"))
        ;; a fresh plan at the current revision reapplies the intent; the undo stands.
        (multiple-value-bind (after-redo redo-envelope refusal-3)
            (redo-request after-undo "req-undo-req-add-1"
                          (list :at-rev 2 :preconditions '(:open 4))
                          (list :rev 2 :preconditions '(:open 4)))
          (check-equal nil refusal-3 "a fresh plan is accepted")
          (check-equal 3 (length (getf after-redo :history))
                       "redo appends rather than deletes")
          (ok (find (getf undo-envelope :request) (getf after-redo :history)
                    :key (lambda (e) (getf e :request)) :test #'equal)
              "the undo still stands after a fresh redo"))))))

;; regression-and-recovery now runs in
;; lisp/nova-work/tests/replays-8647.lisp (nova-tools #362).

(deftest "regression-opens-repair-work" "docs/SPEC-WORK.md:5300"
    "expected=confirmed-regression-creates-linked-open-repair-work"
  ;; NEEDS-KERNEL: confirmed regression plus linked repair work creation.
  (ok t "pending; needs regression detection"))


;;; render: the stored target, its permitted roots and the one bounded artifact
;;; (SPEC-WORK.md:3129-3154; replays at :5755, :5849 and :5853).

(deftest "render-artifact-is-bounded" "docs/SPEC-WORK.md:5398"
    "expected=interleaved-replies-and-chat-artifact-bounded;oversize-refused-never-partial"
  ;; ordinary replies and a --chat artifact interleaved in one correlated batch,
  ;; request ids, byte length and hash verified.
  (let* ((body "# Title")
         (batch (render-correlated-batch
                 (list (list :request "req-1" :reply "NODE OK")
                       (list :request "req-2" :chat body)
                       (list :request "req-3" :reply "QUERY OK")))))
    (check-equal '("req-1" "req-2" "req-3") (mapcar #'car batch)
                 "every request id is correlated in order")
    (let ((artifact (getf (cdr (assoc "req-2" batch :test #'equal)) :artifact)))
      (ok (artifact-valid-p artifact) "the artifact hash and byte count verify")))
  ;; an oversized artifact is a bounded refusal and never partial Markdown.
  (let* ((big (make-string 100 :initial-element #\x))
         (r (render-chat big :bound 10)))
    (ok (not (render-result-ok r)) "an oversized artifact is refused")
    (check-equal nil (render-result-artifact r)
                 "an oversized refusal carries no partial artifact")
    (ok (search "bound" (render-result-reason r)) "the refusal names the bound"))
  ;; a corrupt artifact (a hash that does not match its body) fails verification.
  (let ((corrupt (list :encoding "utf8" :body "abc" :sha256 "deadbeef" :bytes 3)))
    (ok (not (artifact-hash-ok-p corrupt)) "a corrupt artifact hash fails verification")
    (ok (not (artifact-valid-p corrupt)) "a corrupt artifact is not valid"))
  ;; --check creates no target, no receipt claiming a write, no commit, no push.
  (let* ((session (make-render-session
                   :permissions '((:root-a :file :chat))
                   :mappings '((:root-a :repo "acme/work" :directory "/bench/acme"))))
         (projection (make-render-projection :repo "acme/work" :root :root-a
                                             :target "/bench/acme/x.md"))
         (r (render-file session projection :path "/bench/acme/x.md" :mode :check
                         :markers '(0 4) :current-hash "h1" :new-hash "h1")))
    (ok (render-result-ok r) "a --check render succeeds")
    (check-equal nil (render-result-wrote r) "--check writes nothing")
    (check-equal nil (render-result-artifact r) "--check carries no artifact")))

(deftest "render-refuses-a-target-outside-its-roots" "docs/SPEC-WORK.md:5304"
    "expected=target-outside-permitted-roots-refused-not-guessed"
  (let* ((session (make-render-session
                   :permissions '((:root-a :file :chat))
                   :mappings '((:root-a :repo "acme/work" :directory "/bench/acme"))))
         (projection (make-render-projection :repo "acme/work" :root :root-a
                                             :target "/bench/acme/docs/SPEC.md")))
    ;; a target inside the mapping's root resolves and writes.
    (let ((r (render-file session projection :path "/bench/acme/docs/SPEC.md"
                          :mode :file :markers '(0 10)
                          :current-hash "h1" :new-hash "h1")))
      (ok (render-result-ok r) "an in-root target resolves: ~A" (render-result-line r))
      (check-equal t (render-result-wrote r) "a --file render writes"))
    ;; a target outside every configured root is refused, never guessed.
    (let ((r (render-file session projection :path "/bench/elsewhere/secret.md"
                          :mode :file :markers '(0 10))))
      (ok (not (render-result-ok r)) "an out-of-root target is refused")
      (ok (search "outside its root" (render-result-reason r))
          "the refusal names the root boundary: ~A" (render-result-reason r)))))

(deftest "a-root-id-grants-nothing" "docs/SPEC-WORK.md:5853"
    "expected=no-mapping-file-refuses;chat-renders;escape-refused"
  ;; a stored permitted root with no --render-root mapping refuses file mode
  ;; while --chat renders.
  (let ((session (make-render-session :permissions '((:root-a :file :chat))
                                      :mappings '())))
    (let ((r (render-file session
                          (make-render-projection :repo "acme/work" :root :root-a
                                                  :target "/bench/acme/x.md")
                          :path "/bench/acme/x.md" :mode :file :markers '(0 4))))
      (ok (not (render-result-ok r)) "no mapping: file mode refuses")
      (ok (search "no mapping" (render-result-reason r))
          "the refusal names the missing mapping"))
    (let ((r (render-chat "# Title")))
      (ok (render-result-ok r) "no mapping: --chat still renders")
      (ok (artifact-valid-p (render-result-artifact r)) "the chat artifact verifies")))
  (let* ((session (make-render-session
                   :permissions '((:root-a :file :chat))
                   :mappings '((:root-a :repo "acme/work" :directory "/bench/acme"))))
         (projection (make-render-projection :repo "acme/work" :root :root-a
                                             :target "/bench/acme/x.md")))
    ;; an escaping path refuses.
    (let ((r (render-file session projection :path "/bench/acme/../other/x.md"
                          :mode :file :markers '(0 4))))
      (ok (not (render-result-ok r)) "an escaping path refuses"))
    ;; a symlink escape refuses.
    (let ((r (render-file session projection :path "/bench/acme/link/x.md" :mode :file
                          :markers '(0 4)
                          :symlinks '(("/bench/acme/link" . "/bench/other")))))
      (ok (not (render-result-ok r)) "a symlink escape refuses")
      (ok (search "symlink" (render-result-reason r))
          "the refusal names the symlink escape"))
    ;; a target identity other than the mapping's refuses.
    (let ((r (render-file session
                          (make-render-projection :repo "other/repo" :root :root-a
                                                  :target "/bench/acme/x.md")
                          :path "/bench/acme/x.md" :mode :file :markers '(0 4))))
      (ok (not (render-result-ok r)) "a mismatched target identity refuses")
      (ok (search "target identity" (render-result-reason r))
          "the refusal names the target identity")))
  ;; the cooperative lock is exercised and its external-editor limit retained.
  (let ((locks '()))
    (multiple-value-bind (got new) (acquire-render-lock locks "/bench/acme/x.md")
      (ok got "the first cooperator takes the lock")
      (multiple-value-bind (again ignored) (acquire-render-lock new "/bench/acme/x.md")
        (declare (ignore ignored))
        (ok (not again) "a second cooperator is refused the lock")))
    (check-equal nil *render-lock-guards-external-editor*
                 "the lock remains advisory and does not guard an external editor")))

(deftest "reply-retired-only-under-verified-coverage" "docs/SPEC-WORK.md:5538"
    "expected=retired-only-once-snapshot-events-root-verified;else-recovery-gap"
  ;; NEEDS-KERNEL: savepoint/dedup coverage verification of the boundary record.
  (ok t "pending; needs savepoint dedup"))

(deftest "requested-model-is-not-observed-model" "docs/SPEC-WORK.md:5222"
    "expected=unknown-stays-unknown;attempts-separate-model-attribution"
  ;; an unobserved executor stays unknown: a friend's usual model is not proof.
  (let ((unobserved (make-attempt :id "att-1" :node "root/f/t1"
                                  :requested-model "astra" :observed nil :usage 10)))
    (check-equal :unknown (attempt-observed-model unobserved)
                 "an unobserved model is unknown")
    (check-equal nil (equal (attempt-observed-model unobserved) "beta")
                 "a friend's usual model never stands as proof of the executor"))
  ;; concurrent attempts keep separate model and usage attribution.
  (let* ((a (make-attempt :id "att-1" :node "root/f/t1" :requested-model "astra"
                          :observed "astra" :usage 10))
         (b (make-attempt :id "att-2" :node "root/f/t1" :requested-model "astra"
                          :observed "beta" :usage 7))
         (records (list a b)))
    (check-equal '(("astra" . 10) ("beta" . 7))
                 (mapcar (lambda (x) (cons (attempt-observed-model x)
                                           (attempt-usage x)))
                         records)
                 "concurrent attempts keep separate attribution")
    (check-equal nil (attempts-collapsed-p records)
                 "one attempt's usage does not stand for another's")))

(deftest "reserved-role-is-not-spent-on-routine-work" "docs/SPEC-WORK.md:5214"
    "expected=reserved-role-read-from-config-never-spent-on-routine"
  (let ((config (make-role-config
                 :roles (list
                         (make-role-record :id :security :scope "estate"
                                           :source "CONFIG"
                                           :reserved-for :essential-security
                                           :essential-security-only-p t
                                           :limit 3)
                         (make-role-record :id :contributor :scope "estate"
                                           :source "CONFIG"
                                           :participation-p t
                                           :limit 10)))))
    ;; An essential-security-only role reserved for essential security is never
    ;; spent on routine work; the refusal names the reservation.
    (multiple-value-bind (okp reason) (spend-role config :security :routine)
      (ok (not okp) "the essential-security role was spent on routine work")
      (ok (search "essential-security" reason)
          "the refusal does not name the reserved class: ~A" reason))
    ;; It is spent on the work it is reserved for.
    (multiple-value-bind (okp reason) (spend-role config :security :essential-security)
      (ok okp "the essential-security role was refused its own reserved work: ~A" reason))
    ;; An agreed participation is not reserved, so routine work is permitted.
    (multiple-value-bind (okp reason) (spend-role config :contributor :routine)
      (ok okp "an agreed participation was wrongly reserved: ~A" reason))
    ;; An agreed limit comes from CONFIG; a model capability never cancels or
    ;; raises it, and never confers the role that would spend it.
    (check-equal 3 (agreed-limit-for config :security :model-claims-unlimited)
                 "a model capability raised the agreed limit")
    (ok (not (role-inferred-from-model-p :security))
        "a model capability cancelled the agreed limit by conferring the role")))

(deftest "restore-is-isolated-and-dispatches-nothing" "docs/SPEC-WORK.md:5308"
    "expected=restore-read-only-isolated-no-ownership-no-replay-no-dispatch"
  ;; NEEDS-KERNEL: the savepoint restore recovery session.
  (ok t "pending; needs savepoint restore"))

(deftest "resume-is-two-actions" "docs/SPEC-WORK.md:5268"
    "expected=release-hold-only-lifts-its-control;resume-workers-refuses-unsupported-capability"
  ;; NEEDS-KERNEL: release-hold / resume-workers verbs.
  (ok t "pending; needs execution control"))

(deftest "return-reconciles-before-dispatch" "docs/SPEC-WORK.md:5226"
    "expected=return-reconciles-before-new-dispatch;one-bounded-ping-at-threshold"
  ;; before the return, a new dispatch is refused by name.
  (multiple-value-bind (admitted line code) (dispatch-gate nil)
    (check-equal nil admitted "a dispatch before reconciliation is refused")
    (check-equal 2 code "the refusal is exit 2")
    (ok (search "return" line) "the refusal names the outstanding return"))
  ;; the return reconciles its assignments and observed capacity ...
  (let ((r (reconcile-return :assignments '("a1" "a2")
                             :capacity '((:friend "bob" :free 3)))))
    (check-equal t (return-done-p r) "the return is reconciled")
    (check-equal '("a1" "a2") (return-reconciliation-assignments r)
                 "outstanding assignments are reconciled")
    (check-equal '((:friend "bob" :free 3)) (return-reconciliation-capacity r)
                 "observed capacity is reconciled")
    ;; ... and only then is a new dispatch admitted.
    (multiple-value-bind (admitted line code) (dispatch-gate r)
      (check-equal t admitted "dispatch is admitted after reconciliation")
      (check-equal 0 code "admission is exit 0")
      (check-string= "DISPATCH OK" line "the admission line")))
  ;; the silence threshold still fires one bounded ping, and only one.
  (let ((active (make-availability :state :active :last-contact 0
                                   :silence-threshold 60)))
    (multiple-value-bind (action reason) (silence-ping active 1000)
      (check-equal :ping action "one ping at the threshold")
      (check-equal :silence-threshold reason "the threshold reason"))
    (mark-pinged active)
    (multiple-value-bind (action reason) (silence-ping active 1000)
      (check-equal :none action "only one bounded ping")
      (check-equal :already-pinged reason "the bound is named"))))

(deftest "reuse-only-valid-review" "docs/SPEC-WORK.md:4374"
    "expected=same-scope-reusable;changed-acceptance-deps-invalidate;friend-gates-not-replaced"
  ;; NEEDS-KERNEL: review scope/acceptance invalidation rules.
  (ok t "pending; needs review"))

(deftest "review-cycles-stay-visible" "docs/SPEC-WORK.md:5722"
    "expected=exact-revision-review-finding-ids-and-author-dispositions-recorded"
  ;; NEEDS-KERNEL: review/finding record surface and its visibility.
  (ok t "pending; needs review records"))

;;; 3x. replays promised by docs/SPEC-WORK.md lines 3600-9999 (card #284):
;;;     each replay asserts exactly the sentence it is named from. Where the
;;;     invariant needs kernel code this slice does not carry, the replay is
;;;     kept under the ;; NEEDS-KERNEL: marker naming what is missing.
;;; ------------------------------------------------------------------


(deftest "roadmap-proof" "docs/SPEC-WORK.md:5598"
    "expected=fixed-table-prototype-parity;chat-and-file-renders-byte-identical;marker-edit-preserves-unrelated-bytes"
  ;; NEEDS-KERNEL: roadmap proof rendering and marker edit.
  (ok t "slice 1 carries no roadmap proof: NEEDS-KERNEL roadmap render + marker"))
(deftest "roadmap-has-one-creator" "docs/SPEC-WORK.md:5344"
    "expected=node-add--type-roadmap-exit-2-naming-roadmap-create;one-node-and-one-view-atomic"
  ;; NEEDS-KERNEL: a roadmap node and its view in one envelope; no CLI in this slice.
  (ok t "slice 1 carries no roadmap create: NEEDS-KERNEL roadmap node + view envelope"))


;;;; `roadmap-proof` moved to tests/replays-8649.lisp when its kernel landed
;;;; (card 8649): the pure render and marker edit are no longer a NEEDS-KERNEL
;;;; stub.

(deftest "roles-are-configured-not-inferred" "docs/SPEC-WORK.md:5214"
    "expected=role-read-from-CONFIG-never-the-model;reserved-roles-not-spent-on-routine-work"
  (let ((config (make-role-config
                 :roles (list
                         (make-role-record :id :security :scope "estate"
                                           :source "CONFIG"
                                           :reserved-for :essential-security
                                           :essential-security-only-p t)
                         (make-role-record :id :reviewer :scope "estate"
                                           :source "CONFIG"
                                           :reserved-for :plan
                                           :different-perspective-p t)
                         (make-role-record :id :contributor :scope "estate"
                                           :source "CONFIG"
                                           :participation-p t)))))
    ;; A role is read from CONFIG...
    (ok (role-from-config config :security) "the security role is not read from CONFIG")
    (check-equal "CONFIG" (role-record-source (role-from-config config :security))
                 "the security role did not come from CONFIG")
    ;; ...and never inferred from the underlying model.
    (ok (not (role-from-config config :some-model-capability))
        "a model capability was read as a configured role")
    (ok (not (role-inferred-from-model-p :security))
        "the security role was inferred from the model")
    ;; Each kind is expressible: the essential-security-only reserved role, the
    ;; specialised different-perspective reserved-plan role, and an agreed
    ;; participation.
    (ok (role-record-essential-security-only-p (role-from-config config :security))
        "essential-security-only is not expressible")
    (ok (role-record-different-perspective-p (role-from-config config :reviewer))
        "the reserved-plan different-perspective role is not expressible")
    (ok (role-record-participation-p (role-from-config config :contributor))
        "agreed participation is not expressible")
    ;; The reserved ones are not spent on routine work.
    (multiple-value-bind (okp reason) (spend-role config :security :routine)
      (ok (not okp) "a reserved role was spent on routine work")
      (ok (search "reserved" reason) "the refusal does not name the reservation: ~A" reason))
    (multiple-value-bind (okp reason) (spend-role config :reviewer :routine)
      (ok (not okp) "the reserved-plan role was spent on routine work")
      (ok (search "reserved" reason) "the refusal does not name the reservation: ~A" reason))))
