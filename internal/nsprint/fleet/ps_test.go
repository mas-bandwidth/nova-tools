package fleet

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// psAt is the sample time every fixture below is read at.
var psAt = time.Unix(1_800_000_000, 0)

// loopPlist is a declared nova-loop unit as fleet/loops.yml writes it: its
// header comment holds "--", which encoding/xml refuses in a comment.
const loopPlist = `<?xml version="1.0" encoding="UTF-8"?>

<!-- ~/Library/LaunchAgents/com.nova.loop.mirror-refresh.plist -- managed by Ansible (fleet/loops.yml). -->
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.nova.loop.mirror-refresh</string>
	<!-- nova-loop is the single-instance wrapper -- it execs the command -->
	<key>ProgramArguments</key>
	<array>
		<string>/Users/n/.local/bin/nova-loop</string>
		<string>mirror-refresh</string>
		<string>--</string>
		<string>/Users/n/.local/bin/mirror-refresh</string>
		<string>--loop</string>
		<string>60</string>
	</array>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`

// beatPlist is the bench beat's own unit.
const beatPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!-- managed by Ansible (fleet/loops.yml). -->
<plist version="1.0"><dict>
<key>ProgramArguments</key><array><string>/Users/n/.local/bin/nova-loop</string><string>nova-sprint-bench-beat</string><string>--</string>
<string>/Users/n/.local/bin/nova-sprint</string><string>bench</string><string>beat</string><string>--bench</string><string>studio</string></array>
</dict></plist>
`

// runnerPlist is a declared unit whose program starts a child (run.sh ->
// Runner.Listener): the child belongs to the unit too.
const runnerPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!-- managed by Ansible (fleet/runners.yml). -->
<plist version="1.0"><dict>
<key>Program</key><string>/Users/n/runner-1/run.sh</string>
</dict></plist>
`

// handPlist is a unit someone wrote by hand: no play named.
const handPlist = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>com.nova.jev-loop</string>
<key>ProgramArguments</key><array><string>/bin/bash</string><string>/Users/n/bin/jev-loop</string></array>
</dict></plist>
`

const beatService = `# ~/.config/systemd/user/nova-loop-nova-sprint-bench-beat.service -- managed by Ansible (fleet/loops.yml).
[Service]
ExecStart=-/home/nova/.local/bin/nova-loop nova-sprint-bench-beat -- /home/nova/.local/bin/nova-sprint bench beat --bench hulk
`

// darwinFull is PSFullCommand("darwin")'s output on a bench whose user is
// uid 501 and whose beat is pid 300 (started by the unit through nova-loop).
const darwinFull = `    1     0 12-03:00:00   0.5     0 /sbin/launchd
  100     1 03-00:00:00  45.0    88 /System/Library/WindowServer -daemon
  200     1 02-00:00:00   0.0   501 bash /Users/n/.local/bin/mirror-refresh --loop 60
  210   200       00:05  12.0   501 git fetch --prune
  220     1 01-00:00:00   0.1   501 /Users/n/runner-1/run.sh
  230   220 01-00:00:00   3.0   501 /Users/n/runner-1/bin/Runner.Listener run
  240     1 04-00:00:00   0.0   501 /bin/bash /Users/n/bin/jev-loop
  250     1 03-00:00:00   0.0   501 perl delay.pl 1 2 0.080
  260     1 02-00:00:00   0.0   501 redis-server 127.0.0.1:26491
  270     1 00:30   99.0   501 /Users/n/go/bin/busy --forever
  280     1 05-00:00:00   0.0   501 /usr/bin/ssh-agent -l
  290   295 06-00:00:00   0.0   501 -zsh
  295     1 06-00:00:00   0.0   501 /Applications/iTerm.app/Contents/MacOS/iTerm2
  300     1 01:00:00   1.0   501 /Users/n/.local/bin/nova-sprint bench beat --bench studio
  310   300       00:00  80.0   501 ps -Aww -o pid=,ppid=,etime=,pcpu=,uid=,args=
  320     1 07-00:00:00   0.0     0 /usr/sbin/cron
`

func darwinUnits() []UnitFile {
	return []UnitFile{
		{File: "com.nova.loop.mirror-refresh.plist", Body: loopPlist},
		{File: "com.nova.runner.studio-nova-1.plist", Body: runnerPlist},
		{File: "com.nova.jev-loop.plist", Body: handPlist},
		{File: "com.nova.loop.nova-sprint-bench-beat.plist", Body: beatPlist},
	}
}

func TestParsePSRowsBothOSes(t *testing.T) {
	t.Parallel()
	rows, err := ParsePSRows("darwin", darwinFull)
	if err != nil || len(rows) != 16 {
		t.Fatalf("darwin rows = %d, %v", len(rows), err)
	}
	if r := rows[1]; r.PID != 100 || r.PPID != 1 || r.Age != 3*86400 || r.CPU != 45 || r.UID != 88 || r.Args != "/System/Library/WindowServer -daemon" {
		t.Fatalf("darwin row = %+v", r)
	}
	lin, err := ParsePSRows("linux", "  7   1 90061  2.5  1000 /usr/bin/python3  /x/y.py   --z\n")
	if err != nil || len(lin) != 1 || lin[0].Age != 90061 || lin[0].Args != "/usr/bin/python3 /x/y.py --z" {
		t.Fatalf("linux rows = %+v, %v", lin, err)
	}
	for _, bad := range []string{"1 0 5 0.0 0\n", "x 0 5 0.0 0 a\n", "1 0 1-2 0.0 0 a\n"} {
		if _, err := ParsePSRows("darwin", bad); err == nil {
			t.Errorf("ParsePSRows(%q) read a bad line", bad)
		}
	}
	if _, err := PSFullCommand("windows"); err == nil {
		t.Fatal("windows has no ps sample")
	}
}

func TestUnitCommandAndDeclared(t *testing.T) {
	t.Parallel()
	cases := []struct {
		u        UnitFile
		want     string
		declared bool
	}{
		{UnitFile{"com.nova.loop.mirror-refresh.plist", loopPlist}, "/Users/n/.local/bin/nova-loop mirror-refresh -- /Users/n/.local/bin/mirror-refresh --loop 60", true},
		{UnitFile{"com.nova.runner.x.plist", runnerPlist}, "/Users/n/runner-1/run.sh", true},
		{UnitFile{"com.nova.jev-loop.plist", handPlist}, "/bin/bash /Users/n/bin/jev-loop", false},
		{UnitFile{"nova-loop-nova-sprint-bench-beat.service", beatService}, "/home/nova/.local/bin/nova-loop nova-sprint-bench-beat -- /home/nova/.local/bin/nova-sprint bench beat --bench hulk", true},
		{UnitFile{"com.nova.empty.plist", ""}, "", false},
	}
	for _, c := range cases {
		if got := strings.Join(UnitCommand(c.u), " "); got != c.want {
			t.Errorf("UnitCommand(%s) = %q, want %q", c.u.File, got, c.want)
		}
		if got := UnitIsDeclared(c.u.Body); got != c.declared {
			t.Errorf("UnitIsDeclared(%s) = %v, want %v", c.u.File, got, c.declared)
		}
	}
	for file, want := range map[string]bool{
		"com.nova.loop.x.plist": true, "nova-loop-x.service": true, "nova-redis.service": true,
		"com.nova.loop.x.plist.123.2026-09-26@10:39:44~": false, "com.apple.x.plist": false, "redis.service": false,
	} {
		if IsNovaUnit(file) != want {
			t.Errorf("IsNovaUnit(%s) = %v, want %v", file, !want, want)
		}
	}
}

// TestSamplePSClassifies is #4338's bench half: the top processes by CPU
// (the sample's own ps left out), the units declared|undeclared (undeclared
// first), and the bench user's processes outside every declared unit, oldest
// first: a declared loop behind its interpreter, its child, a runner's
// child and the beat itself (and its ancestors) are not there; a system
// daemon, a login shell, an app and ssh-agent are not there; the hand-written
// jev-loop, the leaked redis-server and the perl delay are.
func TestSamplePSClassifies(t *testing.T) {
	t.Parallel()
	s := SamplePS(PSInput{GOOS: "darwin", Out: darwinFull, At: psAt, UID: 501, Self: 300, Units: darwinUnits(),
		User: func(uid int) string { return map[int]string{0: "root", 88: "_windowserver", 501: "n"}[uid] }})
	if s.Err != "" || s.At != psAt.Unix() {
		t.Fatalf("sample err %q at %d", s.Err, s.At)
	}
	var top []string
	for _, p := range s.Top {
		top = append(top, p.User+":"+p.Cmd)
	}
	wantTop := "n:/Users/n/go/bin/busy --forever|_windowserver:/System/Library/WindowServer -daemon|n:git fetch --prune|n:/Users/n/runner-1/bin/Runner.Listener run|n:/Users/n/.local/bin/nova-sprint bench beat --bench studio"
	if got := strings.Join(top, "|"); got != wantTop {
		t.Errorf("top =\n%s\nwant\n%s", got, wantTop)
	}
	var units []string
	for _, u := range s.Units {
		units = append(units, u.Name+"="+u.State)
	}
	wantUnits := "com.nova.jev-loop=undeclared|com.nova.loop.mirror-refresh=declared|com.nova.loop.nova-sprint-bench-beat=declared|com.nova.runner.studio-nova-1=declared"
	if got := strings.Join(units, "|"); got != wantUnits || s.UnitN != 4 {
		t.Errorf("units = %s (n %d), want %s", got, s.UnitN, wantUnits)
	}
	var old []string
	for _, p := range s.Old {
		old = append(old, p.Cmd)
	}
	wantOld := "/bin/bash /Users/n/bin/jev-loop|perl delay.pl 1 2 0.080|redis-server 127.0.0.1:26491|/Users/n/go/bin/busy --forever"
	if got := strings.Join(old, "|"); got != wantOld || s.OldN != 4 {
		t.Errorf("old =\n%s (n %d)\nwant\n%s", got, s.OldN, wantOld)
	}
	if s.Old[0].Start != psAt.Unix()-4*86400 {
		t.Errorf("jev-loop start = %d, want the sample time minus four days", s.Old[0].Start)
	}
}

// TestSamplePSBounds: however many processes and units, the sample carries
// PSTopMax, PSOldMax and PSUnitMax entries, each command capped at PSCmdMax,
// with the totals before the cut.
func TestSamplePSBounds(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Join([]string{strconv.Itoa(1000 + i), "1", strconv.Itoa(100 + i), "1.0", "501", "/x/loop" + strconv.Itoa(i) + " " + strings.Repeat("a", 300)}, " "))
		b.WriteString("\n")
	}
	var units []UnitFile
	for i := 0; i < 60; i++ {
		units = append(units, UnitFile{File: "com.nova.u" + strconv.Itoa(i) + ".plist"})
	}
	s := SamplePS(PSInput{GOOS: "linux", Out: b.String(), At: psAt, UID: 501, Units: units})
	if len(s.Top) != PSTopMax || len(s.Old) != PSOldMax || s.OldN != 200 || len(s.Units) != PSUnitMax || s.UnitN != 60 {
		t.Fatalf("bounds: top %d old %d/%d units %d/%d", len(s.Top), len(s.Old), s.OldN, len(s.Units), s.UnitN)
	}
	if s.Old[0].Cmd[:8] != "/x/loop1" || s.Old[0].PID != 1199 {
		t.Errorf("oldest = %+v, want pid 1199 first", s.Old[0])
	}
	for _, p := range append(s.Top, s.Old...) {
		if len(p.Cmd) > PSCmdMax {
			t.Fatalf("cmd %d bytes over %d", len(p.Cmd), PSCmdMax)
		}
	}
	if len(s.Encode()) > 8<<10 {
		t.Fatalf("encoded sample is %d bytes; the beat field must stay small", len(s.Encode()))
	}
	if bad := SamplePS(PSInput{GOOS: "linux", Out: "garbage\n", At: psAt}); bad.Err == "" {
		t.Fatal("an unreadable ps must set Err, never read as an empty bench")
	}
}

func benchWith(t *testing.T) PSBench {
	t.Helper()
	s := SamplePS(PSInput{GOOS: "darwin", Out: darwinFull, At: psAt, UID: 501, Self: 300, Units: darwinUnits(),
		User: func(uid int) string { return map[int]string{0: "root", 88: "_windowserver", 501: "n"}[uid] }})
	return PSBench{Bench: "studio", Beat: map[string]string{"ps": s.Encode(), "load1": "3.50", "ncpu": "24", "cpu": "41.0"}}
}

// TestPSLinesPrintsTheBench is `fleet ps` for one bench: load, top, the
// undeclared units, the PS line last.
func TestPSLinesPrintsTheBench(t *testing.T) {
	t.Parallel()
	lines, ok := PSLines(benchWith(t), psAt.Add(4*time.Second))
	want := strings.Join([]string{
		"studio load1=3.50 ncpu=24 cpu=41.0 sample=4s",
		"studio top pid=270 cpu=99.0 user=n age=30s cmd=/Users/n/go/bin/busy --forever",
		"studio top pid=100 cpu=45.0 user=_windowserver age=3d0h cmd=/System/Library/WindowServer -daemon",
		"studio top pid=210 cpu=12.0 user=n age=5s cmd=git fetch --prune",
		"studio top pid=230 cpu=3.0 user=n age=1d0h cmd=/Users/n/runner-1/bin/Runner.Listener run",
		"studio top pid=300 cpu=1.0 user=n age=1h0m cmd=/Users/n/.local/bin/nova-sprint bench beat --bench studio",
		"studio unit com.nova.jev-loop undeclared",
		"PS studio top=5 units=4 undeclared=1",
	}, "\n")
	if got := strings.Join(lines, "\n"); !ok || got != want {
		t.Fatalf("ok %v\n%s\nwant\n%s", ok, got, want)
	}
}

// TestStrayLinesAgainstTheLastPlay is `fleet ps --stray` for one bench: the
// undeclared unit, and of the processes outside every declared unit only
// those started before the last play (the busy loop is 30 s old, after it).
func TestStrayLinesAgainstTheLastPlay(t *testing.T) {
	t.Parallel()
	b := benchWith(t)
	b.Play = psAt.Add(-time.Hour)
	lines, n, ok := StrayLines(b)
	want := strings.Join([]string{
		"studio unit com.nova.jev-loop undeclared",
		"studio old pid=240 cpu=0.0 user=n age=4d0h cmd=/bin/bash /Users/n/bin/jev-loop",
		"studio old pid=250 cpu=0.0 user=n age=3d0h cmd=perl delay.pl 1 2 0.080",
		"studio old pid=260 cpu=0.0 user=n age=2d0h cmd=redis-server 127.0.0.1:26491",
		"STRAY studio units=1 old=3 play=" + b.Play.UTC().Format(time.RFC3339),
	}, "\n")
	if got := strings.Join(lines, "\n"); !ok || n != 4 || got != want {
		t.Fatalf("ok %v n %d\n%s\nwant\n%s", ok, n, got, want)
	}
}

// TestNoSampleIsNeverClean: a bench with no beat, a beat with no ps field,
// a failed sample, an unreadable field, or no last play each prints its line
// and is not ok.
func TestNoSampleIsNeverClean(t *testing.T) {
	t.Parallel()
	failed := PSSample{At: psAt.Unix(), Err: "ps: exit status 1"}.Encode()
	cases := []struct {
		beat map[string]string
		want string
	}{
		{nil, "PS b NOBEAT bench:b:beat is absent"},
		{map[string]string{"build": "nova-sprint v1"}, "PS b NOSAMPLE the beat carries no ps (build nova-sprint\\x20v1)"},
		{map[string]string{"ps": failed}, "PS b FAIL ps: exit status 1"},
		{map[string]string{"ps": "{"}, "PS b FAIL ps field: unexpected end of JSON input"},
	}
	for _, c := range cases {
		lines, ok := PSLines(PSBench{Bench: "b", Beat: c.beat}, psAt)
		if ok || len(lines) != 1 || lines[0] != c.want {
			t.Errorf("PSLines(%v) = %v %q, want %q", c.beat, ok, lines, c.want)
		}
	}
	b := benchWith(t)
	lines, n, ok := StrayLines(b)
	if ok || n != 1 || lines[len(lines)-1] != "STRAY studio units=1 old=? no last play: bench:studio build_at is empty; pass --since" {
		t.Errorf("no play: ok %v n %d %q", ok, n, lines)
	}
	b.PlayRaw = "yesterday"
	lines, _, _ = StrayLines(b)
	if lines[len(lines)-1] != "STRAY studio units=1 old=? no last play: bench:studio build_at is yesterday, not RFC 3339; pass --since" {
		t.Errorf("bad play: %q", lines)
	}
}

func TestPSAge(t *testing.T) {
	t.Parallel()
	for sec, want := range map[int64]string{-5: "0s", 12: "12s", 423: "7m3s", 7500: "2h5m", 4*86400 + 3*3600: "4d3h"} {
		if got := PSAge(sec); got != want {
			t.Errorf("PSAge(%d) = %s, want %s", sec, got, want)
		}
	}
}
