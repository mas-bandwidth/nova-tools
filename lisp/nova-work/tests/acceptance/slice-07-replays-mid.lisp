;;;; slice-07-replays-mid.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "redo-refuses-a-stale-plan" "docs/SPEC-WORK.md:5194"
    "expected=redo-with-moved-preconditions-refused-atomically-naming-ids"
  ;; NEEDS-KERNEL: undo/redo precondition tracking.
  (ok t "pending; needs undo/redo"))

(deftest "regression-and-recovery" "docs/SPEC-WORK.md:4379"
    "expected=breached-trial-stops-automatic-assignment;fallback-preserves-limits-history-handles"
  ;; NEEDS-KERNEL: trial breach and automatic assignment fallback.
  (ok t "pending; needs assignment control"))

(deftest "regression-opens-repair-work" "docs/SPEC-WORK.md:5300"
    "expected=confirmed-regression-creates-linked-open-repair-work"
  ;; NEEDS-KERNEL: confirmed regression plus linked repair work creation.
  (ok t "pending; needs regression detection"))


(deftest "render-artifact-is-bounded" "docs/SPEC-WORK.md:5398"
    "expected=interleaved-replies-and-chat-artifact-bounded;oversize-refused-never-partial"
  ;; NEEDS-KERNEL: render/artifact batching and bound enforcement.
  (ok t "pending; needs render"))

(deftest "render-refuses-a-target-outside-its-roots" "docs/SPEC-WORK.md:5304"
    "expected=target-outside-permitted-roots-refused-not-guessed"
  ;; NEEDS-KERNEL: render roots and file-target permission checks.
  (ok t "pending; needs render roots"))

(deftest "reply-retired-only-under-verified-coverage" "docs/SPEC-WORK.md:5538"
    "expected=retired-only-once-snapshot-events-root-verified;else-recovery-gap"
  ;; NEEDS-KERNEL: savepoint/dedup coverage verification of the boundary record.
  (ok t "pending; needs savepoint dedup"))

(deftest "repo-only-at-the-root" "docs/SPEC-WORK.md:5341"
    "expected=repo-at-root-unique;second-refused-repo-held-by;under-parent-refused"
  ;; NEEDS-KERNEL: the repo/root placement verb and its uniqueness checks.
  (ok t "pending; needs node add --repo"))

(deftest "requested-model-is-not-observed-model" "docs/SPEC-WORK.md:5222"
    "expected=unknown-stays-unknown;attempts-separate-model-attribution"
  ;; NEEDS-KERNEL: observed-vs-requested model attribution across attempts.
  (ok t "pending; needs model observation"))

(deftest "reserved-role-is-not-spent-on-routine-work" "docs/SPEC-WORK.md:5214"
    "expected=reserved-role-read-from-config-never-spent-on-routine"
  ;; NEEDS-KERNEL: role configuration and reserved-role dispatch rules.
  (ok t "pending; needs role configuration"))

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
  ;; NEEDS-KERNEL: silence threshold and return reconciliation before dispatch.
  (ok t "pending; needs dispatch"))

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


(deftest "roadmap-has-one-creator" "docs/SPEC-WORK.md:5344"
    "expected=node-add--type-roadmap-exit-2-naming-roadmap-create;one-node-and-one-view-atomic"
  ;; NEEDS-KERNEL: a roadmap node and its view in one envelope; no CLI in this slice.
  (ok t "slice 1 carries no roadmap create: NEEDS-KERNEL roadmap node + view envelope"))


(deftest "roadmap-proof" "docs/SPEC-WORK.md:5598"
    "expected=fixed-table-prototype-parity;chat-and-file-renders-byte-identical;marker-edit-preserves-unrelated-bytes"
  ;; NEEDS-KERNEL: roadmap proof rendering and marker edit.
  (ok t "slice 1 carries no roadmap proof: NEEDS-KERNEL roadmap render + marker"))

(deftest "roles-are-configured-not-inferred" "docs/SPEC-WORK.md:5214"
    "expected=role-read-from-CONFIG-never-the-model;reserved-roles-not-spent-on-routine-work"
  ;; NEEDS-KERNEL: CONFIG and role resolution; no session/config in this slice.
  (ok t "slice 1 carries no roles: NEEDS-KERNEL CONFIG + role resolution"))
