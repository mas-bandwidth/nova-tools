;;;; slice-09-replays-8603.lisp --- one replay slice of the acceptance suite
;;;; (nova-tools #362, card 8603): the five named acceptance replays the
;;;; closed history, the clip bound and the six new verbs promise.
;;;;
;;;; Loaded by ../acceptance.lisp. The two new-verb replays already live in
;;;; slice-05-durable-journal.lisp; this slice carries the three the closed
;;;; history and the clip promise and that no slice asserted yet.

(in-package #:nova-work/tests)

;; ------------------------------------------------------------------
;; as-of-refuses-unavailable-partition   docs/SPEC-WORK.md:5586
;; ------------------------------------------------------------------
;; A query for a state as of a window end whose day partition it cannot read
;; refuses at exit 1, naming the one partition it would need, and is never
;; answered from a later row (docs/SPEC-WORK.md:759,5135,5586).
(deftest "as-of-refuses-unavailable-partition" "docs/SPEC-WORK.md:5586"
    "expected=ask-refused-exit-1-naming-the-one-partition;never-answered-from-a-later-row"
  (let ((k (fresh)))
    (multiple-value-bind (okp line code)
        (ask-state-as-of k :ask :state :as-of "2026-09-01T12:00:00Z"
                           :partitions '("2026-09-02"))
      (ok (not okp) "the ask was answered from a later partition")
      (check-equal 1 code "an unavailable partition must refuse at exit 1")
      (ok (search "QUERY FAIL" line) "not a QUERY FAIL: ~A" line)
      (ok (search "as-of=2026-09-01T12:00:00Z" line) "the stamp is not named: ~A" line)
      (ok (search "partition=2026-09-01" line) "the partition is not named: ~A" line)
      (ok (search "historical window unavailable" line)
          "the refusal does not name the historical window: ~A" line))
    (multiple-value-bind (okp line code)
        (ask-state-as-of k :ask :state :as-of "2026-09-01T12:00:00Z"
                           :partitions '("2026-09-01"))
      (ok okp "the readable partition was refused: ~A" line)
      (check-equal 0 code "a readable partition must answer at exit 0")
      (ok (search "QUERY OK" line) "not a QUERY OK: ~A" line))))

;; ------------------------------------------------------------------
;; indivisible-record-refused-before-ack   docs/SPEC-WORK.md:5543
;; ------------------------------------------------------------------
;; One key with its one locator that no page under --page-bytes could hold is
;; refused `indivisible` at the candidate gate, exit 2, nothing journaled and
;; nothing acknowledged (docs/SPEC-WORK.md:794,5543,5994).
(deftest "indivisible-record-refused-before-ack" "docs/SPEC-WORK.md:5543"
    "expected=indivisible=refused,nothing-journaled"
  (let* ((k (fresh))
         (journal (kernel-journal k))
         (order-before (journal-order journal)))
    (multiple-value-bind (okp line code)
        (admit-record k :mutation :state-to-doing :request "req-big"
                        :key :transition :bytes 900 :page-bytes 512)
      (ok (not okp) "an indivisible record was admitted")
      (check-equal 2 code "an indivisible record must refuse at exit 2")
      (ok (search "indivisible" line) "the refusal does not say indivisible: ~A" line)
      (ok (search "request=req-big" line) "the request is not named: ~A" line)
      (ok (search "key=transition" line) "the key is not named: ~A" line)
      (ok (search "bytes=900" line) "the byte count is not named: ~A" line)
      (ok (search "past --page-bytes=512" line) "the bound is not named: ~A" line))
    (check-equal order-before (journal-order journal)
                 "an indivisible refusal journaled something")
    (check-equal nil (journal-lookup journal "req-big")
                 "an indivisible refusal acknowledged a request")
    ;; The journal's own over-bound record is refused by the same gate, naming
    ;; --max-bytes (docs/SPEC-WORK.md:803).
    (multiple-value-bind (okp line code)
        (admit-record k :mutation :event-reopen :request "req-big-journal"
                        :key :transition :bytes 900 :max-bytes 512)
      (ok (not okp) "an over-bound journal record was admitted")
      (check-equal 2 code "an over-bound record must refuse at exit 2")
      (ok (search "past --max-bytes=512" line) "the max bound is not named: ~A" line)
      (ok (search "indivisible" line) "the refusal does not say indivisible: ~A" line))
    (check-equal order-before (journal-order journal)
                 "an over-bound refusal journaled something")))

;; ------------------------------------------------------------------
;; clip-names-the-index-that-overflowed   docs/SPEC-WORK.md:5553
;; ------------------------------------------------------------------
;; A clip refused with snapshot=, retained=, index= and closed-index= all
;; printed beside --max-bytes and the one remedy named; the lower --retain then
;; passing; and an index page that would pass --page-bytes split, never refused
;; (docs/SPEC-WORK.md:803,5102,5553).
(deftest "clip-names-the-index-that-overflowed" "docs/SPEC-WORK.md:5553"
    "expected=snapshot-retained-index-closed-index-printed;remedy-lower-retain-or-raise-max-bytes;lower-retain-passes;index-page-split-not-refused"
  (let ((k (fresh)))
    (multiple-value-bind (okp line code)
        (clip k :snapshot 101 :retained 50 :index 55 :closed-index 20 :max-bytes 100)
      (ok (not okp) "an over-bound clip was admitted")
      (check-equal 1 code "an over-bound clip must refuse")
      (ok (search "CLIP FAIL" line) "not a CLIP FAIL: ~A" line)
      (ok (search "snapshot=101" line) "snapshot= is not printed: ~A" line)
      (ok (search "retained=50" line) "retained= is not printed: ~A" line)
      (ok (search "index=55" line) "index= is not printed: ~A" line)
      (ok (search "closed-index=20" line) "closed-index= is not printed: ~A" line)
      (ok (search "past --max-bytes=100" line) "the bound is not printed: ~A" line)
      (ok (search "lower --retain or raise --max-bytes" line)
          "the one remedy is not named: ~A" line))
    (multiple-value-bind (okp line code)
        (clip k :snapshot 80 :retained 50 :index 55 :closed-index 20 :max-bytes 100)
      (ok okp "the lowered snapshot still refused: ~A" line)
      (check-equal 0 code "a clip inside the bound must pass at exit 0"))
    (multiple-value-bind (pages refused-p)
        (split-index-page 55 20)
      (ok (not refused-p) "an index page was refused instead of split")
      (check-equal 3 pages "the overflowing page was not split into a further page"))))
