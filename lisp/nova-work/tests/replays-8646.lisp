;;;; replays-8646.lisp --- five named acceptance replays and the paragraphs they
;;;; come from. Each drives the pure model in src/replays-8646.lisp the way its
;;;; paragraph's verbs would, and asserts the outcome the paragraph promises.
;;;;
;;;; SPEC-WORK.md:6358   inventory-expansion-and-contraction
;;;; SPEC-WORK.md:4651,5770 local-tokens-cost-zero-api
;;;; SPEC-WORK.md:1706-1738,6243 materialized-working-set
;;;; SPEC-WORK.md:6231   moving-source
;;;; SPEC-WORK.md:4848   packet-and-route-gates

(in-package #:nova-work/tests)

(deftest "inventory-expansion-and-contraction" "docs/SPEC-WORK.md:6358"
    "expected=inventory=preserved,counted=separately,denominator=visible"
  (let ((a (make-inventory-accounting :baseline '("a" "b" "c"))))
    (inventory-observe a :discovered "d" "i1")
    (inventory-observe a :discovered "e" "i1")
    (inventory-observe a :completed "a" "i1")
    (inventory-observe a :reopened "b" "i1")
    (inventory-observe a :decomposed "c" "i1")
    (inventory-observe a :removed "e" "i1")
    (inventory-observe a :discovered "f" "i2")
    (let ((r (inventory-report a)))
      (check-equal 3 (getf r :baseline) "initial inventory not preserved")
      (check-equal 3 (length (inventory-accounting-baseline a)) "baseline rewritten")
      (check-equal 3 (getf r :discovered) "discovered work not counted separately")
      (check-equal 1 (getf r :completed) "completed work not counted separately")
      (check-equal 1 (getf r :reopened) "reopened work not counted separately")
      (check-equal 1 (getf r :decomposed) "decomposed work not counted separately")
      (check-equal 1 (getf r :removed) "removed work not counted separately")
      (check-equal 5 (getf r :denominator) "denominator")
      (ok (getf r :denominator-changed-p) "changed denominator not shown"))
    (check-equal '(("i1" 2 1) ("i2" 1 0)) (inventory-interval-deltas a)
                 "expansion and contraction over the named intervals")
    (ok (inventory-sustained-divergence-p a :threshold 2)
        "sustained divergence not warned")))

(deftest "local-tokens-cost-zero-api" "docs/SPEC-WORK.md:4651,5770"
    "expected=cost-values=separately-labelled,missing=unknown,local-api-charge=0"
  (let ((c (make-cost :tokens 1200 :measured-cash 1.5 :marginal-cash 0.25
                      :reference-token-cost 0.03)))
    (check-equal 1.5 (cost-record-measured-cash c) "measured provider cash")
    (check-equal 0.25 (cost-record-marginal-cash c) "estimated marginal cash")
    (check-equal 0.03 (cost-record-reference-token-cost c)
                 "virtual reference token cost")
    (ok (cost-known-p (cost-record-measured-cash c)) "measured cash unknown"))
  (let ((c (local-inference-cost :tokens 5000 :marginal-cash 0.10
                                 :reference-token-cost 0.03)))
    (check-equal 0 (cost-record-measured-cash c) "local declared API charge")
    (ok (cost-known-p (cost-record-measured-cash c)) "declared zero read unknown")
    (check-equal 0.03 (cost-record-reference-token-cost c)
                 "local API charge zeroed the reference token cost")
    (check-equal 0.10 (cost-record-marginal-cash c)
                 "hardware/energy folded into the API charge"))
  (let ((c (make-cost :tokens 1 :measured-cash 2.0)))
    (check-equal :unknown (cost-record-marginal-cash c) "missing marginal cash")
    (check-equal :unknown (cost-record-reference-token-cost c)
                 "missing reference token cost")
    (ok (not (cost-known-p (cost-record-marginal-cash c)))
        "missing dimension read as known")))

(deftest "materialized-working-set" "docs/SPEC-WORK.md:1706-1738,6243"
    "expected=unrelated-visits=0,scans=0,reconstruction=equal,watermark=printed,uncertain-retained=yes"
  (let ((ws (make-working-set))
        (k (fresh)))
    (ok (plusp (state-open-count (kernel-state k))) "seed O is empty")
    (w-take ws "t1" 200 :holder "a")
    (w-take ws "t2" 200 :holder "b")
    (with-instrumentation
      (ok (w-member-p ws "t1") "t1 not working")
      (check-equal 2 (w-count ws) "|W|")
      (check-equal 0 *visits* "membership visited unrelated nodes")
      (check-equal 0 *parses* "membership parsed")
      (check-equal 0 *replays* "membership replayed"))
    (ok (w-reconstruction-equal-p ws '("t1" "t2"))
        "reconstruction unequal after take")
    (w-renew ws "t1" 400)
    (w-release ws "t2")
    (ok (w-reconstruction-equal-p ws '("t1"))
        "reconstruction unequal after renew and release")
    (check-equal 1 (w-count ws) "|W| after release")
    (w-take ws "t1" 500 :holder "c")
    (check-equal 1 (w-count ws) "two live attempts on one id counted twice")
    (w-take ws "t3" 250 :holder "d" :attempt 7 :external :uncertain)
    (w-take ws "t9" 900 :holder "e")
    (check-equal 1 (w-advance-clock ws 300) "due leases processed")
    (check-equal 2 (w-count ws) "clock advance did not expire the due lease")
    (ok (w-uncertain-retained-p ws "t3")
        "expiry dropped the uncertain remote execution")
    (ok (w-live-lease-p ws "t9" :clock 300) "an unrelated live lease expired")
    (let ((line (w-ask ws :clock 900)))
      (ok (search "watermark=300" line) "watermark not printed: ~A" line)
      (ok (search "stale" line)
          "stale watermark presented as current: ~A" line))
    (setf (working-set-reconciled-p ws) nil)
    (ok (not (w-live-lease-p ws "t1" :clock 300))
        "unreconciled recovery advertised a live lease")
    (setf (working-set-reconciled-p ws) t)
    (ok (w-live-lease-p ws "t1" :clock 300) "reconciled lease not live")))

(deftest "moving-source" "docs/SPEC-WORK.md:6231"
    "expected=captured=preserved,incomplete=marked,reconciled=yes"
  (let ((cap (make-source-capture :source-revision 42)))
    (capture-observe cap "i1" :body "old body" :revision 42)
    (capture-note-mutation cap "i1")
    (capture-observe cap "i1" :body "new body" :revision 43)
    (capture-observe cap "i1" :comments '("c1") :revision 43)
    (capture-note-mutation cap "i1")
    (capture-observe cap "i1" :labels '("bug") :revision 44)
    (capture-note-mutation cap "i1")
    (check-equal :incomplete (capture-consistent-claim cap)
                 "a mixed capture claimed a consistent snapshot")
    (let ((versions (capture-versions cap "i1" :body)))
      (check-equal 2 (length versions) "captured body versions")
      (check-string= "old body" (source-version-value (first versions))
                     "the earliest captured body was not preserved")
      (check-string= "new body" (source-version-value (second versions))
                     "a later capture was not recorded"))
    (capture-reconcile cap "i1" :body "newest body" :revision 45)
    (let ((versions (capture-versions cap "i1" :body)))
      (check-equal 3 (length versions) "reconcile dropped captured history")
      (check-string= "old body" (source-version-value (first versions))
                     "reconcile rewrote a captured version")
      (check-string= "newest body" (source-version-value (third versions))
                     "a newer observation was not reconciled"))
    (ok (source-capture-reconciled-p cap) "newer observation not reconciled")))

(deftest "packet-and-route-gates" "docs/SPEC-WORK.md:4848"
    "expected=oversized/stale/unexplained-escalation-refuse-before-dispatch;valid-scoped-exception-retained"
  (multiple-value-bind (dispatched line)
      (dispatch-gates (make-dispatch-packet :bytes 4096)
                      (make-dispatch-route :id "r1")
                      :max-bytes 1024)
    (ok (not dispatched) "oversized packet dispatched")
    (ok (search "oversized" line) "oversized packet not refused: ~A" line))
  (multiple-value-bind (dispatched line)
      (dispatch-gates (make-dispatch-packet :bytes 10 :history-disallowed t)
                      (make-dispatch-route :id "r1")
                      :max-bytes 1024)
    (ok (not dispatched) "history-disallowed packet dispatched")
    (ok (search "history-disallowed" line)
        "history-disallowed packet not refused: ~A" line))
  (multiple-value-bind (dispatched line)
      (dispatch-gates (make-dispatch-packet :bytes 10)
                      (make-dispatch-route :id "r2" :reserved t)
                      :max-bytes 1024)
    (ok (not dispatched) "reserved route dispatched")
    (ok (search "reserved" line) "reserved route not refused: ~A" line))
  (multiple-value-bind (dispatched line)
      (dispatch-gates (make-dispatch-packet :bytes 10)
                      (make-dispatch-route :id "r3" :stale t)
                      :max-bytes 1024)
    (ok (not dispatched) "stale route dispatched")
    (ok (search "stale" line) "stale route not refused: ~A" line))
  (multiple-value-bind (dispatched line)
      (dispatch-gates (make-dispatch-packet :bytes 10 :costly-escalation t)
                      (make-dispatch-route :id "r1")
                      :max-bytes 1024)
    (ok (not dispatched) "unexplained costly escalation dispatched")
    (ok (search "unexplained" line)
        "unexplained costly escalation not refused: ~A" line))
  (multiple-value-bind (dispatched line outcome)
      (dispatch-gates (make-dispatch-packet :bytes 10 :costly-escalation t
                                            :escalation-reason "quality gate")
                      (make-dispatch-route :id "r1")
                      :max-bytes 1024)
    (ok dispatched "a valid packet did not dispatch")
    (check-equal :dispatched outcome "dispatch outcome")
    (ok (search "scoped-exception=retained" line)
        "valid scoped exception not retained: ~A" line)))

;;; E02-F04-01 "Check base tip and owner token before every write"
;;;   docs/SPEC-WORK.md:215-221 (rule 2, Holding): the owner reconfirms before
;;;   every write, "first that the fetched tip is the session's base, then that
;;;   OWNER on it still carries its generation and token", and the whole check
;;;   is one predicate, `tip == base`, "made before the CAS of every push".
(deftest "TestE02F04CheckBaseTipAndOwner" "docs/SPEC-WORK.md:215-221"
    "expected=matching-tip-and-owner-token-reconfirm,advance-until;moved-tip-refused-RACED-and-fenced;changed-token-refused-owner-changed"
  ;; A session whose fetched tip is its base and whose OWNER still carries its
  ;; generation and token reconfirms green and advances `until`.
  (let ((sess (session-start :owner "emma" :token "tok-1" :base "abc123"
                             :state-seed '((:id "acme/work" :type :work-set :state :unknown))
                             :now "2026-09-14T12:00:00Z" :every "30s" :skew "5s")))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123"
                           :now "2026-09-14T12:00:30Z"
                           :owner-record (make-ownership-record
                                          :owner "emma" :generation 1 :token "tok-1"))
      (ok okp "a matching base tip and owner token did not reconfirm: ~A" line)
      (check-equal 0 code "a matching reconfirm is not green")
      (ok (search "until=2026-09-14T12:01:30Z" line)
          "the reconfirm does not advance until: ~A" line)
      (check-equal :live (session-state sess) "a matching reconfirm fenced the session")))
  ;; A tip that is not the base is a tip this session did not write: the write
  ;; is withheld, the session fences, and the line names both shas.
  (let ((sess (session-start :owner "emma" :token "tok-1" :base "abc123"
                             :state-seed '((:id "acme/work" :type :work-set :state :unknown))
                             :now "2026-09-14T12:00:00Z" :every "30s" :skew "5s")))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "def456"
                           :now "2026-09-14T12:00:30Z"
                           :owner-record (make-ownership-record
                                          :owner "emma" :generation 1 :token "tok-1"))
      (ok (not okp) "a moved tip reconfirmed and would overwrite a foreign commit")
      (check-equal 1 code "a raced reconfirm is not a refusal")
      (ok (search "SESSION RACED" line) "the raced write does not print RACED: ~A" line)
      (ok (search "expected=abc123" line) "the RACED line does not name the base: ~A" line)
      (ok (search "found=def456" line) "the RACED line does not name the moved tip: ~A" line)
      (check-equal :fenced (session-state sess) "a raced session did not fence")))
  ;; An OWNER whose generation or token changed on the tip is refused even when
  ;; the tip is still the base, before the write goes out.
  (let ((sess (session-start :owner "emma" :token "tok-1" :base "abc123"
                             :state-seed '((:id "acme/work" :type :work-set :state :unknown))
                             :now "2026-09-14T12:00:00Z" :every "30s" :skew "5s")))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "abc123"
                           :now "2026-09-14T12:00:30Z"
                           :owner-record (make-ownership-record
                                          :owner "emma" :generation 2 :token "tok-other"))
      (ok (not okp) "a changed owner token reconfirmed and admitted the takeover")
      (check-equal 1 code "an owner-changed reconfirm is not a refusal")
      (ok (search "owner changed" line) "the owner change is not refused by name: ~A" line)
      (check-equal :fenced (session-state sess) "an owner-changed session did not fence"))))
