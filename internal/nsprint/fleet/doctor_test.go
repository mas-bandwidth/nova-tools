package fleet

import (
	"strings"
	"testing"
	"time"
)

func TestShortBuild(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  string
	}{
		{"nova-sprint v0.16.0-dev.c250d86a darwin/arm64 go1.26.6", "c250d86a"},
		{"nova-sprint v0.16.0-dev.3403baa7 linux/amd64 go1.26.6", "3403baa7"},
		{"c250d86a", "c250d86a"},
		{"0123456789abcdef", "01234567"},
		{"", ""},
	}

	for _, c := range cases {
		if got := ShortBuild(c.input); got != c.want {
			t.Errorf("ShortBuild(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseStampAndFormatAge(t *testing.T) {
	t.Parallel()

	now := time.Now()
	msStr := "1790186398000"
	ts, ok := ParseStamp(msStr)
	if !ok {
		t.Fatalf("ParseStamp failed on ms string")
	}
	if ts.UnixMilli() != 1790186398000 {
		t.Errorf("UnixMilli = %d, want 1790186398000", ts.UnixMilli())
	}

	rfcStr := now.UTC().Format(time.RFC3339)
	ts2, ok := ParseStamp(rfcStr)
	if !ok {
		t.Fatalf("ParseStamp failed on RFC 3339 string")
	}
	if ts2.Unix() != now.Unix() {
		t.Errorf("Unix = %d, want %d", ts2.Unix(), now.Unix())
	}

	if got := FormatAge(15 * time.Second); got != "15s" {
		t.Errorf("FormatAge(15s) = %q, want 15s", got)
	}
	if got := FormatAge(12 * time.Minute); got != "12m" {
		t.Errorf("FormatAge(12m) = %q, want 12m", got)
	}
	if got := FormatAge(3 * time.Hour); got != "3h" {
		t.Errorf("FormatAge(3h) = %q, want 3h", got)
	}
	if got := FormatAge(48 * time.Hour); got != "2d" {
		t.Errorf("FormatAge(48h) = %q, want 2d", got)
	}
}

func TestDoctorLineFormat(t *testing.T) {
	t.Parallel()

	line := DoctorLine{
		Bench:  "hetzner",
		Check:  "play",
		State:  "FIX",
		Words:  []string{"last=tools", "stopped=quack"},
		Why:    "play failed in role quack",
		Remedy: "nova-sprint fleet play tools --limit hetzner",
	}

	want := `DOCTOR FIX bench=hetzner check=play last=tools stopped=quack why="play failed in role quack" remedy="nova-sprint fleet play tools --limit hetzner"`
	if line.String() != want {
		t.Errorf("line.String() = %q, want %q", line.String(), want)
	}
}

func TestDoctorResultTableAndSummary(t *testing.T) {
	t.Parallel()

	res := DoctorResult{
		Benches: []BenchDoctorRecord{
			{
				Bench:             "studio",
				VersionHave:       "c250d86a",
				VersionWant:       "c250d86a",
				GoSDKHave:         "2dc95ce4",
				PlayResult:        "ok",
				PlayRole:          "tools",
				PlayAge:           "12m",
				RunnersDeclared:   4,
				RunnersRegistered: 4,
				RunnersOnline:     4,
				MirrorsHave:       DefaultDeclaredMirrors,
				MirrorsDeclared:   DefaultDeclaredMirrors,
				DiskGiB:           450,
			},
			{
				Bench:             "hetzner",
				VersionHave:       "c250d86a",
				VersionWant:       "c250d86a",
				GoSDKHave:         "708effb7",
				PlayResult:        "failed",
				PlayRole:          "quack",
				RunnersDeclared:   4,
				RunnersRegistered: 4,
				RunnersOnline:     4,
				MirrorsHave:       DefaultDeclaredMirrors[:6],
				MirrorsDeclared:   DefaultDeclaredMirrors,
				DiskGiB:           280,
				Fixes: []DoctorLine{
					{
						Bench:  "hetzner",
						Check:  "play",
						State:  "FIX",
						Why:    "play failed in role quack",
						Remedy: "nova-sprint fleet play tools --limit hetzner",
					},
				},
			},
		},
		TotalChecks: 14,
		TotalFixes:  1,
		Trips:       1,
		MS:          12,
	}

	table := res.ScreenTable()
	if !strings.Contains(table, "FLEET DOCTOR: 2 benches checked") {
		t.Errorf("ScreenTable missing header")
	}
	if !strings.Contains(table, "studio") || !strings.Contains(table, "hetzner") {
		t.Errorf("ScreenTable missing benches")
	}
	if !strings.Contains(table, "4/4/4") {
		t.Errorf("ScreenTable missing runners count")
	}

	summary := res.SummaryLine()
	if want := "DOCTOR FIX benches=2 fixes=1 checks=14 trips=1 ms=12"; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}

	// OK summary
	okRes := DoctorResult{
		Benches:     res.Benches[:1],
		TotalChecks: 7,
		TotalFixes:  0,
		Trips:       1,
		MS:          5,
	}
	if want := "DOCTOR OK benches=1 checks=7 trips=1 ms=5"; okRes.SummaryLine() != want {
		t.Errorf("ok summary = %q, want %q", okRes.SummaryLine(), want)
	}
}
