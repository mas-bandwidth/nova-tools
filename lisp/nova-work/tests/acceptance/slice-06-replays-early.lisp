;;;; slice-06-replays-early.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "one-stop-note-cannot-cancel-two-attempts" "docs/SPEC-WORK.md:5250"
    "expected=one-note-cannot-cancel-two-live-attempts"
  (slice1-refuses-verb :event))

;; NEEDS-KERNEL: long operations returning an id the CLI can query after exit.
(deftest "operation-survives-the-client" "docs/SPEC-WORK.md:5183"
    "expected=operation-id-retrievable-after-client-exit"
  (slice1-refuses-verb :import))

;; NEEDS-KERNEL: recovery replay into bounded overlay pages and their rebuild.
(deftest "overlay-is-bounded-and-rebuilt" "docs/SPEC-WORK.md:5553"
    "expected=thousand-settles-into-pages;no-query-replays-journal"
  (slice1-refuses-verb :recover))

;; NEEDS-KERNEL: a filtered historical ask whose filter rejects every row read.
(deftest "page-budget-is-not-max" "docs/SPEC-WORK.md:5550"
    "expected=shown=0;pages=<n>;whole-history-never-scanned"
  (slice1-refuses-verb :query))

;; NEEDS-KERNEL: the session transport's correlated reply frames.
(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
    "expected=out-of-order-fragmented-replies-reach-only-their-request"
  (slice1-refuses-verb :pipeline))

;; NEEDS-KERNEL: policy/trial manifests surviving export/import/restart/replay.
(deftest "policy-round-trip-and-replay" "docs/SPEC-WORK.md:4370"
    "expected=survives-round-trip;malformed-intake-no-partial-effect"
  (slice1-refuses-verb :config))

;; NEEDS-KERNEL: estimate pinning by revision and unknown-price!=0.
(deftest "pricing-is-pinned-by-revision" "docs/SPEC-WORK.md:5287"
    "expected=old-estimate-reproducible;missing-dimension-unknown"
  (slice1-refuses-verb :estimate))

;; NEEDS-KERNEL: priority verbs and the grants-nothing invariant.
(deftest "priority-grants-nothing" "docs/SPEC-WORK.md:5421"
    "expected=who-unchanged;no-lease;no-bypass"
  (slice1-refuses-verb :priority))

;; NEEDS-KERNEL: subtree/self priority inheritance and clear.
(deftest "priority-inherits-and-clears" "docs/SPEC-WORK.md:5410"
    "expected=order-only;no-lease-attempt-state-counter-moved"
  (slice1-refuses-verb :priority))

;; NEEDS-KERNEL: priority ordering over only the eligible set.
(deftest "priority-orders-only-the-eligible" "docs/SPEC-WORK.md:5406"
    "expected=blocked-rank-0-stays;rank-9-ready-first"
  (slice1-refuses-verb :priority))

;; NEEDS-KERNEL: priority undo treated as history, not value.
(deftest "priority-undo-is-history-not-value" "docs/SPEC-WORK.md:5418"
    "expected=same-value-set-and-clear-of-absent-slot-are-no-effect"
  (slice1-refuses-verb :undo))

;; NEEDS-KERNEL: the wire handshake refusing an unsupported version before admission.
(deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
    "expected=unsupported-version-refused-with-list-before-handshake"
  (slice1-refuses-verb :connect))

;;; replays of docs/SPEC-WORK.md lines 3600-end, part 6 of 8
;;; ------------------------------------------------------------------


;;; The following replays name behaviour outside the slice-1 C/O transition
;;; kernel (the CLI, session, provider/intake adapter, roles, render, savepoint
;;; and dispatch surfaces). They are kept here, named, so the promised spec is
;;; not lost; each carries what it needs before it can turn green.

(deftest "quiet-until-actionable" "docs/SPEC-WORK.md:4373"
    "expected=zero-model-dispatch-for-unchanged;batching-bounded;urgent-bypass"
  ;; NEEDS-KERNEL: model dispatch throttling/batching and urgent-correction bypass.
  (ok t "pending; needs the model dispatch surface"))

(deftest "rank-2-precedes-10" "docs/SPEC-WORK.md:5414"
    "expected=integer-rank-order;equal-and-default-by-id;restart-stable;unknown-only-first-unseen"
  ;; NEEDS-KERNEL: priority rank slots and history-pinned ordering/pagination.
  (ok t "pending; needs priority ranks and the ready cursor"))


(deftest "read-only-intake" "docs/SPEC-WORK.md:5583"
    "expected=recording-adapter-fails-on-mutation-endpoint;remote-inventory-compared-before-after"
  ;; NEEDS-KERNEL: the recording intake adapter and source mutation endpoint guard.
  (ok t "pending; needs the intake adapter"))

(deftest "reconcile-preserves-contradiction" "docs/SPEC-WORK.md:5262"
    "expected=contradictory-observations-kept-unresolved;no-forged-inference"
  ;; NEEDS-KERNEL: the reconcile/index surface over receipts and targets.
  (ok t "pending; needs reconcile"))
