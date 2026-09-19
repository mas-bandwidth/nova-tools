;;;; replays-dep-verb.lisp --- `dep --add` and `dep --remove` (nova-tools#1673).
;;;;
;;;; v1's own body promises the verb (SPEC-WORK.md:987, :1506, :2362, :2868)
;;;; and the wire schema agrees (`"verb": "dep"`, `"event_kind": ":structure"`,
;;;; `"mutating": true`). The kernel had no `:structure` event kind, no
;;;; `apply-event` branch over `wnode-deps` and no submit path, so a `:deps`
;;;; edge could only be seeded and never edited. This is the replay against the
;;;; real file journal, where a restart reads back the edge the verb
;;;; acknowledged.

(in-package #:nova-work/tests)

(defparameter *dep-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/m" :type :task     :parent "acme/work" :state :doing))
  "D needs the open N; M is unattached.")

(defun dep-add (k node need &key (as "rowan") (reason "the new order")
                                 (request (format nil "dep-add-~A-~A" node need)))
  (dep-edit k :node node :add need :as as :reason reason
              :request request :stamp "2026-09-16T14:00:00Z"))

(defun dep-remove (k node need &key (as "rowan") (reason "it does not need it")
                                    (request (format nil "dep-rm-~A-~A" node need)))
  (dep-edit k :node node :remove need :as as :reason reason
              :request request :stamp "2026-09-16T15:00:00Z"))

(deftest "the-dep-verb-edits-the-deps-edge-spec-work-md-987"
    "docs/SPEC-WORK.md:987,2362"
    "expected=an-added-edge-is-on-the-node-and-a-removed-one-is-gone"
  (let* ((j (make-ordering-journal :capacity 64))
         (k (make-kernel :state (make-seed-state *dep-seed*) :journal j :rev-base 1)))
    ;; add: the edge lands on the node and the receipt says so
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/m")
      (ok okp "an edge is admitted: ~A" line)
      (check-equal 0 code "at exit 0")
      (ok (search "change=add" line) "the receipt names the change: ~A" line)
      (ok (search "need=acme/work/m" line) "and the need: ~A" line))
    (check-equal '("acme/work/n" "acme/work/m")
                 (node-deps (kernel-state k) "acme/work/d")
                 "the forward edge is on the node, in the order it was added")
    ;; remove: the override of a direct need
    (multiple-value-bind (okp line code) (dep-remove k "acme/work/d" "acme/work/n")
      (ok okp "the override is a recorded edit of the edge: ~A" line)
      (check-equal 0 code "at exit 0")
      (ok (search "change=remove" line) "the receipt names the removal: ~A" line))
    (check-equal '("acme/work/m")
                 (node-deps (kernel-state k) "acme/work/d")
                 "the removed edge is gone")
    ;; both edits are on the append-only structure log
    (check-equal 2 (length (node-structure-log (kernel-state k) "acme/work/d"))
                 "each edge edit is one structure record")))

(deftest "a-dep-edge-refuses-a-cycle-and-a-dangling-id"
    "docs/SPEC-WORK.md:4997,5041"
    "expected=rule-3-refuses-a-cycle-rule-2-refuses-an-id-that-names-nothing"
  (let* ((j (make-ordering-journal :capacity 64))
         (k (make-kernel :state (make-seed-state *dep-seed*) :journal j :rev-base 1))
         (before (node-deps (kernel-state k) "acme/work/d")))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/d")
      (ok (not okp) "a node that needs itself is a cycle of length one")
      (check-equal 1 code "at exit 1")
      (ok (search "rule 3" line) "and the receipt says rule 3: ~A" line))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/n" "acme/work/d")
      (ok (not okp) "an edge closing a two-node cycle is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "rule 3" line) "and says rule 3: ~A" line))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/nowhere")
      (ok (not okp) "an id naming nothing is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "rule 2: dangling" line) "and says rule 2: ~A" line))
    (check-equal before (node-deps (kernel-state k) "acme/work/d")
                 "and none of the three wrote anything")))

(deftest "a-dep-edit-is-journaled-and-survives-a-restart"
    "docs/SPEC-WORK.md:307,2362"
    "expected=the-edge-the-verb-acknowledged-is-the-edge-a-restart-reads"
  (let* ((path (test-journal-path "dep-verb"))
         (seed (make-seed-state *dep-seed*))
         (digest (root-digest seed))
         (j (open-file-journal path :initial-state-hash digest :capacity 64))
         (k (make-kernel :state (make-seed-state *dep-seed*) :journal j :rev-base 1)))
    (unwind-protect
         (progn
           (multiple-value-bind (okp line) (dep-add k "acme/work/d" "acme/work/m")
             (ok okp "the edge is admitted: ~A" line))
           (close-file-journal j)
           (let* ((j2 (open-file-journal path :initial-state-hash digest :capacity 64))
                  (k2 (make-kernel :state (make-seed-state *dep-seed*) :journal j2 :rev-base 1)))
             (unwind-protect
                  (progn
                    (replay-journal j2 k2)
                    (check-equal '("acme/work/n" "acme/work/m")
                                 (node-deps (kernel-state k2) "acme/work/d")
                                 "the forward edge survives a restart")
                    (check-equal '("acme/work/d")
                                 (node-dependents (kernel-state k2) "acme/work/m")
                                 "and so does the reverse edge"))
               (ignore-errors (close-file-journal j2)))))
      (ignore-errors (close-file-journal j))
      (ignore-errors (delete-file path)))))

(deftest "a-dep-retry-answers-its-original-receipt-once"
    "docs/SPEC-WORK.md:307,315"
    "expected=a-retransmitted-request-answers-its-record-and-conflicts-refuse"
  (let* ((j (make-ordering-journal :capacity 64))
         (k (make-kernel :state (make-seed-state *dep-seed*) :journal j :rev-base 1)))
    (multiple-value-bind (okp first) (dep-add k "acme/work/d" "acme/work/m" :request "dep-once")
      (ok okp "the first request lands: ~A" first)
      (multiple-value-bind (okp2 second code2) (dep-add k "acme/work/d" "acme/work/m" :request "dep-once")
        (ok okp2 "the identical retry is answered, not refused")
        (check-equal 0 code2 "at exit 0")
        (check-string= first second "with the original receipt"))
      ;; the same id with a changed payload is refused, and nothing is applied
      (multiple-value-bind (okp3 line3 code3) (dep-add k "acme/work/d" "acme/work/n" :request "dep-once")
        (ok (not okp3) "a conflicting payload under one id is refused")
        (check-equal 1 code3 "at exit 1")
        (ok (search "different payload" line3) "naming the conflict: ~A" line3))
      (check-equal '("acme/work/n" "acme/work/m")
                   (node-deps (kernel-state k) "acme/work/d")
                   "and the refused conflicting edit wrote nothing"))))
