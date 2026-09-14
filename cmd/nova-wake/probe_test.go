package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// Test 19 of docs/SPEC-WAKE.md: a probe pings once and never diagnoses.
//
// The bounded correlation read is driven over a REAL git lane built inside this
// test's own temporary directory, because what draft 7 to draft 9 argue about
// is which bytes a git walk consumes and where it resumes, and a fake git would
// be a test of the fake. The expected= lines are draft 9's own, quoted from the
// spec's acceptance clauses.

const (
	caller = "Rowan"
	peer   = "peer"
)

// newLaneBus makes a bus checkout with a roster of two and one commit, and
// answers the checkout and the sha of that commit, which is every probe
// record's anchor here.
func newLaneBus(t *testing.T) (busDir, anchor string) {
	t.Helper()
	busDir = t.TempDir()
	git(t, busDir, "init", "-q", "-b", "main")
	write(t, filepath.Join(busDir, "participants.json"),
		fmt.Sprintf(`{"participants":[`+
			`{"name":%q,"lane":"from-rowan","git_name":%q,"git_email":"rowan@mas-bandwidth.com"},`+
			`{"name":%q,"lane":"from-peer","git_name":%q,"git_email":"peer@mas-bandwidth.com"}]}`,
			caller, caller, peer, peer))
	git(t, busDir, "add", "participants.json")
	commit(t, busDir, caller, "the roster", at.Add(-2*time.Hour))
	return busDir, headSHA(t, busDir)
}

// note is one file this test puts on a lane.
type note struct {
	name    string
	from    string
	to      string
	subject string
	pad     int  // extra bytes of Subject, for a header that is large but legal
	noBlank bool // a file with no blank line at all, so no header ends inside 4 KiB
}

func (n note) text() string {
	if n.noBlank {
		return strings.Repeat("From: "+n.from+" and no blank line anywhere in this file\n", 200)
	}
	subject := n.subject
	if n.pad > 0 {
		subject += " " + strings.Repeat("x", n.pad)
	}
	return fmt.Sprintf("From: %s\nTo: %s\nDate: Sun Sep 13 10:00:00 UTC 2026\nId: %s-000000000001\nSubject: %s\n\nthe body\n",
		n.from, n.to, strings.ToLower(n.from), subject)
}

// addLaneCommit puts notes on a lane in one commit and answers its sha.
func addLaneCommit(t *testing.T, busDir, lane string, when time.Time, notes ...note) string {
	t.Helper()
	for _, n := range notes {
		write(t, filepath.Join(busDir, lane, n.name), n.text())
	}
	git(t, busDir, "add", "-A")
	commit(t, busDir, peer, "notes on "+lane, when)
	return headSHA(t, busDir)
}

func commit(t *testing.T, dir, author, subject string, when time.Time) {
	t.Helper()
	stamp := when.UTC().Format(time.RFC3339)
	cmd := exec.Command("git", "-C", dir,
		"-c", "user.name="+author, "-c", "user.email="+strings.ToLower(author)+"@mas-bandwidth.com",
		"commit", "-q", "--allow-empty", "-m", subject)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("committing: %v\n%s", err, out)
	}
}

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(raw))
}

// seedPing writes a probe record by hand, the way a ping that landed would have
// written it. The correlation read is what these tests are about, and a send is
// a separate half with its own assertions below.
func seedPing(t *testing.T, statePath, busDir, name, pingID, anchor string, when time.Time) {
	t.Helper()
	st, err := wake.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	scope := wake.ProbeScope(busDir, "", "", caller, name)
	st.Set("probe:"+name, wake.Compose("deadbeefdeadbeef", "pinged", wake.Stamp(when), pingID, anchor, scope, "-"))
	if err := st.Save(statePath); err != nil {
		t.Fatal(err)
	}
}

// probeAt runs one probe on an injected clock.
func probeAt(t *testing.T, when time.Time, args ...string) result {
	t.Helper()
	clock := wake.NewFake(when)
	var out, errb bytes.Buffer
	exit := runWith(append([]string{"probe"}, args...), &out, &errb, clock)
	return result{exit: exit, stdout: out.String(), stderr: errb.String(), clock: clock}
}

// probeLine is the one WAKE PROBE line, trimmed of the fields the acceptance
// clauses do not name, so an expected= string can be compared whole.
func probeLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "WAKE PROBE ") {
			return line
		}
	}
	t.Fatalf("no WAKE PROBE line:\n%s", out)
	return ""
}

// hasFields asserts every key=value of want appears on the line.
func hasFields(t *testing.T, line, want string) {
	t.Helper()
	for _, tok := range strings.Fields(want) {
		if !strings.Contains(line, tok) {
			t.Errorf("the line does not carry %s:\n  got  %s\n  want %s", tok, line, want)
		}
	}
}

// ---------------------------------------------------------------------------
// probe --here: a readiness receipt is a measurement, taken by the tool.

func TestAProbeHereIsAMeasurement(t *testing.T) {
	r := probeAt(t, at, "--here")
	if r.exit != 0 {
		t.Fatalf("exit %d, want 0:\n%s", r.exit, r.all())
	}
	line := ""
	for _, l := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(l, "WAKE HERE ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no WAKE HERE line:\n%s", r.all())
	}
	load := ""
	for _, tok := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(tok, "load="); ok {
			load = v
		}
	}
	if load != "-" && len(strings.Split(load, ",")) != 3 {
		t.Errorf("load= has three fields: %q", load)
	}
	if n := countField(line, "procs="); n != -1 && n < 1 {
		t.Errorf("procs= is at least 1: %s", line)
	}
	if strings.Contains(r.all(), "READY") {
		t.Errorf("the word READY appears nowhere in this tool's output:\n%s", r.all())
	}
	quiet := probeAt(t, at, "--here", "--quiet-load", "0.5")
	if !strings.Contains(quiet.stdout, "quiet=") {
		t.Errorf("--quiet-load appends quiet=<true|false>:\n%s", quiet.stdout)
	}

	t.Run("the tripwire over here.go", func(t *testing.T) {
		for _, name := range []string{"here.go", "here_linux.go", "here_darwin.go", "here_other.go"} {
			path := filepath.Join("..", "..", "internal", "wake", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			for _, banned := range []string{"exec.Command", "/proc", `"ps"`} {
				if strings.Contains(string(raw), banned) {
					t.Errorf("%s holds %q: the count is a number the kernel already has and no listing of any kind", name, banned)
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// probe --line, the measurement half.

func TestAProbeMeasuresBeforeItPings(t *testing.T) {
	busDir, _ := newLaneBus(t)
	addLaneCommit(t, busDir, "from-peer", at.Add(-4*time.Minute), note{name: "one.md", from: peer, to: caller, subject: "a sign"})

	t.Run("fresh contact is PRESENT", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "probe.state")
		r := probeAt(t, at, "--bus", busDir, "--line", peer, "--state", state)
		if r.exit != 0 {
			t.Errorf("exit %d, want 0:\n%s", r.exit, r.all())
		}
		hasFields(t, probeLine(t, r.stdout), "state=PRESENT contact=FRESH silent-after=5m answer-within=2m")
		if _, err := os.Stat(state); err == nil {
			t.Errorf("a PRESENT probe writes nothing at all")
		}
	})

	t.Run("stale contact with no draft is SILENT", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "probe.state")
		r := probeAt(t, at.Add(2*time.Minute), "--bus", busDir, "--line", peer, "--state", state)
		if r.exit != 1 {
			t.Errorf("exit %d, want 1: do not assign now\n%s", r.exit, r.all())
		}
		hasFields(t, probeLine(t, r.stdout), "state=SILENT contact=STALE")
	})

	t.Run("a name with no sign is SILENT and never PRESENT", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "probe.state")
		r := probeAt(t, at, "--bus", busDir, "--line", "Nobody", "--state", state)
		if r.exit != 1 {
			t.Errorf("exit %d, want 1:\n%s", r.exit, r.all())
		}
		hasFields(t, probeLine(t, r.stdout), "state=SILENT contact=NONE last=- commit=-")
	})

	t.Run("a rest is never probed", func(t *testing.T) {
		rest := filepath.Join(t.TempDir(), "rest")
		write(t, rest, "# who is resting\n\n"+peer+" peer-0000000000ff 2026-09-13T09:00:00Z\n")
		state := filepath.Join(t.TempDir(), "probe.state")
		r := probeAt(t, at.Add(2*time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--rest", rest)
		if r.exit != 1 {
			t.Errorf("exit %d, want 1:\n%s", r.exit, r.all())
		}
		hasFields(t, probeLine(t, r.stdout), "state=RESTING contact=STALE rest=peer-0000000000ff")
		fresh := probeAt(t, at, "--bus", busDir, "--line", peer, "--state", state, "--rest", rest)
		hasFields(t, probeLine(t, fresh.stdout), "state=RESTING contact=FRESH")
		later := probeAt(t, at.Add(4*time.Hour), "--bus", busDir, "--line", peer, "--state", state, "--rest", rest)
		hasFields(t, probeLine(t, later.stdout), "state=RESTING")
		if _, err := os.Stat(state); err == nil {
			t.Errorf("a RESTING probe leaves the state file as it found it")
		}
	})

	t.Run("the ceiling", func(t *testing.T) {
		r := probeAt(t, at, "--bus", busDir, "--line", peer,
			"--state", filepath.Join(t.TempDir(), "s"), "--answer-within", "90m")
		if r.exit != 2 || !strings.Contains(r.all(), "60m") {
			t.Errorf("--answer-within 90m is refused naming the 60m ceiling: exit %d\n%s", r.exit, r.all())
		}
	})

	t.Run("a standing record with no --as is refused", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", headSHA(t, busDir), at)
		r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state)
		if r.exit != 2 || !strings.Contains(r.all(), "--as") {
			t.Errorf("a record read without its caller is a record read blind: exit %d\n%s", r.exit, r.all())
		}
	})

	t.Run("a record is bound to its transport", func(t *testing.T) {
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", headSHA(t, busDir), at)
		before := read(t, state)
		r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer,
			"--state", state, "--as", "Somebody-Else")
		if r.exit != 2 || !strings.Contains(r.all(), "WAKE REFUSED") {
			t.Errorf("a scope mismatch is refused rather than acted on: exit %d\n%s", r.exit, r.all())
		}
		if read(t, state) != before {
			t.Errorf("a refused probe leaves the file byte-identical")
		}
	})
}

// ---------------------------------------------------------------------------
// The bounded correlation read: draft 7's K3, draft 8's K3a-c, draft 9's K4a-c.

func TestTheBoundedCorrelationRead(t *testing.T) {
	t.Run("the empty lane times out", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		early := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		hasFields(t, probeLine(t, early.stdout), "state=PINGED correlation=complete remaining=0 gaps=0")
		late := probeAt(t, at.Add(8*time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		if late.exit != 1 {
			t.Errorf("exit %d, want 1:\n%s", late.exit, late.all())
		}
		hasFields(t, probeLine(t, late.stdout),
			"WAKE PROBE name=peer state=UNAVAILABLE correlation=complete remaining=0 gaps=0")
		if strings.Contains(late.all(), "credits") {
			t.Errorf("the reason is unknown and never a cause:\n%s", late.all())
		}
	})

	t.Run("the drained lane at an unchanged tip times out", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		var notes []note
		for i := 0; i < 8; i++ {
			notes = append(notes, note{name: fmt.Sprintf("n%02d.md", i), from: peer, to: "Somebody-Else", subject: "not for the caller"})
		}
		addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute), notes...)
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		first := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		hasFields(t, probeLine(t, first.stdout), "state=PINGED correlation=complete gaps=0")
		second := probeAt(t, at.Add(8*time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		hasFields(t, probeLine(t, second.stdout),
			"WAKE PROBE name=peer state=UNAVAILABLE correlation=complete remaining=0 gaps=0")
	})

	t.Run("an answer from the line to the caller", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
			note{name: "a.md", from: peer, to: "Somebody-Else", subject: "not for you"},
			note{name: "b.md", from: peer, to: caller, subject: "here I am"})
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		if r.exit != 0 {
			t.Errorf("exit %d, want 0: this probe was answered\n%s", r.exit, r.all())
		}
		hasFields(t, probeLine(t, r.stdout), "state=ANSWERED correlation=complete")
		st, err := wake.Load(state)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := st.Get("probe:" + peer); ok {
			t.Errorf("ANSWERED clears the record once")
		}
	})

	t.Run("a receipt naming the ping id answers on its own", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		write(t, filepath.Join(busDir, "from-peer", "RECEIPTS"),
			"2026-09-13T10:00:00Z rowan-00000000000a\n")
		git(t, busDir, "add", "-A")
		commit(t, busDir, peer, "a receipt", at.Add(-time.Minute))
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		r := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		if r.exit != 0 {
			t.Errorf("exit %d, want 0:\n%s", r.exit, r.all())
		}
		hasFields(t, probeLine(t, r.stdout), "state=ANSWERED")
	})

	t.Run("the over-header-cap header is a permanent gap", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
			note{name: "a.md", from: peer, to: "Somebody-Else", subject: "one"},
			note{name: "b.md", from: peer, to: "Somebody-Else", subject: "two"},
			note{name: "c.md", from: peer, to: caller, noBlank: true})
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		want := "WAKE PROBE name=peer state=PINGED correlation=partial remaining=0 gaps=1"
		named := 0
		for i, when := range []time.Duration{time.Minute, 8 * time.Minute, 20 * time.Minute, time.Hour} {
			r := probeAt(t, at.Add(when), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
			hasFields(t, probeLine(t, r.stdout), want)
			if r.exit != 1 {
				t.Errorf("poll %d: exit %d, want 1", i, r.exit)
			}
			if strings.Contains(r.stdout, "coverage gap") {
				named++
			}
		}
		if named != 1 {
			t.Errorf("a gap is named exactly once, not %d times", named)
		}
		for _, budget := range []string{"1048576", "4194304"} {
			r := probeAt(t, at.Add(time.Hour), "--bus", busDir, "--line", peer, "--state", state,
				"--as", caller, "--correlate-bytes", budget)
			hasFields(t, probeLine(t, r.stdout), want)
			if strings.Contains(r.stdout, "state=ANSWERED") {
				t.Errorf("no budget reaches past the fixed 4 KiB header cap (--correlate-bytes %s)", budget)
			}
		}
	})

	t.Run("the within-cap header over the total budget clears on a raise", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
			note{name: "a.md", from: peer, to: "Somebody-Else", subject: "one"},
			note{name: "b.md", from: peer, to: "Somebody-Else", subject: "two"},
			note{name: "c.md", from: peer, to: caller, subject: "the answer", pad: 3000})
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		small := probeAt(t, at.Add(time.Minute), "--bus", busDir, "--line", peer, "--state", state,
			"--as", caller, "--correlate-bytes", "2048")
		hasFields(t, probeLine(t, small.stdout),
			"WAKE PROBE name=peer state=PINGED correlation=partial remaining=0 gaps=1")
		raised := probeAt(t, at.Add(2*time.Minute), "--bus", busDir, "--line", peer, "--state", state,
			"--as", caller, "--correlate-bytes", "262144")
		hasFields(t, probeLine(t, raised.stdout),
			"WAKE PROBE name=peer state=ANSWERED correlation=complete remaining=0 gaps=0")
		if raised.exit != 0 {
			t.Errorf("exit %d, want 0: the resolved item is the answer\n%s", raised.exit, raised.all())
		}
	})

	t.Run("an unreadable object", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute),
			note{name: "a.md", from: peer, to: "Somebody-Else", subject: "one"},
			note{name: "c.md", from: peer, to: caller, subject: "the answer"})
		blob := blobOf(t, busDir, "from-peer/c.md")
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		removeObject(t, busDir, blob)
		r := probeAt(t, at.Add(8*time.Minute), "--bus", busDir, "--line", peer, "--state", state, "--as", caller)
		hasFields(t, probeLine(t, r.stdout), "state=PINGED correlation=partial gaps=1")
		if strings.Contains(r.stdout, "UNAVAILABLE") {
			t.Errorf("incomplete coverage is never negative evidence about a friend:\n%s", r.stdout)
		}
	})

	t.Run("a deep lane is bounded per poll and complete over polls", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		// 900 notes in nine commits, the answer the 850th.
		n := 0
		for c := 0; c < 9; c++ {
			var batch []note
			for i := 0; i < 100; i++ {
				n++
				to := "Somebody-Else"
				if n == 850 {
					to = caller
				}
				batch = append(batch, note{name: fmt.Sprintf("n%04d.md", n), from: peer, to: to, subject: "note"})
			}
			addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute), batch...)
		}
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		args := []string{"--bus", busDir, "--line", peer, "--state", state, "--as", caller}
		first := probeAt(t, at.Add(time.Minute), args...)
		hasFields(t, probeLine(t, first.stdout), "state=PINGED correlation=partial remaining=600 gaps=0")
		second := probeAt(t, at.Add(8*time.Minute), args...)
		hasFields(t, probeLine(t, second.stdout), "state=PINGED correlation=partial remaining=300 gaps=0")
		if strings.Contains(second.stdout, "UNAVAILABLE") {
			t.Errorf("past --answer-within and never UNAVAILABLE on a partial read:\n%s", second.stdout)
		}
		third := probeAt(t, at.Add(9*time.Minute), args...)
		hasFields(t, probeLine(t, third.stdout), "state=ANSWERED pinged-id=rowan-00000000000a")
		if third.exit != 0 {
			t.Errorf("exit %d, want 0\n%s", third.exit, third.all())
		}
	})

	t.Run("a budget of one item never spins", func(t *testing.T) {
		busDir, anchor := newLaneBus(t)
		for c := 0; c < 3; c++ {
			var batch []note
			for i := 0; i < 2; i++ {
				batch = append(batch, note{name: fmt.Sprintf("c%d-%d.md", c, i), from: peer, to: "Somebody-Else", subject: "note"})
			}
			addLaneCommit(t, busDir, "from-peer", at.Add(-time.Minute), batch...)
		}
		state := filepath.Join(t.TempDir(), "probe.state")
		seedPing(t, state, busDir, peer, "rowan-00000000000a", anchor, at)
		args := []string{"--bus", busDir, "--line", peer, "--state", state, "--as", caller, "--correlate-max", "1"}
		first := probeAt(t, at.Add(time.Minute), args...)
		hasFields(t, probeLine(t, first.stdout),
			"WAKE PROBE name=peer state=PINGED correlation=partial remaining=- gaps=0")
		seen := map[string]bool{}
		for i := 0; i < 8; i++ {
			r := probeAt(t, at.Add(time.Duration(2+i)*time.Minute), args...)
			line := probeLine(t, r.stdout)
			if strings.Contains(line, "correlation=complete") {
				// The lane is covered: a poll that takes no item BECAUSE there
				// is nothing left to read is complete negative evidence and not
				// a spin (draft 9, K4c).
				break
			}
			mark := bookmarkOf(t, state)
			if seen[mark] {
				t.Fatalf("poll %d took no item and recorded no gap while uncovered items remain: %s", i, mark)
			}
			seen[mark] = true
		}
	})
}

func bookmarkOf(t *testing.T, statePath string) string {
	t.Helper()
	st, err := wake.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := st.Get("probe:" + peer)
	p := wake.Decompose(v)
	if len(p) < 7 {
		return ""
	}
	return p[6]
}

func blobOf(t *testing.T, dir, path string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD:"+path)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"))
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse blob: %v", err)
	}
	return strings.TrimSpace(string(raw))
}

func removeObject(t *testing.T, dir, sha string) {
	t.Helper()
	loose := filepath.Join(dir, ".git", "objects", sha[:2], sha[2:])
	if err := os.Remove(loose); err != nil {
		t.Skipf("the object is not loose here: %v", err)
	}
}
