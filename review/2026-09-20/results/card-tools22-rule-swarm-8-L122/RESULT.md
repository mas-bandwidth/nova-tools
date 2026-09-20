RESULT tools22-rule-swarm-8 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SWARM.md rule 8 says?
CONFORMS internal/swarm/triage.go:85
SPEC docs/SPEC-SWARM.md:3996 rule 8
PKG internal/swarm
ASK An implementation must walk the pool's jobs, read each report by its bytes (the retained copy for finalized jobs, the live `RESULT.md` for running ones), hash-parse-rehash it so a mid-read change prints `TRIAGE SKIPPED` and is taken whole next run, skip/dedup by a `(repo,rev,file,line,rule)` key, print exactly one `TRIAGE BATCH` count line plus ranked findings under `--max` and verdict sums with a dash for absence, never consult mtime nor open `RESULT.md.tmp`, and hand a malformed report to `result --id` verbatim.
- `internal/swarm/triage.go:186` `first := HashBytes(raw)` (read), `:190` `report := ParseReport(raw)` (parse), `:191` `again, err := p.rehash(from)` (rehash), `:192-197` `if err != nil || again != first { ... "TRIAGE SKIPPED id=%s: changed while read" ... skipped++; continue }`
- `internal/swarm/triage.go:200-204` `TRIAGE QUARANTINED id=%s rev=%s line=%d: not folded; ... result --pool %s --id %s` (malformed never reached by page writer)
- `internal/swarm/triage.go:433-444` `ReportBytes` reads `<pool>/reports/<job>/RESULT.md` (retained) then falls back to live `ResultPath(sc.Job)` for running jobs
- `internal/swarm/triage.go:102-114` `--batch <id>` filters `sc.Batch == in.Batch`, refused when no sidecar carries the id
- `internal/swarm/triage.go:230-270` de-duplication by `f.Key(k.report.Repo, k.report.Rev)` (repo, rev, file, line, rule)
- `internal/swarm/triage.go:294-297` the one `TRIAGE BATCH batch=… reports=… findings=… new=… dup=… unquoted=… clean=… plan_only=… no_result=… malformed=… budget=… accurate=… wrong=…`
- `internal/swarm/triage.go:281-285` `verdict := func(n int) string { if !anyVerdict { return Dash }; return fmt.Sprint(n) }` (dash for absence)
- `internal/swarm/triage.go:327-335` consumed map advanced only when `!in.NoState && !in.All`, one `state.Runs++` per run
- `internal/swarm/triage.go:460-486` `ResultByID` prints `stdout.Write(raw)` verbatim after the `RESULT OK` line
GUARDED-BY internal/swarm/revision_test.go:19 TestAReportIsARevision
Greps: `grep -rn "triage\|TRIAGE" docs/SPEC-SWARM.md`; `grep -rn "func Test" internal/swarm/revision_test.go`; `grep -rn "ModTime\|RESULT.md.tmp\|NoState\|TriageInput{" --include='*.go' internal/swarm/`; `grep -rn "DEMANDED TEST" --include='*.go' internal/swarm/`. Ran `GOMAXPROCS=8 go test ./internal/swarm/ -count=1 -run 'TestAReportIsARevision|TestNoModTimeDecidesAnythingInThisPackage|TestThePageCarriesTheMergedFindingAndItsJobs'` -> `ok`.
Left owed
