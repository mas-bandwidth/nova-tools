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
   #:root-digest
   #:state-canonical-form
   #:reconstruct-state
   #:apply-event
   ;; execution lease and working set view
   #:wnode-holder
   #:wnode-lease-id
   #:wnode-deadline
   #:wnode-default-action
   #:wnode-last-heartbeat
   #:wnode-heartbeat-evidence
   #:is-working-p
   #:state-working-count
   #:working-ids
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
   ;; ownership record and session lifecycle
   #:ownership-record
   #:make-ownership-record
   #:owner-owner
   #:owner-generation
   #:owner-token
   #:owner-stamp
   #:owner-until
   #:owner-successor
   #:evaluate-ownership-claim
   #:parse-ownership-record
   #:format-ownership-record
   #:session
   #:make-session
   #:session-owner
   #:session-generation
   #:session-token
   #:session-until
   #:session-state
   #:session-kernel
   #:session-start
   #:session-status
   #:session-submit
   #:session-check-admission
   #:session-reconfirm
   #:parse-rfc3339
   #:format-rfc3339
   #:parse-duration))

(defpackage #:nova-work/tests
  (:use #:common-lisp #:nova-work)
  (:export #:run-all #:main))
