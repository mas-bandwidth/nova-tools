; The friends' sprint of 2026-09-22 (fixes day), as a nova-work work set.
;
; Read by `nova-work set check` (SPEC-WORKLANG, the (work-set ...) top form; internal/worklang).
; One unit per sprint item. Each unit carries:
;   :acceptance  in the one criterion schema of SPEC-WORK (:id :kind :subject :predicate);
;                set check validates kind and predicate against their closed sets (A14).
;   :needs       the dependency edge; set check refuses an absent need and a cycle.
;   :status      "landed" or "done" when the evidence below is established, "open" otherwise;
;                set check counts landed/done/merged/closed as done (doneWords).
;   :evidence    the primary record the status was read from.
;
; A PR the lander closed after an integration merge is CLOSED, not MERGED, on GitHub
; (#2614 #2607 #2625 #2631 #2594 #2639). Those units are "landed" on the dev commit that
; names them, cited in :evidence. The :merged-at predicate's pr: resolver establishes only
; "merged at that sha" (SPEC-WORK, Evidence), which a closed-as-landed PR fails: that is the
; nova-work gap filed as #2664.
;
; x/y z%:  done = units - ready - blocked, from the SET OK line.
;   nova-work set check --file docs/roadmaps/sprint-fixes-2026-09-22.sexp | awk -F'[ =]' '/^SET OK/{d=$4-$6-$8; printf "%d/%d %d%%\n", d, $4, 100*d/$4}'
; ready set:
;   nova-work set check --file docs/roadmaps/sprint-fixes-2026-09-22.sexp --ready
;
; Written 2026-09-22 against dev 56a156d3f2b173a3ab827091ec0ee9da0d8876f4.

(work-set "sprint-fixes-2026-09-22"
 :title "Fixes day 2026-09-22: every fix issue tested and landed; no bash/zsh survives"
 :repo "mas-bandwidth/nova-tools"
 :base "dev"
 :units
 (
  ;; ---- landed: PR merged, or closed by the lander after an integration merge into dev
  (unit "tools-2547" :title "pulse: stale-base false positive on space-separated PATHS (#2547)"
   :pr 2598 :status "landed"
   :evidence "pr:mas-bandwidth/nova-tools#2598@affffdf1deeefc315954218a2bc561e10abcf6d9"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2598" :predicate :merged-at)))
  (unit "tools-2550" :title "merge: a HOLD at a superseded head is released by the same friend's verdict (#2550)"
   :pr 2615 :status "landed"
   :evidence "pr:mas-bandwidth/nova-tools#2615@1e1e5fb967007028602b8c51a8cc645e7e5e99d0"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2615" :predicate :merged-at)))
  (unit "tools-2548" :title "native: a card whose last turn is a question ends ASKED (#2548)"
   :pr 2614 :status "landed"
   :evidence "commit:5e7314b8faf2845b27e457dd1850bcaea518bddb"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2614" :predicate :merged-at)))
  (unit "tools-2605" :title "swarm: the card header block is every leading KEY: value line (#2605)"
   :pr 2607 :status "landed"
   :evidence "commit:5e7314b8faf2845b27e457dd1850bcaea518bddb"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2607" :predicate :merged-at)))
  (unit "contract-2608" :title "card contract: the one amended text of #2608"
   :pr 2625 :status "landed"
   :evidence "commit:55728dc49bba67f6af66f686f1a3a2e0aad60005"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2625" :predicate :merged-at)))
  (unit "gate-wordmatch-2626a" :title "harvest secret tests: match the git subcommand as a word (#2626)"
   :pr 2631 :status "landed"
   :evidence "commit:5e118c0672bc80fd3f70182f4825490b6157f9c4"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2631" :predicate :merged-at)))
  (unit "jev-first-pass-2594" :title "nova-decide review: the Jev first pass, four mechanical checks"
   :pr 2594 :status "landed"
   :evidence "commit:63eea6f359612c51aa5f742f8f562d88d407934c"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2594" :predicate :merged-at)))
  (unit "depends-on-clause-2636" :title "card contract clause 10: DEPENDS-ON on every card (#2636)"
   :pr 2639 :status "landed"
   :evidence "commit:56a156d3f2b173a3ab827091ec0ee9da0d8876f4"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2639" :predicate :merged-at)))

  ;; ---- done by receipt: no nova-tools PR; the receipt is the evidence
  (unit "supervisor-shellcheck" :title "supervisor scripts pass shellcheck" :status "done"
   :evidence "rowan-tools#139"
   :acceptance ((:id "c1" :kind :attested :subject "supervisor scripts pass shellcheck (rowan-tools#139)" :predicate :attested-by)))
  (unit "tools-2559" :title "nova-tools #2559" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2559 done-when met" :predicate :attested-by)))
  (unit "tools-2562" :title "nova-tools #2562" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2562 done-when met" :predicate :attested-by)))
  (unit "tools-2568" :title "nova-tools #2568" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2568 done-when met" :predicate :attested-by)))
  (unit "tools-2592" :title "nova-tools #2592" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2592 done-when met" :predicate :attested-by)))
  (unit "a12-receipt" :title "A12: the readiness receipt" :status "done"
   :evidence "rowan-new reports/readiness-receipt-2026-09-22.md"
   :acceptance ((:id "c1" :kind :attested :subject "A12 readiness receipt written" :predicate :attested-by)))
  (unit "a11-cells" :title "A11: the false-confidence cells" :status "done"
   :evidence "rowan-new reports/false-confidence-cells-2026-09-22.md"
   :acceptance ((:id "c1" :kind :attested :subject "A11 cells measured" :predicate :attested-by)))
  (unit "read-2595" :title "read: nova-work walks, links and index (#2595 filed)" :status "done"
   :evidence "rowan-new reports/nova-work-walks-links-index-2026-09-22.md"
   :acceptance ((:id "c1" :kind :attested :subject "walks report read and #2595 filed" :predicate :attested-by)))
  (unit "tools-2551" :title "nova-tools #2551" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2551 done-when met" :predicate :attested-by)))
  (unit "tools-2552" :title "nova-tools #2552" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2552 done-when met" :predicate :attested-by)))
  (unit "recut-59" :title "recut the 59 sprint cards to the v2 contract" :status "done"
   :evidence "59/59 lint, Emma 2026-09-22T21:01Z"
   :acceptance ((:id "c1" :kind :attested :subject "59/59 recut cards pass lint" :predicate :attested-by)))
  (unit "route-autobench-2634" :title "route autobench (#2634)" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2634 done-when met" :predicate :attested-by)))
  (unit "tool-versions-2635" :title "tool versions (#2635)" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "nova-tools #2635 done-when met" :predicate :attested-by)))
  (unit "lander-loop" :title "the lander runs as a loop (land-lane)" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "land-lane loop landing batches into dev" :predicate :attested-by)))
  (unit "jev-on" :title "Jev first pass switched on" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "Jev runs on every new PR" :predicate :attested-by)))
  (unit "keepers-and-wake" :title "keepers and wake" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "keepers and wake running" :predicate :attested-by)))
  (unit "fleet-adoption" :title "fleet adoption" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "fleet adopted the day's tools" :predicate :attested-by)))
  (unit "fleet-tree-landed" :title "fleet tree landed" :status "done" :evidence "done receipt 2026-09-22"
   :acceptance ((:id "c1" :kind :attested :subject "fleet tree landed" :predicate :attested-by)))

  ;; ---- open
  (unit "gate-fix-holds" :title "merge: the friend name in a typed line is case-insensitive (the Go gate drops holds)"
   :pr 2660 :status "open"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2660" :predicate :merged-at)))
  (unit "capture-uncap-2654" :title "merge: gh captures that feed the verdict parser are read whole (#2654)"
   :pr 2663 :status "open"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2663" :predicate :merged-at)))
  (unit "a4-cards-v2" :title "pulse: Swarm Cards v2 items A1-A7 card generator"
   :pr 2522 :status "open" :needs ("gate-fix-holds" "capture-uncap-2654")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2522" :predicate :merged-at)))
  (unit "a5-provider-deadline" :title "A5: end a black-holed provider socket in 45s"
   :pr 2544 :status "open"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2544" :predicate :merged-at)))
  (unit "wait-verb-2546" :title "nova-pulse wait: one Go verb for poll-until-then-act (#2546)"
   :pr 2616 :status "open" :needs ("gate-fix-holds")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2616" :predicate :merged-at)))
  (unit "swarm-table-2561" :title "nova-pulse row / status --store: the swarm table from the fleet (#2561)"
   :pr 2622 :status "open" :needs ("wait-verb-2546")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2622" :predicate :merged-at)))
  (unit "r1-events-2587" :title "redis R1: the ev:cards event stream and its SQLite fold"
   :pr 2587 :status "open" :needs ("gate-fix-holds")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2587" :predicate :merged-at)))
  (unit "r1-writers-2619" :title "the Go writers of ev:cards"
   :pr 2619 :status "open" :needs ("r1-events-2587")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2619" :predicate :merged-at)))
  (unit "sprint-verb-2593" :title "nova-pulse sprint: the sprint verb over the fleet Redis (#2593)"
   :pr 2624 :status "open" :needs ("r1-events-2587")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2624" :predicate :merged-at)))
  (unit "postgres-retire-2623" :title "retire Postgres: decide_log is kind=decide on cards:done (#2623)"
   :pr 2628 :status "open" :needs ("r1-events-2587")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2628" :predicate :merged-at)))
  (unit "presence-2610" :title "nova-wake: friend presence as a zero-token heartbeat (#2610)"
   :pr 2612 :status "open" :needs ("gate-fix-holds")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2612" :predicate :merged-at)))
  (unit "harvest-go-2549" :title "pulse: mechanical harvest commit step in Go (#2549)"
   :pr 2611 :status "open" :needs ("gate-fix-holds")
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2611" :predicate :merged-at)))
  (unit "raw-stream-2626b" :title "nova-merge batch: keep the raw go test -json stream (#2626)"
   :pr 2645 :status "open"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2645" :predicate :merged-at)))
  (unit "wait-selfrepair-2627" :title "nova-bus wait: recover a stale index.lock and a dirty BEAT (#2627)"
   :pr 2651 :status "open"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2651" :predicate :merged-at)))
  (unit "results-dir-2632" :title "native: publish card results outside the job directory (#2632)"
   :pr 2658 :status "open"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/nova-tools#2658" :predicate :merged-at)))
  (unit "rowan-tools-followup" :title "rowan-tools follow-up (no PR yet)" :status "open"
   :acceptance ((:id "c1" :kind :merged :subject "pr:mas-bandwidth/rowan-tools#(not yet opened)" :predicate :merged-at)))
 ))
