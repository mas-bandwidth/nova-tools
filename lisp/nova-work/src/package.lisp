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
   ;; instrumentation
   #:*visits* #:*parses* #:*replays*
   #:with-instrumentation
   ;; journal acceptance interface
   #:journal-accept
   #:journal-record
   #:journal-lookup
   #:ordering-journal
   #:make-ordering-journal
   #:journal-order
   #:rejecting-journal
   #:make-rejecting-journal
   ;; kernel
   #:make-kernel
   #:kernel-state
   #:kernel-journal
   #:submit
   #:ask-size
   #:open-issue-count
   #:open-leaf-count))

(defpackage #:nova-work/tests
  (:use #:common-lisp #:nova-work)
  (:export #:run-all #:main))
