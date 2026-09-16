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
   ;; s6 operation: the clip's transport is one long operation
   #:operation
   #:operation-id #:operation-op #:operation-state #:operation-request
   #:operation-author #:operation-stamp #:operation-updated
   #:operation-staged #:operation-rev #:operation-pushed #:operation-shown
   #:operation-result #:operation-external
   #:make-operation
   #:operation-state-name
   #:operation-ack-line
   #:operation-row-line
   #:operation-note-waiting
   #:operation-no-such-line
   #:operation-terminal
   #:operation-wait
   #:operation-cancel
   ;; s6 partition: day partitions, segments, manifests, the revision merge
   #:parse-duration
   #:stamp-day
   #:window-day-partitions
   #:closed-entry #:ce-rev #:ce-id #:ce-stamp #:make-closed-entry
   #:closed-segment #:seg-name #:seg-entries #:make-closed-segment
   #:day-manifest #:man-name #:man-refs #:make-day-manifest
   #:segmentize-day
   #:manifest-for-day
   #:merge-days-by-revision
   #:closed-day-selection
   #:as-of-refusal
   #:as-of-closed-rows
   ;; s6 snapshot: bounded paged indexes, the retention overlay, publication
   #:*pages-read*
   #:index-page #:page-name #:page-kind #:page-records #:page-bytes
   #:page-entries #:page-available
   #:make-page
   #:open-page
   #:closed-index #:ci-pages #:make-closed-index
   #:closed-row-lookup
   #:dedup-index #:di-pages #:make-dedup-index
   #:dedup-lookup
   #:dedup-unavailable-line
   #:budget-limited-query
   #:query-more-line
   #:overlay #:ov-pages #:ov-entries
   #:replay-overlay
   #:publication #:make-publication
   #:pub-revision #:pub-snapshot-sha #:pub-closed-sha #:pub-dedup-sha
   #:pub-manifests #:pub-files
   #:verify-publication
   ;; s6 clip: the CAS guard, the boundary record, rotation, the refusal
   #:start-clip
   #:clip-cas-guard
   #:clip-terminal-line
   #:clip-raced-line
   #:clip-fail-line
   #:clip-boundary-record
   #:clip-rotate
   #:clip-overflow-p
   #:clip-overflow-line
   ;; s6 savepoint: the local durable snapshot
   #:savepoint #:sp-id #:sp-rev #:sp-checkpoint #:sp-pushed #:sp-boundary
   #:sp-age #:sp-unshared #:sp-manifest #:sp-at #:sp-verdict
   #:savepoint-ok-line
   #:savepoint-row-line
   #:savepoint-verify
   #:savepoint-compare
   #:savepoint-note-line
   #:savepoint-fail-line))

(defpackage #:nova-work/tests
  (:use #:common-lisp #:nova-work)
  (:export #:run-all #:main))
