;;;; package.lisp --- nova-work slice 1: the internal C/O transition kernel.
;;;;
;;;; Spec: docs/SPEC-WORK.md at 7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64
;;;; (mas-bandwidth/nova-tools PR #231, branch spec/nova-work).
;;;;
;;;; This system is the internal kernel only. There is no session, no CLI, no
;;;; socket, no provider and no live import here; see README.md for the boundary.

(defpackage #:nova-work
  (:use #:common-lisp)
  (:export
   ;; conditions
   #:nova-work-error
   #:restricted-data-violation
   #:unsupported-input
   #:not-implemented
   #:journal-error
   #:journal-corrupt-data
   #:journal-corrupt-data-path
   #:journal-corrupt-data-offset
   #:journal-corrupt-data-reason
   #:journal-mismatch
   #:journal-mismatch-path
   #:journal-mismatch-what
   #:journal-sync-failed
   #:journal-sync-failed-path
   #:journal-sync-failed-reason
   #:journal-uncertain-write
   #:journal-uncertain-write-path
   #:journal-uncertain-write-reason
   ;; restricted data and canonical serialization
   #:+absent+
   #:absentp
   #:canonical-string
   #:canonical-print
   #:read-restricted
   ;; digest
   #:sha256-hex
   ;; events
   #:make-work-event
   #:work-event-kind
   #:work-event-node
   #:work-event-by
   #:work-event-fields
   #:work-event-stamp
   #:work-event-clock
   #:work-event-request
   #:work-event-generation-owner
   #:work-event-rev
   #:work-event-session-written-p
   #:event-digest-form
   #:event-record-form
   #:event-id
   #:closed-row-key
   #:payload-digest
   ;; state
   #:make-seed-state
   #:state-open-count
   #:state-closed-count
   #:state-revision
   #:state-history
   #:state-closed-rows
   #:node-open-count
   #:node-branch
   #:node-state
   #:node-required-count
   #:node-required-open
   #:root-digest
   #:state-canonical-form
   #:reconstruct-state
   #:apply-event
   ;; instrumentation
   #:*visits* #:*parses* #:*replays*
   #:with-instrumentation
   ;; journal acceptance interface
   #:*before-apply-hook*
   #:journal-accept
   #:journal-record
   #:journal-lookup
   #:ordering-journal
   #:make-ordering-journal
   #:journal-order
   #:rejecting-journal
   #:make-rejecting-journal
   #:journal-reject-on
   #:file-journal
   #:make-file-journal
   #:open-file-journal
   #:close-file-journal
   #:with-file-journal
   #:journal-fail-sync-on
   #:journal-fail-pre-write-on
   #:journal-fail-partial-write-on
   #:journal-fail-creation-sync-on
   #:replay-journal
   #:read-header
   #:read-record-frame
   #:journal-path
   #:journal-seq
   #:journal-uncertain-p
   #:sync-stream
   #:sync-directory
   ;; kernel
   #:make-kernel
   #:kernel-state
   #:kernel-journal
   #:kernel-next-rev
   #:submit
   #:ask-size
    #:open-issue-count
    #:open-leaf-count
    ;; applicable/delegation replays (Go card 8132)
    #:note-id
    #:make-note
    #:candidate-role
    #:candidate-model
    #:constraint-p
    #:constraint-deny
    #:constraint-prefer
    #:constraint-reason
    #:deny-matches-p
    #:constraint-excludes-p
    #:applicable
    #:make-applicable-answer
    #:applicable-answer-p
    #:applicable-answer-rev
    #:applicable-answer-from
    #:applicable-answer-verdicts
    #:applicable-answer-notes
    #:applicable-answer-fail
    #:applicable-answer-shown
    #:applicable-answer-more
    #:verdict-eligible-p
    #:verdict-unknown-p
    #:verdict-planning-p
    #:verdict-excluded-p
    #:excluded-note-ids
    #:price-route
    #:route-selection
    #:build-card
    #:*delegation-packet-fields*
    #:delegation-admission
    #:gate-2-refusal
    #:gate-3-refusal
    #:admission
    #:admission-expect
    #:gate-4-refusal
    #:offer-to
    #:non-terminating-p
    #:uncertain-outcome-p
    #:retry-permitted-p
    #:make-execution-record
    #:execution-record-requested-limit
    #:execution-record-expiry
    #:execution-record-stop
    #:make-machinery-receipt
    #:machinery-receipt-head
    #:machinery-receipt-result
    #:book-receipt
    #:review-binds-p
    #:review-reusable-p))

(defpackage #:nova-work/tests
  (:use #:common-lisp #:nova-work)
  (:export #:run-all #:main))
