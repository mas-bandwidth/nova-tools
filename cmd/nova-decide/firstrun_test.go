package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (docs/ONBOARDING.md), pinned for this binary: the
// `### First run` transcript in docs/TESTS.md is RUN rather than read, and every
// line of it is compared against what the tool actually prints. Guidance nothing
// checks rots into a claim about a message that has since moved -- and this
// binary had no such test at all, which is how nova-tools #1425 could sit there:
// the ladder's lines lived under a heading of their own, where onboarding.FirstRun
// stops, so nothing reached them.

// firstRunLab is the fixture the documented lines are pointed at: the questions
// and state files a reader makes for themselves, an httptest endpoint standing in
// for Jev, and an escalation log holding the decision the transcript's own route
// line makes. No line here reaches a network.
type firstRunLab struct {
	questions string
	state     string
	log       string
	baseURL   string
}

func newFirstRunLab(t *testing.T) *firstRunLab {
	t.Helper()
	// The answer the documented line shows: the gate at go, above the 0.9 floor,
	// so the transcript's `below=-` and its exit 0 are what this fake produces.
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"answers": {"gate": {"type":"choice","choice":"go","probabilities":{"go":0.94,"wait":0.06},"confidence":0.94}}, "usage": {"input_tokens": 1, "output_tokens": 1}}`))
	})
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	state := filepath.Join(dir, "state.md")
	if err := os.WriteFile(state, []byte("the lane is green and the read is in\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lab := &firstRunLab{
		questions: writeQuestions(t, choiceQuestions()),
		state:     state,
		log:       filepath.Join(dir, "decide.jsonl"),
		baseURL:   srv.URL,
	}

	// The log the transcript's `log` line reads is the one its own `route` line
	// wrote: the same rebase decision, made here with --log so there is a row to
	// summarize. Written by the tool, never by hand.
	var out, errs bytes.Buffer
	if code := run([]string{"route", "--unit-id", "card-41", "--kind", "rebase",
		"--files", "2", "--packages", "1", "--no-jev", "--log", lab.log}, &out, &errs); code != 0 {
		t.Fatalf("seeding the escalation log: exit %d; stderr: %s", code, errs.String())
	}
	return lab
}

// localize points a documented command at the fixture. The paths are
// SUBSTITUTED rather than rewritten, so what runs is the line a reader pastes.
// The endpoint is the one addition, and it is the hard rule rather than a
// convenience: the documented line leans on the default --base-url, which is the
// live provider, and a unit test never touches a network.
func (l *firstRunLab) localize(args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		switch a {
		case "./questions.json":
			out[i] = l.questions
		case "./state.md":
			out[i] = l.state
		case "./decide.jsonl":
			out[i] = l.log
		}
	}
	if len(out) > 0 && out[0] == "--questions" {
		out = append(out, "--base-url", l.baseURL)
	}
	return out
}

// The transcript's commands are run and every line of it must match a line the
// tool actually printed -- the event prefix and the field names, in order.
// Numbers, paths and tails are a run's own business and are deliberately NOT
// compared: pinning those would make the document a fixture.
func TestFirstRunTranscriptMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-decide")
	if err != nil {
		t.Fatal(err)
	}
	// A key that is not one, for an endpoint that is not the provider's: the
	// documented line names neither, so both come from the environment here.
	t.Setenv(decide.DefaultKeyEnv, "sekret")
	t.Setenv(decide.FallbackKeyEnv, "")
	t.Setenv(decide.DecisionsEnv, "")
	lab := newFirstRunLab(t)

	var printed map[string]bool
	seen := map[string]int{}
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-decide"); ok {
			args := lab.localize(strings.Fields(cmd))
			var out, errs bytes.Buffer
			code := run(args, &out, &errs)
			// Exit 2 is "could not run" everywhere except the bare invocation,
			// which this transcript documents AS a refusal: it is the first thing
			// a stranger sees, and it has to keep saying which flag it wants.
			if code == 2 && len(args) > 0 {
				t.Fatalf("the transcript command %q does not run: exit 2; stderr: %s", line, errs.String())
			}
			if code != 2 && len(args) == 0 {
				t.Fatalf("the bare command is documented as a refusal at exit 2, got %d", code)
			}
			printed = map[string]bool{}
			for _, o := range strings.Split(out.String()+"\n"+errs.String(), "\n") {
				if s := onboarding.Shape(o); s != "" {
					printed[s] = true
				}
			}
			continue
		}
		s := onboarding.Shape(line)
		if s == "" {
			continue
		}
		if printed == nil {
			t.Fatalf("transcript line before any command: %q", line)
		}
		if !printed[s] {
			t.Errorf("docs/TESTS.md line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Fields(s)[0]]++
	}
	// The whole ladder is inside `### First run`, which is where a stranger
	// reads and where onboarding.FirstRun stops (nova-tools #1425): route, help
	// and log under a heading of their own are lines no reader is walked through
	// and no test executes.
	for prefix, want := range map[string]int{"DECIDE": 2, "ROUTE": 3, "HELP": 1, "LOG": 2} {
		if seen[prefix] != want {
			t.Errorf("`### First run` under `## nova-decide` shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}
