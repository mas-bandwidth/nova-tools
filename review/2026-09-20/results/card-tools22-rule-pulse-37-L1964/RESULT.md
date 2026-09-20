RESULT tools22-rule-pulse-37 sha=5298f6be12ea
CONFORMS internal/pulse/status.go:535
SPEC docs/SPEC-PULSE.md:1964 rule 37
PKG internal/pulse
ASK An implementation must print `verdict=EXPANDING` only when the counts (cut > done) have been above the ratio-1 threshold for two consecutive sampled hours, `CONVERGING` after a single above-threshold hour or when the ratio is exactly 1, and must derive that verdict purely from the counted rows, never from any word in a note.

Deciding lines (internal/pulse/status.go:141-142) — the verdict is built from bare counts, not note text:

```go
verdict := contractionVerdict(in.Queue,
    hourCut > hourDone || hourOpened > hourMerged || hourFiled > hourClosed, now, in.ExpandingHours)
```

Deciding lines (internal/pulse/status.go:535-561) — the sustained-window run counter, where a ratio at 1 (above=false) and a single hour (run=1 < hours) are both CONVERGING, and two consecutive above-threshold hours (run >= hours, default 2) is EXPANDING:

```go
func contractionVerdict(queue string, above bool, now time.Time, hours int) string {
	path := filepath.Join(queue, "EXPANDING")
	if !above {
		_ = os.Remove(path)
		return "CONVERGING"
	}
	key := now.Format("2006-01-02T15")
	prevHour := now.Add(-time.Hour).Format("2006-01-02T15")
	run := 1
	if line := firstLine(path); line != "" {
		f := strings.Fields(line)
		n := 1
		if len(f) == 2 {
			if v, err := strconv.Atoi(f[1]); err == nil && v >= 1 {
				n = v
			}
		}
		if f[0] == prevHour {
			run = n + 1
		}
	}
	_ = os.WriteFile(path, []byte(key+" "+strconv.Itoa(run)+"\n"), 0o644)
	if run >= hours {
		return "EXPANDING"
	}
	return "CONVERGING"
}
```

GUARDED-BY internal/pulse/status_test.go:291 TestStatusContractionWindowAndThresholdStayVisible

Greps run:
- `grep -rn "EXPANDING\|CONVERGING\|verdict=" --include='*.go' internal/pulse/`
- `grep -rn "func Test" --include='*_test.go' internal/pulse/status_test.go`
- `grep -n "^func Test" internal/pulse/status_test.go`

Spec context read: `sed -n '1944,1990p' docs/SPEC-PULSE.md`.

Left owed
