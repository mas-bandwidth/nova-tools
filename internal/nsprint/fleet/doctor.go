package fleet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultDiskFloorGiB is the minimum free disk in GiB required per bench.
const DefaultDiskFloorGiB = 200

// DefaultDeclaredMirrors is the fleet-wide declared mirror repositories.
var DefaultDeclaredMirrors = []string{
	"nova-tools",
	"schema",
	"nova-work",
	"message-bus",
	"serialize",
	"quack",
}

// PrivateMirrors are repositories that require a private deploy key.
var PrivateMirrors = map[string]bool{
	"nova-work":   true,
	"message-bus": true,
	"quack":       true,
}

// StandardGoSDKSHA holds default Go SDK sha256 per platform.
var StandardGoSDKSHA = map[string]string{
	"darwin-arm64": "2dc95ce46758",
	"darwin-amd64": "08b65a63f244",
	"linux-amd64":  "708effb774be",
}

// DoctorLine is one check result line with why and remedy.
type DoctorLine struct {
	Bench  string
	Check  string
	State  string // "OK", "FIX", or "SKIP"
	Words  []string
	Why    string
	Remedy string
}

func (l DoctorLine) String() string {
	s := "DOCTOR " + l.State + " bench=" + l.Bench + " check=" + l.Check
	for _, w := range l.Words {
		s += " " + w
	}
	if l.Why != "" {
		s += " why=" + oneline.Quote(l.Why)
	}
	if l.Remedy != "" {
		s += " remedy=" + oneline.Quote(l.Remedy)
	}
	return s
}

// BenchDoctorRecord holds all 7 checked dimensions for one bench.
type BenchDoctorRecord struct {
	Bench string

	// 1. Version
	VersionHave string
	VersionWant string
	VersionOK   bool

	// 2. Go SDK
	GoSDKHave string
	GoSDKWant string
	GoSDKOK   bool

	// 3. Play
	PlayTag    string
	PlayRole   string
	PlayResult string
	PlayAge    string
	PlayOK     bool

	// 4. Runners
	RunnersDeclared   int
	RunnersRegistered int
	RunnersOnline     int
	RunnersOK         bool

	// 5. Mirrors
	MirrorsHave     []string
	MirrorsDeclared []string
	MirrorsMissing  []string
	MirrorsOK       bool

	// 6. Disk guard
	DiskGiB      int
	DiskFloorGiB int
	DiskOK       bool

	// 7. Units
	UndeclaredUnits []string
	UnitsOK         bool

	// Drift lines for this bench
	Fixes []DoctorLine
}

// DoctorResult is the complete fleet doctor result across all checked benches.
type DoctorResult struct {
	Benches     []BenchDoctorRecord
	Fixes       []DoctorLine
	TotalChecks int
	TotalFixes  int
	Trips       int64
	MS          int64
}

// ScreenTable formats the one-screen overview table.
func (r DoctorResult) ScreenTable() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("FLEET DOCTOR: %d benches checked\n", len(r.Benches)))
	sb.WriteString(fmt.Sprintf("%-16s %-20s %-10s %-18s %-10s %-8s %-9s %-10s\n",
		"BENCH", "VERSION", "GO_SDK", "PLAY", "RUNNERS", "MIRRORS", "DISK", "UNITS"))

	for _, b := range r.Benches {
		vHave := b.VersionHave
		if vHave == "" {
			vHave = "-"
		}
		vWant := b.VersionWant
		if vWant == "" {
			vWant = "-"
		}
		verStr := fmt.Sprintf("%s/%s", vHave, vWant)
		if len(verStr) > 20 {
			verStr = verStr[:20]
		}

		goStr := b.GoSDKHave
		if goStr == "" {
			goStr = "-"
		}
		if len(goStr) > 10 {
			goStr = goStr[:10]
		}

		playStr := b.PlayResult
		if playStr == "" {
			playStr = "none"
		} else if b.PlayRole != "" {
			playStr = b.PlayResult + ":" + b.PlayRole
		}
		if b.PlayAge != "" && playStr != "none" {
			playStr = fmt.Sprintf("%s (%s)", playStr, b.PlayAge)
		}
		if len(playStr) > 18 {
			playStr = playStr[:18]
		}

		runnersStr := fmt.Sprintf("%d/%d/%d", b.RunnersDeclared, b.RunnersRegistered, b.RunnersOnline)

		mirrorsStr := fmt.Sprintf("%d/%d", len(b.MirrorsHave), len(b.MirrorsDeclared))

		diskStr := "-"
		if b.DiskGiB > 0 {
			diskStr = fmt.Sprintf("%d GiB", b.DiskGiB)
		}

		unitsStr := "clean"
		if len(b.UndeclaredUnits) > 0 {
			unitsStr = fmt.Sprintf("stray:%d", len(b.UndeclaredUnits))
		}

		sb.WriteString(fmt.Sprintf("%-16s %-20s %-10s %-18s %-10s %-8s %-9s %-10s\n",
			b.Bench, verStr, goStr, playStr, runnersStr, mirrorsStr, diskStr, unitsStr))
	}
	return sb.String()
}

// SummaryLine returns the final doctor summary line.
func (r DoctorResult) SummaryLine() string {
	if r.TotalFixes == 0 {
		return fmt.Sprintf("DOCTOR OK benches=%d checks=%d trips=%d ms=%d",
			len(r.Benches), r.TotalChecks, r.Trips, r.MS)
	}
	return fmt.Sprintf("DOCTOR FIX benches=%d fixes=%d checks=%d trips=%d ms=%d",
		len(r.Benches), r.TotalFixes, r.TotalChecks, r.Trips, r.MS)
}

// DoctorRequest configures Doctor execution.
type DoctorRequest struct {
	BenchFilter string
	RunnersPath string
	Now         func() time.Time
	Fetcher     GitHubRunnersFetcher
}

// ShortBuild extracts short commit from a version line or build identity.
func ShortBuild(line string) string {
	fields, ok := buildinfo.Parse(line)
	if ok && fields.Version != "" {
		v := fields.Version
		if idx := strings.LastIndex(v, "."); idx >= 0 {
			return v[idx+1:]
		}
		return v
	}
	// Fall back to trimming
	trimmed := strings.TrimSpace(line)
	if len(trimmed) > 12 {
		return trimmed[:8]
	}
	return trimmed
}

// ParseStamp parses a timestamp from unix ms or RFC 3339.
func ParseStamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		if n > 1e11 {
			return time.UnixMilli(n), true
		}
		return time.Unix(n, 0), true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// FormatAge formats a duration as e.g. "12m", "2h", "5d".
func FormatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Doctor executes the full fleet doctor scan across benches reading Redis alone.
func Doctor(ctx context.Context, c redis.Cmdable, req DoctorRequest) (*DoctorResult, error) {
	start := time.Now()
	nowFn := req.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()

	// 1. Read release facts from fleet:release
	releaseCmd := c.HGetAll(ctx, "fleet:release")
	release, err := releaseCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read fleet:release: %w", err)
	}

	wantCommit := release["commit"]
	if len(wantCommit) > 8 {
		wantCommit = wantCommit[:8]
	}
	if wantCommit == "" {
		wantCommit = ShortBuild(release["version"])
	}

	// 2. Read declared runners table
	runnersPath := req.RunnersPath
	if runnersPath == "" {
		if p, err := FindRunnersPath(""); err == nil {
			runnersPath = p
		}
	}
	var runnersTable *RunnersTable
	if runnersPath != "" {
		t, _ := ReadRunnersFile(runnersPath)
		runnersTable = t
	}
	if runnersTable == nil {
		runnersTable = &RunnersTable{RowIndex: make(map[string]int)}
	}

	// 3. Read registered runners (cached in Redis or via fetcher)
	regMap, _ := GetRegisteredRunners(ctx, c, req.Fetcher)

	// 4. Read list of benches from Redis
	var benches []string
	if req.BenchFilter != "" {
		benches = []string{req.BenchFilter}
	} else {
		members, err := c.SMembers(ctx, "benches").Result()
		if err == nil && len(members) > 0 {
			benches = members
		}
	}
	if len(benches) == 0 && runnersTable != nil {
		for _, r := range runnersTable.Rows {
			benches = append(benches, r.Host)
		}
	}
	sort.Strings(benches)

	if len(benches) == 0 {
		return &DoctorResult{}, nil
	}

	// 5. Pipeline all bench reads: beats, plays, desired
	pipe := c.Pipeline()
	beats := make([]*redis.MapStringStringCmd, len(benches))
	plays := make([]*redis.MapStringStringCmd, len(benches))
	desired := make([]*redis.MapStringStringCmd, len(benches))

	for i, b := range benches {
		beats[i] = pipe.HGetAll(ctx, "bench:"+b+":beat")
		plays[i] = pipe.HGetAll(ctx, "bench:"+b+":play")
		desired[i] = pipe.HGetAll(ctx, "bench:"+b+":desired")
	}
	_, _ = pipe.Exec(ctx)

	result := &DoctorResult{}

	for i, b := range benches {
		beat := beats[i].Val()
		play := plays[i].Val()
		des := desired[i].Val()

		rec := BenchDoctorRecord{
			Bench:           b,
			MirrorsDeclared: DefaultDeclaredMirrors,
			DiskFloorGiB:    DefaultDiskFloorGiB,
		}

		var benchFixes []DoctorLine

		// ------------------------------------------------------------------
		// Dimension 1: Version want|have
		// ------------------------------------------------------------------
		result.TotalChecks++
		rec.VersionWant = wantCommit
		buildLine := beat["build"]
		if buildLine == "" {
			rec.VersionHave = "none"
			rec.VersionOK = false
			remedy := "nova-sprint fleet build --bench " + b
			if strings.Contains(b, "mac") {
				remedy = "launchctl kickstart -k gui/501/com.nova.loop.nova-sprint-bench-beat"
			}
			benchFixes = append(benchFixes, DoctorLine{
				Bench:  b,
				Check:  "version",
				State:  "FIX",
				Words:  []string{"have=none", "want=" + wantCommit},
				Why:    "bench has no live beat",
				Remedy: remedy,
			})
		} else {
			rec.VersionHave = ShortBuild(buildLine)
			if wantCommit != "" && !strings.HasPrefix(wantCommit, rec.VersionHave) && !strings.HasPrefix(rec.VersionHave, wantCommit) {
				rec.VersionOK = false
				benchFixes = append(benchFixes, DoctorLine{
					Bench:  b,
					Check:  "version",
					State:  "FIX",
					Words:  []string{"have=" + rec.VersionHave, "want=" + wantCommit},
					Why:    "binary is behind fleet release",
					Remedy: "nova-sprint fleet build --bench " + b,
				})
			} else {
				rec.VersionOK = true
			}
		}

		// ------------------------------------------------------------------
		// Dimension 2: Go SDK sha
		// ------------------------------------------------------------------
		result.TotalChecks++
		platform := des["platform"]
		if platform == "" {
			if p := beat["platform"]; p != "" {
				platform = p
			} else if osVal := beat["os"]; osVal != "" {
				archVal := beat["arch"]
				if archVal == "" {
					archVal = "arm64"
				}
				platform = osVal + "-" + archVal
			} else if strings.Contains(buildLine, "darwin/arm64") || strings.Contains(buildLine, "darwin-arm64") {
				platform = "darwin-arm64"
			} else if strings.Contains(buildLine, "darwin/amd64") || strings.Contains(buildLine, "darwin-amd64") {
				platform = "darwin-amd64"
			} else if strings.Contains(buildLine, "linux/amd64") || strings.Contains(buildLine, "linux-amd64") {
				platform = "linux-amd64"
			} else if strings.Contains(b, "mac") || strings.Contains(b, "darwin") {
				platform = "darwin-arm64"
			} else {
				platform = "linux-amd64"
			}
		}
		expectedGoSHA := StandardGoSDKSHA[platform]
		if expectedGoSHA == "" {
			expectedGoSHA = "708effb774be"
		}
		rec.GoSDKWant = expectedGoSHA

		haveGoSHA := beat["go_sha"]
		if haveGoSHA == "" {
			haveGoSHA = beat["go_sdk"]
		}
		if haveGoSHA == "" {
			haveGoSHA = beat["sdk_sha"]
		}
		if haveGoSHA == "" {
			haveGoSHA = beat["sdk"]
		}
		rec.GoSDKHave = haveGoSHA

		if haveGoSHA == "" {
			rec.GoSDKOK = false
			benchFixes = append(benchFixes, DoctorLine{
				Bench:  b,
				Check:  "go_sdk",
				State:  "FIX",
				Words:  []string{"have=none", "want=" + expectedGoSHA},
				Why:    "Go SDK sha not reported on beat",
				Remedy: "nova-sprint fleet play runners --limit " + b,
			})
		} else if !strings.HasPrefix(haveGoSHA, expectedGoSHA) && !strings.HasPrefix(expectedGoSHA, haveGoSHA) {
			rec.GoSDKOK = false
			benchFixes = append(benchFixes, DoctorLine{
				Bench:  b,
				Check:  "go_sdk",
				State:  "FIX",
				Words:  []string{"have=" + haveGoSHA, "want=" + expectedGoSHA},
				Why:    "Go SDK sha differs from declared standard",
				Remedy: "nova-sprint fleet play runners --limit " + b,
			})
		} else {
			rec.GoSDKOK = true
		}

		// ------------------------------------------------------------------
		// Dimension 3: Play last run and where it stopped
		// ------------------------------------------------------------------
		result.TotalChecks++
		playTag := play["tag"]
		playRole := play["role"]
		playResult := play["result"]
		playAtStr := play["at"]
		rec.PlayTag = playTag
		rec.PlayRole = playRole
		rec.PlayResult = playResult

		if playAtStr == "" {
			rec.PlayOK = false
			benchFixes = append(benchFixes, DoctorLine{
				Bench:  b,
				Check:  "play",
				State:  "FIX",
				Words:  []string{"last=none"},
				Why:    "no fleet play receipt recorded",
				Remedy: "nova-sprint fleet play tools --limit " + b,
			})
		} else {
			if t, ok := ParseStamp(playAtStr); ok {
				rec.PlayAge = FormatAge(now.Sub(t))
			}
			if strings.HasPrefix(playResult, "failed:") {
				role := strings.TrimPrefix(playResult, "failed:")
				rec.PlayOK = false
				tag := playTag
				if tag == "" {
					tag = "tools"
				}
				benchFixes = append(benchFixes, DoctorLine{
					Bench:  b,
					Check:  "play",
					State:  "FIX",
					Words:  []string{"last=" + tag, "stopped=" + role},
					Why:    "play failed in role " + role,
					Remedy: fmt.Sprintf("nova-sprint fleet play %s --limit %s", tag, b),
				})
			} else {
				rec.PlayOK = true
			}
		}

		// ------------------------------------------------------------------
		// Dimension 4: Runners declared|registered|online
		// ------------------------------------------------------------------
		result.TotalChecks++
		rec.RunnersDeclared = runnersTable.Count(b)
		rec.RunnersRegistered = regMap[b]
		rec.RunnersOnline = CountOnlineRunners(beat)

		if rec.RunnersDeclared != rec.RunnersRegistered || rec.RunnersDeclared != rec.RunnersOnline {
			rec.RunnersOK = false
			benchFixes = append(benchFixes, DoctorLine{
				Bench: b,
				Check: "runners",
				State: "FIX",
				Words: []string{
					fmt.Sprintf("declared=%d", rec.RunnersDeclared),
					fmt.Sprintf("registered=%d", rec.RunnersRegistered),
					fmt.Sprintf("online=%d", rec.RunnersOnline),
				},
				Why:    "runner count mismatch",
				Remedy: "nova-sprint fleet play runners --limit " + b,
			})
		} else {
			rec.RunnersOK = true
		}

		// ------------------------------------------------------------------
		// Dimension 5: Mirrors present with their key
		// ------------------------------------------------------------------
		result.TotalChecks++
		mirrorsRaw := beat["mirrors"]
		var mirrorsHave []string
		haveMap := make(map[string]bool)
		for _, m := range strings.Split(mirrorsRaw, ",") {
			if m = strings.TrimSpace(m); m != "" {
				mirrorsHave = append(mirrorsHave, m)
				haveMap[m] = true
			}
		}
		rec.MirrorsHave = mirrorsHave

		var missing []string
		for _, dm := range DefaultDeclaredMirrors {
			if !haveMap[dm] {
				missing = append(missing, dm)
			}
		}

		// Also check if any private mirror missing deploy key
		missingKeysRaw := beat["missing_keys"]
		if missingKeysRaw == "" {
			missingKeysRaw = beat["keys_missing"]
		}
		var missingKeys []string
		if missingKeysRaw != "" {
			for _, k := range strings.Split(missingKeysRaw, ",") {
				if k = strings.TrimSpace(k); k != "" {
					missingKeys = append(missingKeys, k)
				}
			}
		}

		rec.MirrorsMissing = missing
		if len(missing) > 0 || len(missingKeys) > 0 {
			rec.MirrorsOK = false
			allBad := append([]string{}, missing...)
			allBad = append(allBad, missingKeys...)
			sort.Strings(allBad)
			benchFixes = append(benchFixes, DoctorLine{
				Bench:  b,
				Check:  "mirrors",
				State:  "FIX",
				Words:  []string{"missing=" + strings.Join(allBad, ",")},
				Why:    "mirror absent or missing deploy key",
				Remedy: "nova-sprint fleet play ssh --limit " + b,
			})
		} else {
			rec.MirrorsOK = true
		}

		// ------------------------------------------------------------------
		// Dimension 6: Disk guard
		// ------------------------------------------------------------------
		result.TotalChecks++
		diskStr := beat["disk_gib"]
		if diskStr == "" {
			rec.DiskOK = false
			benchFixes = append(benchFixes, DoctorLine{
				Bench:  b,
				Check:  "disk",
				State:  "FIX",
				Words:  []string{"have=none", fmt.Sprintf("floor=%dGiB", DefaultDiskFloorGiB)},
				Why:    "beat carries no disk fact",
				Remedy: "nova-sprint fleet play tools --limit " + b,
			})
		} else {
			n, err := strconv.Atoi(diskStr)
			if err != nil {
				rec.DiskOK = false
				benchFixes = append(benchFixes, DoctorLine{
					Bench:  b,
					Check:  "disk",
					State:  "FIX",
					Words:  []string{"have=invalid", fmt.Sprintf("floor=%dGiB", DefaultDiskFloorGiB)},
					Why:    "beat carries invalid disk fact",
					Remedy: "nova-sprint fleet play tools --limit " + b,
				})
			} else {
				rec.DiskGiB = n
				if n < DefaultDiskFloorGiB {
					rec.DiskOK = false
					benchFixes = append(benchFixes, DoctorLine{
						Bench:  b,
						Check:  "disk",
						State:  "FIX",
						Words:  []string{fmt.Sprintf("have=%dGiB", n), fmt.Sprintf("floor=%dGiB", DefaultDiskFloorGiB)},
						Why:    fmt.Sprintf("free disk below %d GiB floor", DefaultDiskFloorGiB),
						Remedy: "nova-sprint fleet play disk-guard --limit " + b,
					})
				} else {
					rec.DiskOK = true
				}
			}
		}

		// ------------------------------------------------------------------
		// Dimension 7: Undeclared units
		// ------------------------------------------------------------------
		result.TotalChecks++
		psRaw := beat["ps"]
		var undeclared []string
		if psRaw != "" {
			if sample, err := DecodePS(psRaw); err == nil {
				for _, u := range sample.Units {
					if u.State == UnitUndeclared {
						undeclared = append(undeclared, u.Name)
					}
				}
			}
		}
		rec.UndeclaredUnits = undeclared
		if len(undeclared) > 0 {
			rec.UnitsOK = false
			benchFixes = append(benchFixes, DoctorLine{
				Bench:  b,
				Check:  "units",
				State:  "FIX",
				Words:  []string{"undeclared=" + strings.Join(undeclared, ",")},
				Why:    "stray undeclared units on bench",
				Remedy: "nova-sprint fleet ps --stray --bench " + b,
			})
		} else {
			rec.UnitsOK = true
		}

		rec.Fixes = benchFixes
		result.Benches = append(result.Benches, rec)
		result.Fixes = append(result.Fixes, benchFixes...)
		result.TotalFixes += len(benchFixes)
	}

	result.Trips = 1
	result.MS = time.Since(start).Milliseconds()
	return result, nil
}
