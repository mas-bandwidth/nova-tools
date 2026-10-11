(release "v1.2.12" :status "planned" :text "Tests and models only, no product change.")

(fix "cover-native-prepare-refusals-and-report"
  :release "v1.2.12"
  :title "Cover native prepare's refusals and report's outcomes"
  :text "Pin what nova-swarm native refuses before it spends anything (the model with no provider prefix, a worker description pinning another model, an unset worker secret, a slot outside the root, a walking label, a results root inside the job, a job or slot lease held by a live run, a public worker whose card names an unlisted repository, an auth file looser than 0600) and that onPhase prepares first, driven through nativeRun in the unit tier. The functional report outcomes (a non-zero exit, a silent harness, a last-turn question) remain owed."
  :origin "cover-native-prepare-refusals-and-report.w1")
