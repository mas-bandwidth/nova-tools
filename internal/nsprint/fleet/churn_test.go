package fleet

import (
	"strings"
	"testing"
)

const linuxPS = `  PID ELAPSED  PPID COMMAND
    1 900000     0 systemd
  200 400000     1 nova-sprint
  210      3   200 bench-row
  211      2   200 bench-row
  212      1   200 bench-row
  300 200000     1 ci-run
  301     30     1 ci-run
  400      5     1 grok
  500      0   510 ps
  510      0   520 bash
  520      1     1 sshd
  530  86400     1 old-loop
`

const darwinPS = `  PID     ELAPSED  PPID COMM
    1 12-03:00:00     0 /sbin/launchd
   80  1-01:00:00     1 /usr/sbin/sshd
   90       00:00     95 /bin/ps
   95       00:00     80 -bash
  100  1-00:00:00     1 /Users/glenn/.local/bin/nova-sprint
  110       00:04   100 /usr/bin/awk
  120       00:02     1 /usr/local/bin/bench-row
  130       00:11     1 /usr/local/bin/bench-row
  140       00:12     1 /usr/local/bin/bench-row
`

func TestParseEtime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"00:00", 0}, {"00:04", 4}, {"01:00:00", 3600}, {"1-00:00:00", 86400}, {"12-03:00:00", 12*86400 + 3*3600}, {"59:59", 3599},
	} {
		got, err := ParseEtime(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("%s: %d %v, want %d", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "5", "1-00:00", "a:b", "00:00:00:00", "x-00:00:00"} {
		if _, err := ParseEtime(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestParsePSBothPlatforms(t *testing.T) {
	t.Parallel()
	procs, err := ParsePS("linux", linuxPS)
	if err != nil || len(procs) != 12 || procs[1] != (Proc{PID: 200, PPID: 1, Age: 400000, Comm: "nova-sprint"}) {
		t.Fatalf("linux: %v %+v", err, procs)
	}
	procs, err = ParsePS("darwin", darwinPS)
	if err != nil || len(procs) != 9 {
		t.Fatalf("darwin: %v %+v", err, procs)
	}
	// A path is its base name, a login shell drops its dash, etime is seconds.
	if procs[0] != (Proc{PID: 1, PPID: 0, Age: 12*86400 + 3*3600, Comm: "launchd"}) || procs[1].Age != 90000 || procs[3].Comm != "bash" || procs[4].Comm != "nova-sprint" {
		t.Errorf("darwin rows: %+v", procs[:5])
	}
	for _, bad := range []string{"1 2 3\n", "x 1 1 c\n", "1 x 1 c\n"} {
		if _, err := ParsePS("linux", bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, err := ParsePS("darwin", "1 x 1 c\n"); err == nil {
		t.Error("darwin bad etime parsed")
	}
	if _, err := PSCommand("windows"); err == nil {
		t.Error("windows has a ps line")
	}
}

// TestSampleLeavesTheSampleOut: the young commands are counted without
// ps, awk, sshd or the shell ps runs under; a process a day old under init
// is old; and the lines end with the CHURN summary.
func TestSampleLeavesTheSampleOut(t *testing.T) {
	t.Parallel()
	procs, _ := ParsePS("linux", linuxPS)
	got := strings.Join(Sample(procs, 12).Lines("hulk"), "\n")
	want := strings.Join([]string{
		"hulk young bench-row 3",
		"hulk young grok 1",
		"hulk old pid=200 age=4d comm=nova-sprint",
		"hulk old pid=300 age=2d comm=ci-run",
		"hulk old pid=530 age=1d comm=old-loop",
		"CHURN hulk young=4 old=3",
	}, "\n")
	if got != want {
		t.Errorf("linux:\n%s\nwant:\n%s", got, want)
	}
	procs, _ = ParsePS("darwin", darwinPS)
	got = strings.Join(Sample(procs, 12).Lines("batman"), "\n")
	want = strings.Join([]string{
		"batman young bench-row 2",
		"batman old pid=100 age=1d comm=nova-sprint",
		"CHURN batman young=2 old=1",
	}, "\n")
	if got != want {
		t.Errorf("darwin:\n%s\nwant:\n%s", got, want)
	}
	if got := Sample(nil, 12).Lines("quiet"); len(got) != 1 || got[0] != "CHURN quiet young=0 old=0" {
		t.Errorf("empty: %v", got)
	}
}
