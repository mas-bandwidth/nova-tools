RESULT tools22-rule-wake-15-L3522 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 15 says?
KIND: transcript-test
DEADLINE: 1800
LEG: go
PATHS: internal/wake
FILES: 0
TEST: none
MODE: read
TURNS: 30
SOURCE: docs/SPEC-WAKE.md:3522
BASE: dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
SPEC docs/SPEC-WAKE.md:3522 rule 15
PKG internal/wake
ASK Rule 15 requires the runs source to print final=true when pending=0 with checks, refuse --entry-interval below 30s when --run is given, and mark heads with >100 check runs as unreadable; IsFinalRun and RunIntervalFloor enforce these behaviors.
CONFORMS cmd/nova-wake/main.go:742
"run/go:40:const RunIntervalFloor = 30 * time.Second
cmd/nova-wake/main.go:738-742:
        floor := IntervalFloor
        if len(runs) > 0 {
                floor = wake.RunIntervalFloor
        }
        if entryEveryDur > 0 && entryEveryDur < floor {
                p.add("--entry-interval "+*entryEvery+" is below the "+wake.Dur(floor)+" floor", forgeFloorHint(len(runs) > 0))
        }
run.go:186-195:func IsFinalRun(value string) bool {
        p := Decompose(value)
        if len(p) == 2 && p[0] == "unreadable" {
                return false
        }
        p = fields(p, 4)
        fail, _ := strconv.Atoi(p[0])
        pending, _ := strconv.Atoi(p[1])
        pass, _ := strconv.Atoi(p[2])
        return pending == 0 && fail+pass > 0
}
run.go:136-138:
        if runs.Total > RunCheckLimit {
                return "", fmt.Errorf("more than %d check runs on this head", RunCheckLimit)
        }
run.go:79-81:
                if err != nil {
                        done <- answer{i: i, value: Compose("unreadable", oneLineOf(err.Error())), bad: true}
                        return
                }
GUARDED-BY cmd/nova-wake/events_test.go:259 TestFinalRuns
cmd/nova-wake/events_test.go:93 TestTheRunFloor
cmd/nova-wake/events_test.go:288 Test101CheckRuns
grep ran:
- WAKE RUN in internal/wake/source.go
- entry-interval in internal/wake/run.go, cmd/nova-wake/main.go
- RunIntervalFloor in internal/wake/run.go, cmd/nova-wake/main.go
- 100 in internal/wake/run.go

Left owed: api calls per tick count verification

git status --short
