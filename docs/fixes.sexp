(release "v1.2.12" :status "planned" :text "Tests and models only: no product change."
  (fix "tla-updateapply-newer-and-delivery-scopes" :release "v1.2.12"
       :title "UpdateApply reads NEWER and AHEAD as the code does, and records delivery per recipient scope"
       :text "The model said AHEAD for any installed version greater than the latest and kept one delivered for the whole state file. The code reads a greater release version NEWER and AHEAD only the vX.Y.Z-0 pseudo-version (internal/update/read.go Compare and ahead) and records delivery per recipient scope (internal/update/report.go deliver). The module now does the same, its header names sendOK and writeSnapshotWith, and a reversed witness aheadisnewer fails a new VerdictMatchesVersion."
       :origin "tla-updateapply-newer-and-delivery-scopes"))
