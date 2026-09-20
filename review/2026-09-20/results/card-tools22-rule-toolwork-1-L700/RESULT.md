RESULT tools22-rule-toolwork-1-L700 sha=5298f6be12ea
GAP internal/swarm/templates.go:365
SPEC docs/SPEC-TOOLWORK.md:700 rule 1
PKG internal/docs
ASK cut must write the five typed header lines (KIND, PATHS, TEST, LEGS, SOURCE) below the contract line from the pool row, refuse a gated kind missing any with `CUT REFUSED kind=<kind>: no <LINE>`, and lint --card must check kind-declared, paths-declared, test-named, paused.

CONFORMS: internal/swarm/lintheader.go:202 (LintCardHeader) — the four lint --card tokens are implemented and tested.
Lines 72-77 define the four remedies; lines 239-322 check KIND:finds kind-declared, PATHS: finds paths-declared, TEST: finds test-named, trust state paused finds paused. The test at internal/swarm/lintheader_test.go:291 enforces exactly four tokens.

GAP: internal/swarm/templates.go:365-414 — the six typed templates (pulseRead, pulseFix, pulseText, pulseReplay, pulseDrift, pulseTone) that `cut` renders contain NONE of the five typed header lines. cut's renderCard (internal/pulse/cut.go:457) does not add them during rendering, and the `--kind` cutter (internal/pulse/cutkind.go:184) writes only a SOURCE: line, not the full set. No `CUT REFUSED kind=<kind>: no <LINE>` check exists anywhere in the tree.

The lint tokens are GUARDED-BY internal/swarm/lintheader_test.go:285 (TestCardHeaderEveryTokenHasARemedy). The templates lacking header lines are UNGUARDED — no test ensures the shipped templates carry the typed header.

GREPS:
  grep -rn "kind-declared\|paths-declared\|test-named\|paused" --include='*.go' . (58 matches)
  grep -rn "CUT REFUSED.*kind\|CUT REFUSED.*no.*KIND\|CUT REFUSED.*no.*PATHS\|CUT REFUSED.*no.*TEST" --include='*.go' . (0 matches)
  grep -rn "KIND:\|PATHS:\|TEST:\|LEGS:\|SOURCE:" --include='*.go' internal/pulse/ (0 matches)
  grep -rn "pulseRead\|pulseFix\|pulseText\|pulseReplay\|pulseDrift\|pulseTone" internal/swarm/templates.go (6 matches at lines 365-414)
  ls internal/docs/ (29 files, no cardheader.go)

Left owed: the six typed templates must include the five header lines, cut must refuse a gated kind missing any, and the cardheader.go parser (#1721 f927bccc) must land on dev to read them at the gate.
git status --short
(nothing — clean)