package pulse

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// coordinatorFake is a set of seams that drive the coordinator loop with known
// per-action counts and write one event per action to the events writer.
type coordinatorFake struct {
	harvest int // how many RESULT.md were disposed this tick
	sweep   int // how many pools were folded
	cuts    int // how many cards were cut
	refused int // how many cards were refused on dedup
	source  string
	dedup   string
}

func (f *coordinatorFake) Gate(tick int) (bool, string, error) {
	return false, "success", nil
}

func (f *coordinatorFake) Harvest(tick int, events io.Writer) (int, []Undecided, error) {
	for i := 0; i < f.harvest; i++ {
		fmt.Fprintf(events, "coordinator harvest tick=%d msg=%q\n", tick, fmt.Sprintf("disposed RESULT.md card-%d", i+1))
	}
	return f.harvest, nil, nil
}

func (f *coordinatorFake) Sweep(tick int, events io.Writer) (int, error) {
	for i := 0; i < f.sweep; i++ {
		fmt.Fprintf(events, "coordinator sweep tick=%d msg=%q\n", tick, fmt.Sprintf("folded pool %d", i))
	}
	return f.sweep, nil
}

func (f *coordinatorFake) Reap(tick int) (int, int, []Undecided, error) {
	return 0, 0, nil, nil
}

func (f *coordinatorFake) Refill(tick int, events io.Writer) (int, error) {
	for i := 0; i < f.cuts; i++ {
		fmt.Fprintf(events, "coordinator fill tick=%d source=%s msg=%q\n", tick, f.source, "cut")
	}
	for i := 0; i < f.refused; i++ {
		fmt.Fprintf(events, "coordinator fill tick=%d source=%s msg=%q\n", tick, f.source, f.dedup)
	}
	return f.cuts, nil
}

func (f *coordinatorFake) Launch(tick int) (int, int, error) {
	return 0, 0, nil
}

func TestIssue2186(t *testing.T) {
	f := &coordinatorFake{
		harvest: 2,
		sweep:   1,
		cuts:    2,
		refused: 1,
		source:  "nova-tools",
		dedup:   "dedup: PR42 at abc12345 already has a card in pending",
	}

	queue := t.TempDir()
	if err := createDirs(t, queue, "pending", "launched", "done", "failed"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	Run(RunInput{
		Queue:    queue,
		Roots:    filepath.Join(queue, "root"),
		Once:     true,
		Locked:   true,
		Stdout:   &stdout,
		Stderr:   io.Discard,
		Gate:     f,
		Harvest:  f,
		Sweep:    f,
		Reap:     f,
		Refill:   f,
		Launcher: f,
	})

	out := stdout.String()

	// One harvest line per RESULT.md disposed.
	if n := linesWithPrefix(out, "coordinator harvest"); n != f.harvest {
		t.Errorf("wanted %d coordinator harvest lines, got %d", f.harvest, n)
	}

	// One sweep line per pool folded.
	if n := linesWithPrefix(out, "coordinator sweep"); n != f.sweep {
		t.Errorf("wanted %d coordinator sweep lines, got %d", f.sweep, n)
	}

	// One fill line per card, with source and the dedup in msg.
	if n := linesWithPrefix(out, "coordinator fill"); n != f.cuts+f.refused {
		t.Errorf("wanted %d coordinator fill lines, got %d", f.cuts+f.refused, n)
	}
	if !strings.Contains(out, f.source) {
		t.Errorf("fill line missing source %q", f.source)
	}
	if !strings.Contains(out, f.dedup) {
		t.Errorf("fill line missing dedup %q", f.dedup)
	}

	// The WIDTH line is unchanged.
	if !strings.Contains(out, "PULSE WIDTH ") {
		t.Errorf("WIDTH line is missing")
	}
}

func linesWithPrefix(out, prefix string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			n++
		}
	}
	return n
}

func createDirs(t *testing.T, base string, names ...string) error {
	t.Helper()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// TestIssue2186WiringEmitsTheDocumentedEvents drives the REAL emitters -- Wiring.Harvest,
// Wiring.Sweep and Wiring.Refill, the seams `nova-pulse run` wires -- and holds every line
// they print to the grammar docs/SPEC-PULSE.md documents ("Exit codes and the output
// grammar"). It fails if an emitter stops producing its events (the counts are the seam's
// own return values) and if an emitted line has no documented shape (Stella's hold of #2853
// at c13be5e7: the fake-seam test above only proves Run passes its writer through).
func TestIssue2186WiringEmitsTheDocumentedEvents(t *testing.T) {
	grammar := coordinatorGrammar2186(t)
	for _, verb := range []string{"harvest", "sweep", "fill"} {
		if _, ok := grammar[verb]; !ok {
			t.Fatalf("docs/SPEC-PULSE.md's output grammar has no `coordinator %s` line", verb)
		}
	}
	check := func(step string, out string, verb string, want int) []string {
		t.Helper()
		var got []string
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "coordinator "+verb+" ") {
				t.Errorf("%s printed a line that is not a coordinator %s event: %q", step, verb, line)
				continue
			}
			if !grammar[verb].MatchString(line) {
				t.Errorf("%s printed %q, which the spec's grammar (%s) does not describe", step, line, grammar[verb])
			}
			got = append(got, line)
		}
		if len(got) != want {
			t.Errorf("%s printed %d coordinator %s lines, want %d:\n%s", step, len(got), verb, want, out)
		}
		return got
	}

	// 1. Harvest: one line per RESULT.md disposed, on the bench root's own fold.
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://forge.invalid/owner/repo/pull/2186")
	addCard(t, root, "card-a", "1", "flash", "RESULT card-a sha=000000000000",
		"RESULT card-a sha=000000000000\nDONE\nBRANCH rowan/card-a\nREPO owner/repo\n")
	hq := t.TempDir()
	h := NewWiring(WiringInput{Queue: hq, Roots: root, Repo: "owner/repo", Max: 20})
	var hev bytes.Buffer
	done, _, err := h.Harvest(3, &hev)
	if err != nil {
		t.Fatal(err)
	}
	if done != 1 {
		t.Fatalf("harvest disposed %d cards, want 1 (the fixture is one DONE card); pulse.log:\n%s",
			done, strings.Join(readLines(filepath.Join(hq, "pulse.log")), "\n"))
	}
	for _, line := range check("harvest", hev.String(), "harvest", done) {
		if !strings.HasPrefix(line, "coordinator harvest tick=3 msg=") {
			t.Errorf("harvest line does not carry the tick: %q", line)
		}
	}

	// 2. Sweep and 3. Refill, on the wired fixture: the merged approval closes (one sweep
	// line), and a read for PR812 and a fix for issue 601 are cut (one fill line each).
	now := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	queue, broot, work, restarter := wiredFixture(t, now)
	cfg := DefaultConfig()
	cfg.Slots = map[string]int{"studio": 10}
	cfg.RefillCadence = 1
	w := NewWiring(WiringInput{
		Queue: queue, Roots: broot, Repo: "mas-bandwidth/nova-tools", Branch: "dev",
		Now: func() time.Time { return now }, Config: func() Config { return cfg },
		TempGlob:  filepath.Join(t.TempDir(), "*swarmtest*"),
		Runs:      &fakeRuns{},
		PRs:       &fakeSource{calls: map[int]int{}, views: map[int][]PRView{812: {{State: "MERGED", Head: "deadbeef", Title: "the PR owed a read"}}}},
		Enqueuer:  &fakeEnqueuer{},
		Procs:     &fakeProcs{live: map[int]bool{}},
		Runners:   &fakeRunnerTable{},
		Restarter: restarter,
		Work:      work,
	})

	var sev bytes.Buffer
	swept, err := w.Sweep(1, &sev)
	if err != nil {
		t.Fatal(err)
	}
	if swept != 1 {
		t.Fatalf("sweep moved %d rows, want 1 (the merged approval)", swept)
	}
	check("sweep", sev.String(), "sweep", 1)

	var fev bytes.Buffer
	added, err := w.Refill(1, &fev)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("refill cut %d cards, want 2 (a read for PR812, a fix for 601)", added)
	}
	fills := strings.Join(check("refill", fev.String(), "fill", added), "\n")
	for _, want := range []string{
		`coordinator fill tick=1 source=nova-tools#812 msg="cut"`,
		`coordinator fill tick=1 source=nova-tools#601 msg="cut"`,
	} {
		if !strings.Contains(fills, want) {
			t.Errorf("refill did not print %q:\n%s", want, fills)
		}
	}

	// The next refill finds both cards in pending: one dedup line each, nothing cut.
	var dev bytes.Buffer
	added, err = w.Refill(2, &dev)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatalf("second refill cut %d cards, want 0", added)
	}
	dedups := strings.Join(check("second refill", dev.String(), "fill", 2), "\n")
	for _, want := range []string{
		`coordinator fill tick=2 source=nova-tools#812 msg="dedup: PR812 at abcd1234 already has a card"`,
		`coordinator fill tick=2 source=nova-tools#601 msg="dedup: nova-tools #601 already has a card"`,
	} {
		if !strings.Contains(dedups, want) {
			t.Errorf("second refill did not print %q:\n%s", want, dedups)
		}
	}
}

// coordinatorGrammar2186 reads the `coordinator <verb> ...` lines out of the output
// grammar block of docs/SPEC-PULSE.md and turns each into an anchored pattern: <quoted>
// is a Go-quoted string, every other <placeholder> one field without spaces.
func coordinatorGrammar2186(t *testing.T) map[string]*regexp.Regexp {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot560(t), "docs", "SPEC-PULSE.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	start := strings.Index(text, "\n## Exit codes and the output grammar\n")
	if start < 0 {
		t.Fatal("docs/SPEC-PULSE.md has no \"Exit codes and the output grammar\" section")
	}
	section := text[start+1:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	placeholder := regexp.MustCompile(`<[a-z-]+>`)
	out := map[string]*regexp.Regexp{}
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "coordinator ") {
			continue
		}
		verb := strings.Fields(line)[1]
		pat := placeholder.ReplaceAllStringFunc(regexp.QuoteMeta(line), func(p string) string {
			if p == "<quoted>" {
				return `"(?:[^"\\]|\\.)*"`
			}
			return `\S+`
		})
		out[verb] = regexp.MustCompile("^" + pat + "$")
	}
	return out
}
