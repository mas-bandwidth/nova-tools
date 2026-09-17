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
   #:dedup-page-available-p
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
    ;; state export / isolated load (SPEC-WORK.md:3273-3297, replays :5879-5905)
    #:export-state
    #:export-complete
    #:export-manifest
    #:state-export-id
    #:state-export-revision
    #:state-export-bytes
    #:state-export-members
    #:state-export-status
    #:state-export-terminal
    #:state-export-published
    #:publish-state-export
    #:cancel-state-export
    #:retention-pass
    #:load-state
    #:snapshot-state
    #:snapshot-revision
    #:snapshot-query
    #:snapshot-accept-session-p
    #:with-isolation
    #:isolation-count
    #:isolation-writes
    #:make-fenced-session
    #:fenced-export-start
    #:fenced-status
    #:fenced-cancel
    #:fenced-operation-list
    #:fenced-write
    #:fenced-claim
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
    #:review-reusable-p
    ;; CONFIG roles (SPEC-WORK.md:5214)
    #:make-role-record
    #:role-record-id
    #:role-record-scope
    #:role-record-source
    #:role-record-reserved-for
    #:role-record-essential-security-only-p
    #:role-record-different-perspective-p
    #:role-record-participation-p
    #:role-record-limit
    #:make-role-config
    #:role-config-roles
    #:role-from-config
    #:role-inferred-from-model-p
    #:agreed-limit-for
    #:spend-role
    ;; execution control: the durable hold, the capture, the dispatch barrier and
    ;; the cancellation edge (SPEC-WORK.md:3921-3980)
    #:kernel-controls
    #:kernel-holds
    #:kernel-offers
    #:record-attempt
    #:live-attempt-ids
    #:live-attempt-p
    #:attempt-id
    #:attempt-node
    #:attempt-generation
    #:attempt-live-p
    #:execution-stop
    #:execution-pause
    #:hold-p
    #:hold-id
    #:hold-action
    #:hold-scope
    #:hold-targets
    #:hold-directives
    #:hold-anchor
    #:hold-revision
    #:hold-span
    #:hold-manifest
    #:hold-released-p
    #:hold-durable-p
    #:hold-pin
    #:hold-covers-node-p
    #:held-p
    #:capture-manifest
    #:manifest-hold-id
    #:manifest-revision
    #:manifest-span
    #:manifest-target-ids
    #:manifest-content-hash
    #:clip
    #:recover-controls
    #:prepare-offer
    #:send-offer
    #:accept-offer
    #:reconcile-offer
    #:release-hold
    #:offer-effect
    #:offer-lease-p
    #:offer-launched-p
    #:lease-count
    #:launch-count
    #:launch-attempt
    #:correct-attempt
    #:move-node
    #:request-cancel
    #:cancel-confirm
    #:cancel-requested-p
    #:goal-stop))

(defpackage #:nova-work/tests
  (:use #:common-lisp #:nova-work)
  (:export #:run-all #:main))
