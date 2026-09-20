RESULT tools22-rule-wake-14-L2198 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 14 says?
CONFORMS internal/wake/pr.go:141
SPEC docs/SPEC-WAKE.md:2198 rule 14
PKG internal/wake
ASK The implementation must poll pull requests named via --pr or discovered via --owned-prs at --forge-interval intervals, detect any change in their comment/review/thread counts plus updatedAt and headRefOid fields against stored values, report PR changes with kind/id/author/review state in the display line, handle rescan=true for updatedAt-only movement, suppress own-words under opt-in --not-mine using node ids rather than forge login credentials, and within that suppression only suppress when all changed nodes fall within the ten-node window (PRNodeWindow=10).
DECIDING LINES:
  internal/wake/pr.go:43 const PRNodeWindow = 10
  internal/wake/pr.go:56// PRs is the --pr and --owned-prs source.
  internal/wake/pr.go:141 func (p *PRs) Poll(ctx context.Context, now time.Time) (Result, error) {
  internal/wake/pr.go:202// watched is the --pr names plus this tick's --owned-prs listing.
  internal/wake/pr.go:276// one is the standing read of a pull request, the node fetch --not-mine may
  internal/wake/pr.go:340 countMoved := had && (oldP[0] != strconv.Itoa(comments) || oldP[1] != strconv.Itoa(reviews) || oldP[2] != strconv.Itoa(threads))
  internal/wake/pr.go:341 updatedMoved := had && oldP[3] != pr.UpdatedAt
  internal/wake/pr.go:342 headMoved := had && oldP[4] != pr.HeadRefOid
  internal/wake/pr.go:353 allMine = dComments <= PRNodeWindow && dReviews <= PRNodeWindow && dThreads <= PRNodeWindow
  internal/wake/pr.go:379 rescan := updatedMoved && !countMoved && !headMoved
  internal/wake/pr.go:380 if mine != nil && allMine && updatedMoved {
  internal/wake/pr.go:383 rescan = true
  internal/wake/pr.go:404 display := Compose(strconv.Itoa(comments), strconv.Itoa(reviews), strconv.Itoa(threads),
  internal/wake/pr.go:405 strconv.Itoa(self), boolWord(rescan), boolWord(push), pr.HeadRefOid,
  internal/wake/pr.go:406 newestLine, by, review, atStamp, url)
  internal/wake/source.go:171-183 case KindPR: returns "WAKE PR %s comments=%s reviews=%s threads=%s self=%s rescan=%s push=%s head=%s newest=%s by=%s review=%s at=%s url=%s"
GUARDED-BY cmd/nova-wake/events_test.go:42 TestEveryWaitIsASource (covers --pr flag parsing and cold poll; does not exercise rescan, not-mine suppression depth, or d>PRNodeWindow wake)
GREPS:
  grep -rn "watch" --include='*.go' internal/wake/ | head -40
  grep -rn "pr\|Pr\|PR\|PullRequest\|pull_request" --include='*.go' internal/wake/ | head -60
  grep -rn "owned-prs\|owned_prs\|OwnedPRs\|NotMine\|not-mine\|not_mine\|forge-interval\|forge_interval\|ForgeInterval\|review\|Review\|rescan\|Rescan" --include='*.go' internal/wake/ | head -80
  grep -rn "func Test" --include='*_test.go' internal/wake/
  grep -rn "rule.*14\|Rule.*14\|rule14\|Rule14\|test.*14\|Test14" --include='*.go' internal/wake/
Left owed
git status --short
