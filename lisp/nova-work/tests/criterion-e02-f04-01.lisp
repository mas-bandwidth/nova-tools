;;;; criterion-e02-f04-01.lisp --- E02-F04-01 "Check base tip and owner token before every write"
;;;;
;;;; The criterion tests session-reconfirm, which checks:
;;;; 1. Completion before until
;;;; 2. Tip SHA matches base
;;;; 3. Owner generation and token unchanged
;;;;
;;;; These checks guard the session against divergence and prevent writes that
;;;; would violate lease and ownership constraints (SPEC-WORK.md:200-210, 223-226).

(in-package #:nova-work/tests)

(deftest "e02-f04-01-check-base-tip-owner"
    "docs/SPEC-WORK.md:200-210,223-226,300-341"
    "expected=reconfirm-accepts-matching-base-and-owner;reconfirm-rejects-base-divergence;reconfirm-rejects-generation-change;reconfirm-rejects-token-change"
  (let* ((sess (make-session :owner "alice" :generation 1 :token "tok-alice-1"
                             :base "abc123" :until "2026-10-01T00:00:00Z"
                             :kernel (make-kernel :state (make-seed-state *seed*)
                                                  :journal (make-ordering-journal)
                                                  :rev-base 1))))
    
    ;; TEST 1: reconfirm succeeds when base and owner match
    (multiple-value-bind (ok line exit-code)
        (session-reconfirm sess "abc123" :owner-record
                           (make-ownership-record :owner "alice" :generation 1
                                                 :token "tok-alice-1"
                                                 :stamp "2026-09-23T00:00:00Z"
                                                 :until "2026-10-01T00:00:00Z"))
      (ok ok "reconfirm succeeds when base and owner match")
      (ok (zerop exit-code) "exit code is 0 on success")
      (ok (search "SESSION OK" line) "success message contains SESSION OK"))
    
    ;; TEST 2: reconfirm fails when base diverges
    (multiple-value-bind (ok line exit-code)
        (session-reconfirm sess "different-sha" :owner-record
                           (make-ownership-record :owner "alice" :generation 1
                                                 :token "tok-alice-1"
                                                 :stamp "2026-09-23T00:00:00Z"
                                                 :until "2026-10-01T00:00:00Z"))
      (ok (not ok) "reconfirm fails when base diverges")
      (ok (= 1 exit-code) "exit code is 1 on base divergence")
      (ok (search "RACED" line) "failure message contains RACED"))
    
    ;; TEST 3: reconfirm fails when owner generation changes
    (multiple-value-bind (ok line exit-code)
        (session-reconfirm sess "abc123" :owner-record
                           (make-ownership-record :owner "alice" :generation 2
                                                 :token "tok-alice-1"
                                                 :stamp "2026-09-23T00:00:00Z"
                                                 :until "2026-10-01T00:00:00Z"))
      (ok (not ok) "reconfirm fails when owner generation changes")
      (ok (= 1 exit-code) "exit code is 1 on generation change")
      (ok (search "owner changed" line) "failure message contains owner changed"))
    
    ;; TEST 4: reconfirm fails when owner token changes
    (multiple-value-bind (ok line exit-code)
        (session-reconfirm sess "abc123" :owner-record
                           (make-ownership-record :owner "alice" :generation 1
                                                 :token "tok-alice-different"
                                                 :stamp "2026-09-23T00:00:00Z"
                                                 :until "2026-10-01T00:00:00Z"))
      (ok (not ok) "reconfirm fails when owner token changes")
      (ok (= 1 exit-code) "exit code is 1 on token change")
      (ok (search "owner changed" line) "failure message contains owner changed"))))
