;;;; criterion-e02-f04-01.lisp --- E02-F04-01 "Check base tip and owner token before every write"
;;;;
;;;; The criterion tests session-reconfirm (src/session.lisp), which checks, in order:
;;;; 1. Completion before until (a reconfirm after expiry fences the session)
;;;; 2. Tip SHA matches base (SESSION RACED on divergence)
;;;; 3. Owner generation and token unchanged (SESSION FAIL owner changed)
;;;;
;;;; These checks guard the session against divergence and prevent writes that
;;;; would violate lease and ownership constraints (SPEC-WORK.md:200-210, 223-226).
;;;; Every case passes an explicit :now, so the result never depends on the
;;;; wall clock, and every case builds a fresh session, because a successful
;;;; reconfirm advances the session's until.
;;;;
;;;; Supersedes PR #2758 (card nx-w16 E02-F04-01); recut to load this file from
;;;; nova-work.asd and add the expiry case.

(in-package #:nova-work/tests)

(defun e02-f04-01-session ()
  (make-session :owner "alice" :generation 1 :token "tok-alice-1"
                :base "abc123" :every "30s" :until "2026-10-01T00:00:00Z"))

(defun e02-f04-01-owner (&key (generation 1) (token "tok-alice-1"))
  (make-ownership-record :owner "alice" :generation generation :token token
                         :stamp "2026-09-23T00:00:00Z"
                         :until "2026-10-01T00:00:00Z"))

(defparameter *e02-f04-01-now* "2026-09-23T00:00:00Z"
  "A fixed instant before the session's until.")

(deftest "e02-f04-01-check-base-tip-owner"
    "docs/SPEC-WORK.md:200-210,223-226"
    "expected=reconfirm-accepts-matching-base-and-owner;reconfirm-rejects-expired-session;reconfirm-rejects-exact-expiry-boundary;reconfirm-rejects-base-divergence;reconfirm-rejects-generation-change;reconfirm-rejects-token-change"
  ;; 1. Matching base, owner, and a time before until: accepted, until advanced.
  (let ((sess (e02-f04-01-session)))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123" :now *e02-f04-01-now*
                                         :owner-record (e02-f04-01-owner))
      (ok okp "reconfirm succeeds when base and owner match: ~A" line)
      (check-equal 0 code "exit code on success")
      (ok (search "SESSION OK" line) "success line names SESSION OK: ~A" line)
      (check-equal :live (session-state sess) "session state after success")
      (check-string= "2026-09-23T00:01:00Z" (session-until sess)
                     "until advanced to now + 2 * every")))
  ;; 2. Completion after until: fenced, even with matching base and owner.
  (let ((sess (e02-f04-01-session)))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123" :now "2026-10-01T00:00:01Z"
                                         :owner-record (e02-f04-01-owner))
      (ok (not okp) "reconfirm fails after until: ~A" line)
      (check-equal 1 code "exit code on expiry")
      (ok (search "after expiry" line) "expiry line names after expiry: ~A" line)
      (check-equal :fenced (session-state sess) "session state after expiry")
      (check-string= "2026-10-01T00:00:00Z" (session-until sess)
                     "until unchanged on expiry")))
  ;; 2b. Completion exactly at until: fenced at the boundary, even with a
  ;; matching base and owner. SPEC-WORK requires the owner be able to
  ;; reconfirm before until, so now == until must already refuse.
  (let ((sess (e02-f04-01-session)))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123" :now "2026-10-01T00:00:00Z"
                                         :owner-record (e02-f04-01-owner))
      (ok (not okp) "reconfirm fails exactly at until: ~A" line)
      (check-equal 1 code "exit code at exact expiry boundary")
      (ok (search "after expiry" line) "boundary line names expiry: ~A" line)
      (check-equal :fenced (session-state sess) "session state at exact expiry boundary")
      (check-string= "2026-10-01T00:00:00Z" (session-until sess)
                     "until unchanged at exact expiry boundary")))
  ;; 3. Tip diverged from base: SESSION RACED.
  (let ((sess (e02-f04-01-session)))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "different-sha" :now *e02-f04-01-now*
                                                :owner-record (e02-f04-01-owner))
      (ok (not okp) "reconfirm fails when base diverges: ~A" line)
      (check-equal 1 code "exit code on base divergence")
      (ok (search "SESSION RACED" line) "divergence line names SESSION RACED: ~A" line)
      (check-equal :fenced (session-state sess) "session state after divergence")))
  ;; 4. Owner generation changed on tip.
  (let ((sess (e02-f04-01-session)))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123" :now *e02-f04-01-now*
                                         :owner-record (e02-f04-01-owner :generation 2))
      (ok (not okp) "reconfirm fails when owner generation changes: ~A" line)
      (check-equal 1 code "exit code on generation change")
      (ok (search "owner changed" line) "generation line names owner changed: ~A" line)
      (check-equal :fenced (session-state sess) "session state after generation change")))
  ;; 5. Owner token changed on tip.
  (let ((sess (e02-f04-01-session)))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123" :now *e02-f04-01-now*
                                         :owner-record (e02-f04-01-owner :token "tok-alice-different"))
      (ok (not okp) "reconfirm fails when owner token changes: ~A" line)
      (check-equal 1 code "exit code on token change")
      (ok (search "owner changed" line) "token line names owner changed: ~A" line)
      (check-equal :fenced (session-state sess) "session state after token change"))))
