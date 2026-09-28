;;;; issue-2338.lisp --- tests for nova-tools#2338: derive lease expiry,
;;;; extend-once, escalate and reject a lease with no end.
;;;;
;;;; The deftest drives the pure leasebook of src/assignment.lisp directly.

(in-package #:nova-work/tests)

(deftest "issue-2338" "nova-tools#2338"
    "Derive lease expiry, extend-once, escalate and reject a lease with no end"
  ;; ---- lease-without-end-refused (covers behaviour 10) ----
  ;; A lease with no :deadline or no :default is refused at write time.
  (let* ((book (make-leasebook :nodes '(("n0" . :doing))))
         (obook (nth-value 3 (leasebook-offer book :offer "off-0" :node "n0"
                                 :generation "gen-4" :attempt "att-0"
                                 :to "alice" :profile "cap@1" :reserve 2
                                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)))
         (rbook (nth-value 3 (leasebook-received obook :offer "off-0" :node "n0"
                                    :generation "gen-4" :attempt "att-0"
                                    :receipt-digest "rd-0" :receipt-id "rc-0"
                                    :request "qr" :verifier "v"))))
    (multiple-value-bind (okp line)
        (leasebook-accepted rbook :offer "off-0" :node "n0" :generation "gen-4"
                            :attempt "att-0" :by "alice" :deadline nil :default "release")
      (ok (null okp) "a lease with no deadline was accepted, not refused: ~A" line))
    (multiple-value-bind (okp line)
        (leasebook-accepted rbook :offer "off-0" :node "n0" :generation "gen-4"
                            :attempt "att-0" :by "alice" :deadline "2026-09-14T12:30:00Z" :default nil)
      (ok (null okp) "a lease with no default was accepted, not refused: ~A" line)))

  ;; ---- lease-expiry-is-derived (covers behaviour 11) ----
  ;; A lease past its deadline reads expired: the third value is :expired, the
  ;; W entry is removed, the lease record is retained.
  (let* ((book (make-leasebook :nodes '(("n1" . :doing))))
         (obook (nth-value 3 (leasebook-offer book :offer "off-1" :node "n1"
                                 :generation "gen-4" :attempt "att-1"
                                 :to "alice" :profile "cap@1" :reserve 2
                                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)))
         (rbook (nth-value 3 (leasebook-received obook :offer "off-1" :node "n1"
                                    :generation "gen-4" :attempt "att-1"
                                    :receipt-digest "rd-1" :receipt-id "rc-1"
                                    :request "q1" :verifier "v")))
         (abook (nth-value 3 (leasebook-accepted rbook :offer "off-1" :node "n1"
                                    :generation "gen-4" :attempt "att-1"
                                    :by "alice" :default "release"
                                    :deadline "2026-09-14T12:30:00Z"))))
    ;; Not expired when clock is before deadline.
    (multiple-value-bind (okp line effect nbook)
        (leasebook-expire abook :node "n1" :now "2026-09-14T12:00:00Z")
      (ok okp "not-yet-expired reads as not expired: ~A" line)
      (check-equal :active effect "deadline not yet reached is :active, not :expired"))
    ;; Expired when clock is past deadline: the third value is :expired, the W
    ;; entry is removed.
    (multiple-value-bind (okp line effect nbook)
        (leasebook-expire abook :node "n1" :now "2026-09-14T13:00:00Z")
      (ok okp "expiry was refused: ~A" line)
      (check-equal :expired effect "a past-deadline lease reads :expired")
      (check-equal 0 (length (leasebook-w nbook))
                   "a past-deadline lease is out of W")
      (ok (alist-get "n1" (leasebook-leases nbook))
          "the lease record is retained for capacity")))

  ;; ---- extend-once-extends-then-expires (covers behaviour 12) ----
  ;; On the first expiry a :extend-once lease reads as extended by the same
  ;; length and :stale says so; on the second it reads :expired. The lease runs
  ;; 11:00 -> 12:00 (one hour), so the one extension moves :deadline to 13:00:
  ;; 13:00:00 itself still reads :stale, 13:00:01 reads :expired.
  (let* ((book (make-leasebook :nodes '(("n2" . :doing))))
         (obook (nth-value 3 (leasebook-offer book :offer "off-2" :node "n2"
                                 :generation "gen-4" :attempt "att-2"
                                 :to "alice" :profile "cap@1" :reserve 2
                                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)))
         (rbook (nth-value 3 (leasebook-received obook :offer "off-2" :node "n2"
                                    :generation "gen-4" :attempt "att-2"
                                    :receipt-digest "rd-2" :receipt-id "rc-2"
                                    :request "q2" :verifier "v")))
         (abook (nth-value 3 (leasebook-accepted rbook :offer "off-2" :node "n2"
                                    :generation "gen-4" :attempt "att-2"
                                    :by "alice" :default "release"
                                    :stamp "2026-09-14T11:00:00Z"
                                    :deadline "2026-09-14T12:00:00Z"
                                    :extend-once t))))
    ;; An :extend-once lease with no :stamp has no length to extend by: refused.
    (multiple-value-bind (okp line)
        (leasebook-accepted rbook :offer "off-2" :node "n2" :generation "gen-4"
                            :attempt "att-2" :by "alice" :default "release"
                            :deadline "2026-09-14T12:00:00Z" :extend-once t)
      (ok (null okp) "an :extend-once lease with no :stamp was accepted, not refused: ~A" line))
    ;; First expiry (after deadline): reads :stale, deadline revised by the same length.
    (multiple-value-bind (okp line effect e1book)
        (leasebook-expire abook :node "n2" :now "2026-09-14T12:30:00Z")
      (ok okp "first extend-once expiry refused: ~A" line)
      (check-equal :stale effect "first expiry of extend-once is :stale, not :expired")
      (check-equal 1 (length (leasebook-w e1book))
                   "an extended lease stays in W")
      (let ((lease (alist-get "n2" (leasebook-leases e1book))))
        (ok lease "the lease is retained on extend-once")
        (ok (getf lease :extended-once-p) "the lease carries :extended-once-p")
        (check-string= "2026-09-14T13:00:00Z" (getf lease :deadline)
                       "the first extension moves :deadline by the lease's own length"))
      ;; The new boundary: at the revised deadline it still reads :stale ...
      (multiple-value-bind (okp2 line2 effect2 b2)
          (leasebook-expire e1book :node "n2" :now "2026-09-14T13:00:00Z")
        (ok okp2 "read at the revised deadline refused: ~A" line2)
        (check-equal :stale effect2 "at the revised deadline the lease still reads :stale")
        (check-equal 1 (length (leasebook-w b2))
                     "at the revised deadline the lease is still in W"))
      ;; ... and one second past it, it reads :expired and leaves W.
      (multiple-value-bind (okp3 line3 effect3 b3)
          (leasebook-expire e1book :node "n2" :now "2026-09-14T13:00:01Z")
        (ok okp3 "second extend-once expiry refused: ~A" line3)
        (check-equal :expired effect3 "past the revised deadline the lease reads :expired")
        (check-equal 0 (length (leasebook-w b3))
                     "second expiry takes the task out of W")))
    ;; A first read already past the revised deadline reads :expired at once.
    (multiple-value-bind (okp line effect fbook)
        (leasebook-expire abook :node "n2" :now "2026-09-14T15:00:00Z")
      (ok okp "late first read refused: ~A" line)
      (check-equal :expired effect "a first read past the revised deadline is :expired")
      (check-equal 0 (length (leasebook-w fbook))
                   "a first read past the revised deadline takes the task out of W")))

  ;; ---- escalate-reads-escalated-to (covers behaviour 13) ----
  ;; At expiry a (:escalate "<name>") lease reads expired with the node
  ;; escalated-to=<name>.
  (let* ((book (make-leasebook :nodes '(("n3" . :doing))))
         (obook (nth-value 3 (leasebook-offer book :offer "off-3" :node "n3"
                                 :generation "gen-4" :attempt "att-3"
                                 :to "alice" :profile "cap@1" :reserve 2
                                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)))
         (rbook (nth-value 3 (leasebook-received obook :offer "off-3" :node "n3"
                                    :generation "gen-4" :attempt "att-3"
                                    :receipt-digest "rd-3" :receipt-id "rc-3"
                                    :request "q3" :verifier "v")))
         (abook (nth-value 3 (leasebook-accepted rbook :offer "off-3" :node "n3"
                                    :generation "gen-4" :attempt "att-3"
                                    :by "alice" :default "release"
                                    :deadline "2026-09-14T12:00:00Z"
                                    :escalate "rowan"))))
    (multiple-value-bind (okp line effect ebook)
        (leasebook-expire abook :node "n3" :now "2026-09-14T13:00:00Z")
      (ok okp "escalate expiry refused: ~A" line)
      (check-equal :expired effect "escalate lease expiry reads :expired")
      (check-equal 0 (length (leasebook-w ebook))
                   "an expired escalate lease is out of W")
      (let ((lease (alist-get "n3" (leasebook-leases ebook))))
        (ok lease "the lease record is retained")
        (check-string= "rowan" (getf lease :escalated-to)
                       "escalated-to=<name> is attached to the lease record")))))