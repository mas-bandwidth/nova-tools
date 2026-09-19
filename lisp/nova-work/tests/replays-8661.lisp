;;;; replays-8661.lisp --- five acceptance replays for card 8661.
;;;;
;;;; Each deftest is named exactly as docs/SPEC-WORK.md names its replay, sets
;;;; up the state its paragraph describes, drives the kernel (or the pure model
;;;; the kernel does not yet reach) through its verbs, and asserts the outcome
;;;; the paragraph promises. Where a paragraph admits more than one reading the
;;;; reading is stated in RESULT.md as `read: <name>: ...`.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; cost-per-accepted-decision                   SPEC-WORK.md:6054
;;; ------------------------------------------------------------------

(deftest "cost-per-accepted-decision" "docs/SPEC-WORK.md:6054"
    "expected=cost-of-accepted-over-accepted;wrong-and-missed-are-gates;slow-recovery-is-a-gate;cheap-wrong-is-not-accepted;no-accepted-unknown"
  (let ((measure (coordination-measure
                  '((:id "d1" :cost 10 :recovery-latency 5)
                    (:id "d2" :cost 1 :wrong t :recovery-latency 1)
                    (:id "d3" :cost 2 :missed t :recovery-latency 1)
                    (:id "d4" :cost 3 :recovery-latency 120)
                    (:id "d5" :cost 20 :recovery-latency 10)))))
    ;; only the two decisions that passed every gate are accepted.
    (check-equal 2 (getf measure :accepted) "only ungated decisions are accepted")
    (check-equal 30 (getf measure :accepted-cost) "the accepted cost is what was measured")
    (check-equal 15 (getf measure :cost-per-accepted)
                 "the measure is the accepted cost over the accepted count")
    ;; the three gates are counted, never folded into the measure.
    (check-equal 1 (getf measure :wrong) "a wrong decision is a gate")
    (check-equal 1 (getf measure :missed) "a missed decision is a gate")
    (check-equal 1 (getf measure :slow) "a slowly recovered decision is a gate")
    (check-equal "cost-per-accepted-decision" (getf measure :measure)
                 "the measure replaces decisions-per-token"))
  ;; a cheap decision that was wrong is not an accepted decision: its cost is
  ;; never added and never lowers the measure.
  (let ((lone (coordination-measure '((:id "cheap" :cost 1 :wrong t)))))
    (check-equal 0 (getf lone :accepted) "a cheap wrong decision is not accepted")
    (check-equal 0 (getf lone :accepted-cost) "its cost is not counted")
    (check-equal :unknown (getf lone :cost-per-accepted)
                 "no accepted decision leaves the measure unknown, never zero"))
  ;; an unobserved recovery latency is a gate, not a zero.
  (let ((measure (coordination-measure
                  '((:id "x" :cost 9 :recovery-latency :unknown)))))
    (check-equal 1 (getf measure :slow) "an unobserved recovery latency is gated")
    (check-equal :unknown (getf measure :cost-per-accepted)
                 "a gated-only set leaves the measure unknown")))

;;; ------------------------------------------------------------------
;;; single-writer-kernel-total-order             SPEC-WORK.md:2621
;;; ------------------------------------------------------------------

(deftest "single-writer-kernel-total-order" "docs/SPEC-WORK.md:2621"
    "expected=one-total-order;journal-sequence-in-order;client-relative-order-preserved;outside-loop-is-a-defect"
  (let ((loop (kernel-command-loop
               '((:client "a" :request "r1" :verb "STATE")
                 (:client "b" :request "r2" :verb "STATE")
                 (:client "a" :request "r3" :verb "STATE")))))
    (let ((journal (getf loop :journal)))
      ;; the journal shows one sequence, in the one order the thread applied.
      (check-equal '(1 2 3) (journal-seq-numbers journal)
                   "the journal shows the sequence numbers in total order")
      (check-equal '("r1" "r2" "r3") (mapcar (lambda (e) (getf e :request)) journal)
                   "both clients' commands land in one total order")
      ;; client a's own commands keep their relative order.
      (check-equal '("r1" "r3")
                   (remove-if-not (lambda (r) (member r '("r1" "r3") :test #'equal))
                                  (mapcar (lambda (e) (getf e :request)) journal))
                   "one client's commands keep their relative order")
      (check-equal '() (getf loop :defects)
                   "no command in the loop is a defect")))
  ;; a mutation outside the command loop is a defect, by the validator rule.
  (let* ((bad '(:client "c" :request "r-outside" :outside-loop t))
         (loop (kernel-command-loop (list bad))))
    (check-equal '() (getf loop :journal)
                 "a mutation outside the loop is not applied")
    (check-equal (list bad) (getf loop :defects)
                 "a mutation outside the loop is recorded as a defect")
    (multiple-value-bind (ok line code) (kernel-defect-line bad)
      (check-equal nil ok "the defect refuses")
      (check-equal 2 code "the defect refuses at exit 2")
      (ok (search "outside the command loop" line)
          "the refusal names the validator rule: ~A" line))))

;;; ------------------------------------------------------------------
;;; every-field-has-an-owning-verb               SPEC-WORK.md:2934
;;; ------------------------------------------------------------------

(deftest "every-field-has-an-owning-verb" "docs/SPEC-WORK.md:2934"
    "expected=every-field-one-owning-verb;every-verb-kind-fields-subject;reverse-each-field-resolves;no-generic-set-field"
  ;; both directions hold at once.
  (ok (every-field-has-an-owning-verb-p)
      "every field maps to its verb and every verb to its kind, fields and subject")
  ;; the fields read to their verbs.
  (check-equal '(:state-to-done :state-to-doing) (owning-verbs :blocked-by)
               ":blocked-by is owned by the transition verbs")
  (ok (member :event-reopen (owning-verbs :reason))
      ":reason is owned by the reopen verb too")
  ;; a field no verb owns is unreachable by any recorded act.
  (check-equal '() (owning-verbs :nonesuch)
               "an unknown field has no owning verb")
  ;; the reverse: each verb to its event kind, ordered field list and subject.
  (check-equal :reopen (verb-event-kind :event-reopen) "reopen's event kind")
  (check-equal '(:reason) (verb-ordered-fields :event-reopen)
               "reopen's ordered field list")
  (check-equal :node (verb-subject :event-reopen) "reopen's subject")
  (check-equal :transition (verb-event-kind :state-to-done)
               "a close carries a :transition event")
  ;; and every field a verb lists resolves back to that same verb.
  (dolist (v (mutation-verbs))
    (dolist (f (verb-ordered-fields v))
      (ok (member v (owning-verbs f))
          "~A lists ~A and ~A owns it" v f v)))
  ;; no generic set-field escape hatch: an unknown request field still refuses.
  (let ((kernel (make-kernel :state (make-seed-state *seed*))))
    (multiple-value-bind (ok line)
        (submit kernel (list :verb :state-to-doing :node "acme/work/f1/t2"
                             :by "rowan" :request "req-set" :stamp "2026-09-14T12:00:00Z"
                             :clock :tool :generation-owner "gen-4" :set-field :sneaky))
      (check-equal nil ok "a generic set-field key is refused")
      (ok (search "unknown field" line) "the refusal names the field: ~A" line))))

;;; ------------------------------------------------------------------
;;; prompt-profile-expired-shows-on-the-status-line  SPEC-WORK.md:3366
;;; ------------------------------------------------------------------

(deftest "prompt-profile-expired-shows-on-the-status-line" "docs/SPEC-WORK.md:3366"
    "expected=mismatch-refuses-exit-1;stale-shows-expired-date;absent-shows-absent;mismatch-shows-mismatch;unmeasured-evidence-shows-unknown;pointer-is-a-path-pinned-by-digest"
  (let ((profile (make-prompt-profile
                  :name "sol-manager" :pointer "prompts/sol.md" :digest "aaa"
                  :policy-version "p-7"
                  :evidence '(:measured "2026-09-01" :cost-per-accepted 12)
                  :expiry "2026-09-16" :owner "glenn")))
    ;; the pointer is a repository path and the digest is pinned, never inline.
    (check-equal "prompts/sol.md" (prompt-profile-pointer profile)
                 "the pointer is a repository path")
    (check-equal "aaa" (prompt-profile-digest profile)
                 "the digest pins the bytes the path resolved to")
    ;; a stale profile shows on the status line with its expired date.
    (let ((line (prompt-profile-status-line profile
                                            :content-digest "aaa" :today "2026-09-17")))
      (ok (search "profile=sol-manager" line) "the status line names the profile: ~A" line)
      (ok (search "profile-state=stale" line) "the status line reads stale: ~A" line)
      (ok (search "expired=2026-09-16" line)
          "the stale line carries the expired date: ~A" line))
    ;; a mismatched digest refuses the invocation at exit 1, by name.
    (multiple-value-bind (ok line code)
        (prompt-profile-invocation profile "bbb" :session "/s/session.lisp")
      (check-equal nil ok "a digest mismatch refuses the invocation")
      (check-equal 1 code "the mismatch refusal is exit 1")
      (ok (search "digest mismatch" line) "the refusal names the mismatch: ~A" line)
      (ok (search "profile=sol-manager" line) "the refusal names the profile: ~A" line))
    ;; and the status line reads mismatch for the same profile.
    (ok (search "profile-state=mismatch"
                (prompt-profile-status-line profile
                                            :content-digest "bbb" :today "2026-09-01"))
        "the status line reads mismatch")
    ;; a matching digest at an unexpired date with measured evidence is current.
    (ok (search "profile-state=current"
                (prompt-profile-status-line profile
                                            :content-digest "aaa" :today "2026-09-01"))
        "a healthy profile reads current"))
  ;; an absent profile reads absent.
  (ok (search "profile-state=absent"
              (prompt-profile-status-line nil :today "2026-09-17"))
      "no profile named reads absent")
  ;; evidence never measured reads unknown, never guessed.
  (let ((unmeasured (make-prompt-profile
                     :name "sol" :pointer "prompts/sol.md" :digest "aaa"
                     :evidence :unknown :expiry "2026-12-31" :owner "glenn")))
    (ok (search "profile-state=unknown"
                (prompt-profile-status-line unmeasured
                                            :content-digest "aaa" :today "2026-09-01"))
        "an unmeasured profile reads unknown")))

;;; ------------------------------------------------------------------
;;; machine-is-config-and-never-a-work-tree-node  SPEC-WORK.md:3620
;;; ------------------------------------------------------------------

(deftest "machine-is-config-and-never-a-work-tree-node" "docs/SPEC-WORK.md:3620"
    "expected=machine-is-fleet-config;node-absent;counts-unchanged;settle-refused;done-refused;never-completion-evidence"
  (let* ((kernel (make-kernel :state (make-seed-state *seed*)
                              :friends '("glenn" "rowan")))
         (before-open (state-open-count (kernel-state kernel)))
         (before-rev (state-revision (kernel-state kernel)))
         (before-history (length (state-history (kernel-state kernel)))))
    ;; The real `machine --register` verb writes one CONFIG member through
    ;; `submit`, with `:node (:absent)`, and moves no work-tree count.
    (multiple-value-bind (ok line code envelope)
        (submit kernel
                (list :verb :machine :change :register :machine "m-a1"
                      :name "studio" :owner "glenn" :connect "profile:studio"
                      :roles '(:build :test) :permits '("go-test")
                      :limits '(:cores 16) :facts nil :declared-by "glenn"
                      :request "mreq-8661" :stamp "2026-09-17T00:00:00Z"))
      (ok ok "registering a machine is admitted: ~A" line)
      (check-equal 0 code "machine register exit code")
      (ok (search "MACHINE OK" line) "the register prints a MACHINE OK line")
      (ok (search "machine=m-a1" line) "the line names the machine")
      (check-equal t (absentp (work-event-node (first (getf envelope :events))))
                   "the :machine event writes :node (:absent)")
      (check-equal +absent+ (work-event-node (first (getf envelope :events)))
                   "the node field is the absent value"))
    (check-equal before-open (state-open-count (kernel-state kernel))
                 "|O| is unchanged")
    (check-equal (1+ before-rev) (state-revision (kernel-state kernel))
                 "the revision advanced by exactly one")
    (let* ((history (state-history (kernel-state kernel)))
           (record (car (last history))))
      (check-equal (1+ before-history) (length history)
                   "exactly one envelope record was added")
      (check-equal t (absentp (getf (first (getf record :events)) :node))
                   "the envelope record names no containment node")
      (check-equal +absent+ (getf (first (getf record :events)) :node)
                   "the node field is the absent value"))
    (let ((machine (fleet-member (kernel-fleet kernel) "m-a1")))
      ;; the machine is a :kind :machine member of the fleet section of CONFIG.
      (check-equal :fleet (getf (machine-config-section machine) :section)
                   "a machine is a fleet CONFIG member")
      (check-equal :machine (getf (machine-config-section machine) :kind)
                   "the member's kind is :machine")
      (check-equal nil (machine-work-tree-node-p machine)
                   "a machine is never a work-tree node")
      (check-equal nil (machine-acceptance machine)
                   "a machine has no :acceptance")
      (check-equal nil (machine-derived-state machine)
                   "a machine has no derived state")
      ;; no verb can settle it or take it :to :done.
      (multiple-value-bind (ok line code) (machine-settle machine)
        (check-equal nil ok "no verb can settle a machine")
        (check-equal 2 code "the settle refusal is exit 2")
        (ok (search "m-a1" line) "the refusal names the machine: ~A" line))
      (multiple-value-bind (ok line code) (machine-to-done machine)
        (check-equal nil ok "no verb can take a machine :to :done")
        (check-equal 2 code "the done refusal is exit 2")
        (ok (search "m-a1" line) "the refusal names the machine: ~A" line))
      ;; equipment does not complete: nothing a machine does is evidence.
      (check-equal nil (machine-completion-evidence-p machine)
                   "nothing a machine does is completion evidence"))))
