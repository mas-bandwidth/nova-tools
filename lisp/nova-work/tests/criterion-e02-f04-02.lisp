;;;; criterion-e02-f04-02.lisp --- the E02-F04-02 proof: "Admit each request
;;;; against until and fence on expiry or divergence" (roadmap nova-work.sexp,
;;;; feature E02-F04 "Lease expiry, reconfirmation and self-fencing",
;;;; criterion 2 of 3).
;;;;
;;;; docs/SPEC-WORK.md:250-252 pins admission PER REQUEST, not per reconfirm:
;;;; every read and every write compares the session's clock to `until` at the
;;;; moment it is admitted, and a request that arrives after `until` is refused
;;;; `fenced` even if the reconfirm that would have advanced `until` is in
;;;; flight. docs/SPEC-WORK.md:419-421 pins the divergence half: a session
;;;; whose clip is refused by divergence is fenced exactly as in rule 2 --
;;;; every write and every read refused `fenced` at exit 1, except the status
;;;; and export the rule names.
;;;;
;;;; The production path under proof: every request a session takes passes
;;;; SESSION-SUBMIT (src/session.lisp), which calls SESSION-CHECK-ADMISSION
;;;; before the request is enqueued to the kernel's single command thread
;;;; (src/command-thread.lisp); a reconfirm runs SESSION-RECONFIRM, which
;;;; fences the session when the tip moved off its base or the owner record on
;;;; the tip no longer carries its generation and token.

(in-package #:nova-work/tests)

(deftest "e02-f04-02-admit-each-request-against" "docs/SPEC-WORK.md:250-252,419-421"
    "expected=each-request-admitted-against-until;post-until-request-refused-fenced-exit-1;only-status-export-while-fenced;diverged-reconfirm-fences-raced;whole-reconfirm-admits"
  ;; --------------------------------------------------------------
  ;; Expiry. Admission is checked per request: the clock is compared
  ;; to `until` at the moment each request is admitted, so a session
  ;; live one second is fenced the next -- no reconfirm needs to run
  ;; for the fence to bite.
  ;; --------------------------------------------------------------
  (let ((sess (make-session :owner "emma" :generation 3 :token "tok-3"
                            :until "2026-09-14T12:01:00Z" :base "abc123"
                            :state :live :every "30s")))
    ;; A request admitted one second before `until` runs.
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :state-to-doing :now "2026-09-14T12:00:59Z")
      (declare (ignore reason))
      (ok admitted "a request before `until` was refused")
      (check-equal 0 code "a pre-`until` request did not exit 0")
      (check-equal :live (session-state sess)
                   "a pre-`until` request fenced the session"))
    ;; The next request, admitted two seconds later and so after
    ;; `until`, is refused `fenced` at exit 1 and the session has
    ;; fenced itself: the comparison is made again, per request.
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :state-to-doing :now "2026-09-14T12:01:01Z")
      (check-equal nil admitted "a request after `until` was admitted")
      (check-equal 1 code "the post-`until` refusal is not exit 1")
      (ok (and (stringp reason) (search "fenced" reason))
          "the refusal does not name the fence: ~A" reason)
      (check-equal :fenced (session-state sess)
                   "a post-`until` request did not fence the session"))
    ;; Fenced, only status and export are admitted (exit 0); every
    ;; other request is still refused at exit 1.
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :status :now "2026-09-14T12:01:02Z")
      (declare (ignore reason))
      (ok admitted "status is not admitted on the fenced session")
      (check-equal 0 code "status does not exit 0 while fenced"))
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :export :now "2026-09-14T12:01:02Z")
      (declare (ignore reason))
      (ok admitted "export is not admitted on the fenced session")
      (check-equal 0 code "export does not exit 0 while fenced"))
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :state-to-doing :now "2026-09-14T12:01:02Z")
      (declare (ignore reason))
      (check-equal nil admitted "a second post-`until` mutation was admitted")
      (check-equal 1 code "the second refusal is not exit 1")))
  ;; --------------------------------------------------------------
  ;; Each request against `until`, on the path to the single writer.
  ;; SESSION-SUBMIT admits the request BEFORE it is enqueued to the
  ;; kernel's command thread: a pre-`until` mutation lands; the same
  ;; mutation after `until` is refused `fenced` and never reaches the
  ;; command thread.
  ;; --------------------------------------------------------------
  (let* ((seed '((:id "w1" :type :task :parent nil :state :doing)
                 (:id "w2" :type :task :parent nil :state :doing)))
         (kernel (make-kernel :state (make-seed-state seed)))
         (sess (make-session :owner "emma" :generation 3 :token "tok-3"
                             :until "2026-09-14T12:01:00Z" :base "abc123"
                             :state :live :every "30s" :kernel kernel)))
    (multiple-value-bind (okp line code)
        (session-submit sess (list :verb :state-to-done :node "w1"
                                   :by "rowan" :reason "shipped"
                                   :evidence '("ev-1") :request "e02-f04-02-a"
                                   :stamp "2026-09-14T12:00:59Z" :clock :tool
                                   :generation-owner "gen-4")
                        :now "2026-09-14T12:00:59Z")
      (ok okp "the pre-`until` submit was refused: ~A" line)
      (check-equal 0 code "the pre-`until` submit did not exit 0"))
    (check-equal :c (node-branch (kernel-state kernel) "w1")
                 "the admitted request did not reach the command thread")
    (multiple-value-bind (okp line code)
        (session-submit sess (list :verb :state-to-done :node "w2"
                                   :by "rowan" :reason "shipped"
                                   :evidence '("ev-1") :request "e02-f04-02-b"
                                   :stamp "2026-09-14T12:01:01Z" :clock :tool
                                   :generation-owner "gen-4")
                        :now "2026-09-14T12:01:01Z")
      (check-equal nil okp "the post-`until` submit was admitted")
      (check-equal 1 code "the post-`until` submit is not exit 1")
      (ok (and (stringp line) (search "fenced" line))
          "the refusal does not name the fence: ~A" line)
      (check-equal :fenced (session-state sess)
                   "the post-`until` submit did not fence the session"))
    (check-equal :o (node-branch (kernel-state kernel) "w2")
                 "the refused request reached the command thread"))
  ;; --------------------------------------------------------------
  ;; Divergence. A reconfirm whose tip moved off the session's base
  ;; fences the session and reports the race; one whose owner record
  ;; no longer carries the session's generation and token fences it
  ;; too. A whole reconfirm -- tip == base, generation and token
  ;; unchanged, completed before `until` -- is admitted, so the fence
  ;; bites on divergence and not on every reconfirm.
  ;; --------------------------------------------------------------
  (let ((sess (make-session :owner "emma" :generation 4 :token "tok-4"
                            :until "2026-09-14T12:02:00Z" :base "abc123"
                            :state :live :every "30s")))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "def456" :now "2026-09-14T12:01:00Z")
      (check-equal nil okp "a diverged tip reconfirmed")
      (check-equal 1 code "the divergence refusal is not exit 1")
      (ok (and (stringp line) (search "SESSION RACED" line))
          "the divergence does not say RACED: ~A" line)
      (check-equal :fenced (session-state sess)
                   "the divergence did not fence the session")))
  (let ((sess (make-session :owner "emma" :generation 4 :token "tok-4"
                            :until "2026-09-14T12:02:00Z" :base "abc123"
                            :state :live :every "30s"))
        (moved (make-ownership-record :owner "emma" :generation 5 :token "tok-5")))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123" :now "2026-09-14T12:01:00Z"
                                         :owner-record moved)
      (declare (ignore line))
      (check-equal nil okp "a tip whose owner moved reconfirmed")
      (check-equal 1 code "the owner-divergence refusal is not exit 1")
      (check-equal :fenced (session-state sess)
                   "the owner divergence did not fence the session")))
  (let ((sess (make-session :owner "emma" :generation 4 :token "tok-4"
                            :until "2026-09-14T12:02:00Z" :base "abc123"
                            :state :live :every "30s"))
        (same (make-ownership-record :owner "emma" :generation 4 :token "tok-4")))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123" :now "2026-09-14T12:01:00Z"
                                         :owner-record same)
      (ok okp "a whole reconfirm was refused: ~A" line)
      (check-equal 0 code "the whole reconfirm did not exit 0")
      (ok (and (stringp line) (search "SESSION OK" line))
          "the whole reconfirm does not answer SESSION OK: ~A" line)
      (check-equal :live (session-state sess)
                   "a whole reconfirm fenced the session"))))
