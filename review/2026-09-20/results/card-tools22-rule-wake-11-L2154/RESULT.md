RESULT tools22-rule-wake-11-L2154 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 11 says?
KIND: transcript-test
DEADLINE: 1800
LEG: go
PATHS: internal/wake
FILES: 0
TEST: none
MODE: read
TURNS: 30
SOURCE: docs/SPEC-WAKE.md:2154
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
base-repo: /tmp/nova-tools-mirror.git
base-sha: 5298f6be12eaa0f7e6622334d2b6a1eb427649e3
ROUTE: jev=flash why=- eligible=rules

THIS CARD CHANGES NOTHING. No branch, no new file, no edit, no commit, no push. It names no test
command of its own and writes none; it may RUN the package's existing tests to read their names.
Its entire product is RESULT.md.

SPEC docs/SPEC-WAKE.md:2154 rule 11
PKG internal/wake
ASK On each poll, append observations to queue and save state, then print queued item lines up to cap, then delete printed records and write printed=<id> to state for each printed line, then print the verdict.
internal/wake/state.go:297 func (s *State) ObserveDisplay(key, value, display, id string) bool {
internal/wake/state.go:396 func (s *State) MarkPrinted(r Record) {
cmd/nova-wake/main.go:1511 func (w *watcher) printQueue(now time.Time) (int, int) {
GUARDED-BY internal/wake/state_test.go:341 TestTheStoredFormOfAWatchedKeyCarriesThePrintedLabel
Greps:
- grep -rn "printed=" --include='*.go' internal/wake/
- grep -rn "MarkPrinted" --include='*.go' internal/wake/
- grep -rn "WAKE " --include='*.go' cmd/nova-wake/
- grep -rn "printQueue" --include='*.go' cmd/nova-wake/
- grep -n "func Test" internal/wake/state_test.go

Left owed: none

git status --short
