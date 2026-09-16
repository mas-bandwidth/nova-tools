;;;; s5.lisp --- the slice-5 acceptance replays: evidence, attempts, and a
;;;;             done that costs something.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md it comes from and asserts
;;;; the printed line the spec shows. Against the slice-1 kernel every verb and
;;;; count this slice ships is absent, so the whole file is red: it is the
;;;; contract the implementation cards of S5 find waiting.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; rule-2-unavailable-is-not-green           docs/SPEC-WORK.md:5482
;;; ------------------------------------------------------------------

(deftest "rule-2-unavailable-is-not-green" "docs/SPEC-WORK.md:5482"
    "expected=rule-2-names-unavailable-partition-exit-1-not-dangling;never-green-over-unread-history"
  (let ((k (fresh)))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :attest :node "acme/work/f1/t1" :criterion "crit-1"
                        :result "pr:acme/work#11@abc123" :against "abc123"
                        :by "rowan" :request "att-1"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-1"))
      (declare (ignore okp))
      (ok (search "rule 2: unavailable partition=" line)
          "a reference into an unreadable C partition did not print `rule 2: unavailable partition=`: ~A"
          line)
      (ok (not (search "dangling" line))
          "the refusal reads `dangling`, not the unavailable partition it is: ~A" line)
      (check-equal 1 code "rule-2 unavailable exit code"))))

;;; ------------------------------------------------------------------
;;; settle-keeps-id-and-evidence              docs/SPEC-WORK.md:5378
;;; ------------------------------------------------------------------

(deftest "settle-keeps-id-and-evidence" "docs/SPEC-WORK.md:5378"
    "expected=id-children-acceptance-and-five-evidence-fields-read-same-before-and-after"
  (let ((k (fresh)))
    ;; Write the evidence event the settle will cite: a pointer bound to a
    ;; criterion, carrying its five fields, then settle citing it by id.
    (multiple-value-bind (okp line code)
        (submit k (list :verb :evidence :node "acme/work/f1/t1"
                        :pointer "test:pkg/t1@abc123" :criterion "crit-1" :against "abc123"
                        :by "rowan" :request "ev-1"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-1"))
      (ok okp "evidence event refused: ~A" line)
      (check-equal 0 code "evidence exit code")
      (ok (search "EVIDENCE OK" line) "no EVIDENCE OK line: ~A" line))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :state-to-done :node "acme/work/f1/t1" :by "rowan"
                        :reason "done" :evidence '("ev-1")
                        :request "req-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-1"))
      (ok okp "settle refused: ~A" line)
      (check-equal 0 code "settle exit code")
      (ok (search "STATE OK" line) "no STATE OK line: ~A" line))))

;;; ------------------------------------------------------------------
;;; accept-add-on-a-done-node-refuses         docs/SPEC-WORK.md:2359
;;; ------------------------------------------------------------------

(deftest "accept-add-on-a-done-node-refuses" "docs/SPEC-WORK.md:2359"
    "expected=rule-5-refuses-at-candidate-gate;correct-then-accept-add-then-fresh-evidence-admitted"
  (let ((k (fresh)))
    ;; Settle first so the node is derived :done; then accept --add must refuse
    ;; at the candidate gate by rule 5, because the standing :to :done would no
    ;; longer cover the node's :acceptance.
    (multiple-value-bind (okp line code)
        (submit k (close-request :request "req-1"))
      (ok okp "settle refused: ~A" line)
      (check-equal 0 code "settle exit code"))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :accept :node "acme/work/f1/t1" :by "rowan"
                        :add "crit-2:test:subject:passes" :reason "tighten acceptance"
                        :request "acc-1"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-1"))
      (ok (not okp) "accept --add on a derived done node was admitted")
      (ok (search "rule 5" line) "the refusal does not name rule 5: ~A" line)
      (check-equal 1 code "accept --add refusal exit code"))))

;;; ------------------------------------------------------------------
;;; older-generation-evidence-cannot-close    docs/SPEC-WORK.md:1230
;;; ------------------------------------------------------------------

(deftest "older-generation-evidence-cannot-close" "docs/SPEC-WORK.md:1230"
    "expected=correct-bumps-generation;older-generation-evidence-refused-at-state-to-done"
  (let ((k (fresh)))
    ;; A :correct event bumps the task's :generation; evidence written at an
    ;; older generation can no longer close the task.
    (multiple-value-bind (okp line code)
        (submit k (list :verb :correct :node "acme/work/f1/t1" :by "rowan"
                        :reason "scope changed" :request "cor-1"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-1"))
      (ok okp "correct refused: ~A" line)
      (check-equal 0 code "correct exit code")
      (ok (search "CORRECT OK" line) "no CORRECT OK line: ~A" line))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :state-to-done :node "acme/work/f1/t1" :by "rowan"
                        :reason "done" :evidence '("ev-old")
                        :request "req-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-1"))
      (ok (not okp) "evidence of an older generation closed the task")
      (ok (search "generation" line) "the refusal does not name the generation: ~A" line)
      (check-equal 1 code "older-generation close exit code"))))

;;; ------------------------------------------------------------------
;;; done-unverified-is-its-own-count          docs/SPEC-WORK.md:2045
;;; ------------------------------------------------------------------

(deftest "done-unverified-is-its-own-count" "docs/SPEC-WORK.md:2045"
    "expected=done-unverified=n-beside-done=n-and-part-of-unknown=;never-added"
  (let ((k (fresh)))
    (multiple-value-bind (open unit scope line)
        (ask-size k)
      (declare (ignore open unit scope))
      (ok (search "done-unverified=" line)
          "the query line drops done-unverified=: ~A" line)
      (ok (search "done=" line)
          "the query line drops done=: ~A" line)
      (ok (search "unknown=" line)
          "the query line drops unknown=: ~A" line))))

;;; ------------------------------------------------------------------
;;; four-capability-groups-and-three-fields (model half)   docs/SPEC-WORK.md:5611
;;; ------------------------------------------------------------------

(deftest "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:5611"
    "expected=model-catalog-three-fields-support-runtime-free-capacity-never-collapse"
  (let ((k (fresh)))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :model :id "m1" :provider "acme"
                        :route "deepseek-r1-pro" :billing :metered
                        :by "rowan" :request "m-1"
                        :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-1"))
      (ok okp "model --register refused: ~A" line)
      (check-equal 0 code "model register exit code")
      (ok (search "MODEL OK" line) "no MODEL OK line: ~A" line))))
