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
    ;; S8 — presence, friends and escalations: the duty tier (#500)
    ;; presence (s8-presence.lisp)
    #:make-presence
    #:presence-friend #:presence-at #:presence-clock
    #:presence-source #:presence-seen #:presence-by
    #:presence-source-valid-p #:presence-rank #:newest-presence
    #:presence-reading
    #:make-wait-row
    #:wait-row-friend #:wait-row-process-alive #:wait-row-beat-written
    #:wait-row-delivery-handled #:wait-row-parent-woke
    #:holds-resident-session-p #:holds-duty-session-p #:session-kind
    #:format-friend-row #:format-awake-query-ok
    ;; escalation, the duty tier, quiet time and the coordination measure
    ;; (s8-escalation.lisp)
    #:make-policy-rule #:policy-rule-id #:policy-rule-by
    #:policy-rule-qualifies #:policy-rule-model #:policy-rule-cost
    #:policy-rule-default
    #:load-policy #:duty-policy-id #:duty-policy-rules #:duty-policy-default
    #:make-escalation #:escalation-rule #:escalation-default #:escalation-raised-at
    #:raise-escalation #:escalation-age #:read-escalation
    #:execute-policy
    #:duty-tick #:event-cost
    #:make-decision #:decision-cost #:decision-wrong-p #:decision-missed-p
    #:decision-recovery-latency
    #:accepted-decision-p #:coordination-measure
    ;; friends, capability groups, four facts (s8-friends.lisp)
    #:make-friend-record #:friend-change #:friend-friend #:friend-role
    #:friend-scope #:friend-participation #:friend-capability #:friend-group
    #:friend-limit #:friend-reason
    #:parse-friend-args #:+friend-changes+
    #:make-capability-entry #:capability-group #:capability-id
    #:capability-source #:capability-last-verified #:capability-availability
    #:capability-constraints #:capability-declared-support
    #:capability-verified-runtime #:capability-free-capacity
    #:make-capability
    #:make-offer-intent #:offer-intent-id #:offer-intent-to
    #:offer-intent-profile #:offer-intent-reserve #:offer-intent-until
    #:offer-intent-requested-model
    #:admit-offer #:make-offer-result #:offer-result-effect
    #:offer-result-offer-id #:offer-result-reserve
    #:acknowledge-received #:acknowledge-accepted #:decline-offer
    #:make-receipt #:receipt-offer-id #:receipt-reply #:receipt-stage
    ;; config and observe records (s8-config.lisp)
    #:make-config-record #:config-friend #:config-base #:config-revision
    #:config-hash #:config-parts #:config-reason
    #:make-observation #:observation-change #:observation-friend
    #:observation-state #:observation-source #:observation-attempt
    #:observation-observed-model #:observation-bench #:observation-usage
    #:observation-reason
    #:parse-config-args #:parse-observe-args
    ;; pipeline reply correlation (s8-replies.lisp)
    #:correlate-replies #:reassemble-frames))

(defpackage #:nova-work/tests
  (:use #:common-lisp #:nova-work)
  (:export #:run-all #:main))
