;;;; replays-785-ready-row.lisp --- the `ready` row and `needs-broken`
;;;; (nova-tools #785).
;;;;
;;;; Rule 2's row fields (docs/SPEC-WORK.md:4859) and rule 5
;;;; (docs/SPEC-WORK.md:4965-4988), with the replay at :6965. The grammar line
;;;; this asserts is docs/SPEC-WORK.md:5941:
;;;;
;;;;   QUERY ROW <id> kind=<k> state=<s> ready=<true|false> reason=<text|->
;;;;   need=<id|-> unmet=<n> needs-broken=<true|false> resolver=<name|->
;;;;   ... responsible=<name|-> holder=<name|unowned>
;;;;
;;;; and the count line is docs/SPEC-WORK.md:5933, `WORK OK ... needs-broken=<n>`.
;;;;
;;;; `needs-broken` stops being a slot here. Rules 4 and 5 say it is derived and
;;;; "written nowhere as authority", and it "clears by itself at the first read
;;;; after the node is needs-met again" -- a stored flag cannot promise that.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; fixtures
;;; ------------------------------------------------------------------

(defparameter *row-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    ;; D: leased and :doing on N -- engaged
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    ;; E: a second dependent of N, which will settle into C
    (:id "acme/work/e" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    ;; F: a dependent of E, one edge further out
    (:id "acme/work/f" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/e"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing))
  "The fixture of replay `reverting-a-need-flags-an-engaged-dependent-and-kills-nothing`:
D engaged on N, E a second dependent settled in C, and F a dependent of E. It is
the replay's own fixture and nothing more, so its `needs-broken=2` is exact.")

(defun row-kernel (&optional (seed *row-seed*))
  (make-kernel :state (make-seed-state seed)
               :journal (make-ordering-journal) :rev-base 1))

(defun row-doing (k node &key (request (format nil "doing-~A" node)))
  (submit k (list :verb :state-to-doing :node node :by "rowan" :reason "started"
                  :request request :stamp "2026-09-16T11:00:00Z" :clock :tool
                  :generation-owner "gen-1")))

(defun row-done (k node &key (request (format nil "done-~A" node)))
  (unless (member (node-state (kernel-state k) node) (list :doing :review))
    (row-doing k node :request (format nil "pre-~A" request)))
  (multiple-value-prog1
      (submit k (list :verb :state-to-done :node node :by "rowan" :reason "merged"
                      :evidence (list "ev-1") :request request
                      :stamp "2026-09-16T12:00:00Z" :clock :tool
                      :generation-owner "gen-1"))
    ;; the session rebuilds its view from the settle it just applied, which is
    ;; what rule 1 asks the gate to read (Stella's HOLD on df714493)
    (refresh-needs-view k)))

(defun row-reopen (k node &key (request (format nil "reopen-~A" node)))
  (submit k (list :verb :event-reopen :node node :by "rowan" :reason "reverted"
                  :request request :stamp "2026-09-16T13:00:00Z" :clock :tool
                  :generation-owner "gen-1")))

(defun row-for (k id)
  "A row read through the session's own view, which is what a session prints."
  (find id (state-ready-rows (kernel-state k) :view (kernel-needs-view k))
        :key (function ready-row-id) :test (function equal)))

;;; ------------------------------------------------------------------
;;; the row's three new fields                   SPEC-WORK.md:4859, :5941
;;; ------------------------------------------------------------------

(deftest "the-ready-row-names-the-need-counts-them-and-prints-needs-broken"
    "docs/SPEC-WORK.md:4859"
    "expected=need-unmet-and-needs-broken-replace-the-built-blocked-by-text"
  (let* ((k (row-kernel))
         (row (row-for k "acme/work/d")))
    (ok row "D has a ready row")
    (ok (not (ready-row-ready row)) "D is not ready while N is open")
    (check-string= "acme/work/n" (ready-row-need row) "the row names the first unmet need")
    (check-equal 1 (ready-row-unmet row) "and counts them all")
    (check-string= "need-open" (ready-row-reason row)
                   "reason= is one of the five tokens, never `blocked by <id>`")
    (ok (not (ready-row-needs-broken row))
        "an unengaged todo dependent of an open need is not needs-broken")
    ;; the grammar line, SPEC-WORK.md:5941
    (let ((line (ready-row-line row)))
      (ok (search "need=acme/work/n" line) "need= is on the line: ~A" line)
      (ok (search "unmet=1" line) "unmet= is on the line: ~A" line)
      (ok (search "needs-broken=false" line) "needs-broken= is on the line: ~A" line)
      (ok (search "reason=need-open" line) "reason= carries the token: ~A" line)
      (ok (not (search "blocked by" line)) "and the built text is gone: ~A" line)))
  ;; a needs-met node: need=- unmet=0 reason=- needs-broken=false
  (let ((k (row-kernel)))
    (row-done k "acme/work/n")
    (let ((row (row-for k "acme/work/d")))
      (check-string= "-" (ready-row-need row) "a met node names no need")
      (check-equal 0 (ready-row-unmet row) "and counts none")
      (check-string= "-" (ready-row-reason row) "and carries no token")
      (ok (ready-row-ready row) "and is ready")))
  ;; two unmet needs: the first in :deps order, both counted
  (let* ((k (row-kernel
             '((:id "w"  :type :work-set :parent nil :state :unknown)
               (:id "d"  :type :task :parent "w" :state :todo :deps ("n1" "n2"))
               (:id "n1" :type :task :parent "w" :state :doing)
               (:id "n2" :type :task :parent "w" :state :doing))))
         (row (row-for k "d")))
    (check-string= "n1" (ready-row-need row) "the first in :deps order")
    (check-equal 2 (ready-row-unmet row) "and both counted")))

;;; ------------------------------------------------------------------
;;; reverting-a-need-flags-an-engaged-dependent-and-kills-nothing
;;;                                              SPEC-WORK.md:4965, :6965
;;; ------------------------------------------------------------------

(deftest "reverting-a-need-flags-an-engaged-dependent-and-kills-nothing"
    "docs/SPEC-WORK.md:4965"
    "expected=a-reading-never-a-finding-it-stops-nothing-and-it-clears-by-itself"
  (let ((k (row-kernel)))
    ;; N settles; D takes a lease and goes :doing on it; E settles in C.
    (multiple-value-bind (okp line) (row-done k "acme/work/n")
      (ok okp "N settles: ~A" line))
    (take-lease k "acme/work/d" "emma")
    (multiple-value-bind (okp line) (row-doing k "acme/work/d")
      (ok okp "D goes doing: ~A" line))
    (multiple-value-bind (okp line) (row-done k "acme/work/e")
      (ok okp "E settles into C: ~A" line))
    ;; before the revert, nothing is flagged
    (check-equal 0 (state-needs-broken-count (kernel-state k) :view (kernel-needs-view k))
                 "no node is needs-broken while every need is met")
    ;; the revert
    (multiple-value-bind (okp line) (row-reopen k "acme/work/n")
      (ok okp "N reopens: ~A" line))
    ;; D: engaged, so flagged, and its reason is the reverted token
    (let ((row (row-for k "acme/work/d")))
      (check-string= "need-reverted" (ready-row-reason row) "D reads need-reverted")
      (ok (ready-row-needs-broken row) "and needs-broken=true"))
    ;; E is in C, so flagged too; the count is two
    (ok (node-needs-broken (kernel-state k) "acme/work/e" :view (kernel-needs-view k))
        "E, settled in C over a reverted need, is flagged")
    (check-equal 2 (state-needs-broken-count (kernel-state k) :view (kernel-needs-view k))
                 "check counts two")
    ;; `check` prints the count and exits 0 with no WORK FAIL <id> line
    (multiple-value-bind (code line)
        (validate-report '(:green :green) :needs-broken
                         (state-needs-broken-count (kernel-state k) :view (kernel-needs-view k)))
      (check-equal 0 code "check exits 0 over any number of them")
      (ok (search "WORK OK" line) "and prints WORK OK: ~A" line)
      (ok (search "needs-broken=2" line) "with the count: ~A" line)
      (ok (not (search "WORK FAIL" line)) "and no finding line: ~A" line))
    ;; it stops nothing that runs: D's lease and state stand
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "D's lease stands")
    (check-equal :doing (node-state (kernel-state k) "acme/work/d")
                 "and D is still :doing")
    ;; but the next admission verb on D is refused by rule 3
    (ok (search "unmet need"
                (handler-case (progn (take-lease k "acme/work/d" "sam") "")
                  (unsupported-input (c) (unsupported-input-what c))))
        "the next take on D is refused")
    ;; it goes one edge and no further: F, a dependent of the flagged E, is not
    (let ((row (row-for k "acme/work/f")))
      (ok (not (ready-row-needs-broken row))
          "F, a dependent of a needs-broken node that is itself met, is not flagged"))
    ;; N settled and verified again: D and E read false with no command naming
    ;; either -- it clears by itself at the first read
    (multiple-value-bind (okp line) (row-done k "acme/work/n" :request "done-n-2")
      (ok okp "N settles again: ~A" line))
    (ok (not (ready-row-needs-broken (row-for k "acme/work/d")))
        "D clears by itself")
    (ok (not (node-needs-broken (kernel-state k) "acme/work/e" :view (kernel-needs-view k)))
        "and so does E, with no command naming either")
    (check-equal 0 (state-needs-broken-count (kernel-state k) :view (kernel-needs-view k))
                 "and the count is back to zero")))

;;; ------------------------------------------------------------------
;;; needs-broken is derived, not stored          SPEC-WORK.md:4952, :4972
;;; ------------------------------------------------------------------

(deftest "needs-broken-is-derived-and-survives-a-reconstruction"
    "docs/SPEC-WORK.md:4972"
    "expected=written-nowhere-as-authority-and-rebuilt-from-the-events"
  (let ((k (row-kernel)))
    (row-done k "acme/work/n")
    (take-lease k "acme/work/d" "emma")
    (row-doing k "acme/work/d")
    (row-reopen k "acme/work/n")
    (ok (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k)) "D is flagged")
    ;; the canonical form carries no flag: it is derived, never authority
    (let ((text (canonical-string (state-canonical-form (kernel-state k)))))
      (ok (not (search "NEEDS-BROKEN" (string-upcase text)))
          "the serialized state stores no needs-broken field"))
    ;; and the reading is rebuilt from the events alone
    (let ((fresh (reconstruct-state
                  (canonical-string (state-canonical-form (kernel-state k))))))
      (ok (node-needs-broken fresh "acme/work/d")
          "a reconstruction reads the same flag from the events"))))
