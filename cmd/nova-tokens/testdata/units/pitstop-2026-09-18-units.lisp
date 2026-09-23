;;; The work of 2026-09-18 as a work set, continuing "pitstop-2026-09-17" (work/pitstop-2026-09-17.lisp, mirrored beside this
;;; file). That set names the epic -- shell sketches becoming Go verbs with tests -- and it is where the units below belong;
;;; they are written here rather than appended to it so that today's twenty units can be checked, cut and pulled on their own.
;;;
;;; Grammar: SPEC-WORKLANG Part 4, the amendment A1-A14 (nova-tools#1350). A1: this (work-set ...) form IS a plan. A6: :lane
;;; names a resource of capacity 1 over an area of the tree, and every lane here is a row of queue/control/lanes.tsv -- units
;;; in one lane are serial, unrelated lanes scatter and gather. A7: :writes is the closed list of repo-relative paths a unit
;;; edits, and two units whose :writes intersect serialize EVEN ACROSS LANES. A13: :owner is a mind -- a friend by name, or
;;; "rowan-child" for a child of the coordinating window, or "all". A14: :acceptance is the evidence that closes the unit,
;;; one criterion per line, and the amendment's count of the 09-17 set was 0 of 79 units carrying one. Every unit below
;;; carries one; that is the point of writing today's work in this form rather than in a batch file.
;;;
;;; Checked with `nova-work set check --file work/pitstop-2026-09-18-units.lisp --lanes queue/control/lanes.tsv` (#1355).
;;; Every :needs names a unit of THIS set: cross-set dependencies are :inputs, because set check reads one file.

(work-set "pitstop-2026-09-18-units"
  :title "Certification, the dogfood fixes, batch 8, and the release that carries them to the fleet"
  :under (:open-root)
  :continues "pitstop-2026-09-17"
  :inputs ((:set "rowan-new:work/pitstop-2026-09-17.lisp")
           (:amendment "nova-tools#1350")        ; A1-A14: :writes, :lane, :owner, :acceptance
           (:lanes "rowan-working:queue/control/lanes.tsv")
           (:ledger "rowan-new:reports/pitstop-tests-2026-09-17.md")
           (:rule "a unit names its evidence, or whether it is done is a judgement rather than a reading"))
  :done-when (:all-children-closed)
  :units

  (;; --- certification: a machine is what it can DO, and the loop proves it (the day's first thread)
   (unit "certify:verb"
     :pr 1369 :lane "pulse" :owner "rowan-child" :needs ()
     :writes ("cmd/nova-pulse/fleet_certify.go" "cmd/nova-pulse/fleet.go" "internal/fleet/"
              "docs/spec-pulse/03-fleet.md" "fleet/launchd/com.rowan.fleet-certify.plist")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1369" :predicate :merged-at))
     :title "`nova-pulse fleet certify`: a bench is its capabilities, each one probed, and the loop proves every bench every six hours")

   (unit "certify:fix-loop"
     :lane "pulse" :owner "rowan-child" :needs ("certify:verb")
     :acceptance ((:id "a1" :kind :job :subject "job:nova-pulse/fleet-certify --all" :predicate :succeeds))
     :title "every capability certify reports red is repaired on the bench and re-certified green; no bench stays uncertified overnight")

   (unit "certify:launchd"
     :lane "pulse" :owner "rowan-child" :needs ("certify:verb")
     :writes ("fleet/launchd/com.rowan.fleet-certify.plist")
     :acceptance ((:id "a1" :kind :attested :subject "fleet:certify-every-six-hours" :predicate :attested-by))
     :title "the six-hourly run is a loaded launchd job on the coordinator, not a hand invocation; its receipts reach the fleet page")

   ;; --- the guard, held for a security read (Johnny, by the kind rule: a wall is security work)
   (unit "wall:toolchain-roots"
     :pr 1364 :lane "swarm" :owner "Johnny" :needs ()
     :hold "Johnny's security read of the widened sandbox policy before the wall moves"
     :writes ("cmd/nova-swarm/native.go" "internal/sandbox/policy.go" "internal/sandbox/landlock_linux.go"
              "internal/ci/toolchainroots_class_test.go" "docs/SPEC-SWARM.md")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1364" :predicate :merged-at))
     :title "the swarm wall reads the bench toolchain roots from ONE list checked against the bench standard, instead of a path list per call site")

   ;; --- the dogfood fixes: an edge a friend or a soak found, reproduced, fixed, locked in with a test
   (unit "darwin:measured-shards"
     :pr 1372 :lane "merge" :owner "rowan-child" :needs ()
     :writes ("cmd/nova-merge/land.go" "cmd/nova-merge/queue.go" ".github/workflows/ci.yml"
              ".github/scripts/assert-version-stamp.sh" "Makefile")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1372" :predicate :merged-at))
     :title "the darwin merge leg deals from darwin's OWN measured table (darwin-sizes, darwin-table), never the linux numbers")

   (unit "review:sharedtemp-class"
     :pr 1366 :lane "ci" :owner "rowan-child" :needs ()
     :writes ("internal/review/mutate.go" "internal/ci/sharedtemp_class_test.go"
              "internal/ci/testdata/sharedtemp/" "docs/SPEC-CI.md")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1366" :predicate :merged-at))
     :title "a temp root the CALLER names, and a class test for the read side of t.TempDir() so the shape cannot come back")

   (unit "merge:dogfood-fixes"
     :pr 1363 :lane "merge" :owner "rowan-child" :needs ()
     :writes ("cmd/nova-merge/react.go" "cmd/nova-merge/batch.go" "cmd/nova-merge/queue.go" "cmd/nova-merge/pass.go")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1363" :predicate :merged-at))
     :title "merge lane: react reaches the queue; the lane's hold and skip reach react; the dry run SHOWS the skip instead of swallowing it")

   (unit "harvest:bench"
     :pr 1367 :lane "pulse" :owner "rowan-child" :needs ()
     :writes ("cmd/nova-pulse/harvest_bench_test.go" "cmd/nova-pulse/fill.go" "cmd/nova-pulse/main.go")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1367" :predicate :merged-at))
     :title "`nova-pulse harvest --bench`: the six schema-loop edges found by dogfooding on unrelated work, and the lane drain")

   (unit "sandbox:lock-reap"
     :pr 1365 :lane "sandbox" :owner "rowan-child" :needs ()
     :writes ("cmd/nova-sandbox/reapverb.go" "cmd/nova-sandbox/denied.go" "cmd/nova-sandbox/main.go")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1365" :predicate :merged-at))
     :title "four edges a 20-run soak of `nova-sandbox run` found on the Studio: the reap verb, the create lock, the timeout")

   (unit "decide:validate-stepup"
     :pr 1361 :lane "decide" :owner "rowan-child" :needs ()
     :writes ("cmd/nova-decide/route.go" "cmd/nova-decide/main.go" "docs/CLI.md")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1361" :predicate :merged-at))
     :title "nova-decide: an answer is checked against the question it answers, and the step-up actually takes the step up the ladder")

   (unit "gate:unmatched"
     :pr 1362 :lane "ci" :owner "rowan-child" :needs ()
     :writes ("cmd/nova-check/dogfood.go" "internal/dogfood/ledger.go" "internal/dogfood/cli.go" "docs/SPEC.md")
     :acceptance ((:id "a1" :kind :merged :subject "pr:1362" :predicate :merged-at))
     :title "dogfood evidence that matched nothing is COUNTED, and a not-ok piece fails the gate instead of passing unread")

   (unit "lisp:collision"
     :pr 1373 :lane "work" :owner "Stella" :needs ()
     :writes ("lisp/nova-work/tests/isolation-collision.lisp" "lisp/nova-work/tests/harness.lisp" "lisp/nova-work/nova-work.asd")
     :acceptance ((:id "a1" :kind :test :subject "test:lisp/nova-work/isolation-collision@HEAD" :predicate :passes))
     :title "the collision test for suite isolation: an owned fixture parent, exact cleanup, allocator retry -- cut for Stella's read")

   ;; --- the fleet itself: what was fixed by hand today becomes a certified capability
   (unit "fleet:services"
     :lane "pulse" :owner "rowan-child" :needs ("certify:verb")
     :acceptance ((:id "a1" :kind :job :subject "job:nova-pulse/fleet-certify --capability redis,names" :predicate :succeeds))
     :title "the services fixed by hand on 2026-09-18 are certified, not remembered: the Redis password from the sealed store, one bench name everywhere")

   (unit "threadripper:standard"
     :lane "pulse" :owner "rowan-child" :needs ("certify:verb")
     :acceptance ((:id "a1" :kind :job :subject "job:nova-pulse/fleet-certify --bench batman,superman" :predicate :succeeds))
     :title "the Threadripper benches meet the bench provisioning standard and certify green on every capability, CI-only hosts included")

   (unit "air:bud-setup"
     :lane "docs" :owner "rowan-child" :needs ()
     :writes ("fleet/AIR-BUD-SETUP.md")
     :acceptance ((:id "a1" :kind :attested :subject "doc:fleet/AIR-BUD-SETUP.md" :predicate :attested-by))
     :title "the Air as a bud: Glenn's keeper/bud design walked end to end on the machine, every step of the setup page true as written")

   ;; --- landing: integration batches only, gated on current dev (never green-once)
   (unit "batch:8a"
     :lane "merge" :owner "rowan-child"
     :needs ("merge:dogfood-fixes" "darwin:measured-shards" "review:sharedtemp-class" "gate:unmatched")
     :acceptance ((:id "a1" :kind :merged :subject "pr:integration-8a" :predicate :merged-at))
     :title "integration batch 8a, the merge and ci lanes: gated against current dev on hulk, full suite, reds dropped with the reason, ONE PR through the queue")

   (unit "batch:8b"
     :lane "merge" :owner "rowan-child"
     :needs ("batch:8a" "certify:verb" "harvest:bench" "sandbox:lock-reap" "decide:validate-stepup" "lisp:collision")
     :acceptance ((:id "a1" :kind :merged :subject "pr:integration-8b" :predicate :merged-at))
     :title "integration batch 8b, the pulse, sandbox, decide and work lanes; membership is whatever the batch verb gates green, never a list typed by hand")

   (unit "release:next"
     :lane "pulse" :owner "rowan-child" :needs ("batch:8a" "batch:8b")
     :acceptance ((:id "a1" :kind :job :subject "job:nova-release/cut-build-adopt" :predicate :succeeds))
     :title "the next release after batch 8: cut from main at one green sha, built for every platform, adopted fleet-wide with a receipt per bench")

   ;; --- and then the tools are used, and what using them teaches is written down
   (unit "dogfood:round-5"
     :lane "ci" :owner "all" :needs ("release:next")
     :acceptance ((:id "a1" :kind :job :subject "job:nova-check/dogfood --gate" :predicate :succeeds))
     :title "round 5: every verb of the new release run by a NON-AUTHOR on real unrelated work; rough edges filed as issues; the gate counts the evidence")

   (unit "docs:batch-2"
     :lane "docs" :owner "Stella" :needs ("batch:8a" "batch:8b")
     :writes ("docs/CLI.md" "README.md")
     :acceptance ((:id "a1" :kind :test :subject "test:internal/docs@HEAD" :predicate :passes))
     :title "docs batch 2: CLI.md, the README and the human pages cover every verb batch 8 changed, as it behaves after the fixes and not as it was specified")))
