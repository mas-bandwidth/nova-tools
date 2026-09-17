;;;; acceptance.lisp --- the five slice-1 cases.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md at
;;;; 7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64 it comes from, and carries either
;;;; the acceptance table's own expected= string or the executable invariant the
;;;; table names where that row has none.

(in-package #:nova-work/tests)

(defparameter *seed*
  '((:id "acme/work"       :type :work-set :parent nil            :state :unknown)
    (:id "acme/work/f1"    :type :feature  :parent "acme/work"    :state :unknown)
    (:id "acme/work/f1/t1" :type :task     :parent "acme/work/f1" :state :doing
     :links ("https://github.com/acme/work/issues/11"
             "https://github.com/acme/work/issues/12"))
    (:id "acme/work/f1/t2" :type :task     :parent "acme/work/f1" :state :review
     :links ("https://github.com/acme/work/issues/21"))
    (:id "acme/work/f2"    :type :feature  :parent "acme/work"    :state :unknown))
  "Five canonical item ids, all in O at the seed: |O| = 5, of which two are
leaf tasks and three are open linked issues (SPEC-WORK.md:1577 keeps the three
counters apart).")

(defparameter *cyclic-seed*
  '((:id "a" :type :task :parent "b" :state :doing)
    (:id "b" :type :task :parent "a" :state :doing))
  "a -> b -> a. SPEC-WORK.md:3347 referential-integrity: cycles fail BEFORE
publication.")

(defun fresh (&key (journal (make-ordering-journal)) (rev-base 1))
  (make-kernel :state (make-seed-state *seed*) :journal journal :rev-base rev-base))

(defun close-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "shipped")
                           (evidence '("ev-1")) (request "req-1")
                           (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :state-to-done :node node :by by :reason reason :evidence evidence
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun reopen-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "regressed")
                            (request "req-2") (stamp "2026-09-14T13:00:00Z")
                            (generation-owner "gen-4"))
  (list :verb :event-reopen :node node :by by :reason reason
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun independent-open-count (state)
  "Walk every node and count the ones in O. This is the full count the counter
is compared against; it is never the path `query --ask size` takes."
  (let ((n 0))
    (dolist (node *seed*)
      (when (eq :o (node-branch state (getf node :id))) (incf n)))
    n))

;;;; acceptance.lisp --- loader for the per-slice replay files (nova-tools #560).
;;;;
;;;; One file per replay slice so parallel PRs stop conflicting: each slice
;;;; lives in acceptance/<slice>.lisp and this top file only loads them.
;;;; An amendment edits one slice file, never this loader and never two
;;;; slices at once. The shared seed and request helpers stay here so every
;;;; slice sees the same canonical fixtures.

(eval-when (:compile-toplevel :load-toplevel :execute)
  (unless (find-package :asdf)
    (require :asdf)))

;; The slice files, in canonical order. Each is one replay slice; an
;; amendment edits one of them (nova-tools #560).
(defparameter *acceptance-slices*
  '("slice-01-reader.lisp"
    "slice-02-close-and-counters.lisp"
    "slice-03-containers.lisp"
    "slice-04-doing-and-journal.lisp"
    "slice-05-durable-journal.lisp"
    "slice-06-replays-early.lisp"
    "slice-07-replays-mid.lisp"
    "slice-08-replays-late.lisp"
    "slice-09-state-export-replays.lisp"))

(dolist (f *acceptance-slices*)
  (load (asdf:system-relative-pathname :nova-work/tests
          (concatenate 'string "tests/acceptance/" f))))

;;;; ------------------------------------------------------------------
;;;; Assignment and execution control replays (docs/SPEC-WORK.md:3835-3919,
;;;; the replay summary at :5229-5243). The pure book lives in
;;;; src/assignment.lisp; each replay drives it and asserts the paragraph.
;;;; ------------------------------------------------------------------

(defun alist-get (key alist)
  (cdr (assoc key alist :test #'equal)))

(defun accepted-book (&key (holder "alice") (deadline "2026-09-14T12:30:00Z")
                           (default "release"))
  "One admitted, verified-received and accepted offer off-1 on n1."
  (let ((book (nth-value 3
                (leasebook-offer
                 (make-leasebook :nodes '(("n1" . :doing)))
                 :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                 :to holder :profile "cap@1" :reserve 2
                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t))))
    (let ((book (nth-value 3
                  (leasebook-received book :offer "off-1" :node "n1"
                                      :generation "gen-4" :attempt "att-1"
                                      :receipt-digest "rd-1" :receipt-id "rc-1"
                                      :request "qr" :verifier "v"))))
      (nth-value 3
                 (leasebook-accepted book :offer "off-1" :node "n1"
                                     :generation "gen-4" :attempt "att-1"
                                     :by holder :default default :deadline deadline)))))

(defun declined-book ()
  "One admitted, received and declined offer off-1 on n1."
  (let ((book (nth-value 3
                (leasebook-offer
                 (make-leasebook :nodes '(("n1" . :doing)))
                 :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                 :to "alice" :profile "cap@1" :reserve 2
                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t))))
    (let ((book (nth-value 3
                  (leasebook-received book :offer "off-1" :node "n1"
                                      :generation "gen-4" :attempt "att-1"
                                      :receipt-digest "rd-1" :receipt-id "rc-1"
                                      :request "qr" :verifier "v"))))
      (nth-value 3 (leasebook-decline book :offer "off-1" :node "n1"
                                      :generation "gen-4" :attempt "att-1")))))

(deftest "offer-writes-intent-and-a-reservation" "docs/SPEC-WORK.md:5229"
    "expected=:effect-:dispatched;pending-offer;reservation-keyed-by-offer-attempt;nothing-else"
  (let* ((book (make-leasebook
                :nodes '(("n1" . :doing))
                :w '(("n1" . "alice"))
                :leases '(("n0" . (:holder "carol"
                                          :deadline "2026-09-20T00:00:00Z"
                                          :default "release")))
                :evidence '(("ev-1"))
                :completion '(("c-1"))))
         (before (list (leasebook-nodes book) (leasebook-w book) (leasebook-leases book)
                       (leasebook-evidence book) (leasebook-completion book))))
    (multiple-value-bind (okp line effect nbook)
        (leasebook-offer book :offer "off-1" :node "n1" :generation "gen-4"
                         :attempt "att-1" :to "alice" :profile "cap@1"
                         :reserve 2 :until "2026-09-14T12:30:00Z" :free-slots 4
                         :profile-ok t :payload-sha256 "sha-1" :staged-digest "sha-1"
                         :current-generation "gen-4")
      (ok okp "offer refused: ~A" line)
      (check-equal :dispatched effect "offer writes :effect :dispatched")
      (ok (alist-get "off-1" (leasebook-pending nbook)) "a pending-offer entry is written")
      (let ((r (alist-get (cons "off-1" "att-1") (leasebook-reservations nbook))))
        (ok r "a reservation keyed by (offer,attempt) is written")
        (check-equal :held (getf r :state) "the reservation is held")
        (check-equal 2 (getf r :reserve) "the declared slots are reserved"))
      (ok (equal (list "off-1") (alist-get "n1" (leasebook-index-node nbook)))
          "the pending offer is indexed under the node")
      (ok (equal (list "off-1") (alist-get "alice" (leasebook-index-friend nbook)))
          "the pending offer is indexed under the friend")
      (check-equal (first before) (leasebook-nodes nbook) "node state unchanged")
      (check-equal (second before) (leasebook-w nbook) "W unchanged")
      (check-equal (third before) (leasebook-leases nbook) "the lease index unchanged")
      (check-equal (fourth before) (leasebook-evidence nbook) "evidence unchanged")
      (check-equal (fifth before) (leasebook-completion nbook) "completion unchanged"))))

(deftest "no-shadow-lease-across-holders" "docs/SPEC-WORK.md:5238"
    "expected=cross-holder-offer-refused;no-shadow-lease;cross-holder-reply-creates-no-lease"
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "the first offer to alice is admitted: ~A" line)
    (multiple-value-bind (okp2 line2 effect2 book2)
        (leasebook-offer book :offer "off-2" :node "n1" :generation "gen-4"
                         :attempt "att-2" :to "bob" :profile "cap@1" :reserve 1
                         :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
      (declare (ignore effect2))
      (ok (null okp2) "a cross-holder offer is refused")
      (ok (search "shadow" line2) "the refusal names the shadow lease: ~A" line2)
      (check-equal nil (alist-get (cons "off-2" "att-2") (leasebook-reservations book2))
                   "the refused offer writes no reservation")
      (check-equal (leasebook-leases book) (leasebook-leases book2) "no lease is created"))
    (let* ((held (make-leasebook
                  :nodes '(("n1" . :doing))
                  :leases '(("n1" . (:holder "alice"
                                             :deadline "2026-09-20T00:00:00Z"
                                             :default "release")))
                  :holders '(("n1" . "alice"))
                  :pending '(("off-9" :offer "off-9" :node "n1" :to "bob"
                              :attempt "att-9" :generation "gen-4" :reserve 1
                              :until "2026-09-14T12:30:00Z" :state :pending
                              :received t))
                  :reservations '((("off-9" . "att-9") :reserve 1 :state :held))))
           (nleases (length (leasebook-leases held))))
      (multiple-value-bind (okp3 line3 effect3 held2)
          (leasebook-accepted held :offer "off-9" :node "n1" :generation "gen-4"
                              :attempt "att-9" :by "bob" :default "release"
                              :deadline "2026-09-21T00:00:00Z")
        (ok okp3 "a cross-holder reply is retained, not applied: ~A" line3)
        (check-equal :late effect3 "a cross-holder accept is retained :late")
        (check-equal nleases (length (leasebook-leases held2))
                     "the cross-holder reply creates no lease")
        (check-equal 1 nleases "the lease index still holds only alice's one lease")))))

(deftest "accepted-creates-one-lease-or-binds" "docs/SPEC-WORK.md:5238"
    "expected=one-lease-or-bind-to-holders-own;deadline-and-default-unchanged-on-bind"
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "offer: ~A" line)
    (multiple-value-bind (rok rline reffect rbook)
        (leasebook-received book :offer "off-1" :node "n1" :generation "gen-4"
                            :attempt "att-1" :receipt-digest "rd-1" :receipt-id "rc-1"
                            :request "q-recv" :verifier "op-verifier")
      (declare (ignore reffect))
      (ok rok "received: ~A" rline)
      (multiple-value-bind (aok aline aeffect abook)
          (leasebook-accepted rbook :offer "off-1" :node "n1" :generation "gen-4"
                              :attempt "att-1" :by "alice" :default "release"
                              :deadline "2026-09-14T12:30:00Z" :observed-model nil
                              :receipt-id "rc-2" :receipt-digest "rd-2" :request "q-acc")
        (ok aok "accepted: ~A" aline)
        (check-equal :accepted aeffect "the accepted envelope writes :effect :accepted")
        (check-equal 1 (length (leasebook-leases abook)) "exactly one lease is created")
        (check-equal 1 (length (leasebook-w abook)) "exactly one W entry is created")
        (check-equal :unknown (alist-get "n1" (leasebook-observed-models abook))
                     "an absent observed model is recorded unknown")
        (multiple-value-bind (o2ok o2line o2effect o2book)
            (leasebook-offer abook :offer "off-2" :node "n1" :generation "gen-4"
                             :attempt "att-2" :to "alice" :profile "cap@1" :reserve 1
                             :until "2026-09-14T13:00:00Z" :free-slots 4 :profile-ok t)
          (declare (ignore o2effect))
          (ok o2ok "a second offer to the same holder is admitted: ~A" o2line)
          (multiple-value-bind (r2ok r2line r2effect r2book)
              (leasebook-received o2book :offer "off-2" :node "n1" :generation "gen-4"
                                  :attempt "att-2" :receipt-digest "rd-3" :receipt-id "rc-3"
                                  :request "q-recv2" :verifier "op-verifier")
            (declare (ignore r2effect))
            (ok r2ok "second received: ~A" r2line)
            (multiple-value-bind (a2ok a2line a2effect a2book)
                (leasebook-accepted r2book :offer "off-2" :node "n1" :generation "gen-4"
                                    :attempt "att-2" :by "alice" :default "escalate:bob"
                                    :deadline "2026-09-30T00:00:00Z"
                                    :receipt-id "rc-4" :receipt-digest "rd-4" :request "q-acc2")
              (ok a2ok "second accepted binds: ~A" a2line)
              (check-equal :accepted a2effect "binding is still an :accepted")
              (check-equal 1 (length (leasebook-leases a2book))
                           "binding writes no second lease")
              (let ((lease (alist-get "n1" (leasebook-leases a2book))))
                (check-equal "2026-09-14T12:30:00Z" (getf lease :deadline)
                             "the held lease's deadline is unchanged")
                (check-equal "release" (getf lease :default)
                             "the held lease's default is unchanged")))))))))

(deftest "until-is-overdue-not-released" "docs/SPEC-WORK.md:5243"
    "expected=overdue-unreconciled;no-auto-launch;reservation-stands;lease-expiry-out-of-w"
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "offer: ~A" line)
    (multiple-value-bind (uok uline ueffect ubook)
        (lease-until book :offer "off-1" :now "2026-09-14T13:00:00Z")
      (ok uok "until: ~A" uline)
      (check-equal :overdue ueffect "at --until the offer is :overdue")
      (check-equal :overdue (getf (alist-get "off-1" (leasebook-pending ubook)) :state)
                   "the offer is overdue and unreconciled")
      (check-equal :held
                   (getf (alist-get (cons "off-1" "att-1")
                                    (leasebook-reservations ubook)) :state)
                   "the reservation stands")
      (check-equal nil (alist-get "n1" (leasebook-leases ubook))
                   "no lease is created or released")
      (multiple-value-bind (nok nline neffect nbook)
          (leasebook-offer ubook :offer "off-2" :node "n1" :generation "gen-4"
                           :attempt "att-2" :to "alice" :profile "cap@1" :reserve 1
                           :until "2026-09-14T14:00:00Z" :free-slots 4 :profile-ok t)
        (declare (ignore neffect))
        (ok (null nok) "no new offer is admitted for an overdue offer")
        (check-equal (leasebook-pending ubook) (leasebook-pending nbook)
                     "the refused offer changes nothing"))
      (let* ((accepted (accepted-book))
             (xok (nth-value 0 (leasebook-expire accepted :node "n1"
                                                :now "2026-09-15T00:00:00Z")))
             (xbook (nth-value 3 (leasebook-expire accepted :node "n1"
                                                   :now "2026-09-15T00:00:00Z"))))
        (ok xok "lease expiry is admitted")
        (check-equal 0 (length (leasebook-w xbook))
                     "lease expiry takes the task out of W")
        (ok (alist-get "n1" (leasebook-leases xbook))
            "lease expiry retains the friend's capacity and any uncertain execution")))))

(deftest "late-and-duplicate-receipts-are-retained" "docs/SPEC-WORK.md:5243"
    "expected=:effect-:late-retained;duplicate-consumes-no-capacity;conflicting-bytes-refused"
  (let ((book (declined-book)))
    (multiple-value-bind (okp line effect nbook)
        (leasebook-accepted book :offer "off-1" :node "n1" :generation "gen-4"
                            :attempt "att-1" :by "alice" :default "release"
                            :deadline "2026-09-14T13:00:00Z")
      (ok okp "a late accept is retained, not refused: ~A" line)
      (check-equal :late effect "the late accept carries :effect :late")
      (check-equal nil (alist-get "n1" (leasebook-leases nbook))
                   "a late accept revives no lease")))
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "offer: ~A" line)
    (let ((before (leasebook-reservations book)))
      (multiple-value-bind (rok rline reffect rbook)
          (leasebook-receipt book :receipt-id "rc-1" :receipt-digest "rd-1"
                             :request "q1" :stage :received :offer "off-1" :node "n1"
                             :generation "gen-4" :attempt "att-1" :verifier "v")
        (declare (ignore reffect))
        (ok rok "first receipt: ~A" rline)
        (multiple-value-bind (dok dline deffect dbook)
            (leasebook-receipt rbook :receipt-id "rc-1" :receipt-digest "rd-1"
                               :request "q2" :stage :received :offer "off-1" :node "n1"
                               :generation "gen-4" :attempt "att-1" :verifier "v")
          (ok dok "duplicate receipt: ~A" dline)
          (check-equal :duplicate deffect "a duplicate receipt is :duplicate")
          (check-equal before (leasebook-reservations dbook)
                       "a duplicate consumes no capacity twice")
          (multiple-value-bind (cok cline ceffect cbook)
              (leasebook-receipt dbook :receipt-id "rc-1" :receipt-digest "other-bytes"
                                 :request "q3" :stage :received :offer "off-1" :node "n1"
                                 :generation "gen-4" :attempt "att-1" :verifier "v")
            (declare (ignore ceffect))
            (ok (null cok) "conflicting bytes for one receipt id are refused")
            (ok (search "conflicting" cline)
                "the refusal names the conflict: ~A" cline)))))))
