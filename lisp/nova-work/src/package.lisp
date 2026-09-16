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
   ;; slice 9 (S9): the fleet, the routes and the slots (own files)
   #:s9-stamp< #:s9-stamp<= #:s9-join-names
   ;; the fleet
   #:make-s9-machine #:machine-id #:machine-name #:machine-owner #:machine-connect
   #:machine-roles #:machine-permits #:machine-excludes #:machine-limits #:machine-facts
   #:machine-retired-p #:make-s9-fleet #:fleet-machines #:fleet-order #:fleet-friends
   #:fleet-revision #:fleet-live-machines #:fleet-find-machine
   #:machine-register #:machine-retire #:machine-permit #:machine-exclude #:machine-limit
   #:machine-fact #:fleet-query #:s9-machine-admits-p #:s9-machine-row #:s9-machine-event
   ;; the slots
   #:make-s9-allocation #:allocation-allocation-id #:allocation-machine
   #:allocation-machine-revision #:allocation-machine-generation
   #:allocation-allocation-generation #:allocation-slot #:allocation-slots
   #:allocation-batch #:allocation-node #:allocation-offer #:allocation-attempt
   #:allocation-holder #:allocation-parent #:allocation-suspect-p
   #:allocation-expires-at #:allocation-fenced-p #:allocation-released-p
   #:make-s9-allocator #:allocator-machines #:allocator-aliases #:allocator-writers
   #:allocator-allocations #:allocator-order #:allocator-dedup #:allocator-next-number
   #:allocator-resolve #:allocator-find-allocation #:allocator-live-allocations
   #:allocator-used-slots #:take-allocation #:heartbeat-allocation #:release-allocation
   #:allocation-suspect #:allocation-fence #:allocator-set-concurrent #:list-allocation
   #:machine-probe
   ;; the routes
   #:make-s9-route #:route-id #:route-provider #:route-endpoint #:route-key-location
   #:route-plan #:route-cost-per-mtok #:route-capabilities #:route-owner #:route-probe-record
   #:route-abstains #:route-benched-until #:route-retired-p
   #:make-s9-routes #:routes-routes #:routes-order #:routes-classes #:routes-friends
   #:routes-revision #:routes-live-routes #:routes-find-route
   #:route-register #:route-retire #:route-probe #:route-projection #:routes-query
   #:s9-key-location-p #:s9-key-location-name #:s9-route-row #:s9-route-passes-p
   ;; the prompt profile
   #:make-s9-profile #:profile-name #:profile-model #:profile-harness #:profile-work-type
   #:profile-pointer #:profile-digest #:profile-policy #:profile-evidence #:profile-expiry
   #:profile-owner #:profile-revision #:profile-revisions
   #:make-s9-profiles #:profiles-profiles #:profiles-order #:profiles-friends
   #:profiles-revision #:profiles-find #:profile-write #:profile-edit
   #:profile-status-line #:profile-digest-mismatch-p))

(defpackage #:nova-work/tests
  (:use #:common-lisp #:nova-work)
  (:export #:run-all #:main))
