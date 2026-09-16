;;;; s9.lisp --- the eleven named replays of slice 9 (The fleet, the routes and
;;;; the slots, #678/#687/#691): one deftest per replay, each naming its line of
;;;; docs/SPEC-WORK.md.
;;;;
;;;; Red first: these were written before any s9-*.lisp existed and each name is
;;;; recorded in notes.txt; they are made green by the pure functions and records
;;;; of src/s9-*.lisp, wired into the command thread by a later card.

(in-package #:nova-work/tests)

;;; The fleet and Fleet allocation (PR678's six rules, SPEC-AHEAD: #500).

(deftest "machine-is-config-and-never-a-work-tree-node" "docs/SPEC-WORK.md:3598"
    "expected=kind=machine,node=absent,no-count-moves,no-settle"
  (let ((fleet (make-s9-fleet :friends '("glenn"))))
    (multiple-value-bind (ok line fleet2 event)
        (machine-register
         fleet
         '(:id "m-a1" :name "studio" :owner "glenn" :connect "profile:studio"
           :roles (:build :test) :permits ("go-test") :excludes ("bench:schema")
           :limits (:concurrent 4 :cores 16)
           :facts (:arch "arm64" :os "macos" :declared-by "glenn" :declared-at "2026-09-15T01:05:00Z")
           :request "req-1" :by "glenn" :reason "add studio"))
      (ok ok "machine register refused: ~A" line)
      ;; A machine is a CONFIG member whose subject is a machine identity, not a
      ;; node: the event writes :node (:absent).
      (check-equal :machine (getf event :kind) "the machine event kind")
      (check-equal +absent+ (getf event :node) "the machine event's :node is not absent")
      ;; Equipment is never a work-tree node: a work-tree state sees no machine
      ;; and moves no count.
      (let ((state (make-seed-state
                    '((:id "root" :type :work-set :parent nil :state :unknown)
                      (:id "root/t1" :type :task :parent "root" :state :doing)))))
        (check-equal 2 (state-open-count state) "|O| before")
        (machine-register
         fleet
         '(:id "m-a2" :name "mini" :owner "glenn" :connect "profile:mini"
           :roles (:build) :limits (:concurrent 2) :facts (:declared-by "glenn" :declared-at "2026-09-15T01:06:00Z")
           :request "req-2" :by "glenn" :reason "add mini"))
        (check-equal 2 (state-open-count state) "a machine write moved |O|"))
      (check-equal "m-a1" (machine-id (fleet-find-machine fleet "m-a1")) "the registered member"))))

(deftest "allocation-binds-machine-slot-generation" "docs/SPEC-WORK.md:3621"
    "expected=machine+slot+generation+batch+node+offer+attempt"
  (let ((a (make-s9-allocator :machines '((:id "m-a1" :concurrent 2 :cores 16 :generation 3 :revision 7)))))
    (multiple-value-bind (ok line alloc)
        (take-allocation a '(:machine "m-a1" :node "N1" :slots 1 :offer "o1" :attempt "a1"
                             :generation 3 :request-ref "r1" :batch "b1"
                             :holder "glenn" :allocator "al-1"))
      (ok ok "take refused: ~A" line)
      (check-equal "m-a1" (allocation-machine alloc) "the bound machine")
      (check-equal 7 (allocation-machine-revision alloc) "the CONFIG revision referenced")
      (check-equal 1 (allocation-slot alloc) "the bound slot")
      (check-equal 3 (allocation-machine-generation alloc) "the machine generation")
      (ok (plusp (allocation-allocation-generation alloc)) "the allocation generation was not drawn")
      (check-equal "b1" (allocation-batch alloc) "the bound batch")
      (check-equal "N1" (allocation-node alloc) "the bound node")
      (check-equal "o1" (allocation-offer alloc) "the bound offer")
      (check-equal "a1" (allocation-attempt alloc) "the bound attempt"))))

(deftest "allocation-take-is-atomic-and-idempotent" "docs/SPEC-WORK.md:3651"
    "expected=retry-original-id,changed-payload-refused,no-partial-grant"
  (let ((a (make-s9-allocator :machines '((:id "m-a1" :concurrent 1 :generation 2)))))
    (multiple-value-bind (ok1 line1 alloc1)
        (take-allocation a '(:machine "m-a1" :node "N1" :slots 1 :offer "o1" :attempt "a1"
                             :generation 2 :request-ref "r1" :batch "b1" :holder "g" :allocator "al"))
      (ok ok1 "first take refused: ~A" line1)
      (let ((id1 (allocation-allocation-id alloc1)))
        (multiple-value-bind (ok2 line2 alloc2)
            (take-allocation a '(:machine "m-a1" :node "N1" :slots 1 :offer "o1" :attempt "a1"
                                 :generation 2 :request-ref "r1" :batch "b1" :holder "g" :allocator "al"))
          (ok ok2 "the retry refused instead of replaying: ~A" line2)
          (check-equal id1 (allocation-allocation-id alloc2) "the retry issued a second id")
          (check-string= line1 line2 "the retry did not answer the original OK line")
          (check-equal 1 (length (allocator-live-allocations a "m-a1"))
                       "the retry debited twice"))
        (multiple-value-bind (ok3 line3)
            (take-allocation a '(:machine "m-a1" :node "N2" :slots 1 :offer "o1" :attempt "a1"
                                 :generation 2 :request-ref "r1" :batch "b1" :holder "g" :allocator "al"))
          (ok (not ok3) "a changed payload under the id was accepted")
          (ok (search "reused with a different payload" line3)
              "the dedup refusal does not name itself: ~A" line3))
        (multiple-value-bind (ok4 line4)
            (take-allocation a '(:machine "m-a1" :node "N3" :slots 1 :offer "o2" :attempt "a2"
                                 :generation 2 :request-ref "r2" :batch "b2" :holder "g" :allocator "al"))
          (ok (not ok4) "a full machine granted a partial slot")
          (ok (search "capacity" line4) "the refusal does not name capacity: ~A" line4))))))

(deftest "expiry-marks-suspect-reuse-needs-fencing" "docs/SPEC-WORK.md:3665"
    "expected=suspect-blocks-renewal,not-fenced-blocks-reuse,fencing-frees"
  (let ((a (make-s9-allocator :machines '((:id "m-a1" :concurrent 1 :generation 2)))))
    (multiple-value-bind (ok1 line1 alloc1)
        (take-allocation a '(:machine "m-a1" :node "N1" :slots 1 :offer "o1" :attempt "a1"
                             :generation 2 :request-ref "r1" :batch "b1" :holder "g" :allocator "al"))
      (declare (ignore line1))
      (ok ok1 "take refused")
      (allocation-suspect a (allocation-allocation-id alloc1) "2026-09-16T00:00:00Z")
      (multiple-value-bind (ok2 line2)
          (heartbeat-allocation a '(:allocation "alloc-1" :generation 2))
        (ok (not ok2) "a suspect allocation was renewed")
        (ok (search "suspect since=" line2) "the renewal does not name the suspect: ~A" line2))
      (multiple-value-bind (ok3 line3)
          (take-allocation a '(:machine "m-a1" :node "N2" :slots 1 :offer "o2" :attempt "a2"
                               :generation 2 :request-ref "r2" :batch "b2" :holder "g" :allocator "al"))
        (ok (not ok3) "an unfenced suspect slot was reused")
        (ok (search "not fenced" line3) "the reuse refusal does not name fencing: ~A" line3))
      (allocation-fence a "alloc-1")
      (multiple-value-bind (ok4 line4)
          (take-allocation a '(:machine "m-a1" :node "N2" :slots 1 :offer "o2" :attempt "a2"
                               :generation 2 :request-ref "r3" :batch "b2" :holder "g" :allocator "al"))
        (ok ok4 "fenced capacity did not return: ~A" line4)))))

(deftest "probe-records-observed-active-and-touches-no-config" "docs/SPEC-WORK.md:3680"
    "expected=dated-evidence-with-source,no-config-change"
  (let ((alloc (make-s9-allocator :machines '((:id "m-a1" :concurrent 2 :cores 16 :generation 2)))))
    (let* ((before (copy-tree (gethash "m-a1" (allocator-machines alloc))))
           (line (machine-probe alloc '(:machine "m-a1" :slot 1 :fact :observed
                                        :at "2026-09-16T00:00:00Z" :source "probe:live"))))
      (ok (search "fact=observed" line) "the probe carries no fact=: ~A" line)
      (ok (search "at=2026-09-16T00:00:00Z" line) "the probe carries no date: ~A" line)
      (ok (search "source=probe:live" line) "the probe carries no source: ~A" line)
      (check-equal before (gethash "m-a1" (allocator-machines alloc))
                   "a probe wrote CONFIG"))))

(deftest "one-allocator-per-machine-aliases-share-nested-conserve" "docs/SPEC-WORK.md:3706"
    "expected=second-allocator-refused,alias-shares-identity,counts-once"
  (let ((a (make-s9-allocator :machines '((:id "m-a1" :concurrent 2 :generation 2))
                              :aliases '("studio-alias" "m-a1"))))
    (multiple-value-bind (ok1 line1 alloc1)
        (take-allocation a '(:machine "studio-alias" :node "N1" :slots 1 :offer "o1" :attempt "a1"
                             :generation 2 :request-ref "r1" :batch "b1" :holder "g" :allocator "al-1"))
      (ok ok1 "the alias take refused: ~A" line1)
      (check-equal "m-a1" (allocation-machine alloc1) "an alias did not resolve to one identity"))
    (multiple-value-bind (ok2 line2)
        (take-allocation a '(:machine "m-a1" :node "N1b" :slots 0 :offer "o" :attempt "a"
                             :generation 2 :request-ref "rx" :batch "b" :holder "g" :allocator "al-2"))
      (ok (not ok2) "a second allocator was admitted")
      (ok (search "allocator held" line2) "the refusal does not say allocator held: ~A" line2))
    (multiple-value-bind (ok3 line3)
        (take-allocation a '(:machine "m-a1" :node "N2" :slots 1 :offer "o2" :attempt "a2"
                             :generation 2 :request-ref "r2" :batch "b2" :holder "g" :allocator "al-1"))
      (ok ok3 "the canonical take refused: ~A" line3)
      (check-equal 2 (length (allocator-live-allocations a "m-a1"))
                   "aliases double-counted one slot"))
    (multiple-value-bind (ok4 line4)
        (take-allocation a '(:machine "m-a1" :node "N3" :slots 1 :offer "o3" :attempt "a3"
                             :generation 2 :request-ref "r3" :batch "b3" :holder "g" :allocator "al-1"))
      (ok (not ok4) "capacity was conserved past the declared concurrent")
      (ok (search "capacity" line4) "the refusal does not name capacity: ~A" line4))))

;;; Model routes (PR691's four rules, SPEC-AHEAD: #500).

(deftest "route-config-lists-key-by-path-never-value" "docs/SPEC-WORK.md:3776"
    "expected=key-location-path-or-env,value-never-printed"
  (let ((r (make-s9-routes :friends '("glenn"))))
    (multiple-value-bind (ok line reg route)
        (route-register r '(:id "anthropic/claude-3.7-sonnet" :provider "anthropic"
                            :endpoint "https://api.example.com"
                            :key-location (:env "ANTHROPIC_KEY")
                            :plan :metered :cost-per-mtok 15
                            :capabilities (:text :yes :code :yes :tool-calls :yes)
                            :owner "glenn" :request "req-1" :reason "team route"))
      (declare (ignore reg))
      (ok ok "route register refused: ~A" line)
      (check-equal '(:env "ANTHROPIC_KEY") (route-key-location route)
                   "the key-location is not a location")
      (check-equal "ANTHROPIC_KEY" (s9-key-location-name (route-key-location route))
                   "the listed location is not the env name"))
    (multiple-value-bind (ok2 line2)
        (route-register r '(:id "x/y" :provider "p" :endpoint "e" :key-location "sk-123"
                            :plan :metered :cost-per-mtok 1
                            :capabilities (:text :yes :code :yes :tool-calls :yes)
                            :owner "glenn" :request "req-2"))
      (ok (not ok2) "a bare key value was accepted as a key-location")
      (ok (search "credential in record" line2)
          "the refusal does not name the credential: ~A" line2))))

(deftest "routing-picks-flat-before-metered" "docs/SPEC-WORK.md:3793"
    "expected=flat,free,metered;cost-ascending;tie-bytewise-id;n-shares"
  (let ((r (make-s9-routes :friends '("glenn")
                            :classes '("bench" ("mt-exp" "flat-1" "free-1" "mt-cheap")))))
    (dolist (spec '(("flat-1" :flat nil) ("free-1" :free nil)
                    ("mt-exp" :metered 40) ("mt-cheap" :metered 15)))
      (multiple-value-bind (ok line)
          (route-register r (list :id (first spec) :provider "p" :endpoint "e"
                                  :key-location (list :env "K")
                                  :plan (second spec) :cost-per-mtok (third spec)
                                  :capabilities '(:text :yes :code :yes :tool-calls :yes)
                                  :owner "glenn" :request (format nil "req-~A" (first spec))))
        (ok ok "register ~A refused: ~A" (first spec) line))
      (multiple-value-bind (ok line)
          (route-probe r (list :id (first spec) :card "c" :pass :true
                               :at "2026-09-16T00:00:00Z" :source "s"))
        (ok ok "probe ~A refused: ~A" (first spec) line)))
    (multiple-value-bind (ok line ids) (route-projection r "bench")
      (ok ok "projection refused: ~A" line)
      (check-equal '("flat-1" "free-1" "mt-cheap" "mt-exp") ids
                   "the projection is not cheapest-first (flat, free, metered by cost)"))))

(deftest "unprobed-route-carries-no-card" "docs/SPEC-WORK.md:3795"
    "expected=no-probe-and-failed-and-benched-absent,passing-present"
  (let ((r (make-s9-routes :friends '("glenn")
                            :classes '("bench" ("ok" "never" "failed" "benched")))))
    (dolist (spec '(("ok" :flat nil) ("never" :flat nil)
                    ("failed" :flat nil) ("benched" :flat nil)))
      (multiple-value-bind (ok line)
          (route-register r (list :id (first spec) :provider "p" :endpoint "e"
                                  :key-location (list :env "K")
                                  :plan (second spec) :cost-per-mtok (third spec)
                                  :capabilities '(:text :yes :code :yes :tool-calls :yes)
                                  :owner "glenn" :request (format nil "req-~A" (first spec))))
        (ok ok "register ~A refused: ~A" (first spec) line)))
    (route-probe r '(:id "ok" :card "c" :pass :true :at "2026-09-16T00:00:00Z" :source "s"))
    (route-probe r '(:id "failed" :card "c" :pass :false :at "2026-09-16T00:00:00Z" :source "s"))
    (route-probe r '(:id "benched" :card "c" :pass :absent :at "2026-09-16T00:00:00Z" :source "s"))
    (route-probe r '(:id "benched" :card "c" :pass :absent :at "2026-09-16T00:01:00Z" :source "s"))
    (route-probe r '(:id "benched" :card "c" :pass :absent :at "2026-09-16T00:02:00Z" :source "s"))
    (multiple-value-bind (ok line ids) (route-projection r "bench")
      (ok ok "projection refused: ~A" line)
      (check-equal '("ok") ids "a route with no passing probe carried a card"))))

(deftest "three-abstains-bench-until-probe" "docs/SPEC-WORK.md:3796"
    "expected=three-abstains-bench,further-probe-changes-nothing,pass-clears"
  (let ((r (make-s9-routes :friends '("glenn") :classes '("bench" ("slow")))))
    (multiple-value-bind (ok line)
        (route-register r '(:id "slow" :provider "p" :endpoint "e" :key-location (:env "K")
                            :plan :flat :capabilities (:text :yes :code :yes :tool-calls :yes)
                            :owner "glenn" :request "req-1"))
      (ok ok "register refused: ~A" line))
    (dotimes (i 3)
      (route-probe r (list :id "slow" :card "c" :pass :absent
                           :at (format nil "2026-09-16T00:0~D:00Z" i) :source "s")))
    (ok (route-benched-until (routes-find-route r "slow"))
        "three abstains did not bench the route")
    (multiple-value-bind (ok line ids) (route-projection r "bench")
      (ok ok "projection refused: ~A" line)
      (check-equal '() ids "a benched route carried a card"))
    ;; a further probe before the bench clears changes nothing
    (route-probe r '(:id "slow" :card "c" :pass :absent :at "2026-09-16T00:05:00Z" :source "s"))
    (multiple-value-bind (ok line ids) (route-projection r "bench")
      (ok ok "projection refused: ~A" line)
      (check-equal '() ids "a further abstain before the bench cleared it"))
    ;; the next passing probe clears the bench
    (route-probe r '(:id "slow" :card "c" :pass :true :at "2026-09-16T00:06:00Z" :source "s"))
    (multiple-value-bind (ok line ids) (route-projection r "bench")
      (ok ok "projection refused: ~A" line)
      (check-equal '("slow") ids "a passing probe did not clear the bench"))))

;;; The prompt profile (PR687, SPEC-AHEAD: #500).

(deftest "prompt-profile-expired-shows-on-the-status-line" "docs/SPEC-WORK.md:3344"
    "expected=stale-with-expired-date,absent,current-with-expires,unknown-evidence,digest-pinned"
  (let ((p (make-s9-profiles :friends '("glenn"))))
    ;; write (the coordinator's verb, a versioned record with :by, digest pinned)
    (multiple-value-bind (ok line p2 profile)
        (profile-write p '(:name "sonnet" :model "sonnet" :harness "h" :work-type "w"
                           :pointer "prompts/sonnet.md" :policy "rev-1"
                           :evidence :unknown :expiry "2026-09-15T00:00:00Z"
                           :owner "glenn" :by "glenn" :request "req-1"
                           :prompt-content "the prompt bytes"))
      (ok ok "profile write refused: ~A" line)
      (check-equal "sonnet" (profile-name profile) "the profile name")
      (ok (profile-digest profile) "the write did not pin a digest")
      ;; a stale profile (expired yesterday) prints on the status line as such
      (let ((status (profile-status-line p2 "sonnet" :now "2026-09-16T00:00:00Z")))
        (ok (search "profile=sonnet profile-state=stale" status)
            "an expired profile is not printed stale: ~A" status)
        (ok (search "profile-expired=2026-09-15T00:00:00Z" status)
            "the stale line omits the expired date: ~A" status)
        (ok (search "profile-owner=glenn" status)
            "the stale line omits the owner: ~A" status))
      ;; a name with no profile prints absent
      (let ((status (profile-status-line p2 "opus" :now "2026-09-16T00:00:00Z")))
        (check-string= "profile=opus profile-state=absent" status "the absent profile line")))
    ;; a current profile prints current with its expiry
    (profile-write p '(:name "sol" :model "sol" :harness "h" :work-type "w"
                       :pointer "prompts/sol.md" :policy "rev-1" :evidence :unknown
                       :expiry "2026-09-20T00:00:00Z" :owner "glenn" :by "glenn"
                       :request "req-2" :prompt-content "sol bytes"))
    (multiple-value-bind (ok line)
        (profile-edit p '(:name "sol" :evidence "measure-1" :by "glenn" :request "req-3"))
      (ok ok "profile edit refused: ~A" line))
    (let ((status (profile-status-line p "sol" :now "2026-09-16T00:00:00Z")))
      (ok (search "profile=sol profile-state=current" status)
          "a current profile is not printed current: ~A" status)
      (ok (search "profile-expires=2026-09-20T00:00:00Z" status)
          "the current line omits the expiry: ~A" status))
    ;; evidence never measured prints unknown, never guessed
    (let ((status (profile-status-line p "sonnet" :now "2026-09-10T00:00:00Z")))
      (ok (search "profile-state=unknown" status)
          "unmeasured evidence is not printed unknown: ~A" status))
    ;; a digest mismatch is detected, not silently re-pinned
    (ok (profile-digest-mismatch-p (profiles-find p "sonnet") "wrong bytes")
        "a digest mismatch was not detected")))
