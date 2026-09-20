RESULT tools22-rule-toolwork-5-L742 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-TOOLWORK.md:742 rule 5
PKG internal/pulse (launch lives in internal/pulse/launch.go; accept logic would be checked in internal/pulse/)
ASK launch must reject batches wider than one card when multiple cards share a (kind, template sha12) pair unless <root>/accept/first.tsv contains an "ACCEPT OK" line for that pair.
SEARCHED: No code implements this rule. Greps ran:
grep -rn "<a distinctive word or output string from the rule>" --include='*.go' .
  grep -rn "no accepted first card" --include='*.go' .         (no results)
  grep -rn "PULSE REFUSED.*accepted.*first" --include='*.go' .  (no results)
  grep -rn "(launch one card first)" --include='*.go' .         (no results)
  grep -rn "first\.tsv\|first_tsv\|FirstTSV" --include='*.go' . (1 result in cmd/nova-pulse/cut_validated_test.go:523, but that tests cut appending rows, not first-card acceptance)
  grep -rn "accept/\|AcceptRoot\|AcceptPath\|accept.*filepath" --include='*.go' . (no accept/first.tsv handling found)
  grep -rn "wide.batch\|batch.wider.one\|batch.*size\|len(cards).*>.*1" --include='*.go' internal/pulse/ (no matching refusal logic)
  grep -rn "func Test.*Wide\|func Test.*FirstCard\|func Test.*Refuse.*Batch\|launch-refuses-a-wide-batch-with-no-accepted-first-card" --include='*_test.go' . (no test found; the spec-listed red test does not exist as a function name)
  ls internal/docs/ — contained only *_test.go files, none implementing first-card checks
  cat internal/pulse/launch.go (full read of lines 1-460+) — the Launch function reads cards, applies routing, STOP admission, slot checks, and runs nova-swarm batch; there is no check on batch width per (kind, template) pair against any first.tsv file.
  grep -rn "func Test" --include='*_test.go' internal/pulse/ | grep -i wide\|first.card\|one.card\|batch.size -> empty
WHERE LOOKED: internal/pulse/launch.go, internal/pulse/launch_test.go, internal/pulse/launchfix_test.go, internal/pulse/launchswarm.go, internal/pulse/admission.go, internal/pulse/fill.go, internal/swarm/batch.go, cmd/nova-pulse/main.go — across all Go source files in the repo.
git status --short (empty)

